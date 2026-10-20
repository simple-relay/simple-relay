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

func TestE2E_DLQReplayDeliveryPipeline(t *testing.T) {
	var shouldFail int32 = 1
	var deliveredSuccess int32 = 0

	destServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.LoadInt32(&shouldFail) == 1 {
			http.Error(w, "temporary destination outage", http.StatusServiceUnavailable)
			return
		}
		atomic.StoreInt32(&deliveredSuccess, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": "recovered"}`))
	}))
	defer destServer.Close()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "e2e_dlq.db")

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

	registry := engine.NewRouteRegistry(routeRepo)
	route := &model.Route{
		Name:                 "dlq-e2e-route",
		Type:                 model.RouteTypeOutbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/jobs/dlq-e2e-route",
		DestinationURL:       destServer.URL,
		MaxAttempts:          2,
		TimeoutMS:            500,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := registry.Create(ctx, route); err != nil {
		t.Fatalf("failed to create route: %v", err)
	}

	jobsCh := make(chan *model.Request, 10)
	dispatcher := engine.NewDispatcher(reqRepo, jobsCh, engine.DispatcherConfig{
		PollInterval: 50 * time.Millisecond,
		LeaseTimeout: 5 * time.Minute,
	})

	retryEngine := engine.NewRetryEngine(engine.RetryConfig{
		BaseBackoff: 10 * time.Millisecond,
		MaxBackoff:  30 * time.Millisecond,
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

	batchWriter.Start(ctx)
	defer batchWriter.Close()

	dispatcher.Start(ctx)
	defer dispatcher.Close()

	workerPool.Start(ctx)
	defer workerPool.Close()

	cfg := &config.Config{Port: 8080}
	appHandler := NewRouter(cfg, nil, RouterOptions{
		RouteRegistry: registry,
		RelayEngine:   nil,
		BatchWriter:   batchWriter,
		RequestRepo:   reqRepo,
		AttemptRepo:   attemptRepo,
		WakeFn:        dispatcher.Wake,
	})
	relayServer := httptest.NewServer(appHandler)
	defer relayServer.Close()

	// 1. Submit async job
	jobPayload := []byte(`{"event": "payment_failure", "amount": 99}`)
	ingestReq, err := http.NewRequest(http.MethodPost, relayServer.URL+"/api/v1/jobs/"+route.Name, bytes.NewReader(jobPayload))
	if err != nil {
		t.Fatalf("failed to create ingest request: %v", err)
	}
	ingestReq.Header.Set("Content-Type", "application/json")

	ingestResp, err := http.DefaultClient.Do(ingestReq)
	if err != nil {
		t.Fatalf("failed to submit job: %v", err)
	}
	if ingestResp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d", ingestResp.StatusCode)
	}

	var ingestBody map[string]any
	_ = json.NewDecoder(ingestResp.Body).Decode(&ingestBody)
	ingestResp.Body.Close()

	jobID, ok := ingestBody["job_id"].(string)
	if !ok || jobID == "" {
		t.Fatalf("missing job_id in response: %v", ingestBody)
	}

	// 2. Wait until request exhausts 2 attempts and lands in DLQ
	deadline := time.Now().Add(3 * time.Second)
	var inDLQ bool
	for time.Now().Before(deadline) {
		reqRecord, err := reqRepo.GetRequestByID(ctx, jobID)
		if err == nil && reqRecord.Status == model.RequestStatusDLQ {
			inDLQ = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !inDLQ {
		t.Fatal("request did not reach 'dlq' status in expected time")
	}

	// 3. Inspect DLQ via GET /api/v1/dlq
	dlqListReq, _ := http.NewRequest(http.MethodGet, relayServer.URL+"/api/v1/dlq?route_name="+route.Name, nil)
	dlqListResp, err := http.DefaultClient.Do(dlqListReq)
	if err != nil {
		t.Fatalf("failed to call GET /api/v1/dlq: %v", err)
	}
	if dlqListResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from GET /api/v1/dlq, got %d", dlqListResp.StatusCode)
	}

	var listData DLQListResponse
	_ = json.NewDecoder(dlqListResp.Body).Decode(&listData)
	dlqListResp.Body.Close()

	if listData.Total != 1 || len(listData.Items) != 1 || listData.Items[0].ID != jobID {
		t.Fatalf("unexpected DLQ list content: %+v", listData)
	}

	// 4. Fix destination server to return 200 OK
	atomic.StoreInt32(&shouldFail, 0)

	// 5. Trigger Replay via POST /api/v1/dlq/{id}/replay
	replayReq, _ := http.NewRequest(http.MethodPost, relayServer.URL+"/api/v1/dlq/"+jobID+"/replay", nil)
	replayResp, err := http.DefaultClient.Do(replayReq)
	if err != nil {
		t.Fatalf("failed to call replay: %v", err)
	}
	if replayResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from replay, got %d", replayResp.StatusCode)
	}

	var replayData ReplaySingleResponse
	_ = json.NewDecoder(replayResp.Body).Decode(&replayData)
	replayResp.Body.Close()

	if !replayData.Replayed || replayData.Status != "pending" {
		t.Fatalf("unexpected replay response: %+v", replayData)
	}

	// 6. Wait for Dispatcher & WorkerPool to deliver replayed job to success
	deliveryDeadline := time.Now().Add(3 * time.Second)
	var replayedSuccess bool
	for time.Now().Before(deliveryDeadline) {
		reqRecord, err := reqRepo.GetRequestByID(ctx, jobID)
		if err == nil && reqRecord.Status == model.RequestStatusSuccess {
			replayedSuccess = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !replayedSuccess {
		t.Fatal("replayed request did not reach 'success' status")
	}

	if atomic.LoadInt32(&deliveredSuccess) != 1 {
		t.Fatal("destination server did not receive the successful replayed request")
	}

	// 7. Verify attempt audit trail: 2 failed attempts + 1 successful replay attempt = 3 attempts total
	attempts, err := attemptRepo.ListAttemptsByRequestID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to list attempts: %v", err)
	}
	if len(attempts) != 3 {
		t.Fatalf("expected 3 attempt logs in audit trail, got %d", len(attempts))
	}
}
