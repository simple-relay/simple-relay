package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

// RequestRepo implements storage.RequestRepository for SQLite.
type RequestRepo struct {
	db *DB
}

// NewRequestRepo creates a new SQLite Request repository.
func NewRequestRepo(db *DB) *RequestRepo {
	return &RequestRepo{db: db}
}

// CreateRequest persists a new request record into the requests table.
func (r *RequestRepo) CreateRequest(ctx context.Context, req *model.Request) error {
	if req.ID == "" {
		req.ID = model.NewULID()
	}
	if err := req.Validate(); err != nil {
		return fmt.Errorf("request validation failed: %w", err)
	}

	now := time.Now().UTC()
	if req.CreatedAt.IsZero() {
		req.CreatedAt = now
	}
	if req.UpdatedAt.IsZero() {
		req.UpdatedAt = now
	}

	headersJSON, err := json.Marshal(req.Headers)
	if err != nil {
		return fmt.Errorf("failed to serialize headers to JSON: %w", err)
	}

	query := `
		INSERT INTO requests (
			id, route_id, method, url, headers, body,
			status, attempts_count, next_attempt_at, last_error,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`

	_, err = r.db.Writer().ExecContext(ctx, query,
		req.ID,
		req.RouteID,
		req.Method,
		req.URL,
		string(headersJSON),
		req.Body,
		string(req.Status),
		req.AttemptsCount,
		req.NextAttemptAt,
		req.LastError,
		req.CreatedAt,
		req.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert request: %w", err)
	}

	return nil
}

// CreateRequestsBatch persists a batch of requests atomically in a single transaction.
func (r *RequestRepo) CreateRequestsBatch(ctx context.Context, reqs []*model.Request) error {
	if len(reqs) == 0 {
		return nil
	}

	tx, err := r.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO requests (
			id, route_id, method, url, headers, body,
			status, attempts_count, next_attempt_at, last_error,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare insert statement: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for _, req := range reqs {
		if req.ID == "" {
			req.ID = model.NewULID()
		}
		if err := req.Validate(); err != nil {
			return fmt.Errorf("request validation failed for %s: %w", req.ID, err)
		}
		if req.CreatedAt.IsZero() {
			req.CreatedAt = now
		}
		if req.UpdatedAt.IsZero() {
			req.UpdatedAt = now
		}

		headersJSON, err := json.Marshal(req.Headers)
		if err != nil {
			return fmt.Errorf("failed to serialize headers for %s: %w", req.ID, err)
		}

		_, err = stmt.ExecContext(ctx,
			req.ID,
			req.RouteID,
			req.Method,
			req.URL,
			string(headersJSON),
			req.Body,
			string(req.Status),
			req.AttemptsCount,
			req.NextAttemptAt,
			req.LastError,
			req.CreatedAt,
			req.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("failed to insert request %s in batch: %w", req.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit requests batch: %w", err)
	}

	return nil
}

// GetRequestByID retrieves a single request by its ULID.
func (r *RequestRepo) GetRequestByID(ctx context.Context, id string) (*model.Request, error) {
	query := `
		SELECT id, route_id, method, url, headers, body,
		       status, attempts_count, next_attempt_at, last_error,
		       created_at, updated_at
		FROM requests
		WHERE id = ?;
	`

	var req model.Request
	var headersStr string
	var statusStr string

	err := r.db.Reader().QueryRowContext(ctx, query, id).Scan(
		&req.ID,
		&req.RouteID,
		&req.Method,
		&req.URL,
		&headersStr,
		&req.Body,
		&statusStr,
		&req.AttemptsCount,
		&req.NextAttemptAt,
		&req.LastError,
		&req.CreatedAt,
		&req.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("failed to query request %s: %w", id, err)
	}

	req.Status = model.RequestStatus(statusStr)
	if headersStr != "" {
		if err := json.Unmarshal([]byte(headersStr), &req.Headers); err != nil {
			return nil, fmt.Errorf("failed to deserialize headers for request %s: %w", id, err)
		}
	} else {
		req.Headers = make(map[string]string)
	}

	return &req, nil
}

// ListPendingRequests retrieves pending/failed requests scheduled for execution up to dueBefore.
func (r *RequestRepo) ListPendingRequests(ctx context.Context, limit int, dueBefore time.Time) ([]*model.Request, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT id, route_id, method, url, headers, body,
		       status, attempts_count, next_attempt_at, last_error,
		       created_at, updated_at
		FROM requests
		WHERE status IN ('pending', 'failed') AND next_attempt_at <= ?
		ORDER BY next_attempt_at ASC
		LIMIT ?;
	`

	rows, err := r.db.Reader().QueryContext(ctx, query, dueBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending requests: %w", err)
	}
	defer rows.Close()

	var results []*model.Request
	for rows.Next() {
		var req model.Request
		var headersStr string
		var statusStr string

		err := rows.Scan(
			&req.ID,
			&req.RouteID,
			&req.Method,
			&req.URL,
			&headersStr,
			&req.Body,
			&statusStr,
			&req.AttemptsCount,
			&req.NextAttemptAt,
			&req.LastError,
			&req.CreatedAt,
			&req.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan pending request: %w", err)
		}

		req.Status = model.RequestStatus(statusStr)
		if headersStr != "" {
			if err := json.Unmarshal([]byte(headersStr), &req.Headers); err != nil {
				return nil, fmt.Errorf("failed to deserialize headers: %w", err)
			}
		} else {
			req.Headers = make(map[string]string)
		}

		results = append(results, &req)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return results, nil
}

// UpdateRequestStatus updates the lifecycle status, attempt count, and last error of a request.
func (r *RequestRepo) UpdateRequestStatus(ctx context.Context, id string, status model.RequestStatus, attemptsCount int, nextAttemptAt *time.Time, lastError *string) error {
	query := `
		UPDATE requests
		SET status = ?,
		    attempts_count = ?,
		    next_attempt_at = ?,
		    last_error = ?,
		    updated_at = ?
		WHERE id = ?;
	`

	res, err := r.db.Writer().ExecContext(ctx, query,
		string(status),
		attemptsCount,
		nextAttemptAt,
		lastError,
		time.Now().UTC(),
		id,
	)
	if err != nil {
		return fmt.Errorf("failed to update request status %s: %w", id, err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected for %s: %w", id, err)
	}
	if rowsAffected == 0 {
		return storage.ErrNotFound
	}

	return nil
}

// ClaimPendingRequests atomically queries due requests and transitions their status to 'processing'.
func (r *RequestRepo) ClaimPendingRequests(ctx context.Context, limit int, dueBefore time.Time) ([]*model.Request, error) {
	if limit <= 0 {
		limit = 50
	}

	tx, err := r.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin claim transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	selectQuery := `
		SELECT id, route_id, method, url, headers, body,
		       status, attempts_count, next_attempt_at, last_error,
		       created_at, updated_at
		FROM requests
		WHERE status IN ('pending', 'failed') AND next_attempt_at <= ?
		ORDER BY next_attempt_at ASC
		LIMIT ?;
	`

	rows, err := tx.QueryContext(ctx, selectQuery, dueBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to select pending requests for claim: %w", err)
	}
	defer rows.Close()

	var results []*model.Request
	var ids []string
	for rows.Next() {
		var req model.Request
		var headersStr string
		var statusStr string

		err := rows.Scan(
			&req.ID,
			&req.RouteID,
			&req.Method,
			&req.URL,
			&headersStr,
			&req.Body,
			&statusStr,
			&req.AttemptsCount,
			&req.NextAttemptAt,
			&req.LastError,
			&req.CreatedAt,
			&req.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan request during claim: %w", err)
		}

		req.Status = model.RequestStatusProcessing
		if headersStr != "" {
			if err := json.Unmarshal([]byte(headersStr), &req.Headers); err != nil {
				return nil, fmt.Errorf("failed to deserialize headers for %s during claim: %w", req.ID, err)
			}
		} else {
			req.Headers = make(map[string]string)
		}

		results = append(results, &req)
		ids = append(ids, req.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error during claim: %w", err)
	}
	rows.Close()

	if len(ids) == 0 {
		return results, nil
	}

	now := time.Now().UTC()
	updateStmt, err := tx.PrepareContext(ctx, `UPDATE requests SET status = 'processing', updated_at = ? WHERE id = ?;`)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare claim update statement: %w", err)
	}
	defer updateStmt.Close()

	for _, id := range ids {
		if _, err := updateStmt.ExecContext(ctx, now, id); err != nil {
			return nil, fmt.Errorf("failed to mark request %s as processing: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit claim transaction: %w", err)
	}

	return results, nil
}

// RecoverStuckRequests transitions requests stuck in 'processing' state before stuckBefore back to 'pending'.
func (r *RequestRepo) RecoverStuckRequests(ctx context.Context, stuckBefore time.Time) (int64, error) {
	now := time.Now().UTC()
	query := `
		UPDATE requests
		SET status = 'pending', updated_at = ?
		WHERE status = 'processing' AND updated_at <= ?;
	`

	res, err := r.db.Writer().ExecContext(ctx, query, now, stuckBefore)
	if err != nil {
		return 0, fmt.Errorf("failed to recover stuck requests: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected during stuck request recovery: %w", err)
	}

	return rowsAffected, nil
}
