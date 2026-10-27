---
title: "REST API Reference"
description: "Comprehensive REST API endpoint specifications for webhook ingestion, proxying, DLQ, authentication, and API key management."
section: "Reference"
order: 10
---

Simple Relay exposes a lightweight, high-performance REST API for ingesting webhooks, routing proxies, inspecting deliveries, replaying dead-lettered requests, and managing administrative credentials.

---

## 1. Authentication & Security Model

Simple Relay implements a dual authentication architecture supporting both programmatic service-to-service automation and interactive browser-based Web UI sessions.

### Operational Modes

- **Dev Mode (`RELAY_ADMIN_PASSWORD=""`):**
  When `RELAY_ADMIN_PASSWORD` is empty or unset (default), unauthenticated access to protected endpoints is strictly restricted to **loopback socket connections** (`127.0.0.1`, `::1`, `localhost`). Remote (non-loopback) callers must authenticate using a valid API key (`X-API-Key` or `Authorization: Bearer`). When binding to a non-loopback address (`0.0.0.0` or `::`) without an admin password, Simple Relay logs a startup warning and auto-seeds an initial API key on cold boot.
- **Protected Mode (`RELAY_ADMIN_PASSWORD="<secret>"`):**
  When `RELAY_ADMIN_PASSWORD` is set, all management, telemetry, inspection, and outbound delivery endpoints require authentication from all callers (including loopback). Only public webhook ingestion (`/api/v1/webhook/:route_name`), health probes (`/healthz`), and session auth endpoints remain unauthenticated.

### Dual Authentication Credentials

Authenticate to protected endpoints via either mechanism:

1. **API Key (Service-to-Service — works in both Dev Mode and Protected Mode):**
   Provide a valid API key in incoming HTTP headers:
   ```http
   X-API-Key: sr_live_a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90
   ```
   Or via standard Bearer authorization:
   ```http
   Authorization: Bearer sr_live_a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90
   ```
2. **Admin Session Cookie (Web UI — Protected Mode):**
   Obtained by authenticating via `POST /api/v1/auth/login`. Sets an HTTP-only `relay_session` cookie valid for 24 hours.

### Endpoint Access Matrix

| Method | Endpoint | Authentication | Purpose |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/v1/webhook/:route_name` | Public (Provider Signature) | Durable webhook ingestion into SQLite WAL |
| `POST` | `/api/v1/jobs/:route_name` | Protected | Asynchronous delivery job enqueue |
| `ANY` | `/api/v1/relay/:route_name` | Protected | Synchronous proxy with automatic retries |
| `GET` | `/healthz` | Public | System health check and liveness probe |
| `GET` | `/api/v1/auth/status` | Public | Check auth requirement and active session |
| `POST` | `/api/v1/auth/login` | Public (Rate-limited) | Authenticate admin password and obtain session |
| `POST` | `/api/v1/auth/logout` | Public | Invalidate admin session and clear cookie |
| `GET` | `/api/v1/routes` | Protected | List all configured routes (secrets masked) |
| `POST` | `/api/v1/routes` | Protected | Create and hot-reload a new route |
| `GET` | `/api/v1/routes/:id` | Protected | Retrieve route configuration by ID (secret masked) |
| `PUT` | `/api/v1/routes/:id` | Protected | Update route configuration by ID |
| `DELETE` | `/api/v1/routes/:id` | Protected | Delete route configuration by ID |
| `GET` | `/api/v1/requests` | Protected | List requests with pagination and filters |
| `GET` | `/api/v1/requests/:id` | Protected | Inspect delivery record, payload, and attempts |
| `POST` | `/api/v1/requests/:id/replay` | Protected | Requeue any historical request (success, failed, dlq) for redelivery |
| `GET` | `/api/v1/jobs/:job_id` | Protected | Inspect asynchronous job status and attempts |
| `GET` | `/api/v1/queue/stats` | Protected | Real-time queue metrics and backlog telemetry |
| `GET` | `/api/v1/dlq` | Protected | List dead-lettered requests |
| `POST` | `/api/v1/dlq/:request_id/replay` | Protected | Requeue an individual dead-lettered request |
| `POST` | `/api/v1/dlq/replay-all` | Protected | Bulk requeue dead-lettered requests |
| `POST` | `/api/v1/api-keys` | Protected | Generate a new API key |
| `GET` | `/api/v1/api-keys` | Protected | List existing API keys (masked) |
| `DELETE` | `/api/v1/api-keys/:id` | Protected | Revoke an API key |

---

## 2. Ingress & Outbound Delivery Endpoints

### `POST /api/v1/webhook/:route_name` *(Public — Provider Signature)*
Ingests an inbound webhook, verifies cryptographic signatures (if configured on the route), durably commits raw payload bytes to SQLite WAL storage, and enqueues background delivery. Also supports `PUT` and `PATCH`.

- **Headers:**
  - Provider signature headers (e.g. `Stripe-Signature`, `X-Hub-Signature-256`, etc.).
  - `X-Relay-Request-ID` *(optional)*: Custom tracking ULID/UUID. Simple Relay generates a monotonic ULID if omitted.
- **Status Codes:**
  - `200 OK`: Payload durably persisted in SQLite storage.
  - `400 Bad Request`: Route not configured for inbound mode or invalid JSON body.
  - `401 Unauthorized`: Provider signature verification or header rule failed.
  - `404 Not Found`: Route name not found.
  - `413 Payload Too Large`: Payload exceeds body limit (`RELAY_MAX_BODY_SIZE_MB`, default: `10` MB).
  - `500 Internal Server Error`: SQLite WAL write failure.
- **Response Format:**
  ```json
  {
    "relay_id": "01j9x0m1p00000000000000000",
    "status": "persisted"
  }
  ```

### `POST /api/v1/jobs/:route_name` *(Protected)*
Fire-and-forget asynchronous job ingestion. Requires a valid API key, admin session cookie, or loopback connection in Dev Mode. Persists the request to SQLite WAL storage and schedules immediate delivery by background dispatcher workers.

- **Credential Stripping:** Simple Relay automatically strips its own authentication credentials (`X-API-Key`, `Authorization: Bearer sr_live_*`, and `relay_session` cookie) before persisting `requests.headers` to SQLite or forwarding the job to the destination URL. Any upstream `Authorization` header (e.g., `Bearer sk_live_...`) is preserved intact.
- **Status Codes:**
  - `202 Accepted`: Job durably queued in SQLite WAL storage.
  - `401 Unauthorized`: Missing or invalid API key / session credentials.
  - `413 Payload Too Large`: Payload exceeds body limit (`RELAY_MAX_BODY_SIZE_MB`, default: `10` MB).
- **Response Format:**
  ```json
  {
    "job_id": "01j9x0z87q00000000000000000",
    "status": "queued"
  }
  ```

### `ANY /api/v1/relay/:route_name` *(Protected)*
Synchronous outbound proxy for routes configured with `mode: "sync"`. Requires a valid API key, admin session cookie, or loopback connection in Dev Mode. Transparently forwards HTTP method, headers, and request body to the configured destination URL.

- **Credential Stripping:** Strips internal Simple Relay authentication headers (`X-API-Key`, `Authorization: Bearer sr_live_*`, and `relay_session` cookie) prior to SQLite audit logging and upstream forwarding, while preserving upstream `Authorization` tokens (e.g., `Bearer $OPENAI_API_KEY`).
- **Automatic Retries & Non-Idempotent Guardrails:** Automatically retries transient upstream failures (`429 Too Many Requests`, `500`, `502`, `503`, `504`) with exponential backoff and randomized full jitter (`100ms` base, `10s` max default). Idempotent methods (`GET`, `HEAD`, `PUT`, `DELETE`, `OPTIONS`, `TRACE`) always retry up to `max_attempts`; non-idempotent methods (`POST`, `PATCH`) only retry when an `Idempotency-Key` / `X-Relay-Idempotency-Key` header is provided or `retry_non_idempotent: true` is configured on the route.
- **Client Disconnect Cancellation:** Cancels in-flight upstream execution immediately if the calling client closes the connection (`r.Context()`), eliminating wasted compute and downstream quota consumption.
- **Returns:** Transparent upstream status code, response headers, and body (`401 Unauthorized` if caller is unauthenticated; `413 Payload Too Large` if payload exceeds `RELAY_MAX_BODY_SIZE_MB`, default `10` MB).

---

## 3. Authentication & Sessions (Public)

### `GET /api/v1/auth/status`
Checks whether server authentication is required for the current caller (`true` when `RELAY_ADMIN_PASSWORD` is configured or when calling from a non-loopback IP in Dev Mode) and whether the caller is currently authenticated.

- **Response Format:**
  ```json
  {
    "auth_required": true,
    "authenticated": false
  }
  ```

### `POST /api/v1/auth/login`
Authenticates an administrator with the configured `RELAY_ADMIN_PASSWORD` and issues an HTTP-only session cookie.

- **Rate Limiting & Proxy IP Trust:** Enforces brute-force lockout (max 5 failed attempts within 5 minutes per client IP). Returns `429 Too Many Requests` with a `Retry-After` header when locked. By default, client IP is extracted directly from the TCP socket (`r.RemoteAddr`) to prevent `X-Forwarded-For` header spoofing; proxy headers (`X-Forwarded-For`, `X-Real-IP`) are only trusted when `--trust-proxy` (`RELAY_TRUST_PROXY=true`) is explicitly enabled.
- **Dev Mode Behavior:** When `RELAY_ADMIN_PASSWORD` is empty, loopback callers receive `200 OK` immediately, while non-loopback callers receive `401 Unauthorized` (must authenticate with an API key instead).
- **Request Body:**
  ```json
  {
    "password": "your-admin-password"
  }
  ```
- **Response Format (`200 OK`):**
  - Sets `relay_session` cookie (valid 24 hours, `HttpOnly`, `SameSite=Lax`, `Secure` when HTTPS).
  ```json
  {
    "status": "authenticated"
  }
  ```

### `POST /api/v1/auth/logout`
Invalidates the current session token in memory and clears the `relay_session` cookie.

- **Response Format:**
  ```json
  {
    "status": "logged_out"
  }
  ```

---

## 4. Route Management (Protected)

Routes configure forwarding destinations, retry backoff strategies, timeouts, and cryptographic signature verification. Modifications hot-reload into memory immediately without service restarts.

> [!NOTE] Route Secret Masking
> To prevent credential leakage, `signature_secret` is **always masked** in HTTP JSON responses across `GET`, `POST`, and `PUT /api/v1/routes*` (formatted as `first4*****last4`, e.g., `"whse*****9a1b"`, or `"********"` for secrets `<= 8` characters). The unmasked secret remains intact in SQLite and the in-memory `RouteRegistry` for cryptographic signature verification.

### `GET /api/v1/routes`
Lists all configured routes with `signature_secret` values masked.

- **Response Format:**
  ```json
  [
    {
      "id": "01j9x0r7000000000000000000",
      "name": "stripe-inbound",
      "type": "inbound",
      "mode": "async",
      "path": "/api/v1/webhook/stripe",
      "destination_url": "https://api.internal.com/webhooks/stripe",
      "signature_provider": "stripe",
      "signature_secret": "whse*****9a1b",
      "max_attempts": 5,
      "timeout_ms": 10000,
      "retry_backoff_strategy": "exp",
      "base_backoff_ms": 5000,
      "max_backoff_ms": 900000,
      "retry_non_idempotent": false,
      "created_at": "2026-10-15T01:00:00Z",
      "updated_at": "2026-10-15T01:00:00Z"
    }
  ]
  ```

### `POST /api/v1/routes`
Creates and registers a new routing configuration. The returned JSON payload masks `signature_secret`.

- **Fields & Parameters:**
  - `name` *(string, required)*: Unique slug identifier for the route.
  - `type` *(string, required)*: `"inbound"` (external webhook ingestion) or `"outbound"` (internal to external delivery).
  - `mode` *(string, required)*: `"async"` (durable background queue) or `"sync"` (synchronous transparent proxy).
  - `path` *(string, required)*: Path pattern matching incoming requests (e.g., `/api/v1/webhook/:slug`, `/api/v1/relay/:slug`, `/api/v1/jobs/:slug`).
  - `destination_url` *(string, required)*: Upstream destination target URL (`http://` or `https://`).
  - `max_attempts` *(integer, default: `5`)*: Maximum delivery attempts before routing to the DLQ.
  - `timeout_ms` *(integer, default: `10000`)*: Per-attempt HTTP timeout in milliseconds.
  - `retry_backoff_strategy` *(string, default: `"exp"`)*: Retry backoff curve: `"exp"` (exponential) or `"linear"`.
  - `base_backoff_ms` *(integer, default: `0`)*: Minimum retry backoff delay in milliseconds. When `0`, inherits engine defaults (`100ms` for sync proxy, `5000ms` for async background delivery).
  - `max_backoff_ms` *(integer, default: `0`)*: Maximum retry backoff delay in milliseconds. When `0`, inherits engine defaults (`10000ms` for sync proxy, `900000ms` for async background delivery).
  - `retry_non_idempotent` *(boolean, default: `false`)*: For sync outbound proxying (`mode: "sync"`), enables automatic retries on non-idempotent HTTP methods (`POST`, `PATCH`) without requiring an `Idempotency-Key` header.
  - `signature_provider` *(string, optional, default: `"none"`)*: Inbound HMAC provider (`"stripe"`, `"github"`, `"hmac_sha256"`, or `"none"`).
  - `signature_secret` *(string, optional)*: Cryptographic signing secret.
- **Request Body:**
  ```json
  {
    "name": "github-webhook",
    "type": "inbound",
    "mode": "async",
    "path": "/api/v1/webhook/github",
    "destination_url": "https://api.internal.com/webhooks/github",
    "signature_provider": "github",
    "signature_secret": "secret123",
    "max_attempts": 5,
    "timeout_ms": 10000,
    "retry_backoff_strategy": "exp",
    "base_backoff_ms": 5000,
    "max_backoff_ms": 900000,
    "retry_non_idempotent": false
  }
  ```
- **Status Code:** `201 Created`

### `GET /api/v1/routes/:id`
Retrieves a single route configuration by ULID (with `signature_secret` masked).

### `PUT /api/v1/routes/:id`
Updates an existing route configuration by ULID and hot-reloads the internal registry.

- **Safe Secret Preservation:** When updating an inbound route with an active `signature_provider`, if `signature_secret` is omitted (`""`) or matches the currently masked value (e.g., `"whse*****9a1b"` when saving changes from the Web UI), Simple Relay preserves the existing raw secret in storage. Supplying a new plaintext secret rotates it immediately.

### `DELETE /api/v1/routes/:id`
Deletes a route configuration by ULID.

- **Response Format:**
  ```json
  {
    "status": "deleted"
  }
  ```

---

## 5. Delivery & Request Inspection (Protected)

### `GET /api/v1/requests`
List recorded delivery requests with pagination and status filters.

- **Query Parameters:**
  - `status`: Filter by status (`pending`, `delivering`, `delivered`, `failed`).
  - `route_id`: Filter by route ULID.
  - `search`: Filter by text contained in destination URL.
  - `limit`: Number of records (default: `50`, max: `100`).
  - `offset`: Pagination offset (default: `0`).
- **Response Format:**
  ```json
  {
    "items": [
      {
        "id": "01j9x0m1p00000000000000001",
        "route_id": "01j9x0r7000000000000000000",
        "method": "POST",
        "url": "https://api.internal.com/webhooks/stripe",
        "status": "delivered",
        "attempts_count": 2,
        "last_error": null,
        "created_at": "2026-10-15T01:10:00Z",
        "updated_at": "2026-10-15T01:10:03Z"
      }
    ],
    "total": 1,
    "limit": 50,
    "offset": 0
  }
  ```

### `GET /api/v1/requests/:id`
Fetch single delivery record including headers, raw payload body, and historical attempt logs.

- **Response Format:**
  ```json
  {
    "id": "01j9x0m1p00000000000000001",
    "route_id": "01j9x0r7000000000000000000",
    "method": "POST",
    "url": "https://api.internal.com/webhooks/stripe",
    "headers": {
      "Content-Type": "application/json",
      "Stripe-Signature": "t=1728950000,v1=..."
    },
    "body": "{\"id\":\"evt_123\",\"type\":\"payment_intent.succeeded\"}",
    "status": "delivered",
    "attempts_count": 2,
    "last_error": null,
    "created_at": "2026-10-15T01:10:00Z",
    "updated_at": "2026-10-15T01:10:03Z",
    "attempts": [
      {
        "id": "01j9x0m2000000000000000001",
        "request_id": "01j9x0m1p00000000000000001",
        "attempt_number": 1,
        "status_code": 503,
        "duration_ms": 42,
        "error_message": "upstream service unavailable",
        "attempted_at": "2026-10-15T01:10:01Z"
      },
      {
        "id": "01j9x0m3000000000000000002",
        "request_id": "01j9x0m1p00000000000000001",
        "attempt_number": 2,
        "status_code": 200,
        "duration_ms": 18,
        "error_message": null,
        "attempted_at": "2026-10-15T01:10:03Z"
      }
    ]
  }
  ```

### `POST /api/v1/requests/:id/replay`
Requeue any persisted request—regardless of whether its status is `success`, `failed`, or `dlq`—resetting its attempts count to 0, status to `pending`, and waking the background dispatcher for immediate delivery.

> [!NOTE] State Conflicts
> If the request is currently in-flight (`processing`) or waiting in queue (`pending`), the API returns HTTP `409 Conflict`. Replay is supported exclusively for terminal states (`success`, `failed`, `dlq`).

- **Response Format:**
  ```json
  {
    "id": "01j9x0m1p00000000000000001",
    "status": "pending",
    "replayed": true,
    "replayed_at": "2026-10-21T03:00:00Z"
  }
  ```

### `GET /api/v1/jobs/:job_id`
Inspect status, retry scheduling, and execution history for an asynchronous background job.

- **Response Format:**
  ```json
  {
    "id": "01j9x0z87q00000000000000000",
    "route_id": "01j9x0r7000000000000000000",
    "method": "POST",
    "url": "https://api.internal.com/jobs/process",
    "status": "pending",
    "attempts_count": 0,
    "next_attempt_at": "2026-10-15T01:15:00Z",
    "last_error": null,
    "created_at": "2026-10-15T01:15:00Z",
    "updated_at": "2026-10-15T01:15:00Z",
    "attempts": []
  }
  ```

---

## 6. Queue Telemetry (Protected)

### `GET /api/v1/queue/stats`
Returns real-time queue depth and active worker processing metrics.

- **Response Format:**
  ```json
  {
    "pending": 8,
    "in_flight": 3,
    "queue_depth": 11,
    "status": "HEALTHY"
  }
  ```

> [!NOTE] Backlog Threshold
> When total `queue_depth` (`pending + in_flight`) reaches or exceeds **1,000**, the `status` field switches from `HEALTHY` to `BACKLOG`. This provides an immediate zero-parsing signal for autoscaling policies and system alerts.

---

## 7. Dead Letter Queue (DLQ) Management (Protected)

When a delivery exhausts its maximum retry attempts without receiving a 2xx response, it is permanently marked as `failed` and preserved in the Dead Letter Queue (DLQ).

### `GET /api/v1/dlq`
Lists dead-lettered requests awaiting manual triage or automated replay.

- **Query Parameters:**
  - `route_name`: Optional filter by route name.
  - `limit`: Number of records (default: `50`, max: `500`).
  - `offset`: Pagination offset (default: `0`).
- **Response Format:**
  ```json
  {
    "items": [
      {
        "id": "01j9x0m1p00000000000000001",
        "route_id": "01j9x0r7000000000000000000",
        "method": "POST",
        "url": "https://api.internal.com/webhooks/stripe",
        "status": "dlq",
        "attempts_count": 5,
        "last_error": "connection refused",
        "created_at": "2026-10-15T01:10:00Z",
        "updated_at": "2026-10-15T01:12:30Z"
      }
    ],
    "total": 1,
    "limit": 50,
    "offset": 0
  }
  ```

### `POST /api/v1/dlq/:request_id/replay`
Requeues an individual failed delivery for immediate retry and wakes the dispatcher worker pool immediately.

- **Status Codes:** `200 OK` on replay; `404 Not Found` if ID does not exist; `409 Conflict` if request is not in DLQ status.
- **Response Format:**
  ```json
  {
    "id": "01j9x0m1p00000000000000001",
    "status": "pending",
    "replayed": true,
    "replayed_at": "2026-10-15T01:20:00Z"
  }
  ```

### `POST /api/v1/dlq/replay-all`
Bulk requeues dead-lettered deliveries matching filter criteria and immediately triggers delivery workers.

- **Request Body (Optional):**
  ```json
  {
    "route_name": "stripe-inbound",
    "limit": 100
  }
  ```
  *(Default limit: `100`, max: `1000` to prevent downstream thundering herd shockwaves).*
- **Response Format:**
  ```json
  {
    "replayed_count": 14,
    "request_ids": [
      "01j9x0m1p00000000000000001",
      "01j9x0m1p00000000000000002"
    ]
  }
  ```

---

## 8. API Key Management (Protected)

Administrative API keys allow secure service-to-service communication with Simple Relay management endpoints without managing browser session cookies.

### `POST /api/v1/api-keys`
Generates a new administrative API key. Returns the raw secret token once.

- **Request Body:**
  ```json
  {
    "name": "worker-ci"
  }
  ```
- **Status Code:** `201 Created`
- **Response Format:**
  ```json
  {
    "id": "01j9x0k8000000000000000001",
    "name": "worker-ci",
    "key": "sr_live_a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90",
    "masked_key": "sr_live_a1b*****8f90",
    "created_at": "2026-10-15T01:30:00Z"
  }
  ```

> [!WARNING] Raw Token Exposure
> The unmasked `key` (`sr_live_...`) is returned **only once** upon generation. Simple Relay hashes the token with SHA-256 before persisting to SQLite and never stores the plaintext key. Store the raw key securely in your environment variables or secrets vault.

### `GET /api/v1/api-keys`
Lists all active and revoked API keys with metadata and masked keys.

- **Response Format:**
  ```json
  [
    {
      "id": "01j9x0k8000000000000000001",
      "name": "worker-ci",
      "masked_key": "sr_live_a1b*****8f90",
      "created_at": "2026-10-15T01:30:00Z",
      "revoked_at": null
    }
  ]
  ```

### `DELETE /api/v1/api-keys/:id`
Immediately revokes an active API key by ULID. Revoked keys cannot be used for authentication.

- **Status Code:** `200 OK` on revocation; `404 Not Found` if already revoked or non-existent.
- **Response Format:**
  ```json
  {
    "id": "01j9x0k8000000000000000001",
    "status": "revoked"
  }
  ```

---

## 9. System & Health (Public)

### `GET /healthz`
Standard operational liveness probe returning HTTP `200 OK` and system timestamp.

```json
{
  "status": "ok",
  "timestamp": "2026-10-15T01:25:00Z"
}
```
