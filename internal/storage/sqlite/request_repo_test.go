package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

func setupTestDBWithRoute(t *testing.T) (*DB, *RouteRepo, *RequestRepo, *model.Route) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_req.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test sqlite: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate schema: %v", err)
	}

	routeRepo := NewRouteRepo(db)
	route := &model.Route{
		Name:                 "payment-service",
		Type:                 model.RouteTypeOutbound,
		Mode:                 model.RouteModeSync,
		Path:                 "/api/v1/relay/payment",
		DestinationURL:       "https://api.payment.internal/charge",
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := routeRepo.CreateRoute(ctx, route); err != nil {
		t.Fatalf("failed to create route for test: %v", err)
	}

	return db, routeRepo, NewRequestRepo(db), route
}

func TestRequestRepo_CRUD(t *testing.T) {
	db, _, reqRepo, route := setupTestDBWithRoute(t)
	defer db.Close()

	ctx := context.Background()

	req := &model.Request{
		RouteID: route.ID,
		Method:  "POST",
		URL:     "https://api.payment.internal/charge",
		Headers: map[string]string{
			"Content-Type": "application/json",
			"X-Custom-Key": "secret-value",
		},
		Body:   []byte(`{"amount": 1000, "currency": "usd"}`),
		Status: model.RequestStatusProcessing,
	}

	// 1. CreateRequest
	if err := reqRepo.CreateRequest(ctx, req); err != nil {
		t.Fatalf("CreateRequest failed: %v", err)
	}
	if req.ID == "" {
		t.Fatal("expected request ID to be populated")
	}

	// 2. GetRequestByID
	fetched, err := reqRepo.GetRequestByID(ctx, req.ID)
	if err != nil {
		t.Fatalf("GetRequestByID failed: %v", err)
	}
	if fetched.ID != req.ID {
		t.Errorf("expected ID %s, got %s", req.ID, fetched.ID)
	}
	if fetched.RouteID != route.ID {
		t.Errorf("expected RouteID %s, got %s", route.ID, fetched.RouteID)
	}
	if fetched.Method != "POST" {
		t.Errorf("expected Method POST, got %s", fetched.Method)
	}
	if string(fetched.Body) != string(req.Body) {
		t.Errorf("expected Body %s, got %s", string(req.Body), string(fetched.Body))
	}
	if fetched.Headers["X-Custom-Key"] != "secret-value" {
		t.Errorf("expected custom header preserved, got %v", fetched.Headers)
	}
	if fetched.Status != model.RequestStatusProcessing {
		t.Errorf("expected status processing, got %s", fetched.Status)
	}

	// 3. UpdateRequestStatus
	lastErr := "upstream timeout"
	nextAttempt := time.Now().UTC().Add(5 * time.Second)
	if err := reqRepo.UpdateRequestStatus(ctx, req.ID, model.RequestStatusFailed, 2, &nextAttempt, &lastErr); err != nil {
		t.Fatalf("UpdateRequestStatus failed: %v", err)
	}

	updated, err := reqRepo.GetRequestByID(ctx, req.ID)
	if err != nil {
		t.Fatalf("GetRequestByID after update failed: %v", err)
	}
	if updated.Status != model.RequestStatusFailed {
		t.Errorf("expected status failed, got %s", updated.Status)
	}
	if updated.AttemptsCount != 2 {
		t.Errorf("expected attempts_count 2, got %d", updated.AttemptsCount)
	}
	if updated.LastError == nil || *updated.LastError != lastErr {
		t.Errorf("expected last error %s, got %v", lastErr, updated.LastError)
	}

	// 4. Non-existent request returns ErrNotFound
	_, err = reqRepo.GetRequestByID(ctx, "nonexistent-id")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing request, got %v", err)
	}

	err = reqRepo.UpdateRequestStatus(ctx, "nonexistent-id", model.RequestStatusSuccess, 1, nil, nil)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound for update on missing request, got %v", err)
	}
}

func TestRequestRepo_BatchAndPending(t *testing.T) {
	db, _, reqRepo, route := setupTestDBWithRoute(t)
	defer db.Close()

	ctx := context.Background()

	now := time.Now().UTC()
	past := now.Add(-10 * time.Second)
	future := now.Add(10 * time.Minute)

	req1 := &model.Request{
		RouteID:       route.ID,
		Method:        "POST",
		URL:           "https://api.payment.internal/1",
		Headers:       map[string]string{"Content-Type": "application/json"},
		Body:          []byte(`{"item": 1}`),
		Status:        model.RequestStatusPending,
		NextAttemptAt: &past,
	}

	req2 := &model.Request{
		RouteID:       route.ID,
		Method:        "POST",
		URL:           "https://api.payment.internal/2",
		Headers:       map[string]string{"Content-Type": "application/json"},
		Body:          []byte(`{"item": 2}`),
		Status:        model.RequestStatusPending,
		NextAttemptAt: &future,
	}

	if err := reqRepo.CreateRequestsBatch(ctx, []*model.Request{req1, req2}); err != nil {
		t.Fatalf("CreateRequestsBatch failed: %v", err)
	}

	// Query pending jobs due before now
	pending, err := reqRepo.ListPendingRequests(ctx, 10, now)
	if err != nil {
		t.Fatalf("ListPendingRequests failed: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 due request, got %d", len(pending))
	}
	if pending[0].ID != req1.ID {
		t.Errorf("expected req1 due, got %s", pending[0].ID)
	}
}

func TestRequestRepo_ClaimPendingRequests(t *testing.T) {
	db, _, reqRepo, route := setupTestDBWithRoute(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	past1 := now.Add(-2 * time.Minute)
	past2 := now.Add(-1 * time.Minute)
	future := now.Add(5 * time.Minute)

	req1 := &model.Request{
		RouteID:       route.ID,
		Method:        "POST",
		URL:           "https://api.payment.internal/claim1",
		Headers:       map[string]string{"Content-Type": "application/json"},
		Body:          []byte(`{"claim": 1}`),
		Status:        model.RequestStatusPending,
		NextAttemptAt: &past1,
	}
	req2 := &model.Request{
		RouteID:       route.ID,
		Method:        "POST",
		URL:           "https://api.payment.internal/claim2",
		Headers:       map[string]string{"Content-Type": "application/json"},
		Body:          []byte(`{"claim": 2}`),
		Status:        model.RequestStatusFailed,
		NextAttemptAt: &past2,
	}
	req3 := &model.Request{
		RouteID:       route.ID,
		Method:        "POST",
		URL:           "https://api.payment.internal/claim3",
		Headers:       map[string]string{"Content-Type": "application/json"},
		Body:          []byte(`{"claim": 3}`),
		Status:        model.RequestStatusPending,
		NextAttemptAt: &future,
	}

	if err := reqRepo.CreateRequestsBatch(ctx, []*model.Request{req1, req2, req3}); err != nil {
		t.Fatalf("batch insert failed: %v", err)
	}

	// Claim with limit 1
	claimed, err := reqRepo.ClaimPendingRequests(ctx, 1, now)
	if err != nil {
		t.Fatalf("ClaimPendingRequests failed: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("expected 1 claimed job, got %d", len(claimed))
	}
	if claimed[0].ID != req1.ID {
		t.Errorf("expected oldest due job %s, got %s", req1.ID, claimed[0].ID)
	}
	if claimed[0].Status != model.RequestStatusProcessing {
		t.Errorf("expected status 'processing', got %s", claimed[0].Status)
	}

	// Verify in DB that req1 is actually marked as processing
	inDB, err := reqRepo.GetRequestByID(ctx, req1.ID)
	if err != nil {
		t.Fatalf("GetRequestByID failed: %v", err)
	}
	if inDB.Status != model.RequestStatusProcessing {
		t.Errorf("expected req1 in DB to be 'processing', got %s", inDB.Status)
	}

	// Claim remaining due (should be req2)
	claimed2, err := reqRepo.ClaimPendingRequests(ctx, 10, now)
	if err != nil {
		t.Fatalf("ClaimPendingRequests second round failed: %v", err)
	}
	if len(claimed2) != 1 {
		t.Fatalf("expected 1 claimed job, got %d", len(claimed2))
	}
	if claimed2[0].ID != req2.ID {
		t.Errorf("expected req2 claimed, got %s", claimed2[0].ID)
	}

	// Third claim should return 0 (req3 is in future)
	claimed3, err := reqRepo.ClaimPendingRequests(ctx, 10, now)
	if err != nil {
		t.Fatalf("ClaimPendingRequests third round failed: %v", err)
	}
	if len(claimed3) != 0 {
		t.Errorf("expected 0 claimed jobs, got %d", len(claimed3))
	}
}

func TestRequestRepo_RecoverStuckRequests(t *testing.T) {
	db, _, reqRepo, route := setupTestDBWithRoute(t)
	defer db.Close()

	ctx := context.Background()
	req := &model.Request{
		RouteID: route.ID,
		Method:  "POST",
		URL:     "https://api.payment.internal/stuck",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    []byte(`{"stuck": true}`),
		Status:  model.RequestStatusProcessing,
	}
	if err := reqRepo.CreateRequest(ctx, req); err != nil {
		t.Fatalf("CreateRequest failed: %v", err)
	}

	// Recover with stuckBefore in future -> should recover req
	future := time.Now().UTC().Add(10 * time.Minute)
	recovered, err := reqRepo.RecoverStuckRequests(ctx, future)
	if err != nil {
		t.Fatalf("RecoverStuckRequests failed: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("expected 1 recovered request, got %d", recovered)
	}

	inDB, err := reqRepo.GetRequestByID(ctx, req.ID)
	if err != nil {
		t.Fatalf("GetRequestByID failed: %v", err)
	}
	if inDB.Status != model.RequestStatusPending {
		t.Errorf("expected recovered request status to be 'pending', got %s", inDB.Status)
	}
}

func TestRequestRepo_ListDLQRequests(t *testing.T) {
	db, routeRepo, reqRepo, route1 := setupTestDBWithRoute(t)
	defer db.Close()

	ctx := context.Background()

	// Create route2
	route2 := &model.Route{
		Name:                 "route-two",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/route-two",
		DestinationURL:       "https://api.internal/webhook",
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := routeRepo.CreateRoute(ctx, route2); err != nil {
		t.Fatalf("failed to create route2: %v", err)
	}

	// Create requests: 2 for route1 in DLQ, 1 for route2 in DLQ, 1 for route1 in success
	req1 := &model.Request{
		RouteID:       route1.ID,
		Method:        "POST",
		URL:           "https://dest1",
		Headers:       map[string]string{},
		Body:          []byte("body1"),
		Status:        model.RequestStatusDLQ,
		AttemptsCount: 3,
	}
	req2 := &model.Request{
		RouteID:       route1.ID,
		Method:        "POST",
		URL:           "https://dest1",
		Headers:       map[string]string{},
		Body:          []byte("body2"),
		Status:        model.RequestStatusDLQ,
		AttemptsCount: 3,
	}
	req3 := &model.Request{
		RouteID:       route2.ID,
		Method:        "POST",
		URL:           "https://dest2",
		Headers:       map[string]string{},
		Body:          []byte("body3"),
		Status:        model.RequestStatusDLQ,
		AttemptsCount: 3,
	}
	reqSuccess := &model.Request{
		RouteID:       route1.ID,
		Method:        "POST",
		URL:           "https://dest1",
		Headers:       map[string]string{},
		Body:          []byte("body4"),
		Status:        model.RequestStatusSuccess,
		AttemptsCount: 1,
	}

	for _, r := range []*model.Request{req1, req2, req3, reqSuccess} {
		if err := reqRepo.CreateRequest(ctx, r); err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
	}

	// 1. List all DLQ (no route filter)
	items, total, err := reqRepo.ListDLQRequests(ctx, "", 10, 0)
	if err != nil {
		t.Fatalf("ListDLQRequests failed: %v", err)
	}
	if total != 3 {
		t.Errorf("expected total 3 DLQ records, got %d", total)
	}
	if len(items) != 3 {
		t.Errorf("expected 3 items returned, got %d", len(items))
	}

	// 2. Filter by route1
	itemsR1, totalR1, err := reqRepo.ListDLQRequests(ctx, route1.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListDLQRequests with route1 filter failed: %v", err)
	}
	if totalR1 != 2 {
		t.Errorf("expected total 2 DLQ records for route1, got %d", totalR1)
	}
	if len(itemsR1) != 2 {
		t.Errorf("expected 2 items for route1, got %d", len(itemsR1))
	}

	// 3. Pagination limit and offset
	pagedItems, pagedTotal, err := reqRepo.ListDLQRequests(ctx, "", 1, 1)
	if err != nil {
		t.Fatalf("ListDLQRequests paged failed: %v", err)
	}
	if pagedTotal != 3 {
		t.Errorf("expected total 3, got %d", pagedTotal)
	}
	if len(pagedItems) != 1 {
		t.Errorf("expected 1 item, got %d", len(pagedItems))
	}
}

func TestRequestRepo_ReplayDLQRequest(t *testing.T) {
	db, _, reqRepo, route := setupTestDBWithRoute(t)
	defer db.Close()

	ctx := context.Background()
	errMsg := "failed after 3 attempts"
	reqDLQ := &model.Request{
		RouteID:       route.ID,
		Method:        "POST",
		URL:           "https://dest",
		Headers:       map[string]string{"Content-Type": "application/json"},
		Body:          []byte("payload"),
		Status:        model.RequestStatusDLQ,
		AttemptsCount: 3,
		LastError:     &errMsg,
	}
	if err := reqRepo.CreateRequest(ctx, reqDLQ); err != nil {
		t.Fatalf("CreateRequest failed: %v", err)
	}

	reqSuccess := &model.Request{
		RouteID:       route.ID,
		Method:        "POST",
		URL:           "https://dest",
		Headers:       map[string]string{},
		Body:          []byte("payload2"),
		Status:        model.RequestStatusSuccess,
		AttemptsCount: 1,
	}
	if err := reqRepo.CreateRequest(ctx, reqSuccess); err != nil {
		t.Fatalf("CreateRequest failed: %v", err)
	}

	// 1. Replay DLQ request successfully
	replayed, err := reqRepo.ReplayDLQRequest(ctx, reqDLQ.ID)
	if err != nil {
		t.Fatalf("ReplayDLQRequest failed: %v", err)
	}
	if replayed.Status != model.RequestStatusPending {
		t.Errorf("expected status 'pending', got %s", replayed.Status)
	}
	if replayed.AttemptsCount != 0 {
		t.Errorf("expected attempts_count 0, got %d", replayed.AttemptsCount)
	}
	if replayed.LastError != nil {
		t.Errorf("expected last_error to be nil, got %v", *replayed.LastError)
	}
	if replayed.NextAttemptAt == nil || replayed.NextAttemptAt.IsZero() {
		t.Errorf("expected next_attempt_at to be set, got %v", replayed.NextAttemptAt)
	}

	// 2. Replay non-DLQ request should return ErrConflict
	_, err = reqRepo.ReplayDLQRequest(ctx, reqSuccess.ID)
	if !errors.Is(err, storage.ErrConflict) {
		t.Errorf("expected ErrConflict for non-DLQ request, got %v", err)
	}

	// 3. Replay non-existent request should return ErrNotFound
	_, err = reqRepo.ReplayDLQRequest(ctx, "01nonexistentulid0000000000")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound for non-existent request, got %v", err)
	}
}

func TestRequestRepo_ReplayAllDLQRequests(t *testing.T) {
	db, routeRepo, reqRepo, route1 := setupTestDBWithRoute(t)
	defer db.Close()

	ctx := context.Background()

	route2 := &model.Route{
		Name:                 "route-bulk",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/bulk",
		DestinationURL:       "https://api.internal/webhook",
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := routeRepo.CreateRoute(ctx, route2); err != nil {
		t.Fatalf("failed to create route2: %v", err)
	}

	for i := 0; i < 5; i++ {
		rID := route1.ID
		if i >= 3 {
			rID = route2.ID
		}
		req := &model.Request{
			RouteID:       rID,
			Method:        "POST",
			URL:           "https://dest",
			Headers:       map[string]string{},
			Body:          []byte("body"),
			Status:        model.RequestStatusDLQ,
			AttemptsCount: 3,
		}
		if err := reqRepo.CreateRequest(ctx, req); err != nil {
			t.Fatalf("failed to create dlq req %d: %v", i, err)
		}
	}

	// 1. Replay with route filter (route2 has 2 records)
	replayedRoute2, err := reqRepo.ReplayAllDLQRequests(ctx, route2.ID, 10)
	if err != nil {
		t.Fatalf("ReplayAllDLQRequests for route2 failed: %v", err)
	}
	if len(replayedRoute2) != 2 {
		t.Fatalf("expected 2 records replayed for route2, got %d", len(replayedRoute2))
	}

	// 2. Replay with limit = 1 for route1 (which currently has 3 records)
	replayedRoute1Limited, err := reqRepo.ReplayAllDLQRequests(ctx, route1.ID, 1)
	if err != nil {
		t.Fatalf("ReplayAllDLQRequests limited failed: %v", err)
	}
	if len(replayedRoute1Limited) != 1 {
		t.Fatalf("expected 1 record replayed with limit 1, got %d", len(replayedRoute1Limited))
	}

	// 3. Replay remaining (2 left in route1)
	replayedRemaining, err := reqRepo.ReplayAllDLQRequests(ctx, "", 10)
	if err != nil {
		t.Fatalf("ReplayAllDLQRequests remaining failed: %v", err)
	}
	if len(replayedRemaining) != 2 {
		t.Fatalf("expected 2 remaining records replayed, got %d", len(replayedRemaining))
	}

	// 4. Replay again when 0 DLQ left
	replayedEmpty, err := reqRepo.ReplayAllDLQRequests(ctx, "", 10)
	if err != nil {
		t.Fatalf("ReplayAllDLQRequests empty failed: %v", err)
	}
	if len(replayedEmpty) != 0 {
		t.Fatalf("expected 0 records replayed, got %d", len(replayedEmpty))
	}
}

func TestRequestRepo_ListRequests(t *testing.T) {
	db, routeRepo, reqRepo, route1 := setupTestDBWithRoute(t)
	defer db.Close()

	ctx := context.Background()

	// Create route2
	route2 := &model.Route{
		Name:                 "analytics-service",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/analytics",
		DestinationURL:       "https://api.analytics.internal/events",
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyLinear,
	}
	if err := routeRepo.CreateRoute(ctx, route2); err != nil {
		t.Fatalf("failed to create route2: %v", err)
	}

	// Insert 3 requests for route1: 2 success, 1 dlq
	req1 := &model.Request{
		ID:      "req_01_success",
		RouteID: route1.ID,
		Method:  "POST",
		URL:     "https://example.com/1",
		Status:  model.RequestStatusSuccess,
		Body:    []byte("heavy blob 1"),
	}
	req2 := &model.Request{
		ID:      "req_02_success",
		RouteID: route1.ID,
		Method:  "POST",
		URL:     "https://example.com/2",
		Status:  model.RequestStatusSuccess,
		Body:    []byte("heavy blob 2"),
	}
	req3 := &model.Request{
		ID:      "req_03_dlq",
		RouteID: route1.ID,
		Method:  "POST",
		URL:     "https://example.com/3",
		Status:  model.RequestStatusDLQ,
		Body:    []byte("heavy blob 3"),
	}

	// Insert 2 requests for route2: 1 pending, 1 failed
	req4 := &model.Request{
		ID:      "req_04_pending",
		RouteID: route2.ID,
		Method:  "POST",
		URL:     "https://example.com/4",
		Status:  model.RequestStatusPending,
		Body:    []byte("heavy blob 4"),
	}
	req5 := &model.Request{
		ID:      "req_05_failed",
		RouteID: route2.ID,
		Method:  "POST",
		URL:     "https://example.com/5",
		Status:  model.RequestStatusFailed,
		Body:    []byte("heavy blob 5"),
	}

	for _, r := range []*model.Request{req1, req2, req3, req4, req5} {
		if err := reqRepo.CreateRequest(ctx, r); err != nil {
			t.Fatalf("failed to insert request %s: %v", r.ID, err)
		}
	}

	// 1. List all requests
	items, total, err := reqRepo.ListRequests(ctx, storage.RequestFilter{})
	if err != nil {
		t.Fatalf("ListRequests all failed: %v", err)
	}
	if total != 5 || len(items) != 5 {
		t.Fatalf("expected total 5 and len 5, got total %d, len %d", total, len(items))
	}
	// Verify route name joined correctly
	if items[0].RouteName == "" {
		t.Errorf("expected RouteName to be populated, got empty")
	}

	// 2. Filter by Status = success
	successItems, successTotal, err := reqRepo.ListRequests(ctx, storage.RequestFilter{
		Status: "success",
	})
	if err != nil {
		t.Fatalf("ListRequests status failed: %v", err)
	}
	if successTotal != 2 || len(successItems) != 2 {
		t.Fatalf("expected total 2 success, got total %d, len %d", successTotal, len(successItems))
	}

	// 3. Filter by RouteID = route2
	route2Items, route2Total, err := reqRepo.ListRequests(ctx, storage.RequestFilter{
		RouteID: route2.ID,
	})
	if err != nil {
		t.Fatalf("ListRequests route2 failed: %v", err)
	}
	if route2Total != 2 || len(route2Items) != 2 {
		t.Fatalf("expected total 2 for route2, got total %d, len %d", route2Total, len(route2Items))
	}

	// 4. Search by ID prefix
	searchItems, searchTotal, err := reqRepo.ListRequests(ctx, storage.RequestFilter{
		Search: "req_03",
	})
	if err != nil {
		t.Fatalf("ListRequests search failed: %v", err)
	}
	if searchTotal != 1 || len(searchItems) != 1 || searchItems[0].ID != "req_03_dlq" {
		t.Fatalf("expected 1 search item req_03_dlq, got %v", searchItems)
	}

	// 5. Pagination (Limit 2, Offset 1)
	pagedItems, pagedTotal, err := reqRepo.ListRequests(ctx, storage.RequestFilter{
		Limit:  2,
		Offset: 1,
	})
	if err != nil {
		t.Fatalf("ListRequests pagination failed: %v", err)
	}
	if pagedTotal != 5 {
		t.Fatalf("expected total 5 for paged query, got %d", pagedTotal)
	}
	if len(pagedItems) != 2 {
		t.Fatalf("expected 2 paged items, got %d", len(pagedItems))
	}
}

func TestRequestRepo_GetQueueStats(t *testing.T) {
	db, _, reqRepo, route := setupTestDBWithRoute(t)
	defer db.Close()

	ctx := context.Background()

	// Initial stats should be zero
	p, inflight, err := reqRepo.GetQueueStats(ctx)
	if err != nil {
		t.Fatalf("failed to get initial queue stats: %v", err)
	}
	if p != 0 || inflight != 0 {
		t.Fatalf("expected 0/0, got pending=%d, inFlight=%d", p, inflight)
	}

	// Insert 2 pending, 1 processing, 1 success, 1 failed
	reqs := []*model.Request{
		{RouteID: route.ID, Method: "POST", URL: "http://example.com/1", Status: model.RequestStatusPending, Headers: map[string]string{}},
		{RouteID: route.ID, Method: "POST", URL: "http://example.com/2", Status: model.RequestStatusPending, Headers: map[string]string{}},
		{RouteID: route.ID, Method: "POST", URL: "http://example.com/3", Status: model.RequestStatusProcessing, Headers: map[string]string{}},
		{RouteID: route.ID, Method: "POST", URL: "http://example.com/4", Status: model.RequestStatusSuccess, Headers: map[string]string{}},
		{RouteID: route.ID, Method: "POST", URL: "http://example.com/5", Status: model.RequestStatusFailed, Headers: map[string]string{}},
	}
	for _, req := range reqs {
		if err := reqRepo.CreateRequest(ctx, req); err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
	}

	p, inflight, err = reqRepo.GetQueueStats(ctx)
	if err != nil {
		t.Fatalf("failed to get queue stats: %v", err)
	}
	if p != 2 || inflight != 1 {
		t.Fatalf("expected pending=2, inFlight=1, got pending=%d, inFlight=%d", p, inflight)
	}
}
