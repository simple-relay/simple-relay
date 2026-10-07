# Simple HTTP Relay — Architecture Document

*Tagline: "One binary. No infrastructure. Reliable HTTP delivery."*

---

## 1. Technical Stack

The system is designed with extreme minimalism to optimize operational costs and deployment ease:

* **Core Language:** Go (Golang) — provides high performance, low memory footprint, and static binary compilation without external runtime dependencies.
* **Database & Storage Adapter:** Embedded **SQLite** is the default database to maintain the "zero-config, single binary" philosophy.
  * **Concurrency Configuration:** Configured in **WAL (Write-Ahead Logging)** mode with dedicated connection separation: **1 dedicated Writer connection (`SetMaxOpenConns(1)`)** and a pooled set of Reader connections with `busy_timeout = 5000ms`.
  * **Storage Architecture:** Built using **Storage Interfaces (Adapter / Repository Pattern)**, completely decoupling business logic from SQLite. This enables future plug-ins for PostgreSQL or MySQL when horizontal scale is required.
  * **Disaster Recovery / Replication:** Supports native integration with **Litestream** for zero-downtime, continuous streaming replication to S3/Cloud Storage, eliminating the single point of failure (SPOF) of single-node SQLite.
* **Packaging & Deployment:**
  * Distributed primarily as a **Single binary** containing the backend engine and embedded frontend UI.
  * Minimal **Docker image** (Scratch/Alpine based) for containerized environments.
* **Embedded Web UI:** Integrates an HTTP Server and frontend directly into the binary via `go:embed`.
  * Built with Go HTML Templates + **Alpine.js** + **TailwindCSS (via CDN / embedded static assets)**.
  * Zero Node.js build pipeline required; binary size remains minimal (~15–25MB).
  * Configurations updated via Web UI are committed to SQLite and hot-reloaded in Go without restarting the service.

---

## 2. Core Characteristics & Product Positioning

### Product Positioning

**Simple HTTP Relay** is an open-source, self-hosted **HTTP Reliability Layer**. It sits between your applications and external HTTP endpoints to guarantee reliable delivery, automatic retries, and failure visibility without requiring heavy message queues (Kafka, RabbitMQ) or distributed databases.

### Two Core Use Cases

#### 1. Inbound Webhook (Async Delivery)
* Receives incoming webhooks from external providers (e.g., Stripe, GitHub, Shopify, Facebook).
* **Cryptographic Verification:** Validates webhook authenticity using HMAC signatures (e.g., `Stripe-Signature`, `X-Hub-Signature-256`) against unmodified raw payloads.
* **Durable Persistence:** Writes the unmodified raw request payload to disk (SQLite) before acknowledging the sender.
* **Asynchronous Forwarding:** Worker Pool dispatches the payload to the internal destination service with exponential backoff, jitter, and DLQ tracking.

#### 2. Outbound HTTP Relay (Sync & Async Modes)
Relays HTTP calls originating from internal systems to external services (e.g., Payment gateways, CRM, Messaging APIs). Supports two operational modes:

* **Sync Proxy Mode (with Client Abort Propagation):**
  * Client maintains an open HTTP connection while Relay forwards the request.
  * In-memory retry loop handles transient errors (429, 5xx, timeouts, `Retry-After`).
  * **Context Cancellation:** If the client disconnects or aborts before completion, Relay **immediately cancels** in-flight upstream requests and stops retrying, preventing duplicate transactions and unexpected billing.
* **Async Outbound / Durable Job Mode (Fire-and-Forget):**
  * Solves the inherent limitation of in-process SDK retries when applications restart.
  * Internal application submits request $\rightarrow$ Relay persists it and immediately returns `202 Accepted` with a `job_id`.
  * Relay guarantees background delivery, with optional webhook callback or polling endpoint to notify the application of final execution results.

### Delivery Guarantee

* **At-least-once delivery:** The system guarantees that every accepted request will be delivered at least once.
* **Exactly-once execution is NOT guaranteed:** Destination endpoints must implement idempotency handling. Relay forwards and generates standard idempotency headers (`X-Relay-Request-ID`, `Idempotency-Key`).

### Durability Pipeline (The Golden Rule)

For Inbound webhooks, the pipeline strictly enforces:

$$\text{Receive} \longrightarrow \text{Validate Signature} \longrightarrow \text{Persist to SQLite} \longrightarrow \text{ACK 200} \longrightarrow \text{Deliver Asynchronously}$$

> **Rule:** Absolutely **never return an HTTP 200/202** to the caller until the raw payload has been confirmed flushed to durable storage (SQLite).

### Reliability Mechanisms

* **Bounded Retries:** Strict limits on attempts (`max_attempts`) and timeouts (`timeout_ms`, `total_timeout_ms`). Never retry forever.
* **Exponential Backoff & Full Jitter:** Prevents retry storms (Thundering Herd) against struggling upstream servers.
* **Dead Letter Queue (DLQ):** Requests exceeding retry limits are transitioned to `dlq` with failure diagnostics.
* **Replay Support:** One-click replay of failed/DLQ requests via Web UI or management API.

---

## 3. Scope Boundaries (What the Project Is NOT)

To maintain extreme focus and operational simplicity:

* **NO infrastructure dependencies:** No Redis, Kafka, RabbitMQ, PostgreSQL, or Kubernetes required for the default deployment.
* **NOT an API Gateway:** No complex rate-limiting rules, OAuth2 authorization server, GraphQL aggregation, or developer portal.
* **NO Failover Routing:** Fallback/alternative routing is out of scope. Failures are handled strictly via Time/Retries + DLQ.
* **NO Data Mutation:** Complex payload transformations or schema mapping are not supported. Requests are delivered as-is.

---

## 4. Concurrency & Execution Model

```text
                           INBOUND HTTP
                                │
                        ┌───────▼────────┐
                        │ Receiver Pool  │
                        └───────┬────────┘
                                │
               Push to Ingestion Channel (Non-blocking buffer)
                                │
                        ┌───────▼────────┐
                        │ Single-Writer  │ ──► SQLite (WAL Mode, Batch Sync)
                        │ Batch Worker   │
                        └───────┬────────┘
                                │
                       Wakeup Signal / Scan
                                │
                        ┌───────▼────────┐
                        │   Dispatcher   │
                        └───────┬────────┘
                                │ Job Channel
                        ┌───────▼────────┐
                        │  Worker Pool   │ ──► HTTP Forwarding (Backoff & Jitter)
                        └────────────────┘
```

### 1. Inbound Webhook: Single-Writer Ingestion Pattern

* **The SQLite Concurrency Problem:** In SQLite WAL mode, multiple readers can execute concurrently, but there can only ever be **one writer** at a time. High-burst webhook traffic (e.g., 200 req/sec) competing for write locks will cause lock contention, latency spikes, and 3rd-party sender timeouts.
* **Solution Architecture:**
  1. **Receiver Goroutines:** Receive HTTP request, capture the exact raw byte body, verify HMAC signature, and push an entry to an in-memory `Ingestion Channel` with a completion channel.
  2. **Single-Writer Batch Worker:** A dedicated background goroutine consumes the channel, groups pending writes into micro-batches (e.g., up to 50 items or 10ms timeout), and executes a single atomic SQLite transaction.
  3. **Acknowledgement:** Upon transaction commit, the worker signals the receivers, and the receivers return `200 OK` to the senders. This guarantees durability while maximizing SQLite write throughput (thousands of writes/sec).
  4. **Dispatcher:** Periodically (or upon channel wakeup signal) queries for requests in `pending` status or where `next_attempt_at <= NOW()`. It updates status to `processing` and dispatches to the Worker Pool.
  5. **Worker Pool:** A fixed pool of workers (`default: 20 goroutines`) executes HTTP requests to internal destinations. Upon failure, computes next retry timestamp using exponential backoff with jitter; upon exhausting retries, moves record to `dlq`.

### 2. Outbound Relay Concurrency

* **Sync Proxy Mode:**
  * **1 Request = 1 Goroutine:** Client connection remains open.
  * **Context & Cancellation Binding:** The upstream HTTP request context is bound to `r.Context()`. If the client closes the connection, the context is cancelled immediately, aborting in-flight HTTP calls and terminating the retry loop.
  * **In-memory Retry Loop:** Uses standard `for` loop with timer backoff.
* **Async Outbound Mode:**
  * Follows the same durable ingestion pipeline as Inbound Webhooks: persists to SQLite $\rightarrow$ returns `202 Accepted` + `job_id` $\rightarrow$ background Worker Pool delivers to external destination.

### 3. Database Connection Pool Isolation

* **Writer Pool:** `dbWriter.SetMaxOpenConns(1)` with `PRAGMA busy_timeout = 5000`. Ensures zero SQLITE_BUSY race conditions.
* **Reader Pool:** `dbReader.SetMaxOpenConns(runtime.NumCPU() * 2)` for concurrent dispatcher reads, UI dashboards, and status queries.

---

## 5. Storage Lifecycle & Data Retention (Preventing Disk Bloat)

Storing full request and response payloads in SQLite without a lifecycle strategy will lead to rapid disk exhaustion and degraded SQLite performance:

### 1. Payload Management Rules
* **Request Raw Body:** Stored in full (`BLOB`) in `requests.body` to preserve cryptographic integrity for delivery and replay.
* **Response Body Truncation:** Responses in `attempts.response_body` are capped at a configurable limit (default: **32 KB**). Full response bodies on repeated failures waste storage; diagnostic errors and status codes are prioritized.

### 2. Retention & Purge Worker
* A background cron worker runs periodically (e.g., hourly) to prune historical records based on retention settings:
  * Successful requests (`status = 'success'`): Pruned after `RELAY_RETENTION_SUCCESS_DAYS` (default: 7 days).
  * DLQ / Failed requests: Retained longer for auditing (default: 30 days) before archival or deletion.
  * Cascade deletion cleans up associated rows in `attempts`.

### 3. Incremental Vacuuming
* SQLite is configured at creation with:
  ```sql
  PRAGMA auto_vacuum = INCREMENTAL;
  ```
* After daily prune cycles, the cleaner executes `PRAGMA incremental_vacuum(1000);` to return freed pages to the operating system without triggering an exclusive table lock that would stall traffic.

---

## 6. Database Schema (SQLite Default)

The core Delivery Engine relies on 3 optimized tables:

### 1. `routes` Table (Routing & Verification Configuration)
```sql
CREATE TABLE routes (
    id TEXT PRIMARY KEY,                       -- UUID
    name TEXT NOT NULL UNIQUE,                 -- e.g., "stripe-payments"
    type TEXT NOT NULL,                        -- 'inbound' | 'outbound'
    mode TEXT NOT NULL DEFAULT 'async',        -- 'sync' | 'async'
    path TEXT NOT NULL UNIQUE,                 -- e.g., "/api/v1/webhook/stripe"
    destination_url TEXT NOT NULL,             -- Forward target URL
    signature_provider TEXT DEFAULT 'none',    -- 'none' | 'stripe' | 'github' | 'hmac_sha256'
    signature_secret TEXT,                     -- Secret key for verification
    max_attempts INTEGER NOT NULL DEFAULT 5,   -- Bounded retry limit
    timeout_ms INTEGER NOT NULL DEFAULT 10000, -- Timeout per attempt
    retry_backoff_strategy TEXT DEFAULT 'exp', -- 'exp' | 'linear'
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

### 2. `requests` Table (Durable Payload & Job State)
```sql
CREATE TABLE requests (
    id TEXT PRIMARY KEY,                       -- UUID / Request ID
    route_id TEXT NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
    method TEXT NOT NULL,                      -- HTTP Method (POST, GET, etc.)
    url TEXT NOT NULL,                         -- Incoming Request URL
    headers TEXT NOT NULL,                     -- JSON-encoded HTTP Headers
    body BLOB,                                 -- Exact unmodified raw payload
    status TEXT NOT NULL,                      -- 'pending' | 'processing' | 'success' | 'failed' | 'dlq'
    attempts_count INTEGER NOT NULL DEFAULT 0,
    next_attempt_at DATETIME,                  -- Indexed for Dispatcher pickup
    last_error TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_requests_dispatcher ON requests (status, next_attempt_at) 
WHERE status IN ('pending', 'failed');
CREATE INDEX idx_requests_created_at ON requests (created_at);
```

### 3. `attempts` Table (Delivery Audit Log)
```sql
CREATE TABLE attempts (
    id TEXT PRIMARY KEY,                       -- UUID
    request_id TEXT NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL,
    response_status_code INTEGER,              -- Destination HTTP status code
    response_body TEXT,                        -- Truncated response payload (max 32KB)
    error_message TEXT,                        -- Connection error, timeout, DNS failure
    executed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    duration_ms INTEGER NOT NULL
);

CREATE INDEX idx_attempts_request_id ON attempts (request_id);
```

---

## 7. API Contracts & Application Configuration

### API Contracts

* **Inbound Webhook Ingestion:**
  * `POST /api/v1/webhook/{route_name}`
  * Receives external webhook, verifies signature against `signature_secret`, writes to SQLite via batch worker, returns `200 OK` with `{"relay_id": "...", "status": "persisted"}`.
* **Outbound Relay (Sync Proxy):**
  * `POST /api/v1/relay/{route_name}`
  * Proxies request synchronously to `destination_url` with automatic retry and client-disconnect abort.
* **Outbound Relay (Async Job):**
  * `POST /api/v1/jobs/{route_name}`
  * Accepts request asynchronously, returns `202 Accepted` with `{"job_id": "...", "status": "queued"}`.
  * `GET /api/v1/jobs/{job_id}`: Query status, attempt count, and execution outcome.
* **Special Request Headers:**
  * `X-Relay-Timeout`: Overrides route default timeout for specific call.
  * `X-Relay-Idempotency-Key` / `Idempotency-Key`: Propagated downstream to ensure safe upstream execution.

### Application Startup Configuration

Configured via environment variables with clear naming:

| Variable | Default | Description |
| :--- | :--- | :--- |
| `RELAY_PORT` | `8080` | HTTP server listening port |
| `RELAY_DB_PATH` | `./data/relay.db` | File path for SQLite database |
| `RELAY_ADMIN_PASSWORD` | *(empty = disabled)* | Password to access Web UI and Admin APIs |
| `RELAY_MAX_BODY_SIZE_MB` | `10` | Maximum allowable HTTP request body (in MB) |
| `RELAY_RESPONSE_TRUNCATE_KB` | `32` | Max length of response body stored in `attempts` |
| `RELAY_RETENTION_SUCCESS_DAYS` | `7` | Retention period for successful requests |
| `RELAY_RETENTION_FAILED_DAYS` | `30` | Retention period for DLQ / failed requests |
| `RELAY_WORKER_CONCURRENCY` | `20` | Size of background worker pool |

---

## 8. Security Strategy

1. **Raw Body Cryptographic Verification:**
   * Inbound webhooks support standard HMAC validation algorithms (`stripe`, `github`, `generic_sha256`) before persistence. Invalid requests are rejected with `401 Unauthorized` before exhausting storage.
2. **Data Privacy & Local Storage:**
   * Payloads and auth tokens never leave the local machine/container.
3. **Web UI & Dashboard Protection:**
   * Session-based authentication with `RELAY_ADMIN_PASSWORD`.
   * CSRF protection on all UI state-mutating actions (Route update, Replay DLQ, Purge).
   * Rate limiting on login attempts to mitigate brute-force attacks.
4. **Header Sanitization:**
   * Sensitive headers (e.g., `Authorization`, `Cookie`) are optionally masked in attempt logs and UI dashboards.

---

## 9. Disaster Recovery & High Availability (Production Strategy)

While Simple HTTP Relay is designed as a standalone single-binary application, production deployments requiring zero data loss and fast recovery should pair it with **Litestream**:

```text
┌──────────────────────────┐
│    Simple HTTP Relay     │
│   (Go + Embedded SQLite) │
└────────────┬─────────────┘
             │ Writes WAL
             ▼
      [ relay.db-wal ]
             │
             │ Continuous Stream Replication
             ▼
     ┌───────────────┐
     │  Litestream   │ ──► AWS S3 / Cloudflare R2 / MinIO
     └───────────────┘     (Point-in-Time Recovery, RPO < 1s)
```

* **Zero Infrastructure Overhead:** Litestream runs alongside the binary or inside the same Docker container.
* **Point-in-Time Recovery:** In case of hardware failure, the database can be restored up to the exact second before failure.
* **Simplicity:** Avoids the operational tax of provisioning and managing clustered PostgreSQL/Kafka for small-to-medium workloads.

---

## 10. Project Directory Layout (Standard Go Layout)

```text
simple-relay/
├── cmd/
│   └── relay/
│       └── main.go                 # App entry point, flags, graceful shutdown
├── internal/
│   ├── api/                        # HTTP routers, middlewares, handlers
│   │   ├── handler_webhook.go      # Inbound webhook ingestion
│   │   ├── handler_relay.go        # Outbound sync relay
│   │   ├── handler_job.go          # Outbound async jobs
│   │   ├── handler_ui.go           # Web UI server
│   │   └── middleware/             # Auth, Logging, CSRF, Signature verification
│   ├── engine/                     # Core Delivery Engine
│   │   ├── batch_writer.go         # Single-writer SQLite ingestion worker
│   │   ├── dispatcher.go           # Polls/listens for due jobs
│   │   ├── worker_pool.go          # HTTP dispatching pool
│   │   ├── retry.go                # Backoff & Jitter calculations
│   │   └── cleaner.go              # Retention & Incremental vacuum worker
│   ├── model/                      # Domain entities (Route, Request, Attempt)
│   ├── signature/                  # Webhook HMAC verification adapters
│   └── storage/                    # Storage interface & SQLite adapter
│       ├── storage.go              # Core Repository interfaces
│       └── sqlite/                 # SQLite implementation (WAL, Migrations)
├── ui/                             # Embedded Frontend
│   ├── templates/                  # HTML templates (Alpine.js)
│   └── static/                     # Tailwind CSS / Icons
├── docs/                           # Project documentation
│   ├── architecture.md             # System architecture documentation
│   ├── project-summary.md          # Project summary and vision
│   └── roadmap.md                  # Development roadmap
├── Dockerfile                      # Multi-stage lightweight build
└── Makefile                        # Build, test, and run scripts
```
