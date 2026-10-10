# DLQ Replay Management API — Technical Design Specification

> **Date:** 2026-10-09  
> **Status:** Approved  
> **Author:** pmlonng (<phanminlongg@gmail.com>)  
> **Target Milestone:** Phase 2 — Milestone 2.2: Task 2.2.3

---

## 1. Overview & Problem Statement

Simple Relay ingests and delivers webhook and outbound HTTP jobs. When delivery attempts fail repeatedly and exhaust `route.max_attempts`, the request transitions into the Dead Letter Queue (`status = 'dlq'`). Currently, records in `dlq` remain parked indefinitely in SQLite storage.

This specification details the **DLQ Replay Management API**, providing operators and dashboard services with:
1. Inspection of dead-lettered requests via `GET /api/v1/dlq` with pagination and route filtering.
2. Single request replay via `POST /api/v1/dlq/{request_id}/replay`.
3. Bulk/batch request replay via `POST /api/v1/dlq/replay-all` with rate-limiting protection.
4. Non-blocking dispatcher wakeup for immediate downstream retry execution.

---

## 2. Core Architectural Decisions

- **Atomic State Transition:** Replaying a request atomically sets `status = 'pending'`, `attempts_count = 0`, `next_attempt_at = CURRENT_TIMESTAMP`, `last_error = NULL`, and `updated_at = CURRENT_TIMESTAMP`.
- **Audit Preservation:** Prior delivery records in the `attempts` table are strictly preserved for historical auditability.
- **Single-Writer SQLite Integrity:** Replay write queries run exclusively on the isolated writer connection (`SetMaxOpenConns(1)`), preventing lock contention and `SQLITE_BUSY` errors.
- **Decoupled Dispatcher Wakeup:** Replay handlers notify the background dispatcher via an injected non-blocking callback (`WakeFn func()`), triggering immediate background delivery pickup without waiting for polling ticker intervals.

---

## 3. Storage Layer Specifications

### 3.1 Interface Additions (`internal/storage/storage.go`)

```go
type RequestRepository interface {
    // Existing methods...
    CreateRequest(ctx context.Context, req *model.Request) error
    CreateRequestsBatch(ctx context.Context, reqs []*model.Request) error
    GetRequestByID(ctx context.Context, id string) (*model.Request, error)
    ListPendingRequests(ctx context.Context, limit int, dueBefore time.Time) ([]*model.Request, error)
    UpdateRequestStatus(ctx context.Context, id string, status model.RequestStatus, attemptsCount int, nextAttemptAt *time.Time, lastError *string) error
    ClaimPendingRequests(ctx context.Context, limit int, dueBefore time.Time) ([]*model.Request, error)
    RecoverStuckRequests(ctx context.Context, stuckBefore time.Time) (int64, error)

    // DLQ Management operations
    ListDLQRequests(ctx context.Context, routeID string, limit, offset int) ([]*model.Request, int64, error)
    ReplayDLQRequest(ctx context.Context, id string) (*model.Request, error)
    ReplayAllDLQRequests(ctx context.Context, routeID string, limit int) ([]string, error)
}
```

### 3.2 SQLite Repository Implementation (`internal/storage/sqlite/request_repo.go`)

- **`ListDLQRequests(ctx, routeID, limit, offset)`**:
  - Executed on `db.Reader()`.
  - Queries requests with `status = 'dlq'`. If `routeID != ""`, filters by `route_id = ?`.
  - Executes count query:
    ```sql
    SELECT COUNT(*) FROM requests WHERE status = 'dlq' AND (? = '' OR route_id = ?);
    ```
  - Executes paginated select:
    ```sql
    SELECT id, route_id, method, url, headers, body, status, attempts_count, next_attempt_at, last_error, created_at, updated_at
    FROM requests
    WHERE status = 'dlq' AND (? = '' OR route_id = ?)
    ORDER BY created_at DESC
    LIMIT ? OFFSET ?;
    ```
- **`ReplayDLQRequest(ctx, id)`**:
  - Executed on `db.Writer()`.
  - Performs atomic conditional update:
    ```sql
    UPDATE requests
    SET status = 'pending',
        attempts_count = 0,
        next_attempt_at = ?,
        last_error = NULL,
        updated_at = ?
    WHERE id = ? AND status = 'dlq';
    ```
  - If `RowsAffected == 0`, checks if record exists to distinguish `storage.ErrNotFound` from `storage.ErrConflict` (request exists but not in `dlq` status).
  - Fetches and returns the updated `*model.Request`.
- **`ReplayAllDLQRequests(ctx, routeID, limit)`**:
  - Executed inside a transaction on `db.Writer()`.
  - Selects matching IDs:
    ```sql
    SELECT id FROM requests WHERE status = 'dlq' AND (? = '' OR route_id = ?) ORDER BY created_at ASC LIMIT ?;
    ```
  - If IDs found, executes batch UPDATE setting `status = 'pending'`, `attempts_count = 0`, `next_attempt_at = NOW()`, `last_error = NULL`.
  - Commits transaction and returns the slice of replayed IDs.

---

## 4. API HTTP Contracts

### 4.1 `GET /api/v1/dlq`
- **Method:** `GET`
- **Query Params:**
  - `route_name` *(string, optional)*: Filter by registered route name. Resolved to `route_id` via `RouteRegistry`. Returns `404 Not Found` if route does not exist.
  - `limit` *(int, optional, default: 50, max: 500)*.
  - `offset` *(int, optional, default: 0)*.
- **Response `200 OK`:**
  ```json
  {
    "items": [
      {
        "id": "01h2xcejqtf2nbrexx3vqjhp41",
        "route_id": "01h2xce1...",
        "method": "POST",
        "url": "https://api.internal/webhooks",
        "status": "dlq",
        "attempts_count": 5,
        "last_error": "503 Service Unavailable",
        "created_at": "2026-10-09T07:00:00Z",
        "updated_at": "2026-10-09T07:05:00Z"
      }
    ],
    "total": 1,
    "limit": 50,
    "offset": 0
  }
  ```

### 4.2 `POST /api/v1/dlq/{request_id}/replay`
- **Method:** `POST`
- **Path Param:** `request_id` (ULID string).
- **Behavior:**
  - Calls `RequestRepo.ReplayDLQRequest(ctx, id)`.
  - If not found: `404 Not Found` `{"error": "request not found"}`.
  - If not in `dlq`: `409 Conflict` `{"error": "request is not in dlq status"}`.
  - On success: Triggers `WakeFn()` and responds with `200 OK`.
- **Response `200 OK`:**
  ```json
  {
    "id": "01h2xcejqtf2nbrexx3vqjhp41",
    "status": "pending",
    "replayed": true,
    "replayed_at": "2026-10-09T07:45:00Z"
  }
  ```

### 4.3 `POST /api/v1/dlq/replay-all`
- **Method:** `POST`
- **Request Body (JSON, optional):**
  ```json
  {
    "route_name": "billing-webhook",
    "limit": 100
  }
  ```
- **Validation:**
  - `limit`: defaults to `100`, max `1000`.
  - If `route_name` is present, validates against `RouteRegistry`. Returns `404 Not Found` if missing.
- **Behavior:**
  - Calls `RequestRepo.ReplayAllDLQRequests(ctx, routeID, limit)`.
  - If replayed count > 0, triggers `WakeFn()`.
- **Response `200 OK`:**
  ```json
  {
    "replayed_count": 2,
    "request_ids": [
      "01h2xcejqtf2nbrexx3vqjhp41",
      "01h2xcejqtf2nbrexx3vqjhp42"
    ]
  }
  ```

---

## 5. Router & Dispatcher Integration

In `internal/api/router.go`:
```go
type RouterOptions struct {
    RouteRegistry *engine.RouteRegistry
    RelayEngine   *engine.RelayEngine
    BatchWriter   *engine.BatchWriter
    RequestRepo   storage.RequestRepository
    AttemptRepo   storage.AttemptRepository
    WakeFn        func()
    MaxBodyBytes  int64
}
```

In `cmd/relay/main.go`:
```go
handler := api.NewRouter(cfg, logger, api.RouterOptions{
    RouteRegistry: registry,
    RelayEngine:   relayEngine,
    BatchWriter:   batchWriter,
    RequestRepo:   db.RequestRepo,
    AttemptRepo:   db.AttemptRepo,
    WakeFn:        dispatcher.Wake,
})
```

---

## 6. Verification & Test Plan

1. **Storage Unit Tests (`internal/storage/sqlite/request_repo_test.go`):**
   - `TestListDLQRequests`: Pagination, total count, route filter.
   - `TestReplayDLQRequest`: Normal transition, non-existent record (`ErrNotFound`), non-DLQ record (`ErrConflict`).
   - `TestReplayAllDLQRequests`: Batch update limit clamping and route filtering.
2. **Handler Unit Tests (`internal/api/handler_dlq_test.go`):**
   - HTTP route matching, payload parsing, query params, error responses (400, 404, 409).
   - Mock verification that `WakeFn` is invoked when appropriate.
3. **End-to-End Test (`internal/api/e2e_dlq_replay_test.go`):**
   - Ingest request, exhaust retries until `dlq`.
   - Verify request in `GET /api/v1/dlq`.
   - Call `POST /api/v1/dlq/{id}/replay`.
   - Upstream server accepts retry, request reaches `status = 'success'`.
4. **Mandatory Build & Race Suite:**
   - `go test -v ./...`
   - `go test -race ./...`
   - `CGO_ENABLED=0 go build -o /dev/null ./cmd/relay`
