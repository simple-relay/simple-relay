# Outbound Retry Engine & Synchronous HTTP Proxy Design Specification

- **Topic:** Phase 1 Milestone 1.2: Outbound Retry Logic & Synchronous HTTP Proxy (Tasks 1.2.1 – 1.2.5)
- **Author:** Simple Relay Engineering Team
- **Date:** 2026-10-08
- **Status:** Approved
- **Target Location:** `docs/specs/2026-10-08-outbound-retry-sync-proxy-design.md`
- **Related Specs:** [docs/architecture.md](../architecture.md), [docs/roadmap.md](../roadmap.md)

---

## 1. Executive Summary & Goals

### 1.1 Objective
Implement Phase 1 Milestone 1.2 for Simple Relay: a robust, low-latency, zero-dependency synchronous HTTP proxy layer at `/api/v1/relay/{route_name}`. The system executes outbound HTTP requests against target destination URLs with automatic bounded retries, Full Jitter exponential backoff, rate-limit awareness (`Retry-After`), immediate client-disconnect cancellation, and complete SQLite audit persistence adhering to foreign key relationships.

### 1.2 Non-Goals & Scope Boundaries
- **No Large File Streaming / Multipart Ingestion:** Payloads are capped at a default of 10 MB (`http.MaxBytesReader`) to avoid SQLite BLOB storage exhaustion and excessive memory allocations.
- **No Background Worker Ingestion:** Async queueing (`/api/v1/jobs/...`) belongs to Milestone 1.3. Milestone 1.2 focuses solely on synchronous 1-request-1-goroutine proxying.
- **No Cryptographic HMAC Inbound Verification:** Inbound webhook signature verification belongs to Phase 2.

---

## 2. Architecture & Component Decomposition

```
Caller (Downstream)
       │
       ▼  [HTTP Method: ANY, Headers, Body]
┌────────────────────────────────────────────────────────┐
│ 1. API Layer (internal/api/handler_relay.go)           │
│    - Parse {route_name} via standard net/http ServeMux │
│    - Resolve Route from in-memory RouteRegistry (O(1)) │
│    - Read body with 10MB MaxBytesReader limit          │
│    - Generate lowercase ULID X-Relay-Request-ID        │
│    - Strip RFC 7230 hop-by-hop headers                 │
│    - Forward Idempotency-Key & parse X-Relay-Timeout   │
└──────────────────────────┬─────────────────────────────┘
                           │
                           ▼
┌────────────────────────────────────────────────────────┐
│ 2. Engine Layer (internal/engine/relay.go)             │
│    - Insert Request: RequestRepository.CreateRequest   │
│    - Synchronous Retry Loop (1 .. max_attempts):       │
│      * Derive attempt context: WithTimeout(r.Context())│
│      * Abort on client disconnect (context.Canceled)   │
│      * Execute upstream call with fresh bytes.Reader   │
│      * Check RetryEngine: backoff + jitter / Retry-After│
│      * Record Attempt: AttemptRepository.RecordAttempt │
│        (Truncate response body to 32 KB max)           │
│    - Update Request: UpdateRequestStatus (success/fail)│
└──────────────────────────┬─────────────────────────────┘
                           │
                           ▼
                 Upstream Destination URL
```

### 2.1 File & Package Layout
- `internal/engine/retry.go`: Stateless retry evaluation, exponential/linear backoff math, Full Jitter algorithm, and `Retry-After` header parser.
- `internal/engine/retry_test.go`: Unit tests for backoff formulas, jitter boundaries, `Retry-After` formats, and retryable conditions.
- `internal/storage/sqlite/attempt_repo.go`: Implements `storage.AttemptRepository` (`RecordAttempt`, `ListAttemptsByRequestID`).
- `internal/storage/sqlite/attempt_repo_test.go`: Tests for attempt recording, body truncation (32 KB limit), and retrieval.
- `internal/storage/sqlite/request_repo.go`: Implements foundational `storage.RequestRepository` (`CreateRequest`, `UpdateRequestStatus`, `GetRequestByID`) for SQLite foreign key compliance.
- `internal/storage/sqlite/request_repo_test.go`: Tests for request insertion, state transition, and foreign key cascades.
- `internal/engine/relay.go`: `RelayEngine` orchestrating outbound HTTP execution, per-attempt timeout, retry loop, client-disconnect cancellation, and audit recording.
- `internal/engine/relay_test.go`: Integration tests with mock upstream servers verifying retries, client abortion, headers, and status updates.
- `internal/api/handler_relay.go`: HTTP handler for `/api/v1/relay/{route_name}`.
- `internal/api/handler_relay_test.go`: End-to-end API tests covering HTTP methods, status codes, response headers, and size limits.

---

## 3. Retry Engine Specification (`internal/engine/retry.go`)

### 3.1 Timing Parameters & Defaults
- **`BaseBackoff`:** `100ms`
- **`MaxBackoff`:** `10s`
- **`MaxRetryAfter`:** `30s` (Upper clamp for `Retry-After` to prevent long synchronous HTTP hangs)

### 3.2 Backoff Calculations
Let `attempt` be the 1-based attempt index (e.g., attempt 1 fails, calculating delay before attempt 2):
1. **Exponential Strategy (`exp`):**
   $$T_{\text{wait}} = \min\left(\text{MaxBackoff},\; \text{BaseBackoff} \times 2^{(\text{attempt} - 1)}\right)$$
   *(Attempt 1 fail: 100ms; Attempt 2 fail: 200ms; Attempt 3 fail: 400ms; Attempt 4 fail: 800ms...)*
2. **Linear Strategy (`linear`):**
   $$T_{\text{wait}} = \min\left(\text{MaxBackoff},\; \text{BaseBackoff} \times \text{attempt}\right)$$

### 3.3 Full Jitter Algorithm
To eliminate Thundering Herd concurrency spikes:
$$\text{SleepDuration} = \text{rand.Float64()} \times T_{\text{wait}} \quad (\text{uniformly distributed in } [0, T_{\text{wait}}])$$
Implemented using Go 1.22+ `math/rand/v2` (`rand.N` or `rand.Float64()`) which is thread-safe and seeded automatically.

### 3.4 `Retry-After` Header Parsing
When upstream responds with `429 Too Many Requests`:
1. Check `Retry-After` header value.
2. **Integer Seconds:** If parseable as positive integer (`strconv.Atoi`), delay is `N * time.Second`.
3. **HTTP Date:** If integer parse fails, attempt parsing as HTTP-date (`http.TimeFormat` / RFC 1123, RFC 850, ANSI C). Delay is `targetTime.Sub(now)`.
4. **Fallback & Clamping:**
   - If header is absent, malformed, or yields negative duration $\rightarrow$ fallback to calculated Backoff + Full Jitter.
   - If delay exceeds `MaxRetryAfter` (30s) $\rightarrow$ clamp delay to 30s.

### 3.5 Retryable Condition Rules
- **Retryable HTTP Status Codes:** `429`, `500`, `502`, `503`, `504`.
- **Non-Retryable HTTP Status Codes:** `2xx`, `3xx`, `4xx` (except `429`).
- **Retryable Network Errors:**
  - DNS resolution failures (`*net.DNSError`).
  - Connection refused (`syscall.ECONNREFUSED`).
  - Connection reset by peer (`syscall.ECONNRESET` or broken pipe).
  - Per-attempt timeout (`net.Error` with `Timeout() == true` or attempt-level `context.DeadlineExceeded`).
- **Non-Retryable Errors:**
  - Client downstream disconnect / context abort (`context.Canceled` on incoming request context).
  - Invalid destination URL syntax or certificate validation errors.

---

## 4. Storage Engine & Audit Repositories

### 4.1 Schema Compliance & Foreign Keys
SQLite enforces foreign keys (`PRAGMA foreign_keys = ON;`).
`attempts.request_id` has `FOREIGN KEY(request_id) REFERENCES requests(id) ON DELETE CASCADE`.
Therefore, every synchronous proxy invocation MUST persist a parent row in `requests` before recording rows in `attempts`.

### 4.2 `RequestRepository` Implementation (`internal/storage/sqlite/request_repo.go`)
- `CreateRequest(ctx context.Context, req *model.Request) error`:
  - Validates request model. Generates ULID if ID is empty.
  - Serializes `req.Headers` map to JSON string.
  - Inserts row into `requests` using writer pool (`db.Writer()`).
- `UpdateRequestStatus(ctx context.Context, id string, status model.RequestStatus, attemptsCount int, nextAttemptAt *time.Time, lastError *string) error`:
  - Updates `status`, `attempts_count`, `next_attempt_at`, `last_error`, and `updated_at` where `id = ?`.
  - Returns `storage.ErrNotFound` if row was not affected.
- `GetRequestByID(ctx context.Context, id string) (*model.Request, error)`:
  - Queries reader pool (`db.Reader()`), parses headers JSON, returns `model.Request`.

### 4.3 `AttemptRepository` Implementation (`internal/storage/sqlite/attempt_repo.go`)
- `RecordAttempt(ctx context.Context, attempt *model.Attempt) error`:
  - Validates attempt model. Generates ULID if ID is empty.
  - Enforces 32 KB response bloat limit: applies `model.TruncateResponseBody` if `ResponseBody` is present.
  - Inserts row into `attempts` using writer pool (`db.Writer()`).
- `ListAttemptsByRequestID(ctx context.Context, requestID string) ([]*model.Attempt, error)`:
  - Queries reader pool (`db.Reader()`) ordered by `attempt_number ASC`.

---

## 5. Synchronous Relay Delivery Engine (`internal/engine/relay.go`)

### 5.1 Struct & Signatures
```go
type RelayEngine struct {
    httpClient   *http.Client
    retryEngine  *RetryEngine
    requestRepo  storage.RequestRepository
    attemptRepo  storage.AttemptRepository
    logger       *slog.Logger
}

type RelayResult struct {
    StatusCode int
    Headers    http.Header
    Body       []byte
    Attempts   int
    RequestID  string
}

func (e *RelayEngine) RelaySync(
    ctx context.Context,
    route *model.Route,
    method string,
    rawURL string,
    headers http.Header,
    body []byte,
    timeoutOverrideMS *int,
) (*RelayResult, error)
```

### 5.2 Delivery Algorithm & Invariants
1. **Initialize Parent Request:**
   - Create `model.Request` with status `RequestStatusProcessing`, lowercase ULID `id`, route ID, method, URL, headers, and raw `body`.
   - Persist synchronously to SQLite via `requestRepo.CreateRequest`.
2. **Determine Attempt Timeout:**
   - Default: `time.Duration(route.TimeoutMS) * time.Millisecond`.
   - If `timeoutOverrideMS` is provided and $> 0$: use `time.Duration(*timeoutOverrideMS) * time.Millisecond`.
3. **Loop Over Attempts (`attemptNumber := 1; attemptNumber <= route.MaxAttempts; attemptNumber++`):**
   - **Check Client Context:** If `ctx.Err() != nil` (client disconnected), immediately terminate loop with context error. Do not sleep or retry.
   - **Derive Attempt Context:** `attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)`.
   - **Reconstruct HTTP Body:** Wrap raw byte slice with `bytes.NewReader(body)` so each attempt reads the complete payload from byte 0.
   - **Construct Upstream Request:** Set method, target URL, and copy filtered headers.
   - **Execute Call:** `start := time.Now(); resp, err := e.httpClient.Do(upstreamReq)`. Record `durationMS := time.Since(start).Milliseconds()`.
   - **Handle Network/Timeout Errors:**
     - Record attempt in SQLite with `error_message = err.Error()`.
     - Check `if ctx.Err() != nil`: Client abort $\rightarrow$ finalize request as `failed` ("client disconnected") and exit.
     - Check `e.retryEngine.IsRetryableError(err)`: If retryable and `attemptNumber < route.MaxAttempts`, calculate backoff delay, wait `select { case <-time.After(delay): case <-ctx.Done(): }`, and loop.
     - If retries exhausted, update request as `failed`, return gateway error.
   - **Handle Upstream HTTP Response:**
     - Read response body (capped at reasonable buffer limit, e.g. 10MB; truncated to 32 KB for database logging). Close `resp.Body`.
     - Record attempt in SQLite with status code, truncated response body, and duration.
     - If `e.retryEngine.IsRetryableStatus(resp.StatusCode)`:
       - If `attemptNumber < route.MaxAttempts`: calculate backoff (evaluating `Retry-After` if 429), sleep or wait for `ctx.Done()`, and continue.
       - If `attemptNumber >= route.MaxAttempts`: mark request as `failed`, return final response to caller.
     - If NOT retryable (e.g., 2xx, 3xx, 4xx non-429):
       - Mark request status as `success` (or `failed` for 4xx).
       - Return response immediately.
4. **Finalize State:**
   - Call `requestRepo.UpdateRequestStatus` with final state, attempts count, and last error message.

---

## 6. API Routing, Handler & Header Normalization (`internal/api/handler_relay.go`)

### 6.1 Route Registration
Registered on `ServeMux` at `/api/v1/relay/{route_name}` without method prefix so standard library ServeMux routes `GET`, `POST`, `PUT`, `PATCH`, `DELETE` to the handler.

### 6.2 Execution Pipeline
1. Extract `routeName = r.PathValue("route_name")`.
2. Look up `route` in `routeRegistry.GetRouteByName(routeName)`.
   - If not found: return `404 Not Found` with `{"error": "route not found"}`.
   - If `route.Mode == "async"`: return `400 Bad Request` with `{"error": "route mode is async, use /api/v1/jobs"}`.
3. Limit request body using `http.MaxBytesReader(w, r.Body, 10*1024*1024)`. If exceeded, return `413 Payload Too Large`.
4. Header Processing:
   - Generate `X-Relay-Request-ID` as lowercase ULID if not provided.
   - Extract `X-Relay-Timeout` integer value if present.
   - Strip Hop-by-Hop headers:
     `Connection`, `Keep-Alive`, `Proxy-Authenticate`, `Proxy-Authorization`, `TE`, `Trailers`, `Transfer-Encoding`, `Upgrade`.
   - Propagate `Idempotency-Key` or `X-Relay-Idempotency-Key`.
5. Call `relayEngine.RelaySync`.
6. Write response:
   - Copy upstream response headers.
   - Set diagnostic headers:
     - `X-Relay-Request-ID: <ulid>`
     - `X-Relay-Attempts: <count>`
   - Write status code and body.

---

## 7. Error Handling & Invariants

| Scenario | Retry Loop Action | Response to Downstream | Database Record |
| :--- | :--- | :--- | :--- |
| **Upstream 2xx** | Stop immediately | Upstream Status, Headers, Body | `status='success'` |
| **Upstream 4xx (non-429)** | Stop immediately (client error) | Upstream Status, Headers, Body | `status='failed'` |
| **Upstream 429 / 5xx** | Retry up to `max_attempts` | Last attempt's Status, Headers, Body | `status='failed'`, `last_error='HTTP <status>'` |
| **Upstream Network / Timeout** | Retry up to `max_attempts` | `502 Bad Gateway` / `504 Gateway Timeout` | `status='failed'`, `last_error='<error>'` |
| **Client Disconnect (`context.Canceled`)** | Abort flight immediately; no sleep | Close connection cleanly | `status='failed'`, `last_error='client disconnected'` |
| **Body > 10MB** | Reject before relay | `413 Payload Too Large` | None |
| **Route Not Found** | Reject before relay | `404 Not Found` | None |

---

## 8. Testing & Verification Strategy

### 8.1 Unit Tests
- `internal/engine/retry_test.go`:
  - Exponential backoff progression: 100ms, 200ms, 400ms...
  - Linear backoff progression: 100ms, 200ms, 300ms...
  - Max backoff cap enforcement (capped at 10s).
  - Full Jitter distribution verification ($0 \le \text{sleep} \le T_{\text{wait}}$).
  - `Retry-After` parsing: integer seconds, RFC 1123 date, malformed string fallback, 30s clamp.
  - Classification of retryable statuses (429, 500, 502, 503, 504) vs non-retryable (200, 400, 401, 404).
  - Classification of network/timeout errors vs `context.Canceled`.
- `internal/storage/sqlite/attempt_repo_test.go`:
  - Inserting attempt records on writer pool.
  - Verifying automatic 32 KB truncation on response bodies.
  - Querying attempt history by `request_id`.
- `internal/storage/sqlite/request_repo_test.go`:
  - Inserting request record and verifying serialized JSON headers.
  - Updating status transitions (`processing` $\rightarrow$ `success`/`failed`).
  - Verifying SQLite foreign key cascade.

### 8.2 Integration Tests
- `internal/engine/relay_test.go`:
  - Immediate upstream 200 OK delivery.
  - Upstream 500 failure on attempt 1, followed by 200 OK on attempt 2 (retry success).
  - Persistent 503 failure exhausting `max_attempts`.
  - Rate limiting (429) honoring `Retry-After`.
  - Client context cancellation stopping retry loop without waiting.
  - Byte body immutability across multiple attempts.

### 8.3 End-to-End API Tests
- `internal/api/handler_relay_test.go`:
  - Endpoints verify `GET`, `POST`, `PUT`, `DELETE` methods.
  - Headers `X-Relay-Request-ID` and `X-Relay-Attempts` present in client response.
  - `Idempotency-Key` and `X-Relay-Timeout` propagation.
  - Error endpoints: 404 for missing route, 400 for async route, 413 for oversized body.

### 8.4 Verification Commands
- Race condition testing: `go test -race ./...`
- Verbose test run: `go test -v ./...`
- Static binary compilation: `CGO_ENABLED=0 go build -o /dev/null ./cmd/relay`
