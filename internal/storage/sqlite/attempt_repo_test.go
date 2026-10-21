package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
)

func setupTestDBWithParentRequest(t *testing.T) (*DB, *RequestRepo, *AttemptRepo, *model.Request) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_attempt.db")

	db, err := Open(dbPath, "NORMAL")
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
		Name:                 "attempt-test-route",
		Type:                 model.RouteTypeOutbound,
		Mode:                 model.RouteModeSync,
		Path:                 "/api/v1/relay/attempt-test",
		DestinationURL:       "https://api.internal/test",
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := routeRepo.CreateRoute(ctx, route); err != nil {
		t.Fatalf("failed to create route: %v", err)
	}

	reqRepo := NewRequestRepo(db)
	req := &model.Request{
		RouteID: route.ID,
		Method:  "POST",
		URL:     "https://api.internal/test",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    []byte(`{"hello": "world"}`),
		Status:  model.RequestStatusProcessing,
	}
	if err := reqRepo.CreateRequest(ctx, req); err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	return db, reqRepo, NewAttemptRepo(db), req
}

func TestAttemptRepo_RecordAndList(t *testing.T) {
	db, _, attemptRepo, req := setupTestDBWithParentRequest(t)
	defer db.Close()

	ctx := context.Background()

	// Attempt 1: Failed with 500
	code500 := 500
	body500 := `{"error": "internal error"}`
	errMsg := "server returned 500"
	attempt1 := &model.Attempt{
		RequestID:          req.ID,
		AttemptNumber:      1,
		ResponseStatusCode: &code500,
		ResponseBody:       &body500,
		ErrorMessage:       &errMsg,
		DurationMS:         120,
	}

	if err := attemptRepo.RecordAttempt(ctx, attempt1); err != nil {
		t.Fatalf("RecordAttempt 1 failed: %v", err)
	}
	if attempt1.ID == "" {
		t.Fatal("expected attempt1 ID to be set")
	}

	// Attempt 2: Succeeded with 200 and very large body (exceeding 32 KB)
	code200 := 200
	largeBody := strings.Repeat("A", 40*1024) // 40 KB
	attempt2 := &model.Attempt{
		RequestID:          req.ID,
		AttemptNumber:      2,
		ResponseStatusCode: &code200,
		ResponseBody:       &largeBody,
		DurationMS:         85,
	}

	if err := attemptRepo.RecordAttempt(ctx, attempt2); err != nil {
		t.Fatalf("RecordAttempt 2 failed: %v", err)
	}

	// List attempts
	attempts, err := attemptRepo.ListAttemptsByRequestID(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListAttemptsByRequestID failed: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(attempts))
	}

	// Verify order
	if attempts[0].AttemptNumber != 1 || attempts[1].AttemptNumber != 2 {
		t.Fatalf("expected ordered attempts [1, 2], got [%d, %d]", attempts[0].AttemptNumber, attempts[1].AttemptNumber)
	}

	// Verify 32 KB truncation on attempt 2
	if attempts[1].ResponseBody == nil {
		t.Fatal("expected response body on attempt 2")
	}
	if len(*attempts[1].ResponseBody) != model.MaxResponseBodyBytes {
		t.Errorf("expected response body truncated to %d bytes, got %d", model.MaxResponseBodyBytes, len(*attempts[1].ResponseBody))
	}
}

func TestAttemptRepo_ForeignKeyViolation(t *testing.T) {
	db, _, attemptRepo, _ := setupTestDBWithParentRequest(t)
	defer db.Close()

	ctx := context.Background()

	// Attempt referencing non-existent request ID
	attempt := &model.Attempt{
		RequestID:     "non-existent-request-id",
		AttemptNumber: 1,
		DurationMS:    50,
	}

	err := attemptRepo.RecordAttempt(ctx, attempt)
	if err == nil {
		t.Fatal("expected foreign key constraint violation error, got nil")
	}
}

func TestAttemptRepo_ListAttemptsChronologicalReplayOrder(t *testing.T) {
	db, _, attemptRepo, req := setupTestDBWithParentRequest(t)
	defer db.Close()

	ctx := context.Background()

	// Simulate initial run: attempt 1, attempt 2
	t0 := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	t1 := time.Now().UTC().Add(-9 * time.Minute).Truncate(time.Second)
	att1 := &model.Attempt{
		ID:            model.NewULID(),
		RequestID:     req.ID,
		AttemptNumber: 1,
		ExecutedAt:    t0,
		DurationMS:    50,
	}
	att2 := &model.Attempt{
		ID:            model.NewULID(),
		RequestID:     req.ID,
		AttemptNumber: 2,
		ExecutedAt:    t1,
		DurationMS:    60,
	}

	// Simulate replay run: attempt 1, attempt 2 (overlapping attempt_numbers)
	t2 := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	t3 := time.Now().UTC().Truncate(time.Second)
	att3 := &model.Attempt{
		ID:            model.NewULID(),
		RequestID:     req.ID,
		AttemptNumber: 1,
		ExecutedAt:    t2,
		DurationMS:    40,
	}
	att4 := &model.Attempt{
		ID:            model.NewULID(),
		RequestID:     req.ID,
		AttemptNumber: 2,
		ExecutedAt:    t3,
		DurationMS:    45,
	}

	for _, a := range []*model.Attempt{att1, att2, att3, att4} {
		if err := attemptRepo.RecordAttempt(ctx, a); err != nil {
			t.Fatalf("failed to record attempt %s: %v", a.ID, err)
		}
	}

	attempts, err := attemptRepo.ListAttemptsByRequestID(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListAttemptsByRequestID failed: %v", err)
	}

	if len(attempts) != 4 {
		t.Fatalf("expected 4 attempts, got %d", len(attempts))
	}

	// Ensure chronological order [att1, att2, att3, att4] rather than interleaved attempt_number [att1, att3, att2, att4]
	expectedIDs := []string{att1.ID, att2.ID, att3.ID, att4.ID}
	for i, expectedID := range expectedIDs {
		if attempts[i].ID != expectedID {
			t.Errorf("at index %d: expected attempt ID %s (attempt_number=%d, executed_at=%v), got ID %s (attempt_number=%d, executed_at=%v)",
				i, expectedID, attempts[i].AttemptNumber, attempts[i].ExecutedAt, attempts[i].ID, attempts[i].AttemptNumber, attempts[i].ExecutedAt)
		}
	}
}
