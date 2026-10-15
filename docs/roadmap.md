# Simple Relay — Roadmap

> **Tagline:** One binary. No infrastructure. Reliable HTTP delivery.  
> **Source Document:** See [architecture.md](architecture.md) for architectural details and API contracts.

---

## 1. Executive Summary & Current Status

### Execution Snapshot
| Phase | Focus Area | Status | Progress |
| :--- | :--- | :--- | :--- |
| **Phase 1** | Outbound Relay & Core Storage Engine | **Completed** | 100% Completed |
| **Phase 2** | Inbound Webhooks & Security Pipeline | **Completed** | 100% Completed |
| **Phase 3** | Embedded Web UI & Lifecycle Maintenance | **In Progress** | 55% Completed |
| **Phase 4** | CLI, Observability & Alerting | Backlog | 0% |
| **Phase 5** | Multi-node Clustering & Advanced Features | Backlog | 0% |

### Active Execution Pointer
- **Current Phase:** Phase 3 — Embedded Web UI & Lifecycle Maintenance
- **Next Immediate Focus:** Admin Password Authentication & CSRF Protection ([#14](https://github.com/simple-relay/simple-relay/issues/14)).

---

## 2. High-Level Roadmap Summary

* **Phase 1 — Outbound Relay & Core Storage Engine:** Build the foundational single-writer SQLite storage engine (WAL mode), route configuration handling with in-memory caching and hot-reloading, synchronous outbound HTTP proxy with client-disconnect context cancellation, durable asynchronous job ingestion, and background worker delivery pool.
* **Phase 2 — Inbound Webhooks & Security Pipeline:** Establish durable inbound webhook ingestion with raw-byte cryptographic preservation, HMAC signature adapters (Stripe, GitHub, Generic HMAC-SHA256), burst-protective micro-batch persistence, DLQ state machine, and replay APIs.
* **Phase 3 — Embedded Web UI & Lifecycle Maintenance:** Deliver embedded zero-dependency Web UI (`go:embed`, Alpine.js, Tailwind CSS), dynamic URL navigation and deep-linking, admin authentication with CSRF protection, scheduled retention pruning with incremental SQLite vacuuming, versioned database schema migration engine (`schema_migrations`), Litestream replication guide, automated multi-platform release pipeline (release-please + GoReleaser), and in-app upstream version update checker.
* **Phase 4 — CLI, Observability & Alerting:** Provide CLI inspection binary, Slack/Discord/Email alerting on DLQ threshold breaches, API key management, Prometheus metrics (`/metrics`), and extended signature verification adapters.
* **Phase 5 — Multi-node Clustering & Advanced Features:** Add built-in PostgreSQL storage backend, distributed multi-node worker clustering, and asymmetric public-key webhook verification.

---

## 3. Detailed Roadmap & Execution Checklist

### Phase 1 — Outbound Relay & Core Storage Engine

- [x] **Project Skeleton & Build Toolchain**
  - Standard Go layout (`cmd/relay`, `internal/...`).
  - Strict zero-CGO static compilation (`CGO_ENABLED=0 go build`).
  - Standard library configuration loader (`internal/config`) with 12-factor env vars.
  - Signal-aware graceful shutdown (`SIGINT`, `SIGTERM`) with `net/http` server.
  - Base `GET /healthz` endpoint with Go 1.22+ ServeMux routing.
- [x] **SQLite Connection Pool Isolation**
  - Initialize pure-Go driver (`modernc.org/sqlite`).
  - Writer pool isolation: strictly `SetMaxOpenConns(1)` with `PRAGMA busy_timeout = 5000`.
  - Reader pool: pooled `SetMaxOpenConns(NumCPU * 2)`.
  - WAL mode enablement and performance pragmas (`synchronous=NORMAL`, `foreign_keys=ON`, `auto_vacuum=INCREMENTAL`).
- [x] **SQLite DDL Migrations**
  - Create table `routes` with unique constraints on `name` and `path`.
  - Create table `requests` with dispatcher index `(status, next_attempt_at) WHERE status IN ('pending', 'failed')`.
  - Create table `attempts` with foreign key cascade to `requests(id)`.
- [x] **Domain Models & Bloat Protection Helper**
  - Entity `Route` with validation rules.
  - Entity `Request` with status lifecycle states.
  - Entity `Attempt` with `TruncateResponseBody` (enforcing 32 KB max response storage).
  - Monotonic lowercase ULID generator (`internal/model/ulid.go`) for chronological primary keys.
- [x] **Route SQLite Repository**
  - Implemented `storage.RouteRepository` in `internal/storage/sqlite/route_repo.go`.
  - CRUD operations: `CreateRoute`, `GetRouteByID`, `GetRouteByName`, `GetRouteByPath`, `ListRoutes`, `UpdateRoute`, `DeleteRoute`.
  - Constraint conflict translation to `storage.ErrConflict` and missing records to `storage.ErrNotFound`.
- [x] **In-Memory Route Registry & Hot-Reloading**
  - Implemented `RouteRegistry` in `internal/engine/route_registry.go`.
  - Sub-microsecond $O(1)$ read lookups using `sync.RWMutex` (`byName`, `byPath`, `byID`).
  - Immediate cache invalidation and atomic hot-reload on route mutations without service restart.
- [x] **Retry Engine (`internal/engine/retry.go`)**
  - Calculate exponential backoff: $T_{\text{wait}} = \text{base} \times 2^{\text{attempt}}$.
  - Implement Full Jitter algorithm: sleep duration uniformly distributed in $[0, T_{\text{wait}}]$ to prevent thundering herds.
  - Parse `Retry-After` header (seconds and HTTP-date) on HTTP 429 responses.
  - Support retryable condition checks (status 429, 500, 502, 503, 504, and network/connection errors).
- [x] **Attempts Audit Storage (`internal/storage/sqlite/attempt_repo.go`)**
  - Implement `storage.AttemptRepository` for recording delivery attempts.
  - Store execution duration in milliseconds, status code, error message, and truncated response body (32 KB limit).
  - Query attempt logs by `request_id`.
- [x] **Synchronous Relay Handler (`internal/api/handler_relay.go`)**
  - Mount endpoint `POST /api/v1/relay/{route_name}`.
  - Resolve route from `RouteRegistry` in memory ($O(1)$).
  - Execute HTTP forwarding to route `destination_url` with synchronous retry loop.
  - Return final upstream response body, status code, and response headers to the client.
- [x] **Client-Disconnect Context Cancellation**
  - Bind upstream `http.Request` context directly to incoming client request context (`r.Context()`).
  - Immediately abort upstream flight and terminate retry loop if the caller disconnects or aborts.
- [x] **Header Propagation & Idempotency**
  - Generate and inject unique `X-Relay-Request-ID` (lowercase ULID).
  - Propagate downstream `Idempotency-Key` or `X-Relay-Idempotency-Key`.
  - Support `X-Relay-Timeout` header to override default route timeout for the request.
- [x] **Requests Storage Repository (`internal/storage/sqlite/request_repo.go`)**
  - Implement `storage.RequestRepository` for durable request storage.
  - Fetch pending jobs (`status IN ('pending', 'failed') AND next_attempt_at <= ?`).
  - Atomic status transitions (`pending` $\rightarrow$ `processing` $\rightarrow$ `success` / `failed` / `dlq`).
- [x] **Single-Writer Ingestion Batch Worker (`internal/engine/batch_writer.go`)**
  - In-memory buffered ingestion channel (`chan ingestionItem`).
  - Single-writer goroutine micro-batching writes (up to 50 items or 10ms timeout).
  - Durability guarantee: caller goroutine blocks until batch transaction is committed to SQLite WAL.
- [x] **Asynchronous Job Ingestion API (`POST /api/v1/jobs/{route_name}`)**
  - Accept request body and headers.
  - Push through batch writer to durably persist in SQLite.
  - Return `202 Accepted` with `{"job_id": "<ulid>", "status": "queued"}`.
- [x] **Job Inspection API (`GET /api/v1/jobs/{job_id}`)**
  - Return job status, attempt count, last error, timestamps, and full attempt history.
- [x] **Delivery Dispatcher (`internal/engine/dispatcher.go`)**
  - Polling loop + instant channel wakeup for pending jobs due for execution.
  - Claim due records and dispatch to Worker Pool.
- [x] **Worker Pool (`internal/engine/worker_pool.go`)**
  - Fixed pool of worker goroutines (`RELAY_WORKER_CONCURRENCY`, default 20).
  - Forward request to `destination_url` with per-attempt timeout.
  - Transition status to `success` on 2xx/acceptable code, or schedule next retry timestamp via backoff + jitter.
  - Transition to `dlq` when `attempts_count >= max_attempts`.

---

### Phase 2 — Inbound Webhooks & Security Pipeline

- [x] **Raw Byte Body Preserving Receiver**
  - Ensure zero payload mutation: capture exact bytes into memory/disk.
  - Enforce maximum body size (`RELAY_MAX_BODY_SIZE_MB`, default 10MB) with `http.MaxBytesReader`.
- [x] **Signature Verification Adapters (`internal/signature`)** ([#9](https://github.com/simple-relay/simple-relay/issues/9))
  - Define `Verifier` interface: `Verify(header http.Header, secret string, rawBody []byte) error`.
  - Stripe HMAC adapter: parse `t=timestamp,v1=signature`, verify HMAC-SHA256 across candidate signatures using constant-time comparison.
  - GitHub HMAC adapter: verify `sha256=<hex>` against `X-Hub-Signature-256`.
  - Generic HMAC-SHA256 adapter: auto-detect headers (`X-Signature`, `X-Hub-Signature-256`, `X-Signature-SHA256`), handle hex and base64 with constant-time comparison.
  - Reject invalid signatures at the HTTP boundary with `401 Unauthorized` before SQLite persistence.
  - See design specification: [2026-10-09-signature-verification-adapters-design.md](specs/2026-10-09-signature-verification-adapters-design.md).
- [x] **Inbound Webhook Endpoint (`POST /api/v1/webhook/{route_name}`)**
  - Match route from `RouteRegistry` (`route.Type == "inbound"`).
  - Support `POST`, `PUT`, `PATCH` methods (returns 405 for other methods).
  - Preserve provider signature headers intact for destination verification.
  - Durability Golden Rule: write to SQLite WAL via Batch Worker before returning `200 OK`.
  - Return `200 OK` with `{"relay_id": "<ulid>", "status": "persisted"}`.
- [x] **Internal Webhook Forwarding Dispatch**
  - Dispatch persisted inbound webhooks to internal destination URL via Worker Pool.
  - Exponential backoff with jitter on internal destination failures.
- [x] **Dead Letter Queue (DLQ) State Machine**
  - Mark requests as `dlq` upon reaching `max_attempts`.
  - Record complete diagnostic history in `attempts`.
- [x] **DLQ Replay Management API**
  - Endpoint `POST /api/v1/dlq/{request_id}/replay`: Reset attempts count, set status to `pending`, set `next_attempt_at = NOW()`, trigger dispatcher wakeup.
  - Endpoint `POST /api/v1/dlq/replay-all`: Batch replay all filtered DLQ records.
  - Endpoint `GET /api/v1/dlq`: Filterable and paginated DLQ inspection API.

---

### Phase 3 — Embedded Web UI & Lifecycle Maintenance

- [x] **Embedded Assets & Base Layout (`ui/`)** ([#10](https://github.com/simple-relay/simple-relay/issues/10))
  - Embed HTML templates and static assets using `go:embed`.
  - Zero Node.js build pipeline: Alpine.js + Tailwind CSS.
- [x] **Routes Management UI** ([#11](https://github.com/simple-relay/simple-relay/issues/11))
  - View all active routes (inbound & outbound).
  - Create, edit, and delete routes via UI modal.
  - Instant hot-reloading verification in UI.
- [x] **Requests & Attempts Explorer** ([#12](https://github.com/simple-relay/simple-relay/issues/12))
  - Filterable requests table: search by ID, status (`pending`, `success`, `dlq`), route name, date range.
  - Slide-over detail drawer: inspect request headers, raw body, attempt status codes, duration, and error logs.
- [x] **DLQ Dashboard & Replay Action** ([#13](https://github.com/simple-relay/simple-relay/issues/13))
  - Dedicated view for failed/DLQ events.
  - One-click individual replay and bulk replay buttons.
- [ ] **Dynamic URL Navigation & Deep Linking for Dashboard** ([#49](https://github.com/simple-relay/simple-relay/issues/49))
  - Synchronize active dashboard tab state (`routes`, `requests`, `dlq`) with browser URL hash/history.
  - Support deep-linking and browser back/forward navigation for request inspector drawer (`#requests?inspect=<ulid>`).
  - Native browser event handling (`hashchange` / `popstate`) with zero external frontend routing dependencies.
- [ ] **Admin Password Authentication & CSRF Protection** ([#14](https://github.com/simple-relay/simple-relay/issues/14))
  - Protect UI routes with session cookie authenticated against `RELAY_ADMIN_PASSWORD`.
  - CSRF token validation on all state-mutating requests (Route CRUD, Replay, Purge).
  - Login rate limiter to prevent brute-force attacks.
- [x] **Storage Retention Cleaner (`internal/engine/cleaner.go`)** ([#15](https://github.com/simple-relay/simple-relay/issues/15))
  - Periodic background worker (hourly/daily).
  - Purge successful requests older than `RELAY_RETENTION_SUCCESS_DAYS` (default 7 days).
  - Purge DLQ requests older than `RELAY_RETENTION_FAILED_DAYS` (default 30 days).
  - Execute `PRAGMA incremental_vacuum(1000)` to reclaim OS disk pages without exclusive table locks.
  - See design specification: [2026-10-10-storage-retention-cleaner-design.md](specs/2026-10-10-storage-retention-cleaner-design.md).
- [ ] **Litestream Disaster Recovery Integration** ([#16](https://github.com/simple-relay/simple-relay/issues/16))
  - Provide production configuration guide and Docker compose setup for Litestream S3 streaming replication.
  - Zero-SPOF SQLite durability verification.
- [ ] **Versioned Database Schema Migrations Engine (`schema_migrations`)** ([#17](https://github.com/simple-relay/simple-relay/issues/17))
  - Embed migration SQL files directly into the static binary with `go:embed`.
  - Maintain durable version history via `schema_migrations (version, name, applied_at)` table.
  - Sequentially execute pending migrations on boot within atomic SQLite transactions (`BEGIN IMMEDIATE`).
  - Implement fail-fast safety and automatic baseline migration for existing databases.
  - Multi-database architecture ready for SQLite, PostgreSQL, and MySQL adapters.
  - See design specification: [2026-10-09-database-migrations-engine-design.md](specs/2026-10-09-database-migrations-engine-design.md).
- [x] **Multi-Platform Build Configuration (`.goreleaser.yaml`)** ([#18](https://github.com/simple-relay/simple-relay/issues/18))
  - Zero-CGO static compilation (`CGO_ENABLED=0`) across Linux (`amd64`, `arm64`), macOS (`amd64`, `arm64`), and Windows (`amd64`).
  - Automated archive packaging (`.tar.gz`, `.zip`) with SHA-256 checksum generation (`checksums.txt`).
  - Multi-architecture Docker image build and push configuration.
- [x] **Automated Versioning & Release Workflow (`.github/workflows/release.yml`)** ([#19](https://github.com/simple-relay/simple-relay/issues/19))
  - Integrate `release-please` to parse Conventional Commits and maintain `CHANGELOG.md`.
  - Automatically manage SemVer release pull requests on `main`.
  - Trigger GoReleaser upon Release PR merge to attach cross-platform binaries to GitHub Releases.
- [ ] **In-App Version Update Checker (`internal/version`)** ([#20](https://github.com/simple-relay/simple-relay/issues/20))
  - Background non-blocking version checker querying upstream GitHub Releases (`simple-relay/simple-relay`) created by Release Please.
  - Dual discovery strategy: GitHub REST API with seamless fallback to rate-limit-free HTTP HEAD redirect trick.
  - Robust SemVer comparison using standard `golang.org/x/mod/semver`.
  - In-memory cache and configurable opt-out (`RELAY_CHECK_UPDATES=false`) for air-gapped deployments.
  - Expose version metadata via `GET /api/v1/system/version` and Web UI notification badge.
  - See design specification: [2026-10-09-version-check-design.md](specs/2026-10-09-version-check-design.md).
- [x] **Configurable Dispatcher Concurrency & Active WAL Checkpointing** ([#47](https://github.com/simple-relay/simple-relay/issues/47))
  - Expose `-workers` CLI flag and `RELAY_WORKER_CONCURRENCY` env var (default: 20) to scale outbound delivery throughput beyond ~111 ev/s.
  - Allow tuning dispatcher channel buffer capacity and poll interval (`RELAY_DISPATCHER_BUFFER`, `RELAY_DISPATCHER_POLL_MS`).
  - Implement active SQLite WAL checkpointing (`PRAGMA wal_checkpoint(PASSIVE)` / `TRUNCATE`) in the background cleaner to prevent unbounded WAL file growth during continuous high-throughput bursts.
  - Full benchmark data and analysis available in [benchmarks.md](benchmarks.md).
  - See design specification: [2026-10-13-configurable-dispatcher-concurrency-and-active-wal-checkpointing-design.md](specs/2026-10-13-configurable-dispatcher-concurrency-and-active-wal-checkpointing-design.md).
- [x] **Zero-Downtime Live Binary Upgrade & Socket Handoff** ([#51](https://github.com/simple-relay/simple-relay/issues/51))
  - Implement socket inheritance / listener handover via file descriptor passing (`net.FileListener`) across process restarts.
  - Add UNIX signal listener for `SIGUSR2` to spawn the upgraded binary without terminating network traffic.
  - Implement seamless graceful handover: child process inherits listener socket, starts serving immediately; parent stops listener, drains in-flight HTTP requests and batch writer buffer, and terminates cleanly.
  - Coordinate SQLite WAL and background worker dispatching during handover to avoid duplicate job claims or database lock contention.
  - Provide cross-platform safety: enable socket inheritance on POSIX (Linux/macOS) and fallback gracefully on Windows.
  - See design specification: [2026-10-12-zero-downtime-live-binary-upgrade-design.md](specs/2026-10-12-zero-downtime-live-binary-upgrade-design.md).

---

### Phase 4 — CLI, Observability & Alerting

- [ ] **CLI Inspection Binary (`cmd/relay-cli`)** ([#21](https://github.com/simple-relay/simple-relay/issues/21))
  - Command-line tool to inspect relay health, active routes, and queue depth.
  - Query in-flight jobs and Dead Letter Queue directly from terminal.
- [x] **Webhook Alert Notifications on DLQ Threshold Breaches** ([#22]
  - Push alert notifications to external webhooks (Slack, Discord, Email) when DLQ item count exceeds configured threshold.
- [ ] **API Key Authentication for Programmatic Management** ([#23](https://github.com/simple-relay/simple-relay/issues/23))
  - Secure API endpoints using API keys for machine-to-machine route management and job querying.
- [ ] **Prometheus Metrics Exporter Endpoint (`GET /metrics`)** ([#24](https://github.com/simple-relay/simple-relay/issues/24))
  - Expose Prometheus metrics tracking ingestion rate, delivery latency histograms, retry counts, and worker saturation.
- [ ] **Extended Signature Verification Adapters (`Shopify`, `Slack`, `Svix`)**
  - Implement Shopify base64 HMAC verification adapter.
  - Implement Slack v0 timestamp-based HMAC verification adapter.
  - Implement Standard Webhooks / Svix verification adapter.

---

### Phase 5 — Multi-node Clustering & Advanced Features

- [ ] **Built-in PostgreSQL Storage Backend (`internal/storage/postgres`)** ([#25](https://github.com/simple-relay/simple-relay/issues/25))
  - Implement pure-Go PostgreSQL storage adapter satisfying `storage.Storage` interface.
  - Zero-CGO static compilation (`CGO_ENABLED=0`) support.
  - Switchable via configuration while retaining SQLite as default zero-config option.
- [ ] **Multi-node Worker Clustering with Leader Election** ([#26](https://github.com/simple-relay/simple-relay/issues/26))
  - Support multi-instance relay deployments sharing centralized storage.
  - Distributed leader election and advisory locking to prevent duplicate worker dispatches.
- [x] **Asymmetric Public-Key Webhook Verification** ([#30](https://github.com/simple-relay/simple-relay/issues/30))
  - Support Ed25519 public-key signature verification adapter (e.g. DocuSign, Standard Webhooks).
  - Support RSA-SHA256 signature verification adapter with certificate validation (e.g. AWS SNS).
  - In-memory caching for public keys and X.509 certificates with TTL to eliminate network latency overhead.
  - Reject invalid signatures at HTTP boundary with `401 Unauthorized` before SQLite persistence.
  - Route configuration support for public keys or certificate trust settings.
  - See design specification: [2026-10-14-asymmetric-and-custom-validation-design.md](specs/2026-10-14-asymmetric-and-custom-validation-design.md).
