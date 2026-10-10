package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"time"

	"github.com/simple-relay/simple-relay/internal/storage"
	_ "modernc.org/sqlite" // Pure Go SQLite driver (CGO_ENABLED=0 compatible)
)

// DB encapsulates isolated reader and writer SQLite connection pools and
// implements storage.Storage interface.
type DB struct {
	writer *sql.DB
	reader *sql.DB

	*RouteRepo
	*RequestRepo
	*AttemptRepo
	*RetentionRepo
}

var _ storage.Storage = (*DB)(nil)

// Open initializes SQLite in WAL mode with connection isolation:
// - Writer pool: 1 connection (SetMaxOpenConns(1)) to prevent lock contention.
// - Reader pool: NumCPU*2 connections for concurrent reads.
func Open(dsn string) (*DB, error) {
	// Writer connection pool
	writer, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite writer: %w", err)
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	writer.SetConnMaxLifetime(0)

	// Reader connection pool
	reader, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("failed to open sqlite reader: %w", err)
	}
	readerMax := runtime.NumCPU() * 2
	if readerMax < 4 {
		readerMax = 4
	}
	reader.SetMaxOpenConns(readerMax)
	reader.SetMaxIdleConns(readerMax)
	reader.SetConnMaxLifetime(1 * time.Hour)

	db := &DB{
		writer: writer,
		reader: reader,
	}
	db.RouteRepo = NewRouteRepo(db)
	db.RequestRepo = NewRequestRepo(db)
	db.AttemptRepo = NewAttemptRepo(db)
	db.RetentionRepo = NewRetentionRepo(db)

	// Apply WAL and performance pragmas on writer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA auto_vacuum = INCREMENTAL;",
	}

	for _, pragma := range pragmas {
		if _, err := db.writer.ExecContext(ctx, pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to execute pragma '%s': %w", pragma, err)
		}
	}

	return db, nil
}

// Writer returns the single-writer database pool for mutations.
func (db *DB) Writer() *sql.DB {
	return db.writer
}

// Reader returns the pooled database for concurrent queries.
func (db *DB) Reader() *sql.DB {
	return db.reader
}

// Ping verifies connectivity to both reader and writer pools.
func (db *DB) Ping(ctx context.Context) error {
	if err := db.writer.PingContext(ctx); err != nil {
		return fmt.Errorf("writer ping failed: %w", err)
	}
	if err := db.reader.PingContext(ctx); err != nil {
		return fmt.Errorf("reader ping failed: %w", err)
	}
	return nil
}

// Close gracefully closes both connection pools.
func (db *DB) Close() error {
	var errWriter, errReader error
	if db.writer != nil {
		errWriter = db.writer.Close()
	}
	if db.reader != nil {
		errReader = db.reader.Close()
	}
	if errWriter != nil {
		return errWriter
	}
	return errReader
}
