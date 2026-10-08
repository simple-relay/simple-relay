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

	db, err := Open(dbPath)
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
