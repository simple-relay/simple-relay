# Milestone 3.1: Embedded Dashboard Web UI — Design Specification

> **Status:** Approved  
> **Date:** 2026-10-09  
> **Milestone:** Phase 3 — Milestone 3.1: Embedded Dashboard Web UI  
> **Target Path:** `docs/specs/2026-10-09-embedded-dashboard-ui-design.md`  
> **Reference Architecture:** [architecture.md](../architecture.md)  
> **Reference Roadmap:** [roadmap.md](../roadmap.md)  

---

## 1. Executive Summary & Objective

Simple Relay is a single-binary, zero-infrastructure HTTP delivery and webhook reliability layer. To date, Phase 1 (Outbound Relay & Core Storage Engine) and Phase 2 (Inbound Webhook & Security Pipeline) have provided rock-solid synchronous/asynchronous proxying, signature verification, SQLite WAL persistence, and DLQ replay APIs.

Milestone 3.1 introduces the **Embedded Dashboard Web UI**, providing operators with an out-of-the-box, zero-dependency browser management interface bundled directly into the Go binary. Operators can visualize system traffic, configure routes dynamically with zero downtime, inspect requests and payload audit trails, and replay failed dead-lettered webhooks.

### Key Tenets
1. **Zero External Dependencies / 100% Offline:** Assets (Alpine.js and Tailwind CSS) are fully vendored in `ui/static/` and embedded via Go stdlib `embed.FS`. No Node.js build pipeline, no external CDN requests, and 100% air-gap compatibility.
2. **Layered SPA Architecture:** The interface is an Alpine.js Single Page Application communicating with standard REST JSON endpoints (`/api/v1/...`). It separates backend data operations from presentation.
3. **RAM & Storage Bloat Protection:** The Requests Explorer query API strictly loads request metadata and excludes heavy raw `body` BLOBs. Full payloads and attempt diagnostic logs are loaded lazily on demand when an operator clicks into a specific request.
4. **Phased Execution:** Decomposed into two distinct, independently verifiable sub-tasks:
   - **Sub-task 1:** Backend Admin APIs & Storage Query Layer (Routes CRUD, Requests Explorer filtering).
   - **Sub-task 2:** Frontend Embedded Assets & Alpine.js Dashboard SPA (Base layout, Route modal, Request drawer, DLQ table).

---

## 2. Architecture & Data Flow

### 2.1 Component Interaction Diagram

```text
Operator Browser (Alpine.js SPA)
   │
   ├── [GET /ui/static/*, GET /] ────────► embed.FS (Embedded HTML, CSS, JS)
   │
   ├── [GET/POST/PUT/DELETE /api/v1/routes] ──► Routes Admin API ──► RouteRepo (SQLite)
   │                                                                └──► RouteRegistry (Hot-Reload)
   │
   ├── [GET /api/v1/requests?status=...] ──► Requests Query API ──► RequestRepo.ListRequests
   │                                                               (Metadata only, NO BLOBs)
   │
   ├── [GET /api/v1/jobs/{id}] ──────────► Job Inspect API ────► RequestRepo + AttemptRepo
   │                                                           (Full Headers, Raw Body, Attempts)
   │
   └── [POST /api/v1/dlq/.../replay] ────► DLQ Replay API ─────► RequestRepo + Dispatcher Wakeup
```

### 2.2 Serving Lifecycle
* The Go standard library `net/http` ServeMux routes `GET /ui/static/` to `http.FileServerFS(ui.FS)`.
* Requests to `GET /` and `GET /ui` serve `ui/index.html` with `Content-Type: text/html; charset=utf-8`.
* All state mutations through the UI invoke REST endpoints under `/api/v1/`.

---

## 3. Sub-task 1: Backend Admin APIs & Storage Engine

Sub-task 1 implements the missing REST APIs and storage repository queries required to power the Web UI.

### 3.1 Storage Layer Enhancements (`internal/storage`)

#### 3.1.1 Request Metadata Entity (`internal/model/request.go`)
To prevent memory bloat and SQLite I/O bottlenecks when listing hundreds of requests, a dedicated lightweight projection `RequestMetadata` is introduced:

```go
// RequestMetadata contains high-level request information without heavy payload BLOBs.
type RequestMetadata struct {
	ID            string        `json:"id"`
	RouteID       string        `json:"route_id"`
	RouteName     string        `json:"route_name,omitempty"`
	Method        string        `json:"method"`
	URL           string        `json:"url"`
	Status        RequestStatus `json:"status"`
	AttemptsCount int           `json:"attempts_count"`
	NextAttemptAt *time.Time    `json:"next_attempt_at,omitempty"`
	LastError     *string       `json:"last_error,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}
```

#### 3.1.2 Storage Interface Update (`internal/storage/storage.go`)
Extend `RequestRepository` with `ListRequests`:

```go
type RequestFilter struct {
	Status  string // Optional: 'pending', 'processing', 'success', 'failed', 'dlq'
	RouteID string // Optional: filter by specific route ID
	Search  string // Optional: ID prefix search
	Limit   int    // Default 50, maximum 100
	Offset  int    // Pagination offset
}

type RequestRepository interface {
	// ... existing methods ...
	ListRequests(ctx context.Context, filter RequestFilter) ([]*model.RequestMetadata, int64, error)
}
```

#### 3.1.3 SQLite Implementation (`internal/storage/sqlite/request_repo.go`)
* Execute parameterized query selecting ONLY metadata fields:
  ```sql
  SELECT r.id, r.route_id, r.method, r.url, r.status, r.attempts_count,
         r.next_attempt_at, r.last_error, r.created_at, r.updated_at
  FROM requests r
  WHERE (? = '' OR r.status = ?)
    AND (? = '' OR r.route_id = ?)
    AND (? = '' OR r.id LIKE ? || '%')
  ORDER BY r.created_at DESC
  LIMIT ? OFFSET ?;
  ```
* Accompanying `COUNT(*)` query for pagination calculation:
  ```sql
  SELECT COUNT(*) FROM requests r
  WHERE (? = '' OR r.status = ?)
    AND (? = '' OR r.route_id = ?)
    AND (? = '' OR r.id LIKE ? || '%');
  ```

---

### 3.2 Routes CRUD Handlers (`internal/api/handler_routes.go`)

Handles programmatic and UI administration of routing configurations.

#### Endpoints
1. `GET /api/v1/routes`: Returns a JSON array of all configured `Route` records (`200 OK`).
2. `POST /api/v1/routes`: Creates a new `Route`.
   - **Validation:**
     - `name`: Non-empty, slug format (`^[a-zA-Z0-9_-]+$`).
     - `type`: Must be `inbound` or `outbound`.
     - `mode`: Must be `sync` or `async` (defaults to `async`).
     - `path`: Non-empty, must start with `/`.
     - `destination_url`: Valid absolute HTTP/HTTPS URL.
     - `signature_provider`: One of `none`, `stripe`, `github`, `hmac_sha256`.
     - `max_attempts`: Positive integer $\ge 1$ (defaults to 5).
     - `timeout_ms`: Positive integer $\ge 100$ (defaults to 10000).
   - **Persistence & Hot-Reload:**
     - Calls `RouteRepo.CreateRoute(ctx, route)`.
     - On duplicate name or path, returns `409 Conflict`.
     - On success, invokes `RouteRegistry.Reload(ctx)` to atomically refresh the in-memory cache without restarting the service.
     - Returns `201 Created` with created route JSON.
3. `GET /api/v1/routes/{id}`: Returns the single `Route` JSON (`200 OK`) or `404 Not Found`.
4. `PUT /api/v1/routes/{id}`: Updates an existing route.
   - Validates fields, updates record via `RouteRepo.UpdateRoute(ctx, route)`.
   - Invokes `RouteRegistry.Reload(ctx)`.
   - Returns `200 OK` with updated route JSON.
5. `DELETE /api/v1/routes/{id}`: Deletes route by ID.
   - Deletes via `RouteRepo.DeleteRoute(ctx, id)`.
   - Invokes `RouteRegistry.Reload(ctx)`.
   - Returns `200 OK` (or `204 No Content`).

---

### 3.3 Requests Query Handler (`internal/api/handler_requests.go`)

#### Endpoint: `GET /api/v1/requests`
* **Query Parameters:**
  - `status`: String filter (`pending`, `processing`, `success`, `failed`, `dlq`).
  - `route_id`: Route ULID filter.
  - `search`: Request ID substring / prefix.
  - `limit`: Integer (default 50, clamp to 100 max).
  - `offset`: Integer (default 0).
* **Response:**
  ```json
  {
    "items": [
      {
        "id": "01j9y9b3c9g8m4q7v1x2k5p8r0",
        "route_id": "01j9y9a1f2e3d4c5b6a7987654",
        "route_name": "stripe-payments",
        "method": "POST",
        "url": "/api/v1/webhook/stripe",
        "status": "success",
        "attempts_count": 1,
        "next_attempt_at": null,
        "last_error": null,
        "created_at": "2026-10-09T08:30:00Z",
        "updated_at": "2026-10-09T08:30:01Z"
      }
    ],
    "total": 420,
    "limit": 50,
    "offset": 0
  }
  ```

#### Endpoint: `GET /api/v1/requests/{id}`
* Alias handler mapping directly to existing `JobInspectHandler` (`GET /api/v1/jobs/{job_id}`).
* Returns complete payload details:
  - Exact `headers` (JSON-parsed).
  - Exact raw `body` (decoded string).
  - Complete list of `attempts` with duration, status code, error, and truncated response body (32 KB max).

---

## 4. Sub-task 2: Frontend Embedded Assets & Alpine.js Web UI

Sub-task 2 delivers the user interface embedded within the binary.

### 4.1 Asset Structure & Embedding (`ui/`)

```text
ui/
├── embed.go                  # Go package ui exposing `var FS embed.FS`
├── index.html                # Single Page Application HTML shell
└── static/
    ├── css/
    │   └── tailwind.min.css  # Standalone precompiled Tailwind CSS bundle
    └── js/
        └── alpine.min.js     # Standalone vendored Alpine.js 3.x
```

#### `ui/embed.go` Specification
```go
package ui

import "embed"

//go:embed all:static index.html
var FS embed.FS
```

---

### 4.2 Application Shell & Layout

The UI uses a **Modern Sidebar Navigation** layout:
* **Sidebar (Fixed Left, 260px):**
  * Brand Header: "Simple Relay" with glowing green status dot (`Healthy`).
  * Navigation Tabs:
    1. **Routes (`routes`)**: Icon ⚡, badge displaying total routes count.
    2. **Requests Explorer (`requests`)**: Icon 📋, showing recent requests.
    3. **Dead Letter Queue (`dlq`)**: Icon 💀, badge displaying failed count (colored red when $> 0$).
  * Footer: Version info and link to docs.
* **Main Content Area (Fluid Right):**
  * Dynamic view swapping based on active tab `currentTab` in Alpine.js.

---

### 4.3 View Specifications

#### View 1: Routes Management (`currentTab === 'routes'`)
* **Header:** Title "Routes Configuration" with "+ Create Route" button.
* **Table:**
  - Columns: Name, Type (Badge: Green `Inbound`, Blue `Outbound`), Path, Destination URL, Signature Provider, Max Retries, Timeout, Actions.
  - Action buttons: `Edit` (opens Edit Modal), `Delete` (opens confirmation dialog).
* **Create/Edit Modal:**
  - Form fields: Name, Type (`inbound`/`outbound`), Mode (`async`/`sync`), Path (`/api/v1/...`), Destination URL, Signature Provider (`none`, `stripe`, `github`, `hmac_sha256`), Secret, Max Attempts, Timeout (ms).
  - Real-time client-side validation before sending `POST` or `PUT` to `/api/v1/routes`.
  - On submit success: Modal closes, toast notification displays "Route saved successfully", and list refreshes.

#### View 2: Requests Explorer (`currentTab === 'requests'`)
* **Filter Bar:**
  - Status Filter: Select (`All`, `pending`, `processing`, `success`, `failed`, `dlq`).
  - Route Filter: Select matching available routes.
  - Search Input: Text input for Request ID lookup.
  - Refresh Button: Manually re-fetches data.
* **Table:**
  - Columns: Request ID (truncated with copy button), Route, Method, Status (Pill badges: Green `success`, Red `failed`, Yellow `processing`, Gray `pending`, Purple `dlq`), Attempts, Created At, Action ("Inspect").
* **Slide-over Drawer (Off-Canvas Right Drawer, 640px width):**
  - Smooth slide-in transition triggered on row click.
  - Header: Request ID, Status badge, Close button.
  - Section 1: Overview (Route, URL, Method, Timestamp).
  - Section 2: Request Headers (formatted JSON viewer).
  - Section 3: Raw Request Body (scrollable code view with "Copy Body" button).
  - Section 4: Attempt History Timeline:
    - Attempt number, timestamp, duration in ms.
    - HTTP status code (e.g., `200 OK`, `502 Bad Gateway`).
    - Error message (if connection or DNS error occurred).
    - Response body preview (truncated 32 KB).

#### View 3: Dead Letter Queue (DLQ) (`currentTab === 'dlq'`)
* **Header & Actions:**
  - Header: "Dead Letter Queue" with count indicator.
  - Action: "Replay All" button with confirmation prompt.
* **Table:**
  - Columns: Request ID, Route, Method, Final Failure Reason, Attempts Count, Dead-lettered At, Actions.
  - Individual "Replay" action button per row.
* **Feedback:**
  - Displays instant loading spinner on replay action.
  - Refreshes DLQ table and updates DLQ badge count immediately upon success.

---

## 5. Security & Error Handling

### 5.1 Error Handling & User Feedback
* **Standardized JSON API Errors:**
  ```json
  { "error": "route with path '/api/v1/webhook/stripe' already exists" }
  ```
* **Toast Notification System:**
  - A lightweight Alpine.js toast notification store handles feedback for API mutations (Success, Warning, Error).
* **Defensive Input Handling:**
  - Frontend form prevents submission with invalid paths, URLs, or negative numbers before hitting backend validation.

### 5.2 Security Boundaries
* All user-supplied inputs rendered in HTML are escaped via Alpine.js `x-text` or standard DOM encoding to prevent XSS.
* In Milestone 3.1, the UI and APIs operate in open management mode (assuming local / trusted network deployment).
* Milestone 3.2 will introduce `RELAY_ADMIN_PASSWORD` session authentication, login rate limiting, and CSRF token verification across these endpoints without modifying the underlying UI data structures.

---

## 6. Verification & Testing Strategy

### 6.1 Backend Test Coverage
1. **Repository Tests (`internal/storage/sqlite/request_repo_test.go`):**
   - Verify `ListRequests` with various filter combinations (`status`, `route_id`, `search`, pagination).
   - Ensure the query returns accurate `total` count.
   - Assert that no `body` BLOB bytes are read during list queries.
2. **Route API Tests (`internal/api/handler_routes_test.go`):**
   - Test `POST /api/v1/routes` with valid and invalid payloads (missing name, invalid path, negative timeout).
   - Test conflict detection on duplicate name/path (`409 Conflict`).
   - Test `GET`, `PUT`, and `DELETE` on `/api/v1/routes/{id}`.
   - Verify that `RouteRegistry` in-memory lookup cache reflects updates immediately without restarting.
3. **Requests API Tests (`internal/api/handler_requests_test.go`):**
   - Test `GET /api/v1/requests` pagination, filtering by status, and ID prefix search.
   - Test `GET /api/v1/requests/{id}` returning full payload and attempt history.

### 6.2 Frontend Embedding & Routing Tests
1. **Asset Embedding Test (`ui/embed_test.go`):**
   - Assert `ui.FS` contains `index.html`, `static/css/tailwind.min.css`, and `static/js/alpine.min.js`.
   - Verify files are non-empty.
2. **Router Static Serving Test (`internal/api/router_test.go`):**
   - `GET /`: returns `200 OK` with `Content-Type: text/html; charset=utf-8`.
   - `GET /ui`: returns `200 OK` with HTML content.
   - `GET /ui/static/css/tailwind.min.css`: returns `200 OK` with `Content-Type: text/css`.
   - `GET /ui/static/js/alpine.min.js`: returns `200 OK` with `Content-Type: application/javascript`.

### 6.3 Mandatory Quality Gates
All verification commands must pass cleanly:
```bash
go test -v ./...
go test -race ./...
CGO_ENABLED=0 go build -o /dev/null ./cmd/relay
```

---

## 7. Phased Implementation Plan

```text
┌─────────────────────────────────────────────────────────────┐
│ Sub-task 1: Backend Admin APIs & Storage Query Layer        │
│ 1. Add model.RequestMetadata & RequestRepository.ListRequests│
│ 2. Implement SQLite ListRequests with metadata projection   │
│ 3. Implement Routes CRUD Handler & RouteRegistry reload     │
│ 4. Implement Requests Explorer API Handler & Router mounting│
│ 5. Unit & Integration test suite for all new endpoints     │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│ Sub-task 2: Embedded Assets & Alpine.js Dashboard SPA       │
│ 1. Vendor standalone Tailwind CSS & Alpine.js in ui/static/ │
│ 2. Create ui/embed.go and wire FileServerFS in router       │
│ 3. Implement index.html SPA layout with Sidebar navigation  │
│ 4. Build Routes Management view & modal forms               │
│ 5. Build Requests Explorer view & slide-over detail drawer  │
│ 6. Build Dead Letter Queue view & one-click replay actions  │
│ 7. E2E verification & race condition checks                 │
└─────────────────────────────────────────────────────────────┘
```
