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
