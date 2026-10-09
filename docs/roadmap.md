# Simple HTTP Relay — Roadmap

> **Tagline:** One binary. No infrastructure. Reliable HTTP delivery.  
> **Source Document:** See [architecture.md](architecture.md) for architectural details and API contracts.

---

## 1. Executive Summary & Current Status

### Execution Snapshot
| Phase | Focus Area | Status | Progress |
| :--- | :--- | :--- | :--- |
| **Phase 1** | Outbound Relay & Core Storage Engine | **Completed** | 100% Completed |
| **Phase 2** | Inbound Webhooks & Security Pipeline | In Progress | 80% |
| **Phase 3** | Embedded Web UI & Storage Retention | Planned | 0% |
| **v0.3** | CLI, Observability & Alerting | Backlog | 0% |
| **v1.0** | Enterprise Clustering & PostgreSQL | Backlog | 0% |

### Active Execution Pointer
- **Current Milestone:** Phase 2 — Milestone 2.1: Cryptographic Ingestion & Signature Adapters
- **Next Immediate Task:** `Task 2.1.2` — Signature Verification Adapters (`internal/signature`).

---

## 2. High-Level Roadmap Summary

* **Phase 1 — Outbound Relay & Core Storage Engine:** Build the foundational single-writer SQLite storage engine (WAL mode), route configuration handling with in-memory caching and hot-reloading, synchronous outbound HTTP proxy with client-disconnect context cancellation, durable asynchronous job ingestion, and background worker delivery pool.
* **Phase 2 — Inbound Webhook & Security:** Establish durable inbound webhook ingestion with raw-byte cryptographic preservation, HMAC signature adapters (Stripe, GitHub, Generic HMAC-SHA256), burst-protective micro-batch persistence, DLQ state machine, and replay APIs.
* **Phase 3 — UI, Maintenance & Release Automation:** Deliver embedded zero-dependency Web UI (`go:embed`, Alpine.js, Tailwind CSS), admin authentication with CSRF protection, scheduled retention pruning with incremental SQLite vacuuming, Litestream replication guide, automated multi-platform release pipeline (release-please + GoReleaser), and in-app upstream version update checker.
* **v0.3 — Utility & Extension:** Provide CLI inspection binary, Slack/Discord/Email alerting on DLQ threshold breaches, API key management, and Prometheus metrics (`/metrics`).
* **v1.0 — Enterprise Edition:** Add PostgreSQL pluggable storage adapter, distributed multi-node worker clustering, Single Sign-On (SSO / OIDC), and Role-Based Access Control (RBAC).

---

## 3. Detailed Roadmap & Execution Checklist

### Phase 1 — Outbound Relay & Core Storage Engine

#### Milestone 1.1: Storage Engine & Route Foundation
- [x] **Task 1.1.1: Project Skeleton & Build Toolchain**
  - Standard Go layout (`cmd/relay`, `internal/...`).
  - Strict zero-CGO static compilation (`CGO_ENABLED=0 go build`).
  - Standard library configuration loader (`internal/config`) with 12-factor env vars.
  - Signal-aware graceful shutdown (`SIGINT`, `SIGTERM`) with `net/http` server.
  - Base `GET /healthz` endpoint with Go 1.22+ ServeMux routing.
- [x] **Task 1.1.2: SQLite Connection Pool Isolation**
  - Initialize pure-Go driver (`modernc.org/sqlite`).
  - Writer pool isolation: strictly `SetMaxOpenConns(1)` with `PRAGMA busy_timeout = 5000`.
  - Reader pool: pooled `SetMaxOpenConns(NumCPU * 2)`.
  - WAL mode enablement and performance pragmas (`synchronous=NORMAL`, `foreign_keys=ON`, `auto_vacuum=INCREMENTAL`).
- [x] **Task 1.1.3: SQLite DDL Migrations**
  - Create table `routes` with unique constraints on `name` and `path`.
  - Create table `requests` with dispatcher index `(status, next_attempt_at) WHERE status IN ('pending', 'failed')`.
  - Create table `attempts` with foreign key cascade to `requests(id)`.
- [x] **Task 1.1.4: Domain Models & Bloat Protection Helper**
  - Entity `Route` with validation rules.
  - Entity `Request` with status lifecycle states.
  - Entity `Attempt` with `TruncateResponseBody` (enforcing 32 KB max response storage).
  - Monotonic lowercase ULID generator (`internal/model/ulid.go`) for chronological primary keys.
- [x] **Task 1.1.5: Route SQLite Repository**
  - Implemented `storage.RouteRepository` in `internal/storage/sqlite/route_repo.go`.
  - CRUD operations: `CreateRoute`, `GetRouteByID`, `GetRouteByName`, `GetRouteByPath`, `ListRoutes`, `UpdateRoute`, `DeleteRoute`.
  - Constraint conflict translation to `storage.ErrConflict` and missing records to `storage.ErrNotFound`.
- [x] **Task 1.1.6: In-Memory Route Registry & Hot-Reloading**
  - Implemented `RouteRegistry` in `internal/engine/route_registry.go`.
  - Sub-microsecond $O(1)$ read lookups using `sync.RWMutex` (`byName`, `byPath`, `byID`).
  - Immediate cache invalidation and atomic hot-reload on route mutations without service restart.

#### Milestone 1.2: Outbound Retry Logic & Synchronous HTTP Proxy
- [x] **Task 1.2.1: Retry Engine (`internal/engine/retry.go`)**
  - Calculate exponential backoff: $T_{\text{wait}} = \text{base} \times 2^{\text{attempt}}$.
  - Implement Full Jitter algorithm: sleep duration uniformly distributed in $[0, T_{\text{wait}}]$ to prevent thundering herds.
  - Parse `Retry-After` header (seconds and HTTP-date) on HTTP 429 responses.
  - Support retryable condition checks (status 429, 500, 502, 503, 504, and network/connection errors).
- [x] **Task 1.2.2: Attempts Audit Storage (`internal/storage/sqlite/attempt_repo.go`)**
  - Implement `storage.AttemptRepository` for recording delivery attempts.
  - Store execution duration in milliseconds, status code, error message, and truncated response body (32 KB limit).
  - Query attempt logs by `request_id`.
- [x] **Task 1.2.3: Synchronous Relay Handler (`internal/api/handler_relay.go`)**
  - Mount endpoint `POST /api/v1/relay/{route_name}`.
  - Resolve route from `RouteRegistry` in memory ($O(1)$).
  - Execute HTTP forwarding to route `destination_url` with synchronous retry loop.
  - Return final upstream response body, status code, and response headers to the client.
- [x] **Task 1.2.4: Client-Disconnect Context Cancellation**
  - Bind upstream `http.Request` context directly to incoming client request context (`r.Context()`).
  - Immediately abort upstream flight and terminate retry loop if the caller disconnects or aborts.
- [x] **Task 1.2.5: Header Propagation & Idempotency**
  - Generate and inject unique `X-Relay-Request-ID` (lowercase ULID).
  - Propagate downstream `Idempotency-Key` or `X-Relay-Idempotency-Key`.
  - Support `X-Relay-Timeout` header to override default route timeout for the request.

#### Milestone 1.3: Asynchronous Outbound Durable Jobs & Background Delivery
- [x] **Task 1.3.1: Requests Storage Repository (`internal/storage/sqlite/request_repo.go`)**
  - Implement `storage.RequestRepository` for durable request storage.
  - Fetch pending jobs (`status IN ('pending', 'failed') AND next_attempt_at <= ?`).
  - Atomic status transitions (`pending` $\rightarrow$ `processing` $\rightarrow$ `success` / `failed` / `dlq`).
- [x] **Task 1.3.2: Single-Writer Ingestion Batch Worker (`internal/engine/batch_writer.go`)**
  - In-memory buffered ingestion channel (`chan ingestionItem`).
  - Single-writer goroutine micro-batching writes (up to 50 items or 10ms timeout).
  - Durability guarantee: caller goroutine blocks until batch transaction is committed to SQLite WAL.
- [x] **Task 1.3.3: Asynchronous Job Ingestion API (`POST /api/v1/jobs/{route_name}`)**
  - Accept request body and headers.
  - Push through batch writer to durably persist in SQLite.
  - Return `202 Accepted` with `{"job_id": "<ulid>", "status": "queued"}`.
- [x] **Task 1.3.4: Job Inspection API (`GET /api/v1/jobs/{job_id}`)**
  - Return job status, attempt count, last error, timestamps, and full attempt history.
- [x] **Task 1.3.5: Delivery Dispatcher (`internal/engine/dispatcher.go`)**
  - Polling loop + instant channel wakeup for pending jobs due for execution.
  - Claim due records and dispatch to Worker Pool.
- [x] **Task 1.3.6: Worker Pool (`internal/engine/worker_pool.go`)**
  - Fixed pool of worker goroutines (`RELAY_WORKER_CONCURRENCY`, default 20).
  - Forward request to `destination_url` with per-attempt timeout.
  - Transition status to `success` on 2xx/acceptable code, or schedule next retry timestamp via backoff + jitter.
  - Transition to `dlq` when `attempts_count >= max_attempts`.

---

### Phase 2 — Inbound Webhook & Security Pipeline

#### Milestone 2.1: Cryptographic Ingestion & Signature Adapters
- [x] **Task 2.1.1: Raw Byte Body Preserving Receiver**
  - Ensure zero payload mutation: capture exact bytes into memory/disk.
  - Enforce maximum body size (`RELAY_MAX_BODY_SIZE_MB`, default 10MB) with `http.MaxBytesReader`.
- [ ] **Task 2.1.2: Signature Verification Adapters (`internal/signature`)** *(Deferred to Security Milestone)*
  - Define `Verifier` interface: `Verify(header string, secret string, rawBody []byte) error`.
  - Stripe HMAC adapter: parse `t=timestamp,v1=signature`, verify HMAC-SHA256 with timestamp tolerance.
  - GitHub HMAC adapter: verify `sha256=<hex>` against `X-Hub-Signature-256`.
  - Generic HMAC-SHA256 adapter: verify standard hex/base64 signatures.
- [x] **Task 2.1.3: Inbound Webhook Endpoint (`POST /api/v1/webhook/{route_name}`)**
  - Match route from `RouteRegistry` (`route.Type == "inbound"`).
  - Support `POST`, `PUT`, `PATCH` methods (returns 405 for other methods).
  - Preserve provider signature headers intact for destination verification.
  - Durability Golden Rule: write to SQLite WAL via Batch Worker before returning `200 OK`.
  - Return `200 OK` with `{"relay_id": "<ulid>", "status": "persisted"}`.

#### Milestone 2.2: Inbound Async Delivery & DLQ Replay
- [x] **Task 2.2.1: Internal Webhook Forwarding Dispatch**
  - Dispatch persisted inbound webhooks to internal destination URL via Worker Pool.
  - Exponential backoff with jitter on internal destination failures.
- [x] **Task 2.2.2: Dead Letter Queue (DLQ) State Machine**
  - Mark requests as `dlq` upon reaching `max_attempts`.
  - Record complete diagnostic history in `attempts`.
- [x] **Task 2.2.3: DLQ Replay Management API**
  - Endpoint `POST /api/v1/dlq/{request_id}/replay`: Reset attempts count, set status to `pending`, set `next_attempt_at = NOW()`, trigger dispatcher wakeup.
  - Endpoint `POST /api/v1/dlq/replay-all`: Batch replay all filtered DLQ records.
  - Endpoint `GET /api/v1/dlq`: Filterable and paginated DLQ inspection API.

---

### Phase 3 — Embedded UI & System Lifecycle

#### Milestone 3.1: Embedded Dashboard Web UI
- [ ] **Task 3.1.1: Embedded Assets & Base Layout (`ui/`)**
  - Embed HTML templates and static assets using `go:embed`.
  - Zero Node.js build pipeline: Alpine.js + Tailwind CSS.
- [ ] **Task 3.1.2: Routes Management UI**
  - View all active routes (inbound & outbound).
  - Create, edit, and delete routes via UI modal.
  - Instant hot-reloading verification in UI.
- [ ] **Task 3.1.3: Requests & Attempts Explorer**
  - Filterable requests table: search by ID, status (`pending`, `success`, `dlq`), route name, date range.
  - Slide-over detail drawer: inspect request headers, raw body, attempt status codes, duration, and error logs.
- [ ] **Task 3.1.4: DLQ Dashboard & Replay Action**
  - Dedicated view for failed/DLQ events.
  - One-click individual replay and bulk replay buttons.

#### Milestone 3.2: Security & Storage Maintenance
- [ ] **Task 3.2.1: Admin Password Authentication & CSRF Protection**
  - Protect UI routes with session cookie authenticated against `RELAY_ADMIN_PASSWORD`.
  - CSRF token validation on all state-mutating requests (Route CRUD, Replay, Purge).
  - Login rate limiter to prevent brute-force attacks.
- [ ] **Task 3.2.2: Storage Retention Cleaner (`internal/engine/cleaner.go`)**
  - Periodic background worker (hourly/daily).
  - Purge successful requests older than `RELAY_RETENTION_SUCCESS_DAYS` (default 7 days).
  - Purge DLQ requests older than `RELAY_RETENTION_FAILED_DAYS` (default 30 days).
  - Execute `PRAGMA incremental_vacuum(1000)` to reclaim OS disk pages without exclusive table locks.
- [ ] **Task 3.2.3: Litestream Disaster Recovery Integration**
  - Provide production configuration guide and Docker compose setup for Litestream S3 streaming replication.
  - Zero-SPOF SQLite durability verification.

#### Milestone 3.3: Automated Multi-Platform Release Pipeline
- [ ] **Task 3.3.1: Multi-Platform Build Configuration (`.goreleaser.yaml`)**
  - Zero-CGO static compilation (`CGO_ENABLED=0`) across Linux (`amd64`, `arm64`), macOS (`amd64`, `arm64`), and Windows (`amd64`).
  - Automated archive packaging (`.tar.gz`, `.zip`) with SHA-256 checksum generation (`checksums.txt`).
  - Multi-architecture Docker image build and push configuration.
- [ ] **Task 3.3.2: Automated Versioning & Release Workflow (`.github/workflows/release.yml`)**
  - Integrate `release-please` to parse Conventional Commits and maintain `CHANGELOG.md`.
  - Automatically manage SemVer release pull requests on `main`.
  - Trigger GoReleaser upon Release PR merge to attach cross-platform binaries to GitHub Releases.
- [ ] **Task 3.3.3: In-App Version Update Checker (`internal/version`)**
  - Background non-blocking version checker querying upstream GitHub Releases (`simple-relay/simple-relay`) created by Release Please.
  - Dual discovery strategy: GitHub REST API with seamless fallback to rate-limit-free HTTP HEAD redirect trick.
  - Robust SemVer comparison using standard `golang.org/x/mod/semver`.
  - In-memory cache and configurable opt-out (`RELAY_CHECK_UPDATES=false`) for air-gapped deployments.
  - Expose version metadata via `GET /api/v1/system/version` and Web UI notification badge.
  - See design specification: [2026-10-09-version-check-design.md](specs/2026-10-09-version-check-design.md).

---

### v0.3 — Utility & Observability Extensions
- [ ] **Task 0.3.1:** CLI tool (`cmd/relay-cli`) for inspecting relay health, routes, and queue depth.
- [ ] **Task 0.3.2:** Webhook alert notifications (Slack / Discord / Email) when DLQ count exceeds configurable threshold.
- [ ] **Task 0.3.3:** API Key authentication for programmatic API management.
- [ ] **Task 0.3.4:** Prometheus metrics exporter endpoint (`GET /metrics`) tracking ingestion rate, retry counts, queue depth, and latency histograms.

---

### v1.0 — Enterprise Edition
- [ ] **Task 1.0.1:** Pluggable PostgreSQL storage adapter (`internal/storage/postgres`) implementing `storage.Storage`.
- [ ] **Task 1.0.2:** Multi-node worker clustering with distributed leader election / advisory locks.
- [ ] **Task 1.0.3:** Enterprise SSO integration (OIDC, OAuth2, SAML).
- [ ] **Task 1.0.4:** Role-Based Access Control (RBAC) and security audit logs.