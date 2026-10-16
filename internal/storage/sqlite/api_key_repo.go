package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

// APIKeyRepo implements storage.APIKeyRepository for SQLite with in-memory caching.
type APIKeyRepo struct {
	db    *DB
	mu    sync.RWMutex
	cache map[string]*model.APIKey
}

// NewAPIKeyRepo creates a new SQLite API key repository.
func NewAPIKeyRepo(db *DB) *APIKeyRepo {
	return &APIKeyRepo{
		db:    db,
		cache: make(map[string]*model.APIKey),
	}
}

// CreateAPIKey inserts a new API key record into SQLite using writer pool.
func (r *APIKeyRepo) CreateAPIKey(ctx context.Context, key *model.APIKey) error {
	if err := key.Validate(); err != nil {
		return fmt.Errorf("api key validation failed: %w", err)
	}

	if key.CreatedAt.IsZero() {
		key.CreatedAt = time.Now().UTC()
	}

	query := `
		INSERT INTO api_keys (
			id, name, key_hash, masked_key, created_at, revoked_at
		) VALUES (?, ?, ?, ?, ?, ?);
	`

	var revokedAtStr *string
	if key.RevokedAt != nil {
		s := key.RevokedAt.UTC().Format(time.RFC3339Nano)
		revokedAtStr = &s
	}

	_, err := r.db.Writer().ExecContext(ctx, query,
		key.ID,
		key.Name,
		key.KeyHash,
		key.MaskedKey,
		key.CreatedAt.UTC().Format(time.RFC3339Nano),
		revokedAtStr,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return storage.ErrConflict
		}
		return fmt.Errorf("failed to insert api key: %w", err)
	}

	return nil
}

// GetAPIKeyByHash looks up an active (non-revoked) key by its SHA-256 hash.
func (r *APIKeyRepo) GetAPIKeyByHash(ctx context.Context, keyHash string) (*model.APIKey, error) {
	r.mu.RLock()
	if cached, ok := r.cache[keyHash]; ok {
		r.mu.RUnlock()
		copyKey := *cached
		return &copyKey, nil
	}
	r.mu.RUnlock()

	query := `
		SELECT id, name, key_hash, masked_key, created_at, revoked_at
		FROM api_keys
		WHERE key_hash = ? AND revoked_at IS NULL;
	`

	row := r.db.Reader().QueryRowContext(ctx, query, keyHash)
	key, err := r.scanKey(row)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.cache[keyHash] = key
	r.mu.Unlock()

	copyKey := *key
	return &copyKey, nil
}

// ListAPIKeys retrieves all API keys (both active and revoked), ordered by created_at DESC.
func (r *APIKeyRepo) ListAPIKeys(ctx context.Context) ([]*model.APIKey, error) {
	query := `
		SELECT id, name, key_hash, masked_key, created_at, revoked_at
		FROM api_keys
		ORDER BY created_at DESC;
	`

	rows, err := r.db.Reader().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query api keys: %w", err)
	}
	defer rows.Close()

	var keys []*model.APIKey
	for rows.Next() {
		var (
			k            model.APIKey
			createdAtStr string
			revokedAtStr sql.NullString
		)

		if err := rows.Scan(&k.ID, &k.Name, &k.KeyHash, &k.MaskedKey, &createdAtStr, &revokedAtStr); err != nil {
			return nil, fmt.Errorf("failed to scan api key: %w", err)
		}

		if t, err := time.Parse(time.RFC3339Nano, createdAtStr); err == nil {
			k.CreatedAt = t
		} else if t, err := time.Parse("2006-01-02 15:04:05", createdAtStr); err == nil {
			k.CreatedAt = t
		}

		if revokedAtStr.Valid {
			if t, err := time.Parse(time.RFC3339Nano, revokedAtStr.String); err == nil {
				k.RevokedAt = &t
			} else if t, err := time.Parse("2006-01-02 15:04:05", revokedAtStr.String); err == nil {
				k.RevokedAt = &t
			}
		}

		keys = append(keys, &k)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating api keys rows: %w", err)
	}

	return keys, nil
}

// RevokeAPIKey sets revoked_at = CURRENT_TIMESTAMP for the given key ID.
func (r *APIKeyRepo) RevokeAPIKey(ctx context.Context, id string) error {
	query := `
		UPDATE api_keys
		SET revoked_at = CURRENT_TIMESTAMP
		WHERE id = ? AND revoked_at IS NULL;
	`

	res, err := r.db.Writer().ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to revoke api key: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return storage.ErrNotFound
	}

	r.mu.Lock()
	r.cache = make(map[string]*model.APIKey)
	r.mu.Unlock()

	return nil
}

// CountTotalKeys returns total count of all API key rows in the database.
func (r *APIKeyRepo) CountTotalKeys(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys;`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count total api keys: %w", err)
	}
	return count, nil
}

// CountActiveKeys returns count of non-revoked API keys.
func (r *APIKeyRepo) CountActiveKeys(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys WHERE revoked_at IS NULL;`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count active api keys: %w", err)
	}
	return count, nil
}

func (r *APIKeyRepo) scanKey(row *sql.Row) (*model.APIKey, error) {
	var (
		k            model.APIKey
		createdAtStr string
		revokedAtStr sql.NullString
	)

	if err := row.Scan(&k.ID, &k.Name, &k.KeyHash, &k.MaskedKey, &createdAtStr, &revokedAtStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("failed to scan api key: %w", err)
	}

	if t, err := time.Parse(time.RFC3339Nano, createdAtStr); err == nil {
		k.CreatedAt = t
	} else if t, err := time.Parse("2006-01-02 15:04:05", createdAtStr); err == nil {
		k.CreatedAt = t
	}

	if revokedAtStr.Valid {
		if t, err := time.Parse(time.RFC3339Nano, revokedAtStr.String); err == nil {
			k.RevokedAt = &t
		} else if t, err := time.Parse("2006-01-02 15:04:05", revokedAtStr.String); err == nil {
			k.RevokedAt = &t
		}
	}

	return &k, nil
}
