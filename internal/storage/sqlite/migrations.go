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
`

// Migrate executes initial schema creation on SQLite writer connection.
func (db *DB) Migrate(ctx context.Context) error {
	if _, err := db.writer.ExecContext(ctx, schemaDDL); err != nil {
		return fmt.Errorf("failed to execute sqlite schema migration: %w", err)
	}

	// Ensure validation_rules column exists for existing databases
	var count int
	err := db.writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('routes') WHERE name = 'validation_rules';`).Scan(&count)
	if err == nil && count == 0 {
		if _, err := db.writer.ExecContext(ctx, `ALTER TABLE routes ADD COLUMN validation_rules TEXT DEFAULT '[]';`); err != nil {
			return fmt.Errorf("failed to add validation_rules column to routes: %w", err)
		}
	}

	return nil
}
