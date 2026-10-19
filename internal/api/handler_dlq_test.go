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

func setupTestDLQServer(t *testing.T) (*sqlite.DB, *engine.RouteRegistry, *int32, http.Handler, *model.Route, *model.Route) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_dlq.db")

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
	route1 := &model.Route{
		Name:                 "route-one",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/route-one",
		DestinationURL:       "https://dest1.internal/webhook",
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := registry.Create(ctx, route1); err != nil {
		t.Fatalf("failed to create route1: %v", err)
	}

	route2 := &model.Route{
		Name:                 "route-two",
		Type:                 model.RouteTypeOutbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/jobs/route-two",
		DestinationURL:       "https://dest2.internal/jobs",
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := registry.Create(ctx, route2); err != nil {
		t.Fatalf("failed to create route2: %v", err)
	}

	var wakeCalls int32
	wakeFn := func() {
		atomic.AddInt32(&wakeCalls, 1)
	}

	cfg := &config.Config{
		Port:     8080,
		LogLevel: "debug",
	}

	handler := withLoopbackRemoteAddr(NewRouter(cfg, nil, RouterOptions{
		RouteRegistry: registry,
		RequestRepo:   reqRepo,
		AttemptRepo:   attemptRepo,
		WakeFn:        wakeFn,
	}))

	return db, registry, &wakeCalls, handler, route1, route2
}

func TestDLQHandler_ListDLQ(t *testing.T) {
	db, _, _, handler, route1, route2 := setupTestDLQServer(t)
	defer db.Close()

	ctx := context.Background()
	reqRepo := db.RequestRepo

	// Empty list check
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dlq", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for empty list, got %d", rec.Code)
	}
	var emptyResp DLQListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &emptyResp); err != nil {
		t.Fatalf("failed to parse empty list response: %v", err)
	}
	if emptyResp.Total != 0 || len(emptyResp.Items) != 0 {
		t.Fatalf("expected 0 items, got total=%d items=%d", emptyResp.Total, len(emptyResp.Items))
	}

	// Insert 2 records in DLQ for route1, 1 for route2, and 1 success record
	dlq1 := &model.Request{
		RouteID:       route1.ID,
		Method:        "POST",
		URL:           route1.DestinationURL,
		Headers:       map[string]string{},
		Body:          []byte("body1"),
		Status:        model.RequestStatusDLQ,
		AttemptsCount: 3,
	}
	dlq2 := &model.Request{
		RouteID:       route1.ID,
		Method:        "POST",
		URL:           route1.DestinationURL,
		Headers:       map[string]string{},
		Body:          []byte("body2"),
		Status:        model.RequestStatusDLQ,
		AttemptsCount: 3,
	}
	dlq3 := &model.Request{
		RouteID:       route2.ID,
		Method:        "POST",
		URL:           route2.DestinationURL,
		Headers:       map[string]string{},
		Body:          []byte("body3"),
		Status:        model.RequestStatusDLQ,
		AttemptsCount: 3,
	}
	successReq := &model.Request{
		RouteID:       route1.ID,
		Method:        "POST",
		URL:           route1.DestinationURL,
		Headers:       map[string]string{},
		Body:          []byte("body4"),
		Status:        model.RequestStatusSuccess,
		AttemptsCount: 1,
	}

	for _, r := range []*model.Request{dlq1, dlq2, dlq3, successReq} {
		if err := reqRepo.CreateRequest(ctx, r); err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
	}

	// 1. List all DLQ (should return 3)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/dlq", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	var allResp DLQListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &allResp); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}
	if allResp.Total != 3 || len(allResp.Items) != 3 {
		t.Errorf("expected 3 total items, got %d", allResp.Total)
	}

	// 2. Filter by route_name
	req = httptest.NewRequest(http.MethodGet, "/api/v1/dlq?route_name=route-one", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	var route1Resp DLQListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &route1Resp); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}
	if route1Resp.Total != 2 || len(route1Resp.Items) != 2 {
		t.Errorf("expected 2 total items for route-one, got %d", route1Resp.Total)
	}

	// 3. Filter by unknown route_name -> 404
	req = httptest.NewRequest(http.MethodGet, "/api/v1/dlq?route_name=unknown-route", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown route, got %d", rec.Code)
	}

	// 4. Invalid limit / offset -> 400
	req = httptest.NewRequest(http.MethodGet, "/api/v1/dlq?limit=-5", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for negative limit, got %d", rec.Code)
	}
}

func TestDLQHandler_ReplaySingle(t *testing.T) {
	db, _, wakeCalls, handler, route1, _ := setupTestDLQServer(t)
	defer db.Close()

	ctx := context.Background()
	reqRepo := db.RequestRepo

	errMsg := "destination offline"
	dlqReq := &model.Request{
		RouteID:       route1.ID,
		Method:        "POST",
		URL:           route1.DestinationURL,
		Headers:       map[string]string{"Content-Type": "application/json"},
		Body:          []byte("payload"),
		Status:        model.RequestStatusDLQ,
		AttemptsCount: 3,
		LastError:     &errMsg,
	}
	if err := reqRepo.CreateRequest(ctx, dlqReq); err != nil {
		t.Fatalf("failed to create dlq req: %v", err)
	}

	successReq := &model.Request{
		RouteID:       route1.ID,
		Method:        "POST",
		URL:           route1.DestinationURL,
		Headers:       map[string]string{},
		Body:          []byte("payload"),
		Status:        model.RequestStatusSuccess,
		AttemptsCount: 1,
	}
	if err := reqRepo.CreateRequest(ctx, successReq); err != nil {
		t.Fatalf("failed to create success req: %v", err)
	}

	// 1. Replay DLQ item successfully
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dlq/"+dlqReq.ID+"/replay", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var replayResp ReplaySingleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &replayResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if replayResp.ID != dlqReq.ID || replayResp.Status != "pending" || !replayResp.Replayed {
		t.Errorf("unexpected replay response: %+v", replayResp)
	}

	if atomic.LoadInt32(wakeCalls) != 1 {
		t.Errorf("expected WakeFn to be called once, got %d", atomic.LoadInt32(wakeCalls))
	}

	// Verify request state in database
	inDB, err := reqRepo.GetRequestByID(ctx, dlqReq.ID)
	if err != nil {
		t.Fatalf("failed to get request in DB: %v", err)
	}
	if inDB.Status != model.RequestStatusPending || inDB.AttemptsCount != 0 || inDB.LastError != nil {
		t.Errorf("db record not properly reset: status=%s, attempts=%d, lastError=%v", inDB.Status, inDB.AttemptsCount, inDB.LastError)
	}

	// 2. Replay again (now status is pending, not dlq) -> 409 Conflict
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/dlq/"+dlqReq.ID+"/replay", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for non-DLQ request, got %d", rec2.Code)
	}

	// 3. Replay non-existent item -> 404 Not Found
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/dlq/01nonexistent00000000000000/replay", nil)
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %d", rec3.Code)
	}
}

func TestDLQHandler_ReplayAll(t *testing.T) {
	db, _, wakeCalls, handler, route1, route2 := setupTestDLQServer(t)
	defer db.Close()

	ctx := context.Background()
	reqRepo := db.RequestRepo

	// Insert 3 records for route1, 2 for route2
	for i := 0; i < 5; i++ {
		rID := route1.ID
		if i >= 3 {
			rID = route2.ID
		}
		dlqReq := &model.Request{
			RouteID:       rID,
			Method:        "POST",
			URL:           "https://dest",
			Headers:       map[string]string{},
			Body:          []byte("payload"),
			Status:        model.RequestStatusDLQ,
			AttemptsCount: 3,
		}
		if err := reqRepo.CreateRequest(ctx, dlqReq); err != nil {
			t.Fatalf("failed to create dlq req: %v", err)
		}
	}

	// 1. Replay with route filter (route-two has 2 records)
	body, _ := json.Marshal(ReplayAllRequest{RouteName: "route-two"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dlq/replay-all", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp1 ReplayAllResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp1.ReplayedCount != 2 || len(resp1.RequestIDs) != 2 {
		t.Errorf("expected 2 replayed, got %d", resp1.ReplayedCount)
	}

	// 2. Replay with limit = 1 for route-one (which has 3 records)
	bodyLimit, _ := json.Marshal(ReplayAllRequest{RouteName: "route-one", Limit: 1})
	reqLimit := httptest.NewRequest(http.MethodPost, "/api/v1/dlq/replay-all", bytes.NewReader(bodyLimit))
	recLimit := httptest.NewRecorder()
	handler.ServeHTTP(recLimit, reqLimit)

	if recLimit.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", recLimit.Code, recLimit.Body.String())
	}
	var respLimit ReplayAllResponse
	if err := json.Unmarshal(recLimit.Body.Bytes(), &respLimit); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if respLimit.ReplayedCount != 1 {
		t.Errorf("expected 1 replayed with limit 1, got %d", respLimit.ReplayedCount)
	}

	// 3. Replay with unknown route -> 404
	bodyUnknown, _ := json.Marshal(ReplayAllRequest{RouteName: "non-existent"})
	reqUnknown := httptest.NewRequest(http.MethodPost, "/api/v1/dlq/replay-all", bytes.NewReader(bodyUnknown))
	recUnknown := httptest.NewRecorder()
	handler.ServeHTTP(recUnknown, reqUnknown)
	if recUnknown.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", recUnknown.Code)
	}

	// 4. Replay remaining records with empty body (no filter)
	reqAll := httptest.NewRequest(http.MethodPost, "/api/v1/dlq/replay-all", nil)
	recAll := httptest.NewRecorder()
	handler.ServeHTTP(recAll, reqAll)

	if recAll.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", recAll.Code)
	}
	var respAll ReplayAllResponse
	if err := json.Unmarshal(recAll.Body.Bytes(), &respAll); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if respAll.ReplayedCount != 2 {
		t.Errorf("expected 2 remaining replayed, got %d", respAll.ReplayedCount)
	}

	if atomic.LoadInt32(wakeCalls) < 3 {
		t.Errorf("expected wakeCalls >= 3, got %d", atomic.LoadInt32(wakeCalls))
	}
}
