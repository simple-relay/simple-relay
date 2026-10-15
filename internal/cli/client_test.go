package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient_Health(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" {
				t.Fatalf("expected path /healthz, got %s", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":    "ok",
				"timestamp": "2026-10-14T14:00:00Z",
			})
		}))
		defer ts.Close()

		client := NewClient(ts.URL, "", 2*time.Second)
		resp, latency, err := client.Health(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Status != "ok" {
			t.Fatalf("expected status ok, got %s", resp.Status)
		}
		if latency < 0 {
			t.Fatalf("expected non-negative latency, got %v", latency)
		}
	})

	t.Run("server degraded", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "degraded",
			})
		}))
		defer ts.Close()

		client := NewClient(ts.URL, "", 2*time.Second)
		resp, _, err := client.Health(context.Background())
		if err == nil {
			t.Fatalf("expected error for non-200 status")
		}
		if resp == nil || resp.Status != "degraded" {
			t.Fatalf("expected degraded response, got %+v", resp)
		}
	})

	t.Run("connection refused", func(t *testing.T) {
		client := NewClient("http://127.0.0.1:59999", "", 500*time.Millisecond)
		_, _, err := client.Health(context.Background())
		if err == nil {
			t.Fatalf("expected error for closed connection")
		}
	})
}

func TestClient_AuthHeader(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key != "secret-token-123" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": "unauthorized",
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
		})
	}))
	defer ts.Close()

	// Missing / wrong key
	clientNoAuth := NewClient(ts.URL, "", 2*time.Second)
	_, _, err := clientNoAuth.Health(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}

	// Valid key
	clientAuth := NewClient(ts.URL, "secret-token-123", 2*time.Second)
	resp, _, err := clientAuth.Health(context.Background())
	if err != nil {
		t.Fatalf("unexpected error with valid auth: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("expected status ok, got %s", resp.Status)
	}
}

func TestClient_GetQueueStats(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/queue/stats" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/api/v1/requests" {
			http.NotFound(w, r)
			return
		}
		status := r.URL.Query().Get("status")
		w.Header().Set("Content-Type", "application/json")
		switch status {
		case "pending":
			_ = json.NewEncoder(w).Encode(map[string]any{"total": 15})
		case "processing":
			_ = json.NewEncoder(w).Encode(map[string]any{"total": 5})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "", 2*time.Second)
	pending, inFlight, err := client.GetQueueStats(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pending != 15 || inFlight != 5 {
		t.Fatalf("expected 15 pending and 5 inFlight, got %d, %d", pending, inFlight)
	}
}

func TestClient_ListRoutes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/routes" {
			t.Fatalf("expected path /api/v1/routes, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"routes": []map[string]any{
				{"id": "route_1", "name": "stripe-webhook"},
				{"id": "route_2", "name": "slack-events"},
			},
		})
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "", 2*time.Second)
	routes, err := client.ListRoutes(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if routes["route_1"] != "stripe-webhook" || routes["route_2"] != "slack-events" {
		t.Fatalf("unexpected routes map: %+v", routes)
	}
}

func TestClient_ListDLQ(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/dlq" {
			t.Fatalf("expected path /api/v1/dlq, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{
					"id":             "req_dlq_1",
					"route_id":       "route_1",
					"method":         "POST",
					"url":            "http://upstream/hook",
					"status":         "failed",
					"attempts_count": 5,
					"last_error":     "502 Bad Gateway",
					"created_at":     "2026-10-14T12:00:00Z",
					"updated_at":     "2026-10-14T12:05:00Z",
				},
			},
			"total": 1,
		})
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "", 2*time.Second)
	items, total, err := client.ListDLQ(context.Background(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].ID != "req_dlq_1" || total != 1 {
		t.Fatalf("unexpected items or total: items=%+v, total=%d", items, total)
	}
}

func TestClient_InspectRequest(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/requests/req_123" {
			t.Fatalf("expected /api/v1/requests/req_123, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             "req_123",
			"route_id":       "route_1",
			"method":         "POST",
			"url":            "http://target/api",
			"status":         "failed",
			"attempts_count": 2,
			"attempts": []map[string]any{
				{
					"attempt_number":       1,
					"response_status_code": 500,
					"duration_ms":          150,
					"error_message":        "server error",
					"executed_at":          "2026-10-14T12:00:00Z",
				},
			},
		})
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "", 2*time.Second)
	reqDetail, err := client.InspectRequest(context.Background(), "req_123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reqDetail.ID != "req_123" || len(reqDetail.Attempts) != 1 {
		t.Fatalf("unexpected request detail: %+v", reqDetail)
	}
	att := reqDetail.Attempts[0]
	if att.GetStatusCode() != 500 {
		t.Fatalf("expected status code 500 via getter, got %d", att.GetStatusCode())
	}
	if att.GetAttemptedAt().IsZero() {
		t.Fatalf("expected non-zero executed_at via getter")
	}
}

func TestClient_ReplayDLQ(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/dlq/req_123/replay" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "", 2*time.Second)
	if err := client.ReplayDLQ(context.Background(), "req_123"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient_ReplayAllDLQ(t *testing.T) {
	var callCount int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/dlq/replay-all" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		count := atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		if count == 1 {
			// First batch replays 1000
			_ = json.NewEncoder(w).Encode(map[string]any{
				"replayed_count": 1000,
			})
		} else {
			// Second batch replays 250, finishes
			_ = json.NewEncoder(w).Encode(map[string]any{
				"replayed_count": 250,
			})
		}
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "", 2*time.Second)
	totalReplayed, err := client.ReplayAllDLQ(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if totalReplayed != 1250 {
		t.Fatalf("expected 1250 total replayed, got %d", totalReplayed)
	}
	if atomic.LoadInt32(&callCount) != 2 {
		t.Fatalf("expected 2 batch calls, got %d", callCount)
	}
}

func TestClient_RedirectCredentialStripping(t *testing.T) {
	// Destination server (different host)
	destServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		auth := r.Header.Get("Authorization")
		if apiKey != "" || auth != "" {
			t.Fatalf("credential leak: destination server received sensitive headers: apiKey=%q, auth=%q", apiKey, auth)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer destServer.Close()

	// Origin server that redirects cross-domain
	originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destServer.URL+"/healthz", http.StatusFound)
	}))
	defer originServer.Close()

	client := NewClient(originServer.URL, "super-secret-token", 2*time.Second)
	resp, _, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("unexpected error on redirect: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("expected status ok from redirected host, got %s", resp.Status)
	}
}

func TestClient_GetQueueStats_SingleRoundTrip(t *testing.T) {
	var queueStatsCalled, requestsCalled int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/queue/stats" {
			atomic.AddInt32(&queueStatsCalled, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"pending":     42,
				"in_flight":   8,
				"queue_depth": 50,
				"status":      "HEALTHY",
			})
			return
		}
		if r.URL.Path == "/api/v1/requests" {
			atomic.AddInt32(&requestsCalled, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{"total": 0})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "", 2*time.Second)
	p, inflight, err := client.GetQueueStats(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != 42 || inflight != 8 {
		t.Fatalf("expected 42 pending, 8 inFlight, got %d, %d", p, inflight)
	}
	if atomic.LoadInt32(&queueStatsCalled) != 1 {
		t.Fatalf("expected /api/v1/queue/stats to be called once, got %d", queueStatsCalled)
	}
	if atomic.LoadInt32(&requestsCalled) != 0 {
		t.Fatalf("expected /api/v1/requests NOT to be called, got %d", requestsCalled)
	}
}

func TestClient_InspectRequest_SanitizationAndEscaping(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedEscaped := "/api/v1/requests/id%20with%20spaces%2Fslash"
		if r.URL.EscapedPath() != expectedEscaped && r.URL.Path != "/api/v1/requests/id with spaces/slash" {
			t.Fatalf("expected properly escaped URL, got EscapedPath: %s, RawPath: %s", r.URL.EscapedPath(), r.URL.RawPath)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             "id with spaces/slash",
			"route_id":       "route_1",
			"method":         "POST",
			"url":            "http://example.com",
			"status":         "pending",
			"attempts_count": 0,
		})
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "", 2*time.Second)
	// Test empty id
	_, err := client.InspectRequest(context.Background(), "   ")
	if err == nil {
		t.Fatalf("expected error on empty ID")
	}

	// Test escaped id
	detail, err := client.InspectRequest(context.Background(), " id with spaces/slash ")
	if err != nil {
		t.Fatalf("unexpected error with escaped id: %v", err)
	}
	if detail.ID != "id with spaces/slash" {
		t.Fatalf("unexpected detail id: %s", detail.ID)
	}
}
