package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/config"
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

func TestRouter_AuthMiddleware(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Case 1: Admin password disabled
	cfgNoAuth := config.DefaultConfig()
	cfgNoAuth.AdminPassword = ""
	routerNoAuth := NewRouter(cfgNoAuth, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/routes", nil)
	rr := httptest.NewRecorder()
	routerNoAuth.ServeHTTP(rr, req)
	// Without routeRepo it returns 404 from ServeMux, NOT 401 Unauthorized
	if rr.Code == http.StatusUnauthorized {
		t.Fatalf("expected non-401 for /api/v1/routes when auth disabled, got %d", rr.Code)
	}

	// Case 2: Admin password enabled
	cfgAuth := config.DefaultConfig()
	cfgAuth.AdminPassword = "adminsecret123"
	sessionStore := NewSessionStore(24 * time.Hour)
	routerAuth := NewRouter(cfgAuth, logger, RouterOptions{SessionStore: sessionStore})

	// Public endpoints should pass through
	publicPaths := []string{"/healthz", "/ui/static/js/alpine.min.js", "/", "/api/v1/auth/status"}
	for _, p := range publicPaths {
		reqPub := httptest.NewRequest(http.MethodGet, p, nil)
		rrPub := httptest.NewRecorder()
		routerAuth.ServeHTTP(rrPub, reqPub)
		if rrPub.Code == http.StatusUnauthorized {
			t.Fatalf("expected public endpoint %s to not be blocked by auth, got 401", p)
		}
	}

	// Protected endpoints should return 401 Unauthorized when unauthenticated
	protectedPaths := []string{
		"/api/v1/routes",
		"/api/v1/routes/any-id",
		"/api/v1/requests",
		"/api/v1/requests/any-id",
		"/api/v1/dlq",
		"/api/v1/dlq/any-id/replay",
		"/api/v1/jobs/01hx0000000000000000000000",
	}
	for _, p := range protectedPaths {
		reqProt := httptest.NewRequest(http.MethodGet, p, nil)
		rrProt := httptest.NewRecorder()
		routerAuth.ServeHTTP(rrProt, reqProt)
		if rrProt.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized for %s without cookie, got %d", p, rrProt.Code)
		}
	}

	// Machine ingest POST /api/v1/jobs/{route_name} should remain unauthenticated
	reqIngest := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/test-route", nil)
	rrIngest := httptest.NewRecorder()
	routerAuth.ServeHTTP(rrIngest, reqIngest)
	if rrIngest.Code == http.StatusUnauthorized {
		t.Fatalf("expected POST /api/v1/jobs/test-route not to be blocked by auth, got 401")
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

	// 5. Dev mode (AdminPassword == "") permits unauthenticated access, but rejects invalid API key
	cfgDev := config.DefaultConfig()
	cfgDev.AdminPassword = ""
	routerDev := NewRouter(cfgDev, logger, RouterOptions{
		APIKeyRepo: db.APIKeys(),
	})

	reqDevOpen := httptest.NewRequest(http.MethodGet, "/api/v1/routes", nil)
	rrDevOpen := httptest.NewRecorder()
	routerDev.ServeHTTP(rrDevOpen, reqDevOpen)
	if rrDevOpen.Code == http.StatusUnauthorized {
		t.Fatalf("expected dev mode open access without credentials, got 401")
	}

	reqDevBadKey := httptest.NewRequest(http.MethodGet, "/api/v1/routes", nil)
	reqDevBadKey.Header.Set("X-API-Key", "sr_live_badkey")
	rrDevBadKey := httptest.NewRecorder()
	routerDev.ServeHTTP(rrDevBadKey, reqDevBadKey)
	if rrDevBadKey.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 in dev mode when invalid key is explicitly sent, got %d", rrDevBadKey.Code)
	}
}

