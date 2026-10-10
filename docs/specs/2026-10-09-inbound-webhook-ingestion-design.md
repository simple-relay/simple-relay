# Design Specification: Inbound Webhook Ingestion Pipeline

- **Date:** 2026-10-09
- **Topic:** Inbound Webhook Ingestion & Raw Payload Preservation
- **Milestone:** Phase 2 — Inbound Webhooks & Security Pipeline (Milestone 2.1 Focus)
- **Status:** Approved

---

## 1. Executive Summary & Objective

The objective of this specification is to establish the inbound webhook ingestion layer for Simple Relay. 
Simple Relay acts as a durable shock absorber for incoming webhooks (from services such as Stripe, GitHub, Shopify, etc.), immediately accepting webhooks and storing unmodified raw payloads into SQLite WAL storage before acknowledging HTTP `200 OK` to senders. Subsequently, the durable delivery pipeline (Dispatcher and Worker Pool) forwards the webhooks to internal destination services with retries and DLQ guarantees.

Cryptographic signature verification adapters are deliberately decoupled and deferred to a downstream security layer milestone; during this phase, Relay preserves raw payload bytes and incoming signature headers intact so destination services can verify authenticity without disruption.

---

## 2. Core Architectural Decisions & Invariants

1. **Durability Golden Rule:** The receiver MUST NOT respond with HTTP `200 OK` until the raw payload and request metadata are durably committed to SQLite WAL storage via the dedicated single-writer `BatchWriter`.
2. **Zero Payload Mutation:** The incoming payload bytes must be captured directly from `r.Body` into a byte slice without parsing, unmarshaling, or re-encoding.
3. **Payload Size Enactment:** Payload sizes are bounded by `http.MaxBytesReader` based on the configured limit (`RELAY_MAX_BODY_SIZE_MB`, default 10MB). Payloads exceeding the limit are rejected immediately with HTTP `413 Request Entity Too Large`.
4. **Method Compatibility:** Inbound webhook endpoints support `POST`, `PUT`, and `PATCH` methods. Other HTTP methods are rejected with HTTP `405 Method Not Allowed`.
5. **Route Type Guard:** The matched route must have `route.Type == "inbound"`. If an outbound route is targeted, the request is rejected with HTTP `400 Bad Request`.
6. **Zero-Touch Background Delivery:** Enqueued requests with status `pending` are picked up by the existing `Dispatcher` and `WorkerPool` without requiring modifications to the core delivery engine.

---

## 3. API Contract: Inbound Webhook Endpoint

### Endpoint
```http
ANY /api/v1/webhook/{route_name}
```

### Request Requirements
- **Path Parameters:**
  - `route_name` (string, required): Unique identifier matching a configured route name.
- **Allowed HTTP Methods:**
  - `POST`, `PUT`, `PATCH`. Any other method returns `405 Method Not Allowed` with `Allow: POST, PUT, PATCH`.
- **Headers:**
  - Hop-by-hop headers (`Connection`, `Keep-Alive`, `Proxy-Authenticate`, `Proxy-Authorization`, `TE`, `Trailers`, `Transfer-Encoding`, `Upgrade`) are filtered out.
  - All application headers (including `Content-Type`, `Stripe-Signature`, `X-Hub-Signature-256`, etc.) are preserved verbatim into `requests.headers`.
  - `X-Relay-Request-ID`: Retained if provided by caller, otherwise generated as a new monotonic lowercase ULID.

### Success Response
- **Status Code:** `200 OK` (Standard expectation for external webhook providers)
- **Headers:** `Content-Type: application/json`
- **Body:**
```json
{
  "relay_id": "01jar37v9x22d4f5g6h7j8k9m0",
  "status": "persisted"
}
```

### Error Responses
- **400 Bad Request:**
  - Missing route name: `{"error": "route_name is required in URL path"}`
  - Route type mismatch: `{"error": "route is not configured for inbound webhooks"}`
  - Malformed read: `{"error": "failed to read request body"}`
- **404 Not Found:**
  - Route does not exist in registry: `{"error": "route not found"}`
- **405 Method Not Allowed:**
  - Unsupported HTTP method: `{"error": "method not allowed"}` with header `Allow: POST, PUT, PATCH`
- **413 Request Entity Too Large:**
  - Payload exceeds max limit: `{"error": "payload too large"}`
- **504 Gateway Timeout:**
  - Client aborted or batch write deadline exceeded: `{"error": "ingestion timed out or client disconnected"}`
- **500 Internal Server Error:**
  - Storage write failure: `{"error": "failed to persist webhook"}`

---

## 4. Component Design & Implementation Details

### 4.1. Webhook Handler (`internal/api/handler_webhook.go`)
The handler function `NewWebhookHandler(registry *engine.RouteRegistry, batchWriter *engine.BatchWriter, logger *slog.Logger, maxBodyBytes int64) http.HandlerFunc`:
1. Checks the request method:
   - If `r.Method != "POST" && r.Method != "PUT" && r.Method != "PATCH"`, writes `405 Method Not Allowed` with `Allow` header.
2. Resolves route:
   - Reads `routeName := r.PathValue("route_name")`. If empty, returns `400 Bad Request`.
   - Fetches route via `registry.GetByName(routeName)`. If not found, returns `404 Not Found`.
   - Validates `route.Type == model.RouteTypeInbound`. If not inbound, returns `400 Bad Request`.
3. Reads raw payload:
   - Uses `io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))`.
   - Detects `*http.MaxBytesError` and maps to `413 Request Entity Too Large`.
4. Sanitizes and prepares headers:
   - Copies incoming headers excluding hop-by-hop headers.
   - Ensures `X-Relay-Request-ID` is assigned (generated ULID or existing header value).
5. Constructs `model.Request`:
   - `ID`: ULID string.
   - `RouteID`: `route.ID`.
   - `Method`: `r.Method`.
   - `URL`: `route.DestinationURL`.
   - `Headers`: Processed headers map.
   - `Body`: Raw unparsed `[]byte`.
   - `Status`: `model.RequestStatusPending`.
   - `AttemptsCount`: `0`.
   - `NextAttemptAt`: `&now` (`time.Now().UTC()`).
   - `CreatedAt`: `now`.
   - `UpdatedAt`: `now`.
6. Enqueues to `batchWriter`:
   - Calls `batchWriter.Enqueue(r.Context(), req)`.
   - Blocks until the batch containing this request is committed to SQLite WAL.
   - On context cancellation or deadline expiry, returns `504 Gateway Timeout`.
   - On error, returns `500 Internal Server Error`.
7. Sends `200 OK` response with `relay_id` and `status: "persisted"`.

### 4.2. Routing Integration (`internal/api/router.go`)
- In `NewRouter(cfg, logger, opts...)`:
  - When `opt.RouteRegistry != nil && opt.BatchWriter != nil`:
    - Instantiate `webhookHandler := NewWebhookHandler(opt.RouteRegistry, opt.BatchWriter, logger, opt.MaxBodyBytes)`.
    - Register on ServeMux:
      ```go
      mux.HandleFunc("/api/v1/webhook/{route_name}", webhookHandler)
      ```
    - Omitting the HTTP method prefix in `mux.HandleFunc` allows the handler to inspect any method and return `405 Method Not Allowed` with descriptive error semantics instead of ServeMux's default 404 behavior.

### 4.3. Delivery Engine Integration
No code changes are required in `internal/engine/worker_pool.go` or `internal/engine/dispatcher.go`.
- `BatchWriter` invokes `WakeFn` (`dispatcher.Wake()`) upon committing batches.
- `Dispatcher` dispatches all pending requests to `jobsCh`.
- `WorkerPool` reads `req.Method`, `req.URL`, `req.Headers`, and `req.Body` and delivers them to the destination with bounded retries and exponential backoff.

---

## 5. Verification & Testing Plan

### 5.1. Unit Tests (`internal/api/handler_webhook_test.go`)
- **Success Cases:**
  - `POST`, `PUT`, and `PATCH` requests persist to repository and return `200 OK` with valid ULID `relay_id` and `status: "persisted"`.
  - Headers preserved: provider headers (`Stripe-Signature`, custom headers) retained in request record.
  - Body preservation: raw binary or JSON byte slice matches byte-for-byte in stored record.
- **Error Cases:**
  - Route not found: returns `404 Not Found`.
  - Outbound route type: returns `400 Bad Request` ("route is not configured for inbound webhooks").
  - Disallowed methods (`GET`, `DELETE`): returns `405 Method Not Allowed`.
  - Oversized body: exceeds `maxBodyBytes`, returns `413 Request Entity Too Large`.
  - Client context cancelled: returns `504 Gateway Timeout`.

### 5.2. End-to-End Integration Test (`internal/api/e2e_inbound_webhook_test.go`)
- Starts real SQLite in-memory instance, batch writer, dispatcher, and worker pool.
- Starts a mock HTTP destination server.
- Creates an inbound route with `destination_url` pointing to the mock server.
- Sends an incoming webhook via `POST /api/v1/webhook/{route_name}` with a mock provider signature header.
- Asserts HTTP 200 response with `relay_id`.
- Waits for worker pool delivery:
  - Asserts mock destination received the exact payload bytes, HTTP method, and signature header.
  - Asserts SQLite database records transition from `pending` to `success` with audit attempt logged in `attempts` table.

---

## 6. Spec Self-Review Checklist

- [x] **Placeholder scan:** No TBD, TODO, or ambiguous sections.
- [x] **Internal consistency:** Matches Go 1.22 ServeMux, ULID primary keys, and single-writer batching rules from `architecture.md`.
- [x] **Scope check:** Strictly limited to Inbound Webhook Ingestion, deferring signature adapters to a future milestone as agreed.
- [x] **Ambiguity check:** Clarified method routing (ServeMux pattern vs 405 return), error codes, and header handling.
