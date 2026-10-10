# Storage Retention Cleaner — Technical Design Specification

> **Date:** 2026-10-10  
> **Status:** Approved  
> **Author:** pmlonng (<phanminlongg@gmail.com>)  
> **Target Phase:** Phase 3 — Embedded Web UI & Lifecycle Maintenance  
> **Task:** Storage Retention Cleaner (`internal/engine/cleaner.go`)  
> **Related Epics:** SQLite Storage Engine, Multi-Database Readiness (PostgreSQL / MySQL)  
> **GitHub Issue:** [#15](https://github.com/simple-relay/simple-relay/issues/15)

---

## 1. Overview & Problem Statement

Simple HTTP Relay ingests, persists, and relays HTTP requests with durable guarantees. In production environments processing millions of requests, unbounded persistence of historical requests and attempt audit logs leads to rapid disk space exhaustion and degraded SQLite B-Tree performance.

### 1.1 Requirements & Scope
- **Configurable Retention:**
  - Purge successful requests (`status = 'success'`) older than `RELAY_RETENTION_SUCCESS_DAYS` (default: 7 days).
  - Purge failed/exhausted requests (`status IN ('failed', 'dlq')`) older than `RELAY_RETENTION_FAILED_DAYS` (default: 30 days).
  - Active and in-flight requests (`status IN ('pending', 'processing')`) **must never** be purged, regardless of age.
- **Cascade Deletion:**
  - Removing a parent request must atomically and cleanly remove all associated attempt history records in `attempts`.
- **Concurrency & Lock Contention:**
  - Deletions must occur in bounded chunks (e.g., batches of 1,000 records) to yield the dedicated single-writer SQLite connection between batches, preventing `SQLITE_BUSY` lock contention against inbound `BatchWriter` ingestion.
- **Storage Page Reclamation:**
  - Execute `PRAGMA incremental_vacuum(1000)` only when records were pruned to return unused database pages to the host filesystem without taking exclusive locks.
- **Multi-Database Readiness (Adapter Pattern):**
  - Design the maintenance layer as a clean storage-agnostic adapter (`RetentionRepository`). SQLite-specific dialect nuances (such as `PRAGMA incremental_vacuum`) must remain encapsulated within the SQLite adapter so future PostgreSQL / MySQL drivers can be plugged in seamlessly.
- **Future CLI Compatibility:**
  - Provide an isolated, exportable execution method (`CleanOnce(ctx)`) so subsequent CLI commands or ad-hoc operations can invoke a prune pass directly.

---

## 2. Core Architectural Decisions

### 2.1 Storage Adapter Interface (`internal/storage/storage.go`)

Decouple maintenance operations from SQLite specifics:

```go
// RetentionRepository defines maintenance operations for storage lifecycle management.
// Designed as an adapter interface to support SQLite, PostgreSQL, and MySQL backends.
type RetentionRepository interface {
    // PruneExpiredRequests removes expired requests matching retention policies in bounded batches.
    // It cascades deletion to related attempts and returns the total number of requests purged.
    PruneExpiredRequests(ctx context.Context, successCutoff, failedCutoff time.Time, batchSize int) (int64, error)

    // ReclaimStorage performs dialect-specific disk page reclamation.
    // In SQLite, this executes PRAGMA incremental_vacuum.
    // In PostgreSQL/MySQL, this is a no-op (managed by database daemon autovacuum).
    ReclaimStorage(ctx context.Context, maxPages int) error
}

// Storage interface embeds RetentionRepository
type Storage interface {
    RouteRepository
    RequestRepository
    AttemptRepository
    RetentionRepository

    Ping(ctx context.Context) error
    Close() error
}
```

---

### 2.2 SQLite Implementation & Chunked Batch Deletion (`internal/storage/sqlite/retention_repo.go`)

To avoid holding a long-running transaction over millions of rows, `PruneExpiredRequests` performs chunked deletions in a loop:

```sql
DELETE FROM requests
WHERE id IN (
    SELECT id FROM requests
    WHERE (status = 'success' AND created_at < ?)
       OR (status IN ('failed', 'dlq') AND created_at < ?)
    LIMIT ?
);
```

#### Execution Logic:
1. Validate `batchSize > 0` (default to 1,000 if not specified).
2. Execute batch query inside a loop:
   - Check `ctx.Err()` to respect cancellation.
   - Run `ExecContext` with `(successCutoff, failedCutoff, batchSize)`.
   - Read `RowsAffected()`.
   - Accumulate into `totalPurged`.
   - If `rowsAffected < int64(batchSize)` (or `rowsAffected == 0`), break loop immediately.
3. Foreign key constraints (`PRAGMA foreign_keys = ON;`) coupled with schema definition `REFERENCES requests(id) ON DELETE CASCADE` automatically delete dependent rows in `attempts`.
4. `ReclaimStorage`:
   - Validates `maxPages > 0` (default 1,000).
   - Executes `fmt.Sprintf("PRAGMA incremental_vacuum(%d);", maxPages)` against the single writer pool.

---

### 2.3 Cleaner Worker (`internal/engine/cleaner.go`)

#### Struct & Configuration:
```go
package engine

import (
    "context"
    "log/slog"
    "sync/atomic"
    "time"

    "github.com/simple-relay/simple-relay/internal/storage"
)

type CleanerConfig struct {
    Interval             time.Duration
    RetentionSuccessDays int
    RetentionFailedDays  int
    BatchSize            int
    VacuumPages          int
}

type CleanResult struct {
    PurgedRequests int64
    VacuumExecuted bool
    Duration       time.Duration
}

type Cleaner struct {
    repo       storage.RetentionRepository
    cfg        CleanerConfig
    logger     *slog.Logger
    isCleaning atomic.Bool
    stopCh     chan struct{}
    doneCh     chan struct{}
}
```

#### Lifecycle & Execution Rules:
1. **Concurrency Protection:** `isCleaning.CompareAndSwap(false, true)` prevents overlapping runs if a previous cleanup takes longer than the configured interval.
2. **`CleanOnce(ctx) (CleanResult, error)`:**
   - Computes UTC cutoffs:
     - `successCutoff = time.Now().UTC().AddDate(0, 0, -c.cfg.RetentionSuccessDays)`
     - `failedCutoff = time.Now().UTC().AddDate(0, 0, -c.cfg.RetentionFailedDays)`
   - Invokes `c.repo.PruneExpiredRequests(ctx, successCutoff, failedCutoff, c.cfg.BatchSize)`.
   - If `purged > 0`: executes `c.repo.ReclaimStorage(ctx, c.cfg.VacuumPages)`.
   - Logs structured execution metrics via `slog`.
3. **`Start(ctx context.Context)`:**
   - Launches background goroutine.
   - Executes an initial `CleanOnce(ctx)` pass on startup to clear backlog.
   - Loops on `time.NewTicker(c.cfg.Interval)` until `stopCh` or `ctx.Done()`.
4. **`Close() error`:**
   - Signals `stopCh`, waits for active cleanup pass to exit, and closes `doneCh`.

---

### 2.4 Configuration & Service Integration

#### 1. Configuration in `internal/config/config.go`:
- Field `CleanerInterval time.Duration`:
  - Default: `1 * time.Hour`.
  - Env: `RELAY_CLEANER_INTERVAL` (parsed using `time.ParseDuration`).
  - Flag: `-cleaner-interval`.
- Retains existing `RetentionSuccessDays` (default: 7) and `RetentionFailedDays` (default: 30).

#### 2. Wire-up in `cmd/relay/main.go`:
```go
cleaner := engine.NewCleaner(db, engine.CleanerConfig{
    Interval:             cfg.CleanerInterval,
    RetentionSuccessDays: cfg.RetentionSuccessDays,
    RetentionFailedDays:  cfg.RetentionFailedDays,
    BatchSize:            1000,
    VacuumPages:          1000,
}, logger)

cleaner.Start(ctx)
defer cleaner.Close()
```

#### Shutdown Sequencing:
1. `srv.Shutdown(shutdownCtx)` — Stop accepting new inbound HTTP requests.
2. `cleaner.Close()` — Terminate cleaner loop and wait for in-flight prune operation.
3. `batchWriter.Close()`, `dispatcher.Close()`, `workerPool.Close()`.
4. `db.Close()` — Close SQLite reader and writer connections.

---

## 3. Verification & Testing Strategy

### 3.1 Unit Testing (`internal/engine/cleaner_test.go`)
- Mock implementation of `storage.RetentionRepository`.
- **Test cases:**
  - Verify date cutoff computation matches specified days.
  - Verify `ReclaimStorage` is invoked when `purged > 0`, and skipped when `purged == 0`.
  - Verify overlapping concurrent calls to `CleanOnce` are safely guarded.
  - Verify resilience: repository errors are logged without panics.
  - Verify background worker lifecycle: startup run, ticker trigger, and graceful shutdown.

### 3.2 Integration Testing (`internal/storage/sqlite/retention_repo_test.go`)
- Real SQLite database with WAL and incremental vacuum enabled.
- **Test cases:**
  - **Seed & Purge:**
    - Expired `success` requests (> 7 days) -> purged.
    - Active `success` requests (< 7 days) -> kept.
    - Expired `failed` and `dlq` requests (> 30 days) -> purged.
    - Active `failed` and `dlq` requests (< 30 days) -> kept.
    - Stale `pending` requests (> 30 days) -> kept (untouched).
  - **Cascade Verification:** Verify child `attempts` rows for purged requests are automatically deleted.
  - **Batching Verification:** Seed 5 expired requests with `batchSize = 2`, verify chunked iterations execute completely.
  - **Vacuum Execution:** Verify `ReclaimStorage(ctx, 1000)` succeeds on WAL database.

### 3.3 System Verification Commands
- `go test -v ./...`
- `go test -race ./...`
- `CGO_ENABLED=0 go build -o /dev/null ./cmd/relay`
