package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMockUpstreamEndpoints(t *testing.T) {
	handler := setupHandler()

	// Test /healthz
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("unexpected healthz response: %d, body: %s", rec.Code, rec.Body.String())
	}

	// Test /webhook
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/webhook", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected webhook code: %d", rec.Code)
	}

	// Test /stats with 1 received
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/stats", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected stats code: %d", rec.Code)
	}

	// Test with delayMS > 0
	delay := 5
	delayMS = &delay
	t.Cleanup(func() {
		zero := 0
		delayMS = &zero
	})

	time.Sleep(5 * time.Millisecond)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/webhook", nil)
	handler.ServeHTTP(rec, req)

	// Test /stats with > 1 received
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/stats", nil)
	handler.ServeHTTP(rec, req)
	var stats map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("failed to decode stats: %v", err)
	}
	if stats["received_count"].(float64) < 2 {
		t.Fatalf("expected count >= 2, got %v", stats["received_count"])
	}

	// Test /reset
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/reset", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected reset code: %d", rec.Code)
	}
}
