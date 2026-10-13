package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
)

func setupTestRetentionDB(t *testing.T) (*DB, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "retention_test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test sqlite database: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.Migrate(ctx); err != nil {
		db.Close()
		t.Fatalf("failed to migrate test database: %v", err)
	}

	// Insert dummy route for foreign key constraints
	route := &model.Route{
		ID:             "01hroute000000000000000001",
		Name:           "test-route",
		Type:           model.RouteTypeInbound,
		Mode:           model.RouteModeAsync,
		Path:           "/webhook/test",
		DestinationURL: "https://example.com/webhook",
		MaxAttempts:    3,
		TimeoutMS:      5000,
	}
	if err := db.RouteRepo.CreateRoute(ctx, route); err != nil {
		db.Close()
		t.Fatalf("failed to create dummy route: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
	}

	return db, cleanup
}

func insertRawRequestWithTime(t *testing.T, db *DB, id, status string, createdAt time.Time) {
	t.Helper()
	ctx := context.Background()
	query := `
		INSERT INTO requests (
			id, route_id, method, url, headers, body, status, attempts_count, created_at, updated_at
		) VALUES (?, '01hroute000000000000000001', 'POST', 'https://example.com', '{}', X'CAFEBABE', ?, 0, ?, ?);
	`
	_, err := db.Writer().ExecContext(ctx, query, id, status, createdAt, createdAt)
	if err != nil {
		t.Fatalf("failed to insert raw test request %s: %v", id, err)
	}
}

func insertRawRequestWithNextAttempt(t *testing.T, db *DB, id, status string, createdAt, nextAttemptAt time.Time) {
	t.Helper()
	ctx := context.Background()
	query := `
		INSERT INTO requests (
			id, route_id, method, url, headers, body, status, attempts_count, next_attempt_at, created_at, updated_at
		) VALUES (?, '01hroute000000000000000001', 'POST', 'https://example.com', '{}', X'CAFEBABE', ?, 0, ?, ?, ?);
	`
	_, err := db.Writer().ExecContext(ctx, query, id, status, nextAttemptAt, createdAt, createdAt)
	if err != nil {
		t.Fatalf("failed to insert raw test request %s with next_attempt: %v", id, err)
	}
}

func insertRawAttempt(t *testing.T, db *DB, attemptID, requestID string) {
	t.Helper()
	ctx := context.Background()
	query := `
		INSERT INTO attempts (
			id, request_id, attempt_number, response_status_code, response_body, error_message, executed_at, duration_ms
		) VALUES (?, ?, 1, 500, 'error', 'failed', CURRENT_TIMESTAMP, 50);
	`
	_, err := db.Writer().ExecContext(ctx, query, attemptID, requestID)
	if err != nil {
		t.Fatalf("failed to insert raw test attempt %s: %v", attemptID, err)
	}
}

func TestRetentionRepo_PruneExpiredRequests_RulesAndCascade(t *testing.T) {
	db, cleanup := setupTestRetentionDB(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now().UTC()
	successCutoff := now.AddDate(0, 0, -7)
	failedCutoff := now.AddDate(0, 0, -30)

	// 1. Expired success (created 8 days ago) -> MUST be purged
	insertRawRequestWithTime(t, db, "req_success_expired", string(model.RequestStatusSuccess), now.AddDate(0, 0, -8))
	insertRawAttempt(t, db, "att_success_expired", "req_success_expired")

	// 2. Fresh success (created 6 days ago) -> MUST be kept
	insertRawRequestWithTime(t, db, "req_success_fresh", string(model.RequestStatusSuccess), now.AddDate(0, 0, -6))
	insertRawAttempt(t, db, "att_success_fresh", "req_success_fresh")

	// 3. Expired dlq (created 31 days ago) -> MUST be purged
	insertRawRequestWithTime(t, db, "req_dlq_expired", string(model.RequestStatusDLQ), now.AddDate(0, 0, -31))
	insertRawAttempt(t, db, "att_dlq_expired", "req_dlq_expired")

	// 4. Fresh dlq (created 29 days ago) -> MUST be kept
	insertRawRequestWithTime(t, db, "req_dlq_fresh", string(model.RequestStatusDLQ), now.AddDate(0, 0, -29))
	insertRawAttempt(t, db, "att_dlq_fresh", "req_dlq_fresh")

	// 5. Expired failed (created 35 days ago) -> MUST be purged
	insertRawRequestWithTime(t, db, "req_failed_expired", string(model.RequestStatusFailed), now.AddDate(0, 0, -35))
	insertRawAttempt(t, db, "att_failed_expired", "req_failed_expired")

	// 6. Fresh failed (created 10 days ago) -> MUST be kept
	insertRawRequestWithTime(t, db, "req_failed_fresh", string(model.RequestStatusFailed), now.AddDate(0, 0, -10))
	insertRawAttempt(t, db, "att_failed_fresh", "req_failed_fresh")

	// 7. Old pending (created 40 days ago) -> MUST NEVER be purged
	insertRawRequestWithTime(t, db, "req_pending_old", string(model.RequestStatusPending), now.AddDate(0, 0, -40))

	// 8. Old processing (created 40 days ago) -> MUST NEVER be purged
	insertRawRequestWithTime(t, db, "req_processing_old", string(model.RequestStatusProcessing), now.AddDate(0, 0, -40))

	// 9. Old failed with scheduled retry in the future (created 35 days ago, retry in 1 hour) -> MUST be kept
	futureAttempt := now.Add(1 * time.Hour)
	insertRawRequestWithNextAttempt(t, db, "req_failed_with_future_retry", string(model.RequestStatusFailed), now.AddDate(0, 0, -35), futureAttempt)

	// Execute prune with batchSize = 100
	purged, err := db.RetentionRepo.PruneExpiredRequests(ctx, successCutoff, failedCutoff, 100)
	if err != nil {
		t.Fatalf("PruneExpiredRequests failed: %v", err)
	}

	// We expect exactly 3 requests to be purged: req_success_expired, req_dlq_expired, req_failed_expired
	if purged != 3 {
		t.Fatalf("expected 3 purged requests, got %d", purged)
	}

	// Verify surviving requests
	expectedSurviving := []string{
		"req_success_fresh",
		"req_dlq_fresh",
		"req_failed_fresh",
		"req_pending_old",
		"req_processing_old",
		"req_failed_with_future_retry",
	}

	for _, id := range expectedSurviving {
		var count int
		err := db.Reader().QueryRowContext(ctx, "SELECT COUNT(*) FROM requests WHERE id = ?", id).Scan(&count)
		if err != nil || count != 1 {
			t.Errorf("expected request %s to survive, but count was %d (err: %v)", id, count, err)
		}
	}

	// Verify deleted requests
	deletedIDs := []string{
		"req_success_expired",
		"req_dlq_expired",
		"req_failed_expired",
	}

	for _, id := range deletedIDs {
		var count int
		err := db.Reader().QueryRowContext(ctx, "SELECT COUNT(*) FROM requests WHERE id = ?", id).Scan(&count)
		if err != nil || count != 0 {
			t.Errorf("expected request %s to be deleted, but count was %d", id, count)
		}
	}

	// Verify cascade deletion in attempts table
	for _, attID := range []string{"att_success_expired", "att_dlq_expired", "att_failed_expired"} {
		var count int
		err := db.Reader().QueryRowContext(ctx, "SELECT COUNT(*) FROM attempts WHERE id = ?", idFromAtt(attID)).Scan(&count)
		if err != nil || count != 0 {
			t.Errorf("expected attempt %s to be cascade deleted, got count %d", attID, count)
		}
	}

	// Verify surviving attempts
	for _, attID := range []string{"att_success_fresh", "att_dlq_fresh", "att_failed_fresh"} {
		var count int
		err := db.Reader().QueryRowContext(ctx, "SELECT COUNT(*) FROM attempts WHERE id = ?", idFromAtt(attID)).Scan(&count)
		if err != nil || count != 1 {
			t.Errorf("expected attempt %s to survive, got count %d", attID, count)
		}
	}
}

func idFromAtt(attID string) string {
	return attID
}

func TestRetentionRepo_PruneExpiredRequests_BatchLoop(t *testing.T) {
	db, cleanup := setupTestRetentionDB(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now().UTC()
	successCutoff := now.AddDate(0, 0, -7)
	failedCutoff := now.AddDate(0, 0, -30)

	// Insert 5 expired records
	for i := 1; i <= 5; i++ {
		reqID := string(rune('a'+i)) + "_expired"
		insertRawRequestWithTime(t, db, reqID, string(model.RequestStatusSuccess), now.AddDate(0, 0, -10))
	}

	// Execute with batchSize = 2 to ensure loop runs 3 times (2 + 2 + 1)
	purged, err := db.RetentionRepo.PruneExpiredRequests(ctx, successCutoff, failedCutoff, 2)
	if err != nil {
		t.Fatalf("batch prune failed: %v", err)
	}

	if purged != 5 {
		t.Fatalf("expected 5 purged requests across batches, got %d", purged)
	}

	var remaining int
	_ = db.Reader().QueryRowContext(ctx, "SELECT COUNT(*) FROM requests").Scan(&remaining)
	if remaining != 0 {
		t.Fatalf("expected 0 remaining requests, got %d", remaining)
	}
}

func TestRetentionRepo_ReclaimStorage(t *testing.T) {
	db, cleanup := setupTestRetentionDB(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.RetentionRepo.ReclaimStorage(ctx, 1000); err != nil {
		t.Fatalf("ReclaimStorage failed: %v", err)
	}

	if err := db.RetentionRepo.ReclaimStorage(ctx, 0); err != nil {
		t.Fatalf("ReclaimStorage with default fallback failed: %v", err)
	}
}

func TestRetentionRepo_Checkpoint(t *testing.T) {
	db, cleanup := setupTestRetentionDB(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Insert raw requests to generate WAL activity
	insertRawRequestWithTime(t, db, "01hckpt00000000000000000001", "success", time.Now().UTC())
	insertRawRequestWithTime(t, db, "01hckpt00000000000000000002", "failed", time.Now().UTC())

	// Execute Checkpoint
	if err := db.RetentionRepo.Checkpoint(ctx); err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}
}
