package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/config"
	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
	"github.com/simple-relay/simple-relay/internal/storage/sqlite"
)

func TestHealthzEndpoint(t *testing.T) {
	cfg := config.DefaultConfig()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(cfg, logger)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode json response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", resp["status"])
	}
}

func TestHealthzEndpoint_MethodNotAllowed(t *testing.T) {
	cfg := config.DefaultConfig()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(cfg, logger)

	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	// Go 1.22 net/http ServeMux returns 405 Method Not Allowed when method pattern doesn't match
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405 for POST /healthz, got %d", rr.Code)
	}
}

func TestUIRoutes_RootAndUI(t *testing.T) {
	cfg := config.DefaultConfig()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(cfg, logger)

	paths := []string{"/", "/ui", "/ui/"}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200 for %s, got %d", p, rr.Code)
		}
		contentType := rr.Header().Get("Content-Type")
		if contentType != "text/html; charset=utf-8" {
			t.Errorf("expected Content-Type text/html; charset=utf-8 for %s, got %q", p, contentType)
		}
		if len(rr.Body.String()) == 0 {
			t.Errorf("expected non-empty HTML body for %s", p)
		}
	}
}

func TestUIRoutes_StaticAssets(t *testing.T) {
	cfg := config.DefaultConfig()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(cfg, logger)

	tests := []struct {
		path            string
		expectedCode    int
		checkContentSub string
	}{
		{"/ui/static/js/alpine.min.js", http.StatusOK, "Alpine"},
		{"/ui/static/css/tailwind.min.css", http.StatusOK, "tailwind"},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, tt.path, nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != tt.expectedCode {
			t.Fatalf("expected %d for %s, got %d", tt.expectedCode, tt.path, rr.Code)
		}
		if len(rr.Body.Bytes()) == 0 {
			t.Errorf("expected non-empty body for %s", tt.path)
		}
	}
}

// withLoopbackRemoteAddr wraps an http.Handler for unit tests using httptest.NewRequest,
// rewriting the stdlib default "192.0.2.1:1234" to "127.0.0.1:12345" while preserving any explicit RemoteAddr.
func withLoopbackRemoteAddr(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.RemoteAddr == "" || r.RemoteAddr == "192.0.2.1:1234" || r.RemoteAddr == httptest.DefaultRemoteAddr {
			r.RemoteAddr = "127.0.0.1:12345"
		}
		next.ServeHTTP(w, r)
	})
}

func TestRouter_AuthMiddleware(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Case 1: Admin password disabled (Dev Mode)
	cfgNoAuth := config.DefaultConfig()
	cfgNoAuth.AdminPassword = ""
	routerNoAuth := NewRouter(cfgNoAuth, logger)

	// 1a. Loopback caller in dev mode is permitted without credentials
	req := httptest.NewRequest(http.MethodGet, "/api/v1/routes", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	routerNoAuth.ServeHTTP(rr, req)
	// Without routeRepo it returns 404 from ServeMux, NOT 401 Unauthorized
	if rr.Code == http.StatusUnauthorized {
		t.Fatalf("expected non-401 for loopback /api/v1/routes when auth disabled, got %d", rr.Code)
	}

	// 1b. Non-loopback caller in dev mode MUST be rejected with 401 without API key
	reqRemote := httptest.NewRequest(http.MethodGet, "/api/v1/routes", nil)
	reqRemote.RemoteAddr = "203.0.113.50:54321"
	rrRemote := httptest.NewRecorder()
	routerNoAuth.ServeHTTP(rrRemote, reqRemote)
	if rrRemote.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for non-loopback caller in dev mode without credentials, got %d", rrRemote.Code)
	}

	// Case 2: Admin password enabled
	cfgAuth := config.DefaultConfig()
	cfgAuth.AdminPassword = "adminsecret123"
	sessionStore := NewSessionStore(24 * time.Hour)
	routerAuth := NewRouter(cfgAuth, logger, RouterOptions{SessionStore: sessionStore})

	// Public endpoints (including inbound webhook receiver) should pass through
	publicPaths := []string{"/healthz", "/ui/static/js/alpine.min.js", "/", "/api/v1/auth/status", "/api/v1/webhook/stripe"}
	for _, p := range publicPaths {
		reqPub := httptest.NewRequest(http.MethodGet, p, nil)
		rrPub := httptest.NewRecorder()
		routerAuth.ServeHTTP(rrPub, reqPub)
		if rrPub.Code == http.StatusUnauthorized {
			t.Fatalf("expected public endpoint %s to not be blocked by auth, got 401", p)
		}
	}

	// Protected endpoints (including outbound jobs and relay) should return 401 Unauthorized when unauthenticated
	protectedPaths := []string{
		"/api/v1/routes",
		"/api/v1/routes/any-id",
		"/api/v1/requests",
		"/api/v1/requests/any-id",
		"/api/v1/dlq",
		"/api/v1/dlq/any-id/replay",
		"/api/v1/jobs/01hx0000000000000000000000",
		"/api/v1/jobs/test-route",
		"/api/v1/relay/test-route",
	}
	for _, p := range protectedPaths {
		reqProt := httptest.NewRequest(http.MethodGet, p, nil)
		rrProt := httptest.NewRecorder()
		routerAuth.ServeHTTP(rrProt, reqProt)
		if rrProt.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized for %s without cookie, got %d", p, rrProt.Code)
		}
	}

	// Outbound POST /api/v1/jobs/{route_name} and POST /api/v1/relay/{route_name} must also be protected
	for _, p := range []string{"/api/v1/jobs/test-route", "/api/v1/relay/test-route"} {
		reqOut := httptest.NewRequest(http.MethodPost, p, nil)
		rrOut := httptest.NewRecorder()
		routerAuth.ServeHTTP(rrOut, reqOut)
		if rrOut.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized for unauthenticated POST %s, got %d", p, rrOut.Code)
		}
	}

	// Protected endpoints with valid cookie should not return 401
	token, err := sessionStore.Create()
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	for _, p := range protectedPaths {
		reqAuth := httptest.NewRequest(http.MethodGet, p, nil)
		reqAuth.AddCookie(&http.Cookie{Name: "relay_session", Value: token})
		rrAuth := httptest.NewRecorder()
		routerAuth.ServeHTTP(rrAuth, reqAuth)
		if rrAuth.Code == http.StatusUnauthorized {
			t.Fatalf("expected non-401 for %s with valid session cookie, got %d", p, rrAuth.Code)
		}
	}
}

func TestRouter_DualAuth(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db := setupTestAPIKeysDB(t)
	defer db.Close()

	ctx := context.Background()
	rawKey, apiKey, err := GenerateRawKey("ci-key")
	if err != nil {
		t.Fatalf("failed to generate raw key: %v", err)
	}
	if err := db.APIKeys().CreateAPIKey(ctx, apiKey); err != nil {
		t.Fatalf("failed to create api key: %v", err)
	}

	cfgAuth := config.DefaultConfig()
	cfgAuth.AdminPassword = "adminpassword123"
	sessionStore := NewSessionStore(24 * time.Hour)

	router := NewRouter(cfgAuth, logger, RouterOptions{
		SessionStore: sessionStore,
		APIKeyRepo:   db.APIKeys(),
	})

	// 1. Valid X-API-Key should allow access
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/routes", nil)
	req1.Header.Set("X-API-Key", rawKey)
	rr1 := httptest.NewRecorder()
	router.ServeHTTP(rr1, req1)
	if rr1.Code == http.StatusUnauthorized {
		t.Fatalf("expected access with valid X-API-Key, got 401")
	}

	// 2. Valid Authorization: Bearer should allow access
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/routes", nil)
	req2.Header.Set("Authorization", "Bearer "+rawKey)
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, req2)
	if rr2.Code == http.StatusUnauthorized {
		t.Fatalf("expected access with valid Bearer token, got 401")
	}

	// 3. Invalid API key should return 401
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/routes", nil)
	req3.Header.Set("X-API-Key", "sr_live_invalidkey")
	rr3 := httptest.NewRecorder()
	router.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid api key, got %d", rr3.Code)
	}

	// 4. Revoked API key should return 401
	if err := db.APIKeys().RevokeAPIKey(ctx, apiKey.ID); err != nil {
		t.Fatalf("failed to revoke key: %v", err)
	}
	req4 := httptest.NewRequest(http.MethodGet, "/api/v1/routes", nil)
	req4.Header.Set("X-API-Key", rawKey)
	rr4 := httptest.NewRecorder()
	router.ServeHTTP(rr4, req4)
	if rr4.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for revoked api key, got %d", rr4.Code)
	}

	// 5. Dev mode (AdminPassword == "") permits unauthenticated access on loopback, rejects remote unauthenticated, and rejects invalid API key
	cfgDev := config.DefaultConfig()
	cfgDev.AdminPassword = ""
	routerDev := NewRouter(cfgDev, logger, RouterOptions{
		APIKeyRepo: db.APIKeys(),
	})

	reqDevOpen := httptest.NewRequest(http.MethodGet, "/api/v1/routes", nil)
	reqDevOpen.RemoteAddr = "127.0.0.1:12345"
	rrDevOpen := httptest.NewRecorder()
	routerDev.ServeHTTP(rrDevOpen, reqDevOpen)
	if rrDevOpen.Code == http.StatusUnauthorized {
		t.Fatalf("expected dev mode open access on loopback without credentials, got 401")
	}

	reqDevRemote := httptest.NewRequest(http.MethodGet, "/api/v1/routes", nil)
	reqDevRemote.RemoteAddr = "203.0.113.99:54321"
	rrDevRemote := httptest.NewRecorder()
	routerDev.ServeHTTP(rrDevRemote, reqDevRemote)
	if rrDevRemote.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 in dev mode for remote non-loopback caller without API key, got %d", rrDevRemote.Code)
	}

	reqDevBadKey := httptest.NewRequest(http.MethodGet, "/api/v1/routes", nil)
	reqDevBadKey.RemoteAddr = "127.0.0.1:12345"
	reqDevBadKey.Header.Set("X-API-Key", "sr_live_badkey")
	rrDevBadKey := httptest.NewRecorder()
	routerDev.ServeHTTP(rrDevBadKey, reqDevBadKey)
	if rrDevBadKey.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 in dev mode when invalid key is explicitly sent, got %d", rrDevBadKey.Code)
	}
}

func TestRouter_QueueStatsProtected(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db := setupTestAPIKeysDB(t)
	defer db.Close()

	ctx := context.Background()
	rawKey, apiKey, err := GenerateRawKey("ci-key")
	if err != nil {
		t.Fatalf("failed to generate raw key: %v", err)
	}
	if err := db.APIKeys().CreateAPIKey(ctx, apiKey); err != nil {
		t.Fatalf("failed to create api key: %v", err)
	}

	cfgAuth := config.DefaultConfig()
	cfgAuth.AdminPassword = "adminpassword123"
	sessionStore := NewSessionStore(24 * time.Hour)

	router := NewRouter(cfgAuth, logger, RouterOptions{
		SessionStore: sessionStore,
		APIKeyRepo:   db.APIKeys(),
		RequestRepo:  db.RequestRepo,
	})

	// Unauthenticated request to /api/v1/queue/stats should be rejected with 401
	reqUnauth := httptest.NewRequest(http.MethodGet, "/api/v1/queue/stats", nil)
	rrUnauth := httptest.NewRecorder()
	router.ServeHTTP(rrUnauth, reqUnauth)
	if rrUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated /api/v1/queue/stats, got %d", rrUnauth.Code)
	}

	// Authenticated request to /api/v1/queue/stats should be allowed
	reqAuth := httptest.NewRequest(http.MethodGet, "/api/v1/queue/stats", nil)
	reqAuth.Header.Set("X-API-Key", rawKey)
	rrAuth := httptest.NewRecorder()
	router.ServeHTTP(rrAuth, reqAuth)
	if rrAuth.Code == http.StatusUnauthorized {
		t.Fatalf("expected authenticated access for /api/v1/queue/stats, got 401")
	}
}

type failingAPIKeyRepo struct {
	storage.APIKeyRepository
	err error
}

func (m *failingAPIKeyRepo) GetAPIKeyByHash(ctx context.Context, keyHash string) (*model.APIKey, error) {
	return nil, m.err
}

func TestRouter_InternalDBError_Returns500(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfgAuth := config.DefaultConfig()
	cfgAuth.AdminPassword = "adminpassword123"

	failingRepo := &failingAPIKeyRepo{err: errors.New("sqlite connection lost / disk I/O error")}
	router := NewRouter(cfgAuth, logger, RouterOptions{
		APIKeyRepo: failingRepo,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/routes", nil)
	req.Header.Set("X-API-Key", "sr_live_testkey123")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 Internal Server Error on database failure, got %d", rr.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["error"] != "Internal database error during authentication" {
		t.Fatalf("unexpected error response: %s", body["error"])
	}
}

func TestRouter_QueueStats_PayloadBreakdown(t *testing.T) {
	db := setupTestAPIKeysDB(t)
	defer db.Close()

	ctx := context.Background()
	route := &model.Route{
		ID:             "route_stats_test",
		Name:           "Stats Test Route",
		Type:           "inbound",
		Mode:           "async",
		Path:           "/stats-route",
		DestinationURL: "http://example.com/dest",
		MaxAttempts:    3,
		TimeoutMS:      5000,
	}
	routeRepo := sqlite.NewRouteRepo(db)
	if err := routeRepo.CreateRoute(ctx, route); err != nil {
		t.Fatalf("failed to create route: %v", err)
	}

	now := time.Now().UTC()
	past := now.Add(-5 * time.Minute)
	future := now.Add(5 * time.Minute)

	reqs := []*model.Request{
		{RouteID: route.ID, Method: "POST", URL: "http://example.com", Status: model.RequestStatusPending, NextAttemptAt: &past, Headers: map[string]string{}},
		{RouteID: route.ID, Method: "POST", URL: "http://example.com", Status: model.RequestStatusProcessing, Headers: map[string]string{}},
		{RouteID: route.ID, Method: "POST", URL: "http://example.com", Status: model.RequestStatusFailed, NextAttemptAt: &future, Headers: map[string]string{}},
		{RouteID: route.ID, Method: "POST", URL: "http://example.com", Status: model.RequestStatusDLQ, Headers: map[string]string{}},
	}
	for _, req := range reqs {
		if err := db.RequestRepo.CreateRequest(ctx, req); err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.DefaultConfig()
	router := NewRouter(cfg, logger, RouterOptions{
		RequestRepo: db.RequestRepo,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/queue/stats", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["pending"] != float64(1) {
		t.Errorf("expected pending=1, got %v", resp["pending"])
	}
	if resp["in_flight"] != float64(1) {
		t.Errorf("expected in_flight=1, got %v", resp["in_flight"])
	}
	if resp["delayed"] != float64(1) {
		t.Errorf("expected delayed=1, got %v", resp["delayed"])
	}
	if resp["dlq"] != float64(1) {
		t.Errorf("expected dlq=1, got %v", resp["dlq"])
	}
	if resp["queue_depth"] != float64(2) {
		t.Errorf("expected queue_depth=2, got %v", resp["queue_depth"])
	}
	if resp["status"] != "HEALTHY" {
		t.Errorf("expected status=HEALTHY, got %v", resp["status"])
	}
}
