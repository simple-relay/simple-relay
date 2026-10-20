package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/config"
	"github.com/simple-relay/simple-relay/internal/engine"
	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage/sqlite"
)

func setupTestRouterWithRelay(t *testing.T) (*sqlite.DB, *engine.RouteRegistry, http.Handler, *httptest.Server) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_router_relay.db")

	db, err := sqlite.Open(dbPath, "NORMAL")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate schema: %v", err)
	}

	routeRepo := sqlite.NewRouteRepo(db)
	registry := engine.NewRouteRegistry(routeRepo)

	// Upstream test server
	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.Header.Get("X-Relay-Request-ID") == "" {
			t.Errorf("expected X-Relay-Request-ID in upstream request")
		}
		if idem := r.Header.Get("Idempotency-Key"); idem != "" {
			w.Header().Set("X-Echo-Idempotency", idem)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"upstream_status": "ok"}`))
	}))

	// Create test routes in database
	syncRoute := &model.Route{
		Name:                 "test-sync",
		Type:                 model.RouteTypeOutbound,
		Mode:                 model.RouteModeSync,
		Path:                 "/api/v1/relay/test-sync",
		DestinationURL:       upstream.URL,
		MaxAttempts:          3,
		TimeoutMS:            2000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := routeRepo.CreateRoute(ctx, syncRoute); err != nil {
		t.Fatalf("failed to create sync route: %v", err)
	}

	asyncRoute := &model.Route{
		Name:                 "test-async",
		Type:                 model.RouteTypeOutbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/jobs/test-async",
		DestinationURL:       upstream.URL,
		MaxAttempts:          3,
		TimeoutMS:            2000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := routeRepo.CreateRoute(ctx, asyncRoute); err != nil {
		t.Fatalf("failed to create async route: %v", err)
	}

	if err := registry.Init(ctx); err != nil {
		t.Fatalf("failed to init registry: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	retryEngine := engine.NewRetryEngine(engine.RetryConfig{
		BaseBackoff: 10 * time.Millisecond,
		MaxBackoff:  50 * time.Millisecond,
	})
	relayEngine := engine.NewRelayEngine(
		&http.Client{},
		retryEngine,
		sqlite.NewRequestRepo(db),
		sqlite.NewAttemptRepo(db),
		logger,
	)

	cfg := config.DefaultConfig()
	router := withLoopbackRemoteAddr(NewRouter(cfg, logger, RouterOptions{
		RouteRegistry: registry,
		RelayEngine:   relayEngine,
	}))

	return db, registry, router, upstream
}

func TestRelayHandler_Success(t *testing.T) {
	db, _, router, upstream := setupTestRouterWithRelay(t)
	defer db.Close()
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/relay/test-sync", strings.NewReader(`{"input": 123}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "test-key-abc")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body: %s", rr.Code, rr.Body.String())
	}

	// Verify diagnostic headers
	relayID := rr.Header().Get("X-Relay-Request-ID")
	if relayID == "" {
		t.Errorf("expected X-Relay-Request-ID response header")
	}
	attempts := rr.Header().Get("X-Relay-Attempts")
	if attempts != "1" {
		t.Errorf("expected X-Relay-Attempts to be 1, got %s", attempts)
	}
	if rr.Header().Get("X-Echo-Idempotency") != "test-key-abc" {
		t.Errorf("expected Idempotency-Key propagated to upstream")
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}
	if resp["upstream_status"] != "ok" {
		t.Errorf("unexpected body payload: %v", resp)
	}
}

func TestRelayHandler_AllHTTPMethods(t *testing.T) {
	db, _, router, upstream := setupTestRouterWithRelay(t)
	defer db.Close()
	defer upstream.Close()

	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/relay/test-sync", strings.NewReader("payload"))
			rr := httptest.NewRecorder()

			router.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("method %s failed with status %d", method, rr.Code)
			}
		})
	}
}

func TestRelayHandler_RouteNotFound(t *testing.T) {
	db, _, router, upstream := setupTestRouterWithRelay(t)
	defer db.Close()
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/relay/nonexistent-route", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rr.Code)
	}
}

func TestRelayHandler_AsyncModeRejected(t *testing.T) {
	db, _, router, upstream := setupTestRouterWithRelay(t)
	defer db.Close()
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/relay/test-async", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for async route called on relay, got %d", rr.Code)
	}
}

func TestRelayHandler_PayloadTooLarge(t *testing.T) {
	db, _, router, upstream := setupTestRouterWithRelay(t)
	defer db.Close()
	defer upstream.Close()

	// Default body limit is 10 MB. Send 11 MB.
	hugePayload := strings.Repeat("A", 11*1024*1024)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/relay/test-sync", strings.NewReader(hugePayload))
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413 Payload Too Large, got %d", rr.Code)
	}
}

func TestRelayHandler_TimeoutOverride(t *testing.T) {
	db, _, router, upstream := setupTestRouterWithRelay(t)
	defer db.Close()
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/relay/test-sync", strings.NewReader(`{}`))
	req.Header.Set("X-Relay-Timeout", strconv.Itoa(1500))
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
}
