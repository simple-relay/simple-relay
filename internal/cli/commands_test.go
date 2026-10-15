package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestCommands_Health(t *testing.T) {
	t.Run("healthy status", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":    "ok",
				"timestamp": "2026-10-14T14:00:00Z",
			})
		}))
		defer ts.Close()

		var outBuf, errBuf bytes.Buffer
		err := ExecuteCommand([]string{"health", "--server", ts.URL}, &outBuf, &errBuf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		out := outBuf.String()
		if !strings.Contains(out, "ok") || !strings.Contains(out, ts.URL) {
			t.Fatalf("expected health table output with status and URL, got:\n%s", out)
		}
	})

	t.Run("json output", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":    "ok",
				"timestamp": "2026-10-14T14:00:00Z",
			})
		}))
		defer ts.Close()

		var outBuf, errBuf bytes.Buffer
		err := ExecuteCommand([]string{"status", "--server", ts.URL, "-o", "json"}, &outBuf, &errBuf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		out := outBuf.String()
		if !strings.Contains(out, `"status": "ok"`) {
			t.Fatalf("expected json health output, got:\n%s", out)
		}
	})

	t.Run("degraded returns exit code 2", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "degraded",
			})
		}))
		defer ts.Close()

		var outBuf, errBuf bytes.Buffer
		err := ExecuteCommand([]string{"health", "--server", ts.URL}, &outBuf, &errBuf)
		if err == nil {
			t.Fatalf("expected exit code error for degraded health")
		}
		var exitErr ExitCodeError
		if ok := errorsAsExitCode(err, &exitErr); !ok || exitErr.ExitCode() != 2 {
			t.Fatalf("expected exit code 2, got %v", err)
		}
	})
}

func TestCommands_Queue(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/queue/stats" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"pending":     10,
				"in_flight":   2,
				"queue_depth": 12,
				"status":      "HEALTHY",
			})
			return
		}
		status := r.URL.Query().Get("status")
		if status == "pending" {
			_ = json.NewEncoder(w).Encode(map[string]any{"total": 10})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{"total": 2})
		}
	}))
	defer ts.Close()

	var outBuf, errBuf bytes.Buffer
	err := ExecuteCommand([]string{"queue", "--server", ts.URL}, &outBuf, &errBuf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := outBuf.String()
	if !strings.Contains(out, "QUEUE DEPTH") || !strings.Contains(out, "12") || !strings.Contains(out, "HEALTHY") {
		t.Fatalf("expected queue depth 12 and HEALTHY status, got:\n%s", out)
	}
}

func TestCommands_DLQ(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/routes":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"routes": []map[string]any{
					{"id": "route_1", "name": "stripe-inbound"},
				},
			})
		case r.URL.Path == "/api/v1/dlq":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":             "01ja2b3c4d5e6f7g8h9j0k1m2n",
						"route_id":       "route_1",
						"method":         "POST",
						"url":            "http://target/webhook",
						"status":         "failed",
						"attempts_count": 5,
						"last_error":     "502 Bad Gateway",
						"created_at":     "2026-10-14T12:00:00Z",
						"updated_at":     "2026-10-14T12:05:00Z",
					},
				},
				"total": 1,
			})
		case r.URL.Path == "/api/v1/requests/01ja2b3c4d5e6f7g8h9j0k1m2n":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "01ja2b3c4d5e6f7g8h9j0k1m2n",
				"route_id":       "route_1",
				"method":         "POST",
				"url":            "http://target/webhook",
				"status":         "failed",
				"attempts_count": 1,
				"attempts": []map[string]any{
					{
						"attempt_number": 1,
						"status_code":    502,
						"duration_ms":    45,
						"error_message":  "bad gateway",
						"attempted_at":   "2026-10-14T12:00:00Z",
					},
				},
			})
		case r.URL.Path == "/api/v1/dlq/01ja2b3c4d5e6f7g8h9j0k1m2n/replay":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case r.URL.Path == "/api/v1/dlq/replay-all":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"replayed_count": 1,
				"request_ids":    []string{"01ja2b3c4d5e6f7g8h9j0k1m2n"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	t.Run("dlq list resolves route name", func(t *testing.T) {
		var outBuf, errBuf bytes.Buffer
		err := ExecuteCommand([]string{"dlq", "list", "--server", ts.URL}, &outBuf, &errBuf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := outBuf.String()
		if !strings.Contains(out, "01ja2b3c4d5e6f7g8h9j0k1m2n") || !strings.Contains(out, "stripe-inbound") {
			t.Fatalf("expected request ID and resolved route name in dlq list, got:\n%s", out)
		}
	})

	t.Run("dlq inspect", func(t *testing.T) {
		var outBuf, errBuf bytes.Buffer
		err := ExecuteCommand([]string{"dlq", "inspect", "01ja2b3c4d5e6f7g8h9j0k1m2n", "--server", ts.URL}, &outBuf, &errBuf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := outBuf.String()
		if !strings.Contains(out, "01ja2b3c4d5e6f7g8h9j0k1m2n") || !strings.Contains(out, "502") {
			t.Fatalf("expected inspect details, got:\n%s", out)
		}
	})

	t.Run("dlq replay single", func(t *testing.T) {
		var outBuf, errBuf bytes.Buffer
		err := ExecuteCommand([]string{"dlq", "replay", "01ja2b3c4d5e6f7g8h9j0k1m2n", "--server", ts.URL}, &outBuf, &errBuf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := outBuf.String()
		if !strings.Contains(out, "Successfully requeued") {
			t.Fatalf("expected success message, got:\n%s", out)
		}
	})

	t.Run("dlq replay-all non-interactive requires yes flag", func(t *testing.T) {
		var outBuf, errBuf bytes.Buffer
		// Force non-interactive check in test
		err := ExecuteCommand([]string{"dlq", "replay-all", "--server", ts.URL}, &outBuf, &errBuf)
		if err == nil {
			t.Fatalf("expected error without --yes flag in non-interactive environment")
		}
		if !strings.Contains(err.Error(), "--yes") {
			t.Fatalf("expected error mentioning --yes flag, got: %v", err)
		}
	})

	t.Run("dlq replay-all with yes flag succeeds", func(t *testing.T) {
		var outBuf, errBuf bytes.Buffer
		err := ExecuteCommand([]string{"dlq", "replay-all", "--server", ts.URL, "-y"}, &outBuf, &errBuf)
		if err != nil {
			t.Fatalf("unexpected error with -y: %v", err)
		}
		out := outBuf.String()
		if !strings.Contains(out, "Successfully requeued 1 DLQ requests") {
			t.Fatalf("expected replayed 1 message, got:\n%s", out)
		}
	})
}

func TestRootCmd_TimeoutEnv(t *testing.T) {
	t.Setenv("RELAY_TIMEOUT", "45s")
	cmd := NewRootCmd()
	timeoutFlag := cmd.PersistentFlags().Lookup("timeout")
	if timeoutFlag == nil {
		t.Fatalf("timeout flag not found")
	}
	if timeoutFlag.DefValue != "45s" {
		t.Fatalf("expected default timeout 45s from env, got %s", timeoutFlag.DefValue)
	}
}

func TestCommands_DLQ_OptimizationAndSanitization(t *testing.T) {
	var listRoutesCalled int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/routes" {
			atomic.AddInt32(&listRoutesCalled, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"routes": []map[string]any{
					{"id": "route_1", "name": "stripe-webhook"},
				},
			})
			return
		}
		if r.URL.Path == "/api/v1/dlq" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":             "req_pwned_1",
						"route_id":       "route_1",
						"method":         "POST",
						"url":            "http://target/webhook",
						"status":         "failed",
						"attempts_count": 3,
						"last_error":     "\x1b[31m502 Bad Gateway\x1b[0m\x1b]0;Title\x07",
						"created_at":     "2026-10-14T12:00:00Z",
						"updated_at":     "2026-10-14T12:05:00Z",
					},
				},
				"total": 1,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	t.Run("json output skips ListRoutes", func(t *testing.T) {
		atomic.StoreInt32(&listRoutesCalled, 0)
		var outBuf, errBuf bytes.Buffer
		err := ExecuteCommand([]string{"dlq", "list", "--server", ts.URL, "-o", "json"}, &outBuf, &errBuf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if atomic.LoadInt32(&listRoutesCalled) != 0 {
			t.Fatalf("expected ListRoutes NOT to be called when output is json, got called %d times", listRoutesCalled)
		}
	})

	t.Run("table output sanitizes ANSI escapes", func(t *testing.T) {
		atomic.StoreInt32(&listRoutesCalled, 0)
		var outBuf, errBuf bytes.Buffer
		err := ExecuteCommand([]string{"dlq", "list", "--server", ts.URL}, &outBuf, &errBuf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := outBuf.String()
		if strings.Contains(out, "\x1b[31m") || strings.Contains(out, "\x1b]0;") {
			t.Fatalf("expected raw ANSI sequences to be stripped from output table, got:\n%q", out)
		}
		if !strings.Contains(out, "502 Bad Gateway") {
			t.Fatalf("expected clean error message, got:\n%s", out)
		}
	})
}

func TestCommands_ParallelExecution_ZeroRaces(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/healthz":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "timestamp": "2026-10-14T14:00:00Z"})
		case "/api/v1/queue/stats":
			_ = json.NewEncoder(w).Encode(map[string]any{"pending": 5, "in_flight": 2, "queue_depth": 7, "status": "HEALTHY"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	const concurrency = 20
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			var outBuf, errBuf bytes.Buffer
			var err error
			if idx%2 == 0 {
				err = ExecuteCommand([]string{"health", "--server", ts.URL, "-o", "json"}, &outBuf, &errBuf)
			} else {
				err = ExecuteCommand([]string{"queue", "--server", ts.URL, "-o", "table"}, &outBuf, &errBuf)
			}
			if err != nil {
				t.Errorf("parallel run %d failed: %v", idx, err)
			}
		}(i)
	}

	wg.Wait()
}
