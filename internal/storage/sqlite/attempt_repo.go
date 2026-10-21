package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
)

// AttemptRepo implements storage.AttemptRepository for SQLite.
type AttemptRepo struct {
	db *DB
}

// NewAttemptRepo creates a new SQLite Attempt repository.
func NewAttemptRepo(db *DB) *AttemptRepo {
	return &AttemptRepo{db: db}
}

// RecordAttempt persists an execution attempt audit log.
// It automatically enforces the 32 KB response body truncation to protect against SQLite bloat.
func (a *AttemptRepo) RecordAttempt(ctx context.Context, attempt *model.Attempt) error {
	if attempt.ID == "" {
		attempt.ID = model.NewULID()
	}
	if err := attempt.Validate(); err != nil {
		return fmt.Errorf("attempt validation failed: %w", err)
	}

	if attempt.ExecutedAt.IsZero() {
		attempt.ExecutedAt = time.Now().UTC()
	}

	// Protect against storage bloat: truncate response body to MaxResponseBodyBytes (32 KB)
	if attempt.ResponseBody != nil {
		truncated := model.TruncateResponseBody(*attempt.ResponseBody)
		attempt.ResponseBody = &truncated
	}

	query := `
		INSERT INTO attempts (
			id, request_id, attempt_number, response_status_code,
			response_body, error_message, executed_at, duration_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?);
	`

	_, err := a.db.Writer().ExecContext(ctx, query,
		attempt.ID,
		attempt.RequestID,
		attempt.AttemptNumber,
		attempt.ResponseStatusCode,
		attempt.ResponseBody,
		attempt.ErrorMessage,
		attempt.ExecutedAt,
		attempt.DurationMS,
	)
	if err != nil {
		return fmt.Errorf("failed to record attempt %s for request %s: %w", attempt.ID, attempt.RequestID, err)
	}

	return nil
}

// ListAttemptsByRequestID retrieves all attempts for a given request ordered chronologically.
func (a *AttemptRepo) ListAttemptsByRequestID(ctx context.Context, requestID string) ([]*model.Attempt, error) {
	query := `
		SELECT id, request_id, attempt_number, response_status_code,
		       response_body, error_message, executed_at, duration_ms
		FROM attempts
		WHERE request_id = ?
		ORDER BY executed_at ASC, id ASC;
	`

	rows, err := a.db.Reader().QueryContext(ctx, query, requestID)
	if err != nil {
		return nil, fmt.Errorf("failed to list attempts for request %s: %w", requestID, err)
	}
	defer rows.Close()

	var attempts []*model.Attempt
	for rows.Next() {
		var att model.Attempt
		err := rows.Scan(
			&att.ID,
			&att.RequestID,
			&att.AttemptNumber,
			&att.ResponseStatusCode,
			&att.ResponseBody,
			&att.ErrorMessage,
			&att.ExecutedAt,
			&att.DurationMS,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan attempt row: %w", err)
		}
		attempts = append(attempts, &att)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("attempts row iteration error: %w", err)
	}

	return attempts, nil
}
