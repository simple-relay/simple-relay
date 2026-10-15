package cli

import (
	"bytes"
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/api"
	"github.com/simple-relay/simple-relay/internal/config"
	"github.com/simple-relay/simple-relay/internal/engine"
	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage/sqlite"
)

func TestE2E_CLI_WithRealServer(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_relay.db")

	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}

	routeRegistry := engine.NewRouteRegistry(db.RouteRepo)
	if err := routeRegistry.Init(context.Background()); err != nil {
		t.Fatalf("failed to init route registry: %v", err)
	}

	cfg := &config.Config{
		Port: 8080,
	}

	router := api.NewRouter(cfg, nil, api.RouterOptions{
		RouteRepo:     db.RouteRepo,
		RouteRegistry: routeRegistry,
		RequestRepo:   db.RequestRepo,
		AttemptRepo:   db.AttemptRepo,
	})

	server := httptest.NewServer(router)
	defer server.Close()

	// Seed Route
	testRoute := &model.Route{
		ID:                   "route_test_1",
		Name:                 "payment-webhook",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/payment",
		DestinationURL:       "http://127.0.0.1:9999/dummy",
		SignatureProvider:    model.SignatureProviderNone,
		MaxAttempts:          5,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := db.RouteRepo.CreateRoute(context.Background(), testRoute); err != nil {
		t.Fatalf("failed to seed test route: %v", err)
	}
	_ = routeRegistry.Reload(context.Background())

	// Seed Requests: 1 pending, 1 failed (DLQ)
	reqPending := &model.Request{
		ID:            "req_pending_1",
		RouteID:       testRoute.ID,
		Method:        "POST",
		URL:           testRoute.DestinationURL,
		Status:        model.RequestStatusPending,
		AttemptsCount: 0,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := db.RequestRepo.CreateRequest(context.Background(), reqPending); err != nil {
		t.Fatalf("failed to seed pending request: %v", err)
	}

	lastErr := "503 Service Unavailable: upstream down"
	reqFailed := &model.Request{
		ID:            "req_failed_1",
		RouteID:       testRoute.ID,
		Method:        "POST",
		URL:           testRoute.DestinationURL,
		Status:        model.RequestStatusDLQ,
		AttemptsCount: 5,
		LastError:     &lastErr,
		CreatedAt:     time.Now().UTC().Add(-10 * time.Minute),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := db.RequestRepo.CreateRequest(context.Background(), reqFailed); err != nil {
		t.Fatalf("failed to seed failed request: %v", err)
	}

	// Seed attempt for reqFailed
	attemptErr := "503 Service Unavailable"
	statusCode := 503
	att := &model.Attempt{
		ID:                 "att_1",
		RequestID:          reqFailed.ID,
		AttemptNumber:      5,
		ResponseStatusCode: &statusCode,
		DurationMS:         120,
		ErrorMessage:       &attemptErr,
		ExecutedAt:         time.Now().UTC(),
	}
	if err := db.AttemptRepo.RecordAttempt(context.Background(), att); err != nil {
		t.Fatalf("failed to record attempt: %v", err)
	}

	// 1. Test health command
	t.Run("e2e health", func(t *testing.T) {
		var outBuf, errBuf bytes.Buffer
		if err := ExecuteCommand([]string{"health", "--server", server.URL}, &outBuf, &errBuf); err != nil {
			t.Fatalf("health command failed: %v", err)
		}
		out := outBuf.String()
		if !strings.Contains(out, "ok") || !strings.Contains(out, server.URL) {
			t.Fatalf("unexpected health output:\n%s", out)
		}
	})

	// 2. Test queue command
	t.Run("e2e queue", func(t *testing.T) {
		var outBuf, errBuf bytes.Buffer
		if err := ExecuteCommand([]string{"queue", "--server", server.URL}, &outBuf, &errBuf); err != nil {
			t.Fatalf("queue command failed: %v", err)
		}
		out := outBuf.String()
		if !strings.Contains(out, "QUEUE DEPTH") || !strings.Contains(out, "1") || !strings.Contains(out, "HEALTHY") {
			t.Fatalf("unexpected queue output:\n%s", out)
		}
	})

	// 3. Test dlq list command
	t.Run("e2e dlq list", func(t *testing.T) {
		var outBuf, errBuf bytes.Buffer
		if err := ExecuteCommand([]string{"dlq", "list", "--server", server.URL}, &outBuf, &errBuf); err != nil {
			t.Fatalf("dlq list command failed: %v", err)
		}
		out := outBuf.String()
		if !strings.Contains(out, "req_failed_1") || !strings.Contains(out, "payment-webhook") {
			t.Fatalf("unexpected dlq list output:\n%s", out)
		}
	})

	// 4. Test dlq inspect command
	t.Run("e2e dlq inspect", func(t *testing.T) {
		var outBuf, errBuf bytes.Buffer
		if err := ExecuteCommand([]string{"dlq", "inspect", "req_failed_1", "--server", server.URL}, &outBuf, &errBuf); err != nil {
			t.Fatalf("dlq inspect command failed: %v", err)
		}
		out := outBuf.String()
		if !strings.Contains(out, "req_failed_1") || !strings.Contains(out, "Attempt History") || !strings.Contains(out, "503") {
			t.Fatalf("unexpected dlq inspect output:\n%s", out)
		}
	})

	// 5. Test dlq replay single command
	t.Run("e2e dlq replay", func(t *testing.T) {
		var outBuf, errBuf bytes.Buffer
		if err := ExecuteCommand([]string{"dlq", "replay", "req_failed_1", "--server", server.URL}, &outBuf, &errBuf); err != nil {
			t.Fatalf("dlq replay command failed: %v", err)
		}
		out := outBuf.String()
		if !strings.Contains(out, "Successfully requeued request req_failed_1") {
			t.Fatalf("unexpected dlq replay output:\n%s", out)
		}

		// Verify status transitioned back to pending
		req, err := db.RequestRepo.GetRequestByID(context.Background(), "req_failed_1")
		if err != nil {
			t.Fatalf("failed to fetch req_failed_1: %v", err)
		}
		if req.Status != model.RequestStatusPending {
			t.Fatalf("expected status pending after replay, got %s", req.Status)
		}
	})

	// 6. Test dlq replay-all command with -y
	t.Run("e2e dlq replay-all", func(t *testing.T) {
		// Put req_failed_1 back to failed
		if err := db.RequestRepo.UpdateRequestStatus(context.Background(), "req_failed_1", model.RequestStatusDLQ, 5, nil, &lastErr); err != nil {
			t.Fatalf("failed to update status: %v", err)
		}

		var outBuf, errBuf bytes.Buffer
		if err := ExecuteCommand([]string{"dlq", "replay-all", "--server", server.URL, "-y"}, &outBuf, &errBuf); err != nil {
			t.Fatalf("dlq replay-all command failed: %v", err)
		}
		out := outBuf.String()
		if !strings.Contains(out, "Successfully requeued 1 DLQ requests") {
			t.Fatalf("unexpected dlq replay-all output:\n%s", out)
		}
	})
}
