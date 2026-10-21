package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

// RouteRepo implements storage.RouteRepository for SQLite.
type RouteRepo struct {
	db *DB
}

// NewRouteRepo creates a new SQLite Route repository.
func NewRouteRepo(db *DB) *RouteRepo {
	return &RouteRepo{db: db}
}

// CreateRoute inserts a new route record using the writer pool.
func (r *RouteRepo) CreateRoute(ctx context.Context, route *model.Route) error {
	if err := route.Validate(); err != nil {
		return fmt.Errorf("route validation failed: %w", err)
	}

	if route.ID == "" {
		route.ID = model.NewULID()
	}

	now := time.Now().UTC()
	if route.CreatedAt.IsZero() {
		route.CreatedAt = now
	}
	if route.UpdatedAt.IsZero() {
		route.UpdatedAt = now
	}

	rulesJSON, err := json.Marshal(route.ValidationRules)
	if err != nil {
		return fmt.Errorf("failed to marshal validation rules: %w", err)
	}

	retryNonIdem := 0
	if route.RetryNonIdempotent {
		retryNonIdem = 1
	}

	query := `
		INSERT INTO routes (
			id, name, type, mode, path, destination_url,
			signature_provider, signature_secret, validation_rules, max_attempts,
			timeout_ms, retry_backoff_strategy, base_backoff_ms, max_backoff_ms,
			retry_non_idempotent, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`

	_, err = r.db.Writer().ExecContext(ctx, query,
		route.ID,
		route.Name,
		route.Type,
		route.Mode,
		route.Path,
		route.DestinationURL,
		route.SignatureProvider,
		route.SignatureSecret,
		string(rulesJSON),
		route.MaxAttempts,
		route.TimeoutMS,
		route.RetryBackoffStrategy,
		route.BaseBackoffMS,
		route.MaxBackoffMS,
		retryNonIdem,
		route.CreatedAt.Format(time.RFC3339Nano),
		route.UpdatedAt.Format(time.RFC3339Nano),
	)

	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return storage.ErrConflict
		}
		return fmt.Errorf("failed to insert route: %w", err)
	}

	return nil
}

// GetRouteByID fetches a route by its ULID from the reader pool.
func (r *RouteRepo) GetRouteByID(ctx context.Context, id string) (*model.Route, error) {
	query := `
		SELECT id, name, type, mode, path, destination_url,
		       signature_provider, signature_secret, validation_rules, max_attempts,
		       timeout_ms, retry_backoff_strategy, base_backoff_ms, max_backoff_ms,
		       retry_non_idempotent, created_at, updated_at
		FROM routes WHERE id = ?;
	`
	return r.scanRoute(r.db.Reader().QueryRowContext(ctx, query, id))
}

// GetRouteByName fetches a route by its unique name from the reader pool.
func (r *RouteRepo) GetRouteByName(ctx context.Context, name string) (*model.Route, error) {
	query := `
		SELECT id, name, type, mode, path, destination_url,
		       signature_provider, signature_secret, validation_rules, max_attempts,
		       timeout_ms, retry_backoff_strategy, base_backoff_ms, max_backoff_ms,
		       retry_non_idempotent, created_at, updated_at
		FROM routes WHERE name = ?;
	`
	return r.scanRoute(r.db.Reader().QueryRowContext(ctx, query, name))
}

// GetRouteByPath fetches a route by its unique path from the reader pool.
func (r *RouteRepo) GetRouteByPath(ctx context.Context, path string) (*model.Route, error) {
	query := `
		SELECT id, name, type, mode, path, destination_url,
		       signature_provider, signature_secret, validation_rules, max_attempts,
		       timeout_ms, retry_backoff_strategy, base_backoff_ms, max_backoff_ms,
		       retry_non_idempotent, created_at, updated_at
		FROM routes WHERE path = ?;
	`
	return r.scanRoute(r.db.Reader().QueryRowContext(ctx, query, path))
}

// ListRoutes returns all configured routes sorted by created_at descending from the reader pool.
func (r *RouteRepo) ListRoutes(ctx context.Context) ([]*model.Route, error) {
	query := `
		SELECT id, name, type, mode, path, destination_url,
		       signature_provider, signature_secret, validation_rules, max_attempts,
		       timeout_ms, retry_backoff_strategy, base_backoff_ms, max_backoff_ms,
		       retry_non_idempotent, created_at, updated_at
		FROM routes ORDER BY created_at DESC, id DESC;
	`

	rows, err := r.db.Reader().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query routes: %w", err)
	}
	defer rows.Close()

	var routes []*model.Route
	for rows.Next() {
		var (
			rt           model.Route
			createdAt    string
			updatedAt    string
			secret       sql.NullString
			rules        sql.NullString
			retryNonIdem int
		)

		err := rows.Scan(
			&rt.ID,
			&rt.Name,
			&rt.Type,
			&rt.Mode,
			&rt.Path,
			&rt.DestinationURL,
			&rt.SignatureProvider,
			&secret,
			&rules,
			&rt.MaxAttempts,
			&rt.TimeoutMS,
			&rt.RetryBackoffStrategy,
			&rt.BaseBackoffMS,
			&rt.MaxBackoffMS,
			&retryNonIdem,
			&createdAt,
			&updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan route row: %w", err)
		}

		rt.RetryNonIdempotent = retryNonIdem != 0
		if secret.Valid {
			rt.SignatureSecret = secret.String
		}
		if rules.Valid && strings.TrimSpace(rules.String) != "" {
			if err := json.Unmarshal([]byte(rules.String), &rt.ValidationRules); err != nil {
				return nil, fmt.Errorf("failed to unmarshal validation rules for route %s: %w", rt.ID, err)
			}
		}
		rt.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		rt.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)

		routes = append(routes, &rt)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading route rows: %w", err)
	}

	return routes, nil
}

// UpdateRoute updates an existing route record in the writer pool.
func (r *RouteRepo) UpdateRoute(ctx context.Context, route *model.Route) error {
	if err := route.Validate(); err != nil {
		return fmt.Errorf("route validation failed: %w", err)
	}

	route.UpdatedAt = time.Now().UTC()

	rulesJSON, err := json.Marshal(route.ValidationRules)
	if err != nil {
		return fmt.Errorf("failed to marshal validation rules: %w", err)
	}

	retryNonIdem := 0
	if route.RetryNonIdempotent {
		retryNonIdem = 1
	}

	query := `
		UPDATE routes SET
			name = ?,
			type = ?,
			mode = ?,
			path = ?,
			destination_url = ?,
			signature_provider = ?,
			signature_secret = ?,
			validation_rules = ?,
			max_attempts = ?,
			timeout_ms = ?,
			retry_backoff_strategy = ?,
			base_backoff_ms = ?,
			max_backoff_ms = ?,
			retry_non_idempotent = ?,
			updated_at = ?
		WHERE id = ?;
	`

	res, err := r.db.Writer().ExecContext(ctx, query,
		route.Name,
		route.Type,
		route.Mode,
		route.Path,
		route.DestinationURL,
		route.SignatureProvider,
		route.SignatureSecret,
		string(rulesJSON),
		route.MaxAttempts,
		route.TimeoutMS,
		route.RetryBackoffStrategy,
		route.BaseBackoffMS,
		route.MaxBackoffMS,
		retryNonIdem,
		route.UpdatedAt.Format(time.RFC3339Nano),
		route.ID,
	)

	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return storage.ErrConflict
		}
		return fmt.Errorf("failed to update route: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return storage.ErrNotFound
	}

	return nil
}

// DeleteRoute removes a route by its ID using the writer pool.
func (r *RouteRepo) DeleteRoute(ctx context.Context, id string) error {
	query := `DELETE FROM routes WHERE id = ?;`
	res, err := r.db.Writer().ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete route: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return storage.ErrNotFound
	}

	return nil
}

func (r *RouteRepo) scanRoute(row *sql.Row) (*model.Route, error) {
	var (
		rt           model.Route
		createdAt    string
		updatedAt    string
		secret       sql.NullString
		rules        sql.NullString
		retryNonIdem int
	)

	err := row.Scan(
		&rt.ID,
		&rt.Name,
		&rt.Type,
		&rt.Mode,
		&rt.Path,
		&rt.DestinationURL,
		&rt.SignatureProvider,
		&secret,
		&rules,
		&rt.MaxAttempts,
		&rt.TimeoutMS,
		&rt.RetryBackoffStrategy,
		&rt.BaseBackoffMS,
		&rt.MaxBackoffMS,
		&retryNonIdem,
		&createdAt,
		&updatedAt,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("failed to scan route: %w", err)
	}

	rt.RetryNonIdempotent = retryNonIdem != 0

	if secret.Valid {
		rt.SignatureSecret = secret.String
	}
	if rules.Valid && strings.TrimSpace(rules.String) != "" {
		if err := json.Unmarshal([]byte(rules.String), &rt.ValidationRules); err != nil {
			return nil, fmt.Errorf("failed to unmarshal validation rules for route %s: %w", rt.ID, err)
		}
	}
	rt.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	rt.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)

	return &rt, nil
}
