package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/config"
	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage/sqlite"
)

func TestRequestsHandler_ListAndDetail(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_req_handler.db")

	db, err := sqlite.Open(dbPath, "NORMAL")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}

	route := &model.Route{
		Name:                 "order-service",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/orders",
		DestinationURL:       "https://orders.internal/webhook",
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := db.RouteRepo.CreateRoute(ctx, route); err != nil {
		t.Fatalf("failed to create route: %v", err)
	}

	req1 := &model.Request{
		ID:      "01j9y9req001",
		RouteID: route.ID,
		Method:  "POST",
		URL:     "https://orders.internal/webhook",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    []byte(`{"order_id": 1001}`),
		Status:  model.RequestStatusSuccess,
	}
	req2 := &model.Request{
		ID:      "01j9y9req002",
		RouteID: route.ID,
		Method:  "POST",
		URL:     "https://orders.internal/webhook",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    []byte(`{"order_id": 1002}`),
		Status:  model.RequestStatusDLQ,
	}
	if err := db.RequestRepo.CreateRequest(ctx, req1); err != nil {
		t.Fatalf("failed to create req1: %v", err)
	}
	if err := db.RequestRepo.CreateRequest(ctx, req2); err != nil {
		t.Fatalf("failed to create req2: %v", err)
	}

	// Record an attempt for req2
	attempt := &model.Attempt{
		RequestID:          req2.ID,
		AttemptNumber:      1,
		ResponseStatusCode: func(i int) *int { return &i }(500),
		ResponseBody:       func(s string) *string { return &s }("Internal Server Error"),
		DurationMS:         120,
	}
	if err := db.AttemptRepo.RecordAttempt(ctx, attempt); err != nil {
		t.Fatalf("failed to record attempt: %v", err)
	}

	cfg := &config.Config{Port: 8080}
	router := withLoopbackRemoteAddr(NewRouter(cfg, nil, RouterOptions{
		RequestRepo: db.RequestRepo,
		AttemptRepo: db.AttemptRepo,
	}))

	// 1. List all requests
	req := httptest.NewRequest(http.MethodGet, "/api/v1/requests", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var listResp RequestsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}
	if listResp.Total != 2 || len(listResp.Items) != 2 {
		t.Fatalf("expected 2 requests, got total %d, len %d", listResp.Total, len(listResp.Items))
	}

	// 2. Filter by status = dlq
	reqDLQ := httptest.NewRequest(http.MethodGet, "/api/v1/requests?status=dlq", nil)
	recDLQ := httptest.NewRecorder()
	router.ServeHTTP(recDLQ, reqDLQ)

	if recDLQ.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", recDLQ.Code)
	}

	var dlqListResp RequestsListResponse
	if err := json.NewDecoder(recDLQ.Body).Decode(&dlqListResp); err != nil {
		t.Fatalf("failed to decode dlq list: %v", err)
	}
	if dlqListResp.Total != 1 || len(dlqListResp.Items) != 1 || dlqListResp.Items[0].ID != req2.ID {
		t.Fatalf("unexpected dlq item: %+v", dlqListResp)
	}

	// 3. Get request detail for req2
	detailReq := httptest.NewRequest(http.MethodGet, "/api/v1/requests/"+req2.ID, nil)
	detailRec := httptest.NewRecorder()
	router.ServeHTTP(detailRec, detailReq)

	if detailRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on detail, got %d", detailRec.Code)
	}

	var detailResp RequestDetailResponse
	if err := json.NewDecoder(detailRec.Body).Decode(&detailResp); err != nil {
		t.Fatalf("failed to decode detail response: %v", err)
	}
	if detailResp.ID != req2.ID || detailResp.Body != `{"order_id": 1002}` {
		t.Fatalf("unexpected detail body: %s", detailResp.Body)
	}
	if len(detailResp.Attempts) != 1 || *detailResp.Attempts[0].ResponseStatusCode != 500 {
		t.Fatalf("unexpected attempts in detail: %+v", detailResp.Attempts)
	}

	// 4. Non-existent request returns 404
	missingReq := httptest.NewRequest(http.MethodGet, "/api/v1/requests/non_existent", nil)
	missingRec := httptest.NewRecorder()
	router.ServeHTTP(missingRec, missingReq)

	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing request, got %d", missingRec.Code)
	}
}

func TestRequestsHandler_Replay(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_req_replay.db")

	db, err := sqlite.Open(dbPath, "NORMAL")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}

	route := &model.Route{
		Name:                 "payment-service",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/payments",
		DestinationURL:       "https://payments.internal/webhook",
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := db.RouteRepo.CreateRoute(ctx, route); err != nil {
		t.Fatalf("failed to create route: %v", err)
	}

	reqSuccess := &model.Request{
		ID:            "01j9y9success001",
		RouteID:       route.ID,
		Method:        "POST",
		URL:           "https://payments.internal/webhook",
		Headers:       map[string]string{"Content-Type": "application/json"},
		Body:          []byte(`{"payment_id": "pay_1"}`),
		Status:        model.RequestStatusSuccess,
		AttemptsCount: 1,
	}
	reqPending := &model.Request{
		ID:            "01j9y9pending001",
		RouteID:       route.ID,
		Method:        "POST",
		URL:           "https://payments.internal/webhook",
		Headers:       map[string]string{"Content-Type": "application/json"},
		Body:          []byte(`{"payment_id": "pay_2"}`),
		Status:        model.RequestStatusPending,
		AttemptsCount: 0,
	}
	if err := db.RequestRepo.CreateRequest(ctx, reqSuccess); err != nil {
		t.Fatalf("failed to create reqSuccess: %v", err)
	}
	if err := db.RequestRepo.CreateRequest(ctx, reqPending); err != nil {
		t.Fatalf("failed to create reqPending: %v", err)
	}

	wakeCalled := false
	wakeFn := func() {
		wakeCalled = true
	}

	cfg := &config.Config{Port: 8080}
	router := withLoopbackRemoteAddr(NewRouter(cfg, nil, RouterOptions{
		RequestRepo: db.RequestRepo,
		AttemptRepo: db.AttemptRepo,
		WakeFn:      wakeFn,
	}))

	// 1. Replay success request -> returns 200, triggers wakeFn
	replayReq := httptest.NewRequest(http.MethodPost, "/api/v1/requests/"+reqSuccess.ID+"/replay", nil)
	replayRec := httptest.NewRecorder()
	router.ServeHTTP(replayRec, replayReq)

	if replayRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on replay, got %d: %s", replayRec.Code, replayRec.Body.String())
	}

	var replayResp RequestReplayResponse
	if err := json.NewDecoder(replayRec.Body).Decode(&replayResp); err != nil {
		t.Fatalf("failed to decode replay response: %v", err)
	}
	if replayResp.ID != reqSuccess.ID || replayResp.Status != "pending" || !replayResp.Replayed || replayResp.ReplayedAt == "" {
		t.Fatalf("unexpected replay response: %+v", replayResp)
	}
	if !wakeCalled {
		t.Fatalf("expected wakeFn to be called on replay")
	}

	// Verify database record transitioned to pending
	refreshed, err := db.RequestRepo.GetRequestByID(ctx, reqSuccess.ID)
	if err != nil {
		t.Fatalf("failed to reload replayed request: %v", err)
	}
	if refreshed.Status != model.RequestStatusPending || refreshed.AttemptsCount != 0 {
		t.Fatalf("expected status pending and attempts_count 0, got %s and %d", refreshed.Status, refreshed.AttemptsCount)
	}

	// 2. Replay pending request -> returns 409 Conflict
	conflictReq := httptest.NewRequest(http.MethodPost, "/api/v1/requests/"+reqPending.ID+"/replay", nil)
	conflictRec := httptest.NewRecorder()
	router.ServeHTTP(conflictRec, conflictReq)

	if conflictRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for pending request, got %d: %s", conflictRec.Code, conflictRec.Body.String())
	}

	// 3. Replay non-existent request -> returns 404 Not Found
	missingReq := httptest.NewRequest(http.MethodPost, "/api/v1/requests/01nonexistentulid0000000000/replay", nil)
	missingRec := httptest.NewRecorder()
	router.ServeHTTP(missingRec, missingReq)

	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing request, got %d: %s", missingRec.Code, missingRec.Body.String())
	}
}

func TestRequestsHandler_Replay_AuthEnforced(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_req_replay_auth.db")

	db, err := sqlite.Open(dbPath, "NORMAL")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}

	// Setup API key
	apiKey := &model.APIKey{
		ID:        model.NewULID(),
		Name:      "test-key",
		KeyHash:   HashKey("sr_live_secret123"),
		MaskedKey: "sr_live_...t123",
		CreatedAt: time.Now().UTC(),
	}
	if err := db.APIKeyRepo.CreateAPIKey(ctx, apiKey); err != nil {
		t.Fatalf("failed to create api key: %v", err)
	}

	route := &model.Route{
		Name:                 "route-auth",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/auth",
		DestinationURL:       "https://auth.internal/webhook",
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := db.RouteRepo.CreateRoute(ctx, route); err != nil {
		t.Fatalf("failed to create route: %v", err)
	}

	req := &model.Request{
		ID:            "01j9y9authreq001",
		RouteID:       route.ID,
		Method:        "POST",
		URL:           "https://auth.internal/webhook",
		Headers:       map[string]string{},
		Body:          []byte(`{}`),
		Status:        model.RequestStatusSuccess,
		AttemptsCount: 1,
	}
	if err := db.RequestRepo.CreateRequest(ctx, req); err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	cfg := &config.Config{
		Port:          8080,
		AdminPassword: "supersecretadmin",
	}
	// Note: Remote IP (non-loopback) to trigger auth check
	router := NewRouter(cfg, nil, RouterOptions{
		RequestRepo: db.RequestRepo,
		AttemptRepo: db.AttemptRepo,
		APIKeyRepo:  db.APIKeyRepo,
	})

	// 1. Unauthenticated request from external IP -> 401 Unauthorized
	unauthReq := httptest.NewRequest(http.MethodPost, "/api/v1/requests/"+req.ID+"/replay", nil)
	unauthReq.RemoteAddr = "192.168.1.100:12345"
	unauthRec := httptest.NewRecorder()
	router.ServeHTTP(unauthRec, unauthReq)

	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for unauth replay, got %d", unauthRec.Code)
	}

	// 2. Authenticated request with Bearer API Key -> 200 OK
	authReq := httptest.NewRequest(http.MethodPost, "/api/v1/requests/"+req.ID+"/replay", nil)
	authReq.RemoteAddr = "192.168.1.100:12345"
	authReq.Header.Set("Authorization", "Bearer sr_live_secret123")
	authRec := httptest.NewRecorder()
	router.ServeHTTP(authRec, authReq)

	if authRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for authenticated replay, got %d: %s", authRec.Code, authRec.Body.String())
	}
}
