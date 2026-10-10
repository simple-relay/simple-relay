package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/config"
	"github.com/simple-relay/simple-relay/internal/engine"
	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage/sqlite"
)

func TestE2E_InboundWebhookDeliveryPipeline(t *testing.T) {
	// 1. Mock destination server that receives forwarded webhooks
	var mu sync.Mutex
	var receivedMethod string
	var receivedHeaders http.Header
	var receivedBody []byte
	deliveredCh := make(chan struct{}, 1)

	destServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		receivedMethod = r.Method
		receivedHeaders = r.Header.Clone()
		receivedBody = body
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"received": true}`))

		select {
		case deliveredCh <- struct{}{}:
		default:
		}
	}))
	defer destServer.Close()

	// 2. Setup SQLite database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "e2e_webhook.db")

	db, err := sqlite.Open(dbPath)
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

	// 3. Register Inbound Webhook Route
	registry := engine.NewRouteRegistry(routeRepo)
	route := &model.Route{
		Name:                 "github-releases",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/github-releases",
		DestinationURL:       destServer.URL,
		MaxAttempts:          3,
		TimeoutMS:            1000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := registry.Create(ctx, route); err != nil {
		t.Fatalf("failed to create route: %v", err)
	}

	// 4. Initialize Delivery Pipeline
	jobsCh := make(chan *model.Request, 10)

	dispatcher := engine.NewDispatcher(reqRepo, jobsCh, engine.DispatcherConfig{
		PollInterval: 50 * time.Millisecond,
		LeaseTimeout: 5 * time.Minute,
	})

	retryEngine := engine.NewRetryEngine(engine.RetryConfig{
		BaseBackoff: 20 * time.Millisecond,
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

	// Start pipeline
	batchWriter.Start(ctx)
	defer batchWriter.Close()

	dispatcher.Start(ctx)
	defer dispatcher.Close()

	workerPool.Start(ctx)
	defer workerPool.Close()

	// 5. Mount HTTP server
	cfg := &config.Config{Port: 8080}
	appHandler := NewRouter(cfg, nil, RouterOptions{
		RouteRegistry: registry,
		BatchWriter:   batchWriter,
		RequestRepo:   reqRepo,
		AttemptRepo:   attemptRepo,
		MaxBodyBytes:  10 * 1024 * 1024,
	})
	relayServer := httptest.NewServer(appHandler)
	defer relayServer.Close()

	// 6. Post inbound webhook to relay
	webhookPayload := []byte(`{"action": "published", "release": {"tag_name": "v1.2.0"}}`)
	req, err := http.NewRequest(http.MethodPost, relayServer.URL+"/api/v1/webhook/"+route.Name, bytes.NewReader(webhookPayload))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", "sha256=abcdef1234567890")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to call webhook endpoint: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(respBody))
	}

	var ingestResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&ingestResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	relayID, ok := ingestResp["relay_id"].(string)
	if !ok || relayID == "" {
		t.Fatalf("expected relay_id in response, got %v", ingestResp)
	}
	if ingestResp["status"] != "persisted" {
		t.Fatalf("expected status 'persisted', got %v", ingestResp["status"])
	}

	// 7. Await delivery to destination server
	select {
	case <-deliveredCh:
		// Delivery completed
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for webhook to be delivered to destination")
	}

	// 8. Assert destination received unmodified raw payload and headers
	mu.Lock()
	defer mu.Unlock()

	if receivedMethod != http.MethodPost {
		t.Errorf("expected destination method POST, got %s", receivedMethod)
	}
	if !bytes.Equal(receivedBody, webhookPayload) {
		t.Errorf("expected payload %s, got %s", string(webhookPayload), string(receivedBody))
	}
	if receivedHeaders.Get("X-Hub-Signature-256") != "sha256=abcdef1234567890" {
		t.Errorf("expected X-Hub-Signature-256 preserved, got %s", receivedHeaders.Get("X-Hub-Signature-256"))
	}
	if receivedHeaders.Get("X-Relay-Request-ID") != relayID {
		t.Errorf("expected X-Relay-Request-ID %s, got %s", relayID, receivedHeaders.Get("X-Relay-Request-ID"))
	}

	// 9. Verify request transitioned to success and attempt audit logged
	var storedReq *model.Request
	pollDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(pollDeadline) {
		storedReq, err = reqRepo.GetRequestByID(ctx, relayID)
		if err == nil && storedReq.Status == model.RequestStatusSuccess {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	if storedReq == nil || storedReq.Status != model.RequestStatusSuccess {
		t.Fatalf("expected status 'success', got %v", storedReq)
	}
	if storedReq.AttemptsCount != 1 {
		t.Errorf("expected 1 attempt, got %d", storedReq.AttemptsCount)
	}

	attempts, err := attemptRepo.ListAttemptsByRequestID(ctx, relayID)
	if err != nil {
		t.Fatalf("failed to list attempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("expected 1 attempt record, got %d", len(attempts))
	}
	if attempts[0].ResponseStatusCode == nil || *attempts[0].ResponseStatusCode != 200 {
		t.Errorf("expected attempt status code 200, got %v", attempts[0].ResponseStatusCode)
	}
}

func TestE2E_InboundWebhook_WithSignatureVerification(t *testing.T) {
	var mu sync.Mutex
	var receivedBody []byte
	deliveredCh := make(chan struct{}, 1)

	destServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		receivedBody = body
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
		select {
		case deliveredCh <- struct{}{}:
		default:
		}
	}))
	defer destServer.Close()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "e2e_webhook_sig.db")

	db, err := sqlite.Open(dbPath)
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

	secret := "super-secure-github-token"
	registry := engine.NewRouteRegistry(routeRepo)
	route := &model.Route{
		Name:                 "github-secured-e2e",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/github-secured-e2e",
		DestinationURL:       destServer.URL,
		SignatureProvider:    model.SignatureProviderGitHub,
		SignatureSecret:      secret,
		MaxAttempts:          3,
		TimeoutMS:            1000,
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
		BaseBackoff: 20 * time.Millisecond,
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

	batchWriter.Start(ctx)
	defer batchWriter.Close()
	dispatcher.Start(ctx)
	defer dispatcher.Close()
	workerPool.Start(ctx)
	defer workerPool.Close()

	cfg := &config.Config{Port: 8080}
	appHandler := NewRouter(cfg, nil, RouterOptions{
		RouteRegistry: registry,
		BatchWriter:   batchWriter,
		RequestRepo:   reqRepo,
		AttemptRepo:   attemptRepo,
		MaxBodyBytes:  10 * 1024 * 1024,
	})
	relayServer := httptest.NewServer(appHandler)
	defer relayServer.Close()

	payload := []byte(`{"action":"push","ref":"refs/heads/feature"}`)

	// Step 1: Send forged / invalid signature -> Must receive 401 Unauthorized
	reqInvalid, _ := http.NewRequest(http.MethodPost, relayServer.URL+"/api/v1/webhook/"+route.Name, bytes.NewReader(payload))
	reqInvalid.Header.Set("X-Hub-Signature-256", "sha256=1111111111111111111111111111111111111111111111111111111111111111")
	respInvalid, err := http.DefaultClient.Do(reqInvalid)
	if err != nil {
		t.Fatalf("failed to execute invalid request: %v", err)
	}
	defer respInvalid.Body.Close()
	if respInvalid.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for forged signature, got %d", respInvalid.StatusCode)
	}

	// Verify no pending requests were persisted
	pendingReqs, _ := reqRepo.ListPendingRequests(ctx, 10, time.Now())
	if len(pendingReqs) != 0 {
		t.Fatalf("forged request was persisted in SQLite! Expected 0 pending, got %d", len(pendingReqs))
	}

	// Step 2: Compute authentic signature and send -> Must receive 200 OK and deliver
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	validSig := hex.EncodeToString(mac.Sum(nil))

	reqValid, _ := http.NewRequest(http.MethodPost, relayServer.URL+"/api/v1/webhook/"+route.Name, bytes.NewReader(payload))
	reqValid.Header.Set("X-Hub-Signature-256", "sha256="+validSig)
	respValid, err := http.DefaultClient.Do(reqValid)
	if err != nil {
		t.Fatalf("failed to execute valid request: %v", err)
	}
	defer respValid.Body.Close()
	if respValid.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for authentic signature, got %d", respValid.StatusCode)
	}

	// Wait for async delivery to destination
	select {
	case <-deliveredCh:
		// Succeeded
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for authentic webhook delivery")
	}

	mu.Lock()
	defer mu.Unlock()
	if !bytes.Equal(receivedBody, payload) {
		t.Fatalf("expected destination to receive %s, got %s", payload, receivedBody)
	}
}
