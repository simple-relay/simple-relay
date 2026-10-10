package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

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

