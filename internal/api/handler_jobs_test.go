package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/config"
	"github.com/simple-relay/simple-relay/internal/engine"
	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage/sqlite"
)

func setupTestJobServer(t *testing.T) (*sqlite.DB, *engine.RouteRegistry, *engine.BatchWriter, http.Handler, *model.Route) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_jobs.db")

	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}

	routeRepo := sqlite.NewRouteRepo(db)
	reqRepo := sqlite.NewRequestRepo(db)
	attemptRepo := sqlite.NewAttemptRepo(db)

	registry := engine.NewRouteRegistry(routeRepo)
	route := &model.Route{
		Name:                 "order-service",
		Type:                 model.RouteTypeOutbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/jobs/order-service",
		DestinationURL:       "https://orders.internal.app/process",
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := registry.Create(ctx, route); err != nil {
		t.Fatalf("failed to create route: %v", err)
	}

	batchWriter := engine.NewBatchWriter(reqRepo, engine.BatchWriterConfig{
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		QueueCapacity: 50,
	})
	batchWriter.Start(context.Background())

	cfg := &config.Config{Port: 8080}
	handler := withLoopbackRemoteAddr(NewRouter(cfg, nil, RouterOptions{
		RouteRegistry: registry,
		BatchWriter:   batchWriter,
		RequestRepo:   reqRepo,
		AttemptRepo:   attemptRepo,
		MaxBodyBytes:  1024 * 1024,
	}))

	return db, registry, batchWriter, handler, route
}

func TestJobIngest_Success(t *testing.T) {
	db, _, bw, handler, route := setupTestJobServer(t)
	defer db.Close()
	defer bw.Close()

	payload := `{"order_id": 12345, "amount": 99.99}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+route.Name, bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-xyz")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202 Accepted, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	jobID, ok := resp["job_id"].(string)
	if !ok || jobID == "" {
		t.Fatalf("expected job_id in response, got %v", resp)
	}
	if resp["status"] != "queued" {
		t.Errorf("expected status 'queued', got %v", resp["status"])
	}

	// Verify persistence in SQLite
	reqRepo := sqlite.NewRequestRepo(db)
	savedReq, err := reqRepo.GetRequestByID(context.Background(), jobID)
	if err != nil {
		t.Fatalf("failed to query saved request: %v", err)
	}
	if savedReq.RouteID != route.ID {
		t.Errorf("expected route ID %s, got %s", route.ID, savedReq.RouteID)
	}
	if string(savedReq.Body) != payload {
		t.Errorf("expected saved body %q, got %q", payload, string(savedReq.Body))
	}
	if savedReq.Headers["Idempotency-Key"] != "idem-xyz" {
		t.Errorf("expected header Idempotency-Key preserved, got %q", savedReq.Headers["Idempotency-Key"])
	}
}

func TestJobIngest_NotFoundRoute(t *testing.T) {
	db, _, bw, handler, _ := setupTestJobServer(t)
	defer db.Close()
	defer bw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/unknown-service", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 Not Found, got %d", rec.Code)
	}
}

func TestJobInspect_SuccessAndNotFound(t *testing.T) {
	db, _, bw, handler, route := setupTestJobServer(t)
	defer db.Close()
	defer bw.Close()

	reqRepo := sqlite.NewRequestRepo(db)
	attemptRepo := sqlite.NewAttemptRepo(db)

	jobID := model.NewULID()
	now := time.Now().UTC()
	err := reqRepo.CreateRequest(context.Background(), &model.Request{
		ID:            jobID,
		RouteID:       route.ID,
		Method:        "POST",
		URL:           route.DestinationURL,
		Status:        model.RequestStatusPending,
		AttemptsCount: 1,
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	if err != nil {
		t.Fatalf("CreateRequest failed: %v", err)
	}

	attemptID := model.NewULID()
	code := 500
	errMsg := "internal error"
	err = attemptRepo.RecordAttempt(context.Background(), &model.Attempt{
		ID:                 attemptID,
		RequestID:          jobID,
		AttemptNumber:      1,
		ResponseStatusCode: &code,
		ErrorMessage:       &errMsg,
		ExecutedAt:         now,
		DurationMS:         120,
	})
	if err != nil {
		t.Fatalf("RecordAttempt failed: %v", err)
	}

	// 1. Inspect existing job
	inspectReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, inspectReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var inspectResp JobInspectionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &inspectResp); err != nil {
		t.Fatalf("failed to decode inspect response: %v", err)
	}
	if inspectResp.ID != jobID {
		t.Errorf("expected job ID %s, got %s", jobID, inspectResp.ID)
	}
	if len(inspectResp.Attempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(inspectResp.Attempts))
	}
	if inspectResp.Attempts[0].ID != attemptID {
		t.Errorf("expected attempt ID %s, got %s", attemptID, inspectResp.Attempts[0].ID)
	}

	// 2. Inspect non-existent job
	notFoundReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/non-existent-id", nil)
	recNotFound := httptest.NewRecorder()
	handler.ServeHTTP(recNotFound, notFoundReq)

	if recNotFound.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing job, got %d", recNotFound.Code)
	}
}
