package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestOpen_WALModeAndConnectionIsolation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath, "NORMAL")
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.Ping(ctx); err != nil {
		t.Fatalf("failed to ping database: %v", err)
	}

	// Verify journal_mode is WAL
	var journalMode string
	err = db.Writer().QueryRowContext(ctx, "PRAGMA journal_mode;").Scan(&journalMode)
	if err != nil {
		t.Fatalf("failed to query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("expected journal_mode 'wal', got '%s'", journalMode)
	}

	// Verify writer can write and reader can read
	_, err = db.Writer().ExecContext(ctx, "CREATE TABLE test_ping (id INT);")
	if err != nil {
		t.Fatalf("failed to create table via writer: %v", err)
	}

	_, err = db.Writer().ExecContext(ctx, "INSERT INTO test_ping VALUES (42);")
	if err != nil {
		t.Fatalf("failed to insert via writer: %v", err)
	}

	var val int
	err = db.Reader().QueryRowContext(ctx, "SELECT id FROM test_ping;").Scan(&val)
	if err != nil {
		t.Fatalf("failed to read via reader: %v", err)
	}
	if val != 42 {
		t.Fatalf("expected val 42, got %d", val)
	}
}

func TestOpen_SynchronousModes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 1. Default/NORMAL -> PRAGMA synchronous = 1
	dbNormal, err := Open(filepath.Join(t.TempDir(), "normal.db"), "NORMAL")
	if err != nil {
		t.Fatalf("failed to open sqlite with NORMAL: %v", err)
	}
	defer dbNormal.Close()

	var syncVal int
	if err := dbNormal.Writer().QueryRowContext(ctx, "PRAGMA synchronous;").Scan(&syncVal); err != nil {
		t.Fatalf("failed to query PRAGMA synchronous: %v", err)
	}
	if syncVal != 1 {
		t.Fatalf("expected synchronous=1 (NORMAL), got %d", syncVal)
	}

	// 2. FULL (case-insensitive) -> PRAGMA synchronous = 2
	dbFull, err := Open(filepath.Join(t.TempDir(), "full.db"), "full")
	if err != nil {
		t.Fatalf("failed to open sqlite with full: %v", err)
	}
	defer dbFull.Close()

	if err := dbFull.Writer().QueryRowContext(ctx, "PRAGMA synchronous;").Scan(&syncVal); err != nil {
		t.Fatalf("failed to query PRAGMA synchronous on full db: %v", err)
	}
	if syncVal != 2 {
		t.Fatalf("expected synchronous=2 (FULL), got %d", syncVal)
	}

	// 3. Empty string -> defaults to NORMAL (1)
	dbDefault, err := Open(filepath.Join(t.TempDir(), "default.db"), "")
	if err != nil {
		t.Fatalf("failed to open sqlite with empty syncMode: %v", err)
	}
	defer dbDefault.Close()

	if err := dbDefault.Writer().QueryRowContext(ctx, "PRAGMA synchronous;").Scan(&syncVal); err != nil {
		t.Fatalf("failed to query PRAGMA synchronous on default db: %v", err)
	}
	if syncVal != 1 {
		t.Fatalf("expected synchronous=1 (NORMAL) for empty syncMode, got %d", syncVal)
	}

	// 4. Invalid modes ("OFF", "0", "EXTRA") -> rejected with error
	for _, invalid := range []string{"OFF", "0", "EXTRA", "NORMAL; DROP TABLE routes;"} {
		if badDB, err := Open(filepath.Join(t.TempDir(), "bad.db"), invalid); err == nil {
			_ = badDB.Close()
			t.Fatalf("expected error for invalid syncMode %q, got nil", invalid)
		}
	}
}

func TestMigrate_LegacyRoutesTableUpgrade(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	db, err := Open(filepath.Join(t.TempDir(), "legacy.db"), "NORMAL")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	// Seed a legacy pre-#83 routes table without base_backoff_ms, max_backoff_ms, or retry_non_idempotent
	legacyRoutesDDL := `
CREATE TABLE routes (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    path TEXT UNIQUE NOT NULL,
    destination_url TEXT NOT NULL,
    type TEXT NOT NULL,
    mode TEXT NOT NULL,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    timeout_ms INTEGER NOT NULL DEFAULT 10000,
    total_timeout_ms INTEGER NOT NULL DEFAULT 30000,
    retry_strategy TEXT NOT NULL DEFAULT 'exponential',
    signature_provider TEXT DEFAULT 'none',
    signature_secret TEXT,
    active INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);`
	if _, err := db.Writer().ExecContext(ctx, legacyRoutesDDL); err != nil {
		t.Fatalf("failed to create legacy routes table: %v", err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("db.Migrate failed on legacy routes table: %v", err)
	}

	// Verify all 4 ALTER TABLE columns now exist
	for _, col := range []string{"validation_rules", "base_backoff_ms", "max_backoff_ms", "retry_non_idempotent"} {
		var count int
		if err := db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('routes') WHERE name = ?;`, col).Scan(&count); err != nil {
			t.Fatalf("failed to query pragma_table_info for %s: %v", col, err)
		}
		if count != 1 {
			t.Fatalf("expected column %s to exist after migration, got count=%d", col, count)
		}
	}
}
