package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/simple-relay/simple-relay/internal/storage"
)

// RetentionRepo implements storage.RetentionRepository for SQLite.
type RetentionRepo struct {
	db *DB
}

// NewRetentionRepo creates a new SQLite Retention repository.
func NewRetentionRepo(db *DB) *RetentionRepo {
	return &RetentionRepo{db: db}
}

var _ storage.RetentionRepository = (*RetentionRepo)(nil)

// PruneExpiredRequests removes expired requests matching retention policies in bounded batches.
// Cascades to attempts automatically via ON DELETE CASCADE in SQLite schema.
func (r *RetentionRepo) PruneExpiredRequests(ctx context.Context, successCutoff, failedCutoff time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}

	// Subquery delete with LIMIT ensures bounded chunk execution on SQLite without requiring
	// the optional SQLITE_ENABLE_UPDATE_DELETE_LIMIT compile flag ⚡.
	// We also guard against deleting requests that might still have a scheduled future retry.
	query := `
		DELETE FROM requests
		WHERE id IN (
			SELECT id FROM requests
			WHERE (status = 'success' AND created_at < ?)
			   OR (status IN ('failed', 'dlq') AND created_at < ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?))
			LIMIT ?
		);
	`

	var totalPurged int64
	now := time.Now().UTC()

	for {
		if err := ctx.Err(); err != nil {
			return totalPurged, err
		}

		res, err := r.db.Writer().ExecContext(ctx, query, successCutoff, failedCutoff, now, batchSize)
		if err != nil {
			return totalPurged, fmt.Errorf("failed to prune expired requests batch: %w", err)
		}

		rowsAffected, err := res.RowsAffected()
		if err != nil {
			return totalPurged, fmt.Errorf("failed to get rows affected during prune: %w", err)
		}

		totalPurged += rowsAffected

		// If fewer rows were affected than the batch limit or zero, all expired requests have been purged
		if rowsAffected < int64(batchSize) || rowsAffected == 0 {
			break
		}

		// Cooperative yield between batch deletes to prevent starving concurrent BatchWriter transactions ⚡
		time.Sleep(1 * time.Millisecond)
	}

	return totalPurged, nil
}

// ReclaimStorage executes PRAGMA incremental_vacuum to return unused pages to the OS.
func (r *RetentionRepo) ReclaimStorage(ctx context.Context, maxPages int) error {
	if maxPages <= 0 {
		maxPages = 1000
	} else if maxPages > 10000 {
		// Cap maxPages to prevent holding exclusive I/O locks on slow mechanical storage 💀
		maxPages = 10000
	}

	// SQLite PRAGMA commands do not support bind parameters: formatting validated int is safe.
	query := fmt.Sprintf("PRAGMA incremental_vacuum(%d);", maxPages)
	if _, err := r.db.Writer().ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to execute incremental vacuum: %w", err)
	}

	return nil
}
