# Database Migrations Engine — Technical Design Specification

> **Date:** 2026-10-09  
> **Status:** Approved  
> **Author:** pmlonng (<phanminlongg@gmail.com>)  
> **Target Milestone:** Phase 3 — Milestone 3.2: Task 3.2.4  
> **Related Epics:** SQLite Storage Engine, Multi-Database Readiness (PostgreSQL / MySQL)  

---

## 1. Overview & Problem Statement

Simple HTTP Relay relies on embedded SQLite as its primary zero-infrastructure storage backend. Currently, schema initialization is implemented via a monolithic hardcoded string (`CREATE TABLE IF NOT EXISTS`) in `internal/storage/sqlite/migrations.go`.

### The Problem
When new releases introduce database schema changes (e.g., adding table columns, new indexes, or altering constraints):
1. `CREATE TABLE IF NOT EXISTS` is completely ineffective for schema evolution: it ignores existing tables and cannot add columns or modify constraints.
2. Upgrading an existing instance to a newer binary version without proper schema migration will cause runtime SQL errors, crash data ingestion, or fail background dispatchers.
3. As the project roadmap progresses towards PostgreSQL (v1.0) and MySQL support, a cohesive, automated migration engine is required.

### The Solution
This specification details the **Versioned Database Migrations Engine**, ensuring:
1. **Automated Boot-Time Migrations:** On application startup, pending migrations are automatically discovered and applied sequentially before the HTTP server begins accepting traffic.
2. **Version Tracking (`schema_migrations`):** Applied migration versions and timestamps are tracked durably inside the database.
3. **Zero External Dependencies (`go:embed`):** SQL migration files are compiled directly into the static binary using standard Go `embed.FS`, preserving the "One binary. Zero infrastructure" guarantee.
4. **Transactional Atomicity & Rollback:** Each migration runs inside an isolated database transaction, failing fast with zero corrupt state if an error occurs.
5. **Multi-Dialect Readiness:** Clean folder organization supporting SQLite today, and PostgreSQL / MySQL in future releases.

---

## 2. Core Architectural Decisions

### 2.1 File Organization & `go:embed`
Migration files are stored as plain SQL files with a monotonic numeric prefix and a descriptive name:
```
internal/storage/migrations/
├── sqlite/
│   ├── 000001_initial_schema.sql
│   ├── 000002_add_index_optimizations.sql
│   └── embed.go
└── postgres/       # Reserved for Milestone 1.0 (Enterprise)
```

In `embed.go`:
```go
package migrations

import "embed"

//go:embed sqlite/*.sql
var SQLiteMigrations embed.FS

//go:embed postgres/*.sql
var PostgresMigrations embed.FS
```

### 2.2 Version Tracking Table (`schema_migrations`)
Before running any migrations, the engine ensures the tracking table exists:

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

### 2.3 Migration Execution State Machine
On database initialization (`db.Migrate(ctx)`):
1. **Acquire Dedicated Writer Lock:** Migration operates strictly on the single dedicated SQLite writer connection (`SetMaxOpenConns(1)`).
2. **Read Current State:** Query `SELECT version FROM schema_migrations ORDER BY version ASC`.
3. **Discover Embedded Files:** Parse and sort all `*.sql` files by their version prefix.
4. **Identify Pending Migrations:** Compare available files against recorded versions.
5. **Execute Pending Sequentially:** For each pending migration:
   - Begin a transaction (`BEGIN IMMEDIATE`).
   - Execute the SQL migration script.
   - Insert tracking record: `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`.
   - Commit the transaction (`COMMIT`).
6. **Fail-Fast Safety:** If any script fails:
   - Immediately roll back the active transaction (`ROLLBACK`).
   - Abort application startup with a structured error log identifying the failing file.
   - Prevent corrupt or partial schema states.

### 2.4 Backward Compatibility for Existing Databases
To ensure seamless upgrades for installations already running the initial schema without `schema_migrations`:
- The engine checks if table `routes` already exists.
- If `routes` exists but `schema_migrations` is empty, the baseline migration `000001_initial_schema.sql` is automatically marked as applied without re-running the creation statements.

---

## 3. Storage Layer Interfaces & Implementation

### 3.1 Migration Runner (`internal/storage/migrator/migrator.go`)

```go
package migrator

import (
    "context"
    "database/sql"
    "embed"
    "fmt"
    "io/fs"
    "log/slog"
    "sort"
    "strconv"
    "strings"
)

type Migration struct {
    Version int
    Name    string
    SQL     string
}

type Runner struct {
    db     *sql.DB
    fs     embed.FS
    dir    string
    logger *slog.Logger
}

func NewRunner(db *sql.DB, fs embed.FS, dir string, logger *slog.Logger) *Runner {
    return &Runner{
        db:     db,
        fs:     fs,
        dir:    dir,
        logger: logger,
    }
}

// Up applies all pending migrations in ascending order.
func (r *Runner) Up(ctx context.Context) error { ... }

// CurrentVersion returns the highest applied migration version.
func (r *Runner) CurrentVersion(ctx context.Context) (int, error) { ... }
```

---

## 4. Multi-Database (MySQL & PostgreSQL) Readiness

### Dialect Differences Handling
- SQLite, PostgreSQL, and MySQL have syntax variations for certain DDL commands (e.g., `DATETIME` vs `TIMESTAMPTZ`, `BLOB` vs `BYTEA`, auto-increment semantics).
- Rather than translating SQL at runtime with complex AST parsers, the migration runner reads from dialect-specific embedded directories:
  - SQLite: `internal/storage/migrations/sqlite/*.sql`
  - PostgreSQL: `internal/storage/migrations/postgres/*.sql`
  - MySQL: `internal/storage/migrations/mysql/*.sql`
- The core runner engine (`migrator.Runner`) is database-agnostic and accepts any standard `*sql.DB` connection.

---

## 5. Verification & Testing Strategy

1. **Clean Installation Test:**
   - Execute `migrator.Up()` on an empty in-memory SQLite database.
   - Assert all tables (`routes`, `requests`, `attempts`, `schema_migrations`) are created and `schema_migrations` contains all version records.
2. **Incremental Upgrade Test:**
   - Apply migrations up to version 1.
   - Insert mock records.
   - Apply migration version 2.
   - Assert existing records remain intact and new schema changes are applied.
3. **Idempotency Test:**
   - Run `migrator.Up()` twice in succession.
   - Assert second run applies 0 migrations and returns `nil`.
4. **Transaction Rollback on Error:**
   - Inject a migration containing invalid SQL syntax.
   - Assert migration fails, transaction rolls back, and `schema_migrations` does not record the failed version.
5. **Existing DB Baseline Migration:**
   - Create a database with the old unversioned schema.
   - Run `migrator.Up()`; verify it baselines version 1 without error.
