package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/config"
	"github.com/simple-relay/simple-relay/internal/engine"
	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage/sqlite"
)

func TestE2E_AsyncJobsDeliveryPipeline(t *testing.T) {
	// 1. Mock Upstream destination server
	var upstreamCalls int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := atomic.AddInt32(&upstreamCalls, 1)
		if call == 1 {
			// First attempt fails with 500
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("temporary upstream glitch"))
			return
		}
		// Subsequent attempt succeeds with 200 OK
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": "delivered"}`))
	}))
	defer upstreamServer.Close()

	// 2. Setup SQLite database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "e2e_jobs.db")

	db, err := sqlite.Open(dbPath, "NORMAL")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}

	routeRepo := sqlite.NewRouteRepo(db)
	reqRepo := sqlite.NewRequestRepo(db)
	attemptRepo := sqlite.NewAttemptRepo(db)

	// 3. Register Async Route
	registry := engine.NewRouteRegistry(routeRepo)
	route := &model.Route{
		Name:                 "payment-notifications",
		Type:                 model.RouteTypeOutbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/jobs/payment-notifications",
		DestinationURL:       upstreamServer.URL,
		MaxAttempts:          3,
		TimeoutMS:            1000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := registry.Create(ctx, route); err != nil {
		t.Fatalf("failed to create route: %v", err)
	}

	// 4. Initialize Delivery Pipeline: Dispatcher, WorkerPool, BatchWriter
	jobsCh := make(chan *model.Request, 10)

	dispatcher := engine.NewDispatcher(reqRepo, jobsCh, engine.DispatcherConfig{
		PollInterval: 50 * time.Millisecond,
		LeaseTimeout: 5 * time.Minute,
	})

	retryEngine := engine.NewRetryEngine(engine.RetryConfig{
		BaseBackoff: 20 * time.Millisecond, // Fast backoff for test speed
		MaxBackoff:  100 * time.Millisecond,
	})

	workerPool := engine.NewWorkerPool(jobsCh, registry, reqRepo, attemptRepo, retryEngine, engine.WorkerPoolConfig{
		Concurrency: 2,
	})

	batchWriter := engine.NewBatchWriter(reqRepo, engine.BatchWriterConfig{
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		QueueCapacity: 50,
		WakeFn: func() {
			dispatcher.Wake()
		},
	})

	// Start engine components
	batchWriter.Start(ctx)
	defer batchWriter.Close()

	dispatcher.Start(ctx)
	defer dispatcher.Close()

	workerPool.Start(ctx)
	defer workerPool.Close()

	// 5. Mount HTTP API Router
	cfg := &config.Config{Port: 8080}
	appHandler := withLoopbackRemoteAddr(NewRouter(cfg, nil, RouterOptions{
		RouteRegistry: registry,
		BatchWriter:   batchWriter,
		RequestRepo:   reqRepo,
		AttemptRepo:   attemptRepo,
		MaxBodyBytes:  1024 * 1024,
	}))

	// 6. Send HTTP Ingestion Request
	payload := `{"event": "charge.success", "id": "ch_123"}`
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/payment-notifications", bytes.NewBufferString(payload))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Relay-Idempotency-Key", "idemp-001")

	rec := httptest.NewRecorder()
	appHandler.ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d: %s", rec.Code, rec.Body.String())
	}

	var ingestResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &ingestResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	jobID := ingestResp["job_id"].(string)
	if jobID == "" {
		t.Fatal("expected non-empty job_id")
	}

	// 7. Poll Inspection API until status is "success" (allowing for 1 retry)
	var finalJob JobInspectionResponse
	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		inspectRec := httptest.NewRecorder()
		inspectReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID, nil)
		appHandler.ServeHTTP(inspectRec, inspectReq)

		if inspectRec.Code == http.StatusOK {
			var resp JobInspectionResponse
			if err := json.Unmarshal(inspectRec.Body.Bytes(), &resp); err == nil {
				if resp.Status == "success" {
					finalJob = resp
					break
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if finalJob.Status != "success" {
		t.Fatalf("expected job status 'success', got %q", finalJob.Status)
	}

	if finalJob.AttemptsCount != 2 {
		t.Errorf("expected 2 attempts (1 failure + 1 success), got %d", finalJob.AttemptsCount)
	}

	if len(finalJob.Attempts) != 2 {
		t.Errorf("expected 2 attempt audit records, got %d", len(finalJob.Attempts))
	}
}
