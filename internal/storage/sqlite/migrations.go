package sqlite

import (
	"context"
	"fmt"
)

const schemaDDL = `
CREATE TABLE IF NOT EXISTS routes (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    type TEXT NOT NULL,
    mode TEXT NOT NULL DEFAULT 'async',
    path TEXT NOT NULL UNIQUE,
    destination_url TEXT NOT NULL,
    signature_provider TEXT DEFAULT 'none',
    signature_secret TEXT,
    validation_rules TEXT DEFAULT '[]',
    max_attempts INTEGER NOT NULL DEFAULT 5,
    timeout_ms INTEGER NOT NULL DEFAULT 10000,
    retry_backoff_strategy TEXT DEFAULT 'exp',
    base_backoff_ms INTEGER NOT NULL DEFAULT 0,
    max_backoff_ms INTEGER NOT NULL DEFAULT 0,
    retry_non_idempotent INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS requests (
    id TEXT PRIMARY KEY,
    route_id TEXT NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
    method TEXT NOT NULL,
    url TEXT NOT NULL,
    headers TEXT NOT NULL,
    body BLOB,
    status TEXT NOT NULL,
    attempts_count INTEGER NOT NULL DEFAULT 0,
    next_attempt_at DATETIME,
    last_error TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_requests_dispatcher ON requests (status, next_attempt_at) 
WHERE status IN ('pending', 'failed');

CREATE INDEX IF NOT EXISTS idx_requests_processing ON requests (status)
WHERE status = 'processing';

CREATE INDEX IF NOT EXISTS idx_requests_dlq ON requests (status)
WHERE status = 'dlq';

CREATE INDEX IF NOT EXISTS idx_requests_created_at ON requests (created_at);

CREATE TABLE IF NOT EXISTS attempts (
    id TEXT PRIMARY KEY,
    request_id TEXT NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL,
    response_status_code INTEGER,
    response_body TEXT,
    error_message TEXT,
    executed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    duration_ms INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_attempts_request_id ON attempts (request_id);

CREATE TABLE IF NOT EXISTS api_keys (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    key_hash TEXT NOT NULL UNIQUE,
    masked_key TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at DATETIME NULL
);
`

// Migrate executes initial schema creation on SQLite writer connection.
func (db *DB) Migrate(ctx context.Context) error {
	if _, err := db.writer.ExecContext(ctx, schemaDDL); err != nil {
		return fmt.Errorf("failed to execute sqlite schema migration: %w", err)
	}

	// Ensure newer route columns exist for existing databases
	alterColumns := []struct {
		name string
		ddl  string
	}{
		{name: "validation_rules", ddl: `ALTER TABLE routes ADD COLUMN validation_rules TEXT DEFAULT '[]';`},
		{name: "base_backoff_ms", ddl: `ALTER TABLE routes ADD COLUMN base_backoff_ms INTEGER NOT NULL DEFAULT 0;`},
		{name: "max_backoff_ms", ddl: `ALTER TABLE routes ADD COLUMN max_backoff_ms INTEGER NOT NULL DEFAULT 0;`},
		{name: "retry_non_idempotent", ddl: `ALTER TABLE routes ADD COLUMN retry_non_idempotent INTEGER NOT NULL DEFAULT 0;`},
	}

	for _, col := range alterColumns {
		var count int
		err := db.writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('routes') WHERE name = ?;`, col.name).Scan(&count)
		if err != nil {
			return fmt.Errorf("failed to inspect %s column on routes: %w", col.name, err)
		}
		if count == 0 {
			if _, err := db.writer.ExecContext(ctx, col.ddl); err != nil {
				return fmt.Errorf("failed to add %s column to routes: %w", col.name, err)
			}
		}
	}

	if _, err := db.writer.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_requests_dlq ON requests (status) WHERE status = 'dlq';`); err != nil {
		return fmt.Errorf("failed to ensure idx_requests_dlq: %w", err)
	}

	return nil
}
