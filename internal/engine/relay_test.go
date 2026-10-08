package engine

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage/sqlite"
)

func setupTestRelayEngine(t *testing.T) (*sqlite.DB, *RelayEngine, *model.Route) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_relay_engine.db")

	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate schema: %v", err)
	}

	routeRepo := sqlite.NewRouteRepo(db)
	route := &model.Route{
		Name:                 "sync-test",
		Type:                 model.RouteTypeOutbound,
		Mode:                 model.RouteModeSync,
		Path:                 "/api/v1/relay/sync-test",
		DestinationURL:       "http://127.0.0.1:0", // replaced in tests
		MaxAttempts:          3,
		TimeoutMS:            500,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := routeRepo.CreateRoute(ctx, route); err != nil {
		t.Fatalf("failed to create route: %v", err)
	}

	retryEngine := NewRetryEngine(RetryConfig{
		BaseBackoff:   10 * time.Millisecond,
		MaxBackoff:    50 * time.Millisecond,
		MaxRetryAfter: 100 * time.Millisecond,
	})

	relayEngine := NewRelayEngine(
		&http.Client{},
		retryEngine,
		sqlite.NewRequestRepo(db),
		sqlite.NewAttemptRepo(db),
		slog.Default(),
	)

	return db, relayEngine, route
}

func TestRelayEngine_ImmediateSuccess(t *testing.T) {
	db, engine, route := setupTestRelayEngine(t)
	defer db.Close()

	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		if r.Header.Get("X-Relay-Request-ID") == "" {
			t.Errorf("expected X-Relay-Request-ID header to be present")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "hello upstream" {
			t.Errorf("expected body 'hello upstream', got '%s'", string(body))
		}

		w.Header().Set("X-Custom-Resp", "ok")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	route.DestinationURL = server.URL

	ctx := context.Background()
	headers := make(http.Header)
	headers.Set("Content-Type", "text/plain")

	res, err := engine.RelaySync(ctx, route, "POST", "/api/v1/relay/sync-test", headers, []byte("hello upstream"), nil)
	if err != nil {
		t.Fatalf("RelaySync returned unexpected error: %v", err)
	}

	if res.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", res.StatusCode)
	}
	if string(res.Body) != `{"status": "success"}` {
		t.Errorf("expected response body '{\"status\": \"success\"}', got '%s'", string(res.Body))
	}
	if res.Attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", res.Attempts)
	}
	if res.Headers.Get("X-Custom-Resp") != "ok" {
		t.Errorf("expected custom response header 'ok'")
	}

	// Verify SQLite database state
	req, err := db.GetRequestByID(ctx, res.RequestID)
	if err != nil {
		t.Fatalf("failed to query request: %v", err)
	}
	if req.Status != model.RequestStatusSuccess {
		t.Errorf("expected request status 'success', got '%s'", req.Status)
	}

	attempts, err := db.ListAttemptsByRequestID(ctx, res.RequestID)
	if err != nil {
		t.Fatalf("failed to query attempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("expected 1 attempt record, got %d", len(attempts))
	}
}

func TestRelayEngine_RetryAndSuccessOnSecondAttempt(t *testing.T) {
	db, engine, route := setupTestRelayEngine(t)
	defer db.Close()

	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&callCount, 1)
		if current == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error 1"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok 2"))
	}))
	defer server.Close()

	route.DestinationURL = server.URL

	ctx := context.Background()
	res, err := engine.RelaySync(ctx, route, "POST", "/api/v1/relay/sync-test", make(http.Header), []byte("retry payload"), nil)
	if err != nil {
		t.Fatalf("RelaySync failed: %v", err)
	}

	if res.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", res.StatusCode)
	}
	if res.Attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", res.Attempts)
	}
	if string(res.Body) != "ok 2" {
		t.Errorf("expected body 'ok 2', got '%s'", string(res.Body))
	}

	// Verify 2 attempt rows in SQLite
	attempts, err := db.ListAttemptsByRequestID(ctx, res.RequestID)
	if err != nil {
		t.Fatalf("failed to query attempts: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts in DB, got %d", len(attempts))
	}
	if *attempts[0].ResponseStatusCode != 500 {
		t.Errorf("expected attempt 1 status 500, got %d", *attempts[0].ResponseStatusCode)
	}
	if *attempts[1].ResponseStatusCode != 200 {
		t.Errorf("expected attempt 2 status 200, got %d", *attempts[1].ResponseStatusCode)
	}
}

func TestRelayEngine_ExhaustRetries(t *testing.T) {
	db, engine, route := setupTestRelayEngine(t)
	defer db.Close()

	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway persistent"))
	}))
	defer server.Close()

	route.DestinationURL = server.URL
	route.MaxAttempts = 3

	ctx := context.Background()
	res, err := engine.RelaySync(ctx, route, "POST", "/api/v1/relay/sync-test", make(http.Header), []byte("body"), nil)
	if err != nil {
		t.Fatalf("RelaySync should not return fatal error when response was received: %v", err)
	}

	if res.StatusCode != http.StatusBadGateway {
		t.Errorf("expected status 502, got %d", res.StatusCode)
	}
	if res.Attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", res.Attempts)
	}

	req, _ := db.GetRequestByID(ctx, res.RequestID)
	if req.Status != model.RequestStatusFailed {
		t.Errorf("expected status failed, got %s", req.Status)
	}
}

func TestRelayEngine_ClientDisconnectCancellation(t *testing.T) {
	db, engine, route := setupTestRelayEngine(t)
	defer db.Close()

	firstCallStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(firstCallStarted)
		// Simulate slow upstream
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	route.DestinationURL = server.URL

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		<-firstCallStarted
		// Client aborts in-flight request
		cancel()
	}()

	start := time.Now()
	_, err := engine.RelaySync(ctx, route, "POST", "/api/v1/relay/sync-test", make(http.Header), []byte("abort body"), nil)
	duration := time.Since(start)

	if err == nil {
		t.Fatal("expected error due to client context cancellation, got nil")
	}

	// The abort should happen swiftly without waiting for multiple backoffs
	if duration > 500*time.Millisecond {
		t.Errorf("expected fast abortion, but took %v", duration)
	}
}
