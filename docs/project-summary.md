# Simple Relay — Project Summary

> **One binary. No infrastructure. Reliable HTTP delivery.**

---

## 1. Project Overview

**Simple Relay** is a small, self-hosted HTTP reliability layer.

Its core job is simple:

> **Receive HTTP → persist/forward → retry when necessary → deliver reliably.**

The project is intentionally narrower than an API Gateway, webhook platform, workflow engine, or event bus.

### Core principles

- Open source
- Self-hosted
- Single binary
- Embedded SQLite by default (WAL mode + single-writer batching)
- Docker or standalone binary
- Zero external infrastructure required (no Redis, Kafka, Postgres, RabbitMQ)
- Embedded web UI (Alpine.js + TailwindCSS)
- Production durability (Litestream streaming replication compatible)
- Low operational complexity

---

## 2. Two Main Use Cases

### A. Inbound HTTP / Webhook (Async Delivery)

Third-party services such as Stripe, GitHub, Shopify, or Facebook send HTTP webhooks to the relay.

```text
Third-party (e.g. Stripe, GitHub)
    │
    │ HTTP POST (with HMAC signature)
    ▼
Simple Relay
    │
    ├── Verify raw HMAC signature (Stripe, GitHub, Generic)
    ├── Buffer to Single-Writer Batch Queue
    ├── Persist raw byte body to SQLite (WAL)
    ├── Return 200 OK quickly
    │
    ▼
Delivery Worker Pool
    │
    ├── Forward to internal app
    ├── Exponential backoff + Jitter on failure
    └── Transition to DLQ / Manual & Auto Replay
    │
    ▼
Internal Application
```

Flow:

1. Receive request.
2. Validate cryptographic signature against raw payload.
3. Persist unmodified raw body via single-writer batch worker.
4. Return fast success response (ACK 200).
5. Deliver asynchronously via Worker Pool.
6. Retry failed deliveries with backoff and jitter.
7. Record attempt history (with response truncation).
8. Move to DLQ upon retry exhaustion; support replay.

Guarantee: **at-least-once delivery**, not exactly-once execution.

---

### B. Outbound HTTP / API Relay (Sync & Async Modes)

An internal application calls external HTTP APIs (e.g., Stripe, LINE, CRM, internal microservices) through the relay.

#### 1. Sync Proxy Mode (with Client Abort Handling)

```text
My Application ──────► Simple Relay ──────► External API
  (HTTP open)            (Sync retry loop)       (429 / 5xx / Timeout)
```

- Kept open while relay communicates with upstream.
- Handles transient network blips, 429s, and `Retry-After` headers.
- **Client Cancellation Safety:** If the client disconnects or times out, the relay **immediately cancels** in-flight upstream requests and stops retrying, avoiding duplicate transactions or overbilling.

#### 2. Async Outbound Mode (Durable Job / Fire-and-Forget)

*Solves the fundamental flaw of in-process SDK retries: if the calling app restarts or crashes, ephemeral retries are lost.*

```text
My Application
    │
    │ POST /api/v1/jobs/{route}
    ▼
Simple Relay
    │
    ├── Persist job to SQLite
    └── Return 202 Accepted + job_id immediately
    │
    ▼
Delivery Worker Pool
    │
    ├── Reliable background delivery (even if calling app restarts)
    └── Optional webhook callback to notify caller upon completion
    │
    ▼
External Destination API
```

---

## 3. The Core Abstraction: Delivery Engine

Both inbound and outbound modes share the same reliability primitives:

```text
                    SIMPLE RELAY
                            │
                 ┌──────────┴──────────┐
                 │                     │
              INBOUND              OUTBOUND
                 │                     │
           Async delivery        Sync / Async Job
                 │                     │
                 └──────────┬──────────┘
                            │
                     Delivery Engine
                            │
               ┌────────────┴────────────┐
               │                         │
            Retry                     Timeout
            Backoff                   Jitter
            History & DLQ             Auth & HMAC
               │                         │
               └────────────┬────────────┘
                            │
                        HTTP → HTTP
```

Key abstraction:

> **An HTTP request enters the relay, and the relay reliably delivers it to another HTTP endpoint.**

---

## 4. MVP Direction

### Phase 1 — Outbound Relay & Core Storage Engine

- Single-writer SQLite ingestion engine with WAL mode and connection pooling isolation (`SetMaxOpenConns(1)` for writer).
- Route configuration (CRUD via SQLite).
- Synchronous HTTP proxy with context cancellation on client disconnect.
- Asynchronous Outbound job ingestion (`202 Accepted` + `job_id`).
- Delivery Engine: Timeout, exponential backoff, jitter, `Retry-After`, 429 & 5xx handling.
- Attempt history logging with response body truncation (capped at 32KB).

### Phase 2 — Inbound Webhook & Security

- Webhook ingestion endpoint (`POST /api/v1/webhook/{route}`).
- Raw body cryptographic preservation.
- Webhook signature verification adapters (Stripe HMAC, GitHub HMAC, Generic SHA256).
- Batch ingestion worker for burst protection.
- Immediate ACK 200 only after disk sync.
- Background Dispatcher + Worker Pool.
- DLQ state machine and Replay API.

### Phase 3 — Simple UI & Lifecycle Maintenance

- Embedded Web UI via `go:embed` (Alpine.js + TailwindCSS).
- Dashboard: Routes, Requests, Attempts log, DLQ inspect & Replay.
- Admin password authentication + CSRF protection + Login rate limiting.
- Storage Retention Cleaner: Scheduled purge of successful logs after $N$ days and `PRAGMA incremental_vacuum`.
- Litestream documentation and backup integration.

---

## 5. Reliability Rules

### 1. Never retry forever

Retry must be bounded by both:
- `max_attempts`
- `timeout_ms` / `total_timeout_ms`

Jitter is required on all backoffs to avoid Thundering Herd retry storms.

### 2. Context propagation & Client cancellation

For synchronous outbound proxy calls, if the caller closes the TCP connection, Relay terminates the upstream context immediately to prevent phantom duplicate operations.

### 3. Method-aware retry & Idempotency

Idempotency headers (`X-Relay-Request-ID`, `Idempotency-Key`) are propagated downstream. Non-idempotent synchronous operations (`POST`, `PATCH`) only retry when an idempotency key is provided or `retry_non_idempotent` is explicitly enabled on the route.

### 4. Storage bloat control

- Full raw bodies are stored for incoming requests.
- Response bodies in attempt logs are truncated to a safe threshold (e.g. 32KB) to prevent database exhaustion.
- Incremental vacuuming and scheduled TTL purges maintain healthy disk usage.

### 5. Guarantee

> **At-least-once delivery, not exactly-once delivery.**

---

## 6. Durability Model

Inbound webhook processing follows:

$$\text{Receive} \longrightarrow \text{Validate Signature} \longrightarrow \text{Single-Writer Buffer} \longrightarrow \text{Commit SQLite WAL} \longrightarrow \text{ACK 200} \longrightarrow \text{Deliver}$$

- Never acknowledge before durable persistence.
- Single-writer micro-batching avoids SQLite lock contention under high-volume webhook bursts.
- Litestream enables continuous WAL replication to cloud object storage (S3/R2) for disaster recovery.

---

## 7. Architecture

```text
                    HTTP RELAY
                         │
                ┌────────▼────────┐
                │     Router      │
                └────────┬────────┘
                         │
              ┌──────────┴──────────┐
              │                     │
       SYNC OUTBOUND         ASYNC INBOUND & JOBS
              │                     │
      Client cancel ctx     Single-Writer Buffer
              │                     │
              │             SQLite WAL Storage
              │                     │
              │             Dispatcher & Worker Pool
              │                     │
              └──────────┬──────────┘
                         │
                  Delivery Engine
               (Backoff, Jitter, DLQ)
                         │
                  HTTP Transport
                         │
                    Destination
```

---

## 8. Technology Direction

Preferred stack:

- **Go (Golang)**: Fast, single static binary, low memory footprint, excellent concurrency.
- **SQLite (Embedded)**: Zero external dependency, WAL mode, single-writer pattern, incremental vacuuming.
- **Litestream**: Real-time replication companion for high availability without operational complexity.
- **Embedded Web UI**: Go `html/template`, Alpine.js, TailwindCSS (zero Node.js build overhead).
- **Docker**: Scratch/Alpine minimal container image.

---

## 9. What the Project Is NOT

To keep the product focused, do **not** turn it into:

- Full-fledged API Gateway
- API Management platform
- OAuth / IAM platform
- Developer portal
- GraphQL gateway
- Service mesh
- Workflow engine (Temporal replacement)
- Event bus (Kafka replacement)
- Complex payload transformation platform
- Failover / dynamic routing fallback platform

Focus strictly on:

> **Reliable HTTP relay.**

---

## 10. Competitive Landscape

| Solution | Strength | Main limitation / Difference vs Simple Relay |
|---|---|---|
| **In-process SDK retry** (e.g., Axios retry, Tenacity) | Simple, in-memory | Retry state is lost if application restarts or crashes; doesn't help inbound webhooks |
| **Celery / BullMQ** | Durable async retry | Requires Redis/broker; language-locked; changes semantics to job workers |
| **SQS / EventBridge** | Managed & durable | Cloud vendor lock-in; requires substantial cloud infrastructure setup |
| **Temporal / Cadence** | Extremely powerful workflows | Massive operational complexity; massive overkill for simple HTTP delivery |
| **Envoy / NGINX** | High-performance proxying | Network-level proxying; lacks durable webhook buffering and UI replay |
| **Svix / Convoy / Hook0** | Specialized webhook gateways | Tailored primarily for SaaS webhook providers rather than general lightweight HTTP relay |

The unique opportunity:

> **A self-hosted, single-binary HTTP reliability layer that provides durable retries and webhook protection without requiring external brokers or databases.**

---

## 11. Product Positioning

Tagline:

> **One binary. No infrastructure. Reliable HTTP delivery.**

Elevator pitch:

> **A tiny self-hosted HTTP relay for reliable HTTP delivery with built-in retries, webhook persistence, and replay.**

---

## 12. Open Source & Monetization Strategy

- **Open Source First:** Full core capabilities (engine, SQLite storage, retry, DLQ, Web UI, Docker image) are 100% open source and free forever.
- **No Artificial Feature Gating:** Core reliability features are never locked behind paywalls.
- **Initial Revenue:** GitHub Sponsors, Open Collective, Ko-fi donations.
- **Future Growth (Post-Traction):** Optional Managed Cloud (hosted version) or community sponsorships.

---

## 13. Current Decisions & Refinements

### Keep & Refined
- Open source, Go, SQLite (WAL mode, single-writer connection isolation).
- Single binary + Docker distribution.
- Embedded UI (Alpine.js + Tailwind).
- Inbound Webhook (with HMAC raw-body signature verification).
- Outbound Relay with **both** Sync Proxy (with client cancellation) and Async Durable Job mode.
- Retry, Timeout, Exponential Backoff, Full Jitter, DLQ & Replay.
- Storage lifecycle: Response body truncation (32KB), retention worker, `incremental_vacuum`.
- Litestream readiness for continuous streaming replication.

### Drop
- Failover / dynamic alternate routing.
- Heavy API gateway / OAuth features.
- Workflow DAG engines.
- Complex payload transformation.
- Unbounded retries and full response body storage without TTL.
