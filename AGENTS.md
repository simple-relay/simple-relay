# Simple HTTP Relay — Agent Mandates

> **Tagline:** One binary. No infrastructure. Reliable HTTP delivery.  
> **Core Mission:** A tiny, self-hosted HTTP reliability layer providing durable webhook ingestion, reliable retries with jitter, DLQ replay, and client-abort-aware outbound proxying.

---

## 1. Project Reference Docs
- [project-summary.md](docs/project-summary.md) — Product vision, core use cases, reliability rules, and scope boundaries.
- [architecture.md](docs/architecture.md) — System architecture, SQLite WAL single-writer model, schema, API contracts, and project directory layout.
- [roadmap.md](docs/roadmap.md) — Phased delivery plan (Phase 1: Core Outbound & Storage, Phase 2: Inbound Webhooks & Security, Phase 3: Embedded UI & Maintenance).

---

## 2. Core Technical Decisions
- **Language & Runtime:** Go (1.22+).
- **HTTP Routing:** Standard library `net/http` ServeMux (Go 1.22+ method matching & path parameters like `/api/v1/webhook/{route_name}`). Keep external routing dependencies at zero.
- **Database & Driver:** Embedded SQLite using **pure Go driver** (`modernc.org/sqlite`) to support fully static binaries without CGO (`CGO_ENABLED=0`).
- **Primary Keys:** **ULID lowercase string** for all database primary keys (preserves monotonic chronological order and avoids SQLite B-Tree index fragmentation).
- **Storage Interface:** Adapter / Repository pattern (`internal/storage/storage.go`) decoupling business logic from SQLite.
- **Embedded Web UI:** Built into the binary via `go:embed` (Alpine.js + TailwindCSS; zero Node.js build pipeline).
- **Zero External Infra:** No Redis, Kafka, RabbitMQ, or PostgreSQL required for the default deployment. Compatible with Litestream for continuous streaming replication.

---

## 3. Non-Negotiable Operational Rules
- **Durability Golden Rule (Inbound):** NEVER return HTTP `200 OK` or `202 Accepted` to the sender until the raw, unmodified byte payload is durably committed to SQLite WAL storage.
- **Client Disconnect Cancellation (Sync Outbound):** Always bind upstream outgoing calls to incoming request context (`r.Context()`). If the caller disconnects or aborts, immediately cancel in-flight upstream requests and stop retrying.
- **Bounded Retries & Jitter:** Never retry forever. Enforce `max_attempts` and timeouts (`timeout_ms`, `total_timeout_ms`). Always apply exponential backoff with full jitter to eliminate Thundering Herd retry storms.
- **Storage Bloat Protection:** Store unmodified raw request payloads in `requests.body`. ALWAYS truncate upstream response bodies in `attempts.response_body` to **32 KB max**.
- **SQLite Concurrency Model:** Run SQLite in WAL mode. Strictly enforce **1 dedicated writer connection** (`SetMaxOpenConns(1)`) with micro-batch worker to eliminate `SQLITE_BUSY` lock contention. Use pooled reader connections (`SetMaxOpenConns(runtime.NumCPU() * 2)`).
- **Cryptographic Preservation:** Compute and verify webhook HMAC signatures against the raw byte body before any parsing or ingestion. Never mutate payload bytes.

---

## 4. Engineering Principles
- **YAGNI & Minimalism:** Implement only what is explicitly requested. Keep logic concrete until reuse is proven.
- **Surgical Execution:** Minimal diff. Touch only the files and lines directly relevant to the task. Do not perform drive-by refactorings.
- **Idiomatic Go:** Follow standard Go idioms, clear error handling, structured logging via `log/slog`, and clean separation of concerns.
- **Directory Layout:** Adhere strictly to the Go layout defined in [architecture.md](docs/architecture.md#L302-L335) (`cmd/relay`, `internal/api`, `internal/engine`, `internal/model`, `internal/signature`, `internal/storage`, `ui`).

---

## 5. Verification-Driven Development (Testing)
- **TDD & Reproduction First:** For bug fixes, write a failing reproduction test before implementing the fix.
- **Verification Loops:** A task is incomplete until test suites pass.
- **Standard Commands:**
  - Run tests: `go test -v ./...`
  - Run tests with race detection: `go test -race ./...`
  - Verify static compilation: `CGO_ENABLED=0 go build -o /dev/null ./cmd/relay`

---

## 6. Source Control & Git Policy
- **Prompt After Code Changes:** After completing a request that involves code changes, ask the user if they would like to commit the changes.
- **Issue Branches & Pull Requests:** If the thread/task is associated with a remote repository issue, check out a new branch before committing, and create a pull request after pushing.
- **User Confirmation:** Only execute state-changing git commands (`git add`, `git commit`, `git push`, `git checkout`, etc.) when instructed or explicitly confirmed by the user.

---

## 7. How to Start a Task
1. Review relevant specifications in [architecture.md](docs/architecture.md) and [roadmap.md](docs/roadmap.md).
2. Propose a **Surgical Plan** with clear verification steps.
3. Execute only after plan approval.
4. Verify with `go test -race ./...`.
5. Summarize changes and update project documentation (`docs/roadmap.md`, `docs/architecture.md`) if architecture or milestones change.

---

## 8. Language & Documentation Policy
- **Flexible Interaction Language:** Conversations and discussions with the user may be conducted in any preferred language (e.g., Vietnamese, English).
- **Strict English for Project Artifacts:** All codebase contents, source code, Go comments, docstrings, variable/function identifiers, error messages, log messages, Makefiles, tests, and documentation (`.md` files) MUST be written in English.
- **Design Specifications Location:** When completing brainstorming or design sessions, store all design specification documents in `docs/specs/YYYY-MM-DD-<topic>-design.md`.

