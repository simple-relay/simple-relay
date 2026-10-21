# Design Specification: Delivery Overview Metrics Cards & Dashboard Reorganization

- **Issue:** [#90](https://github.com/simple-relay/simple-relay/issues/90)
- **Date:** 2026-10-21
- **Status:** Approved

---

## 1. Overview & Problem Statement

Operators triaging delivery bottlenecks, retry spikes, and dead-letter queues in Simple Relay need immediate situational awareness upon opening the embedded dashboard. Currently:
1. `GetQueueStats` in [`internal/storage/sqlite/request_repo.go`](file:///workspace/simple-relay/internal/storage/sqlite/request_repo.go) only counts `pending` and `processing` requests (`status IN ('pending', 'processing')`), missing requests currently waiting in exponential backoff delay (`delayed`: `status = 'failed' AND next_attempt_at > now`) and dead-lettered requests (`status = 'dlq'`).
2. Synchronous relay requests that fail persist with `status = 'failed'` and `next_attempt_at = NULL`. The query must distinguish terminal failures from active retryable backlog.
3. The `GET /api/v1/queue/stats` endpoint returns an incomplete breakdown, omitting `delayed` and `dlq`.
4. The embedded web UI [`ui/index.html`](file:///workspace/simple-relay/ui/index.html) lacks an aggregated overview page or metric cards, forcing operators to jump between tabs and manually inspect tables.
5. The sidebar navigation contains non-realtime badge counters (`routes.length`, `reqTotal`, `dlqTotal`) that do not update dynamically, creating visual noise and false state impressions.
6. In the sidebar, `Routes` is placed at the very top, while operational workflow naturally prioritizes runtime telemetry (`Metrics`, `Requests`, `DLQ`) over configuration management (`Routes`, `API Keys`).

---

## 2. Goals, Non-Goals & Anti-Goals

### Goals
- **Single Round-Trip Storage Query:** Extend [`storage.RequestRepository`](file:///workspace/simple-relay/internal/storage/storage.go) to aggregate `pending` (ready to run immediately: `next_attempt_at <= now`), `in_flight` (currently processing), `delayed` (retry backoff: `next_attempt_at > now`), and `dlq` in a single SQL round-trip utilizing SQLite partial indexes.
- **Exclusion of Terminal Sync Failures:** Exclude completed terminal failures (`status = 'failed' AND next_attempt_at IS NULL`) from active `pending` queue depth to avoid false `BACKLOG` alerts.
- **Typed Model:** Introduce `model.QueueStats` in [`internal/model/request.go`](file:///workspace/simple-relay/internal/model/request.go) representing queue metrics and depth.
- **Backward-Compatible HTTP API:** Update `GET /api/v1/queue/stats` to return `pending`, `in_flight`, `delayed`, `dlq`, `queue_depth`, and `status`.
- **Consistent Queue Depth Definition:** Maintain `queue_depth = pending + in_flight` as executable pipeline workload.
- **Dedicated Metrics View:** Create a dedicated "Metrics" page in [`ui/index.html`](file:///workspace/simple-relay/ui/index.html) with overview metric cards and prepared layout for future real-time charts.
- **Interactive Card Navigation:** Clicking metric cards immediately switches to the corresponding tab with active filters (`pending` -> Requests with status `pending`, `in_flight` -> Requests with status `processing`, `delayed` -> Requests with status `failed`, `dlq` -> DLQ tab).
- **Sidebar Cleanup & Reordering:**
  - Order: `Metrics` -> `Requests` -> `Dead letter` -> `Routes` -> `API Keys`.
  - Strip static/non-realtime badge counters from all sidebar navigation items.

### Non-Goals
- **Prometheus Exporter Endpoint (`GET /metrics`):** Tracked separately in Phase 4 under Issue [#24](https://github.com/simple-relay/simple-relay/issues/24). This task focuses purely on the storage stats abstraction, JSON API, and embedded UI.
- **Metrics Time-Series Storage / Historical Rollups:** In-memory or database time-series retention is out of scope. The cards reflect instant state snapshots.

### Anti-Goals
- **No Full Table Scans:** Never run full table scans across millions of settled `success` rows. Utilize partial indexes (`idx_requests_dispatcher`, `idx_requests_processing`, `idx_requests_dlq`).
- **No External Dependencies:** No client-side graphing libraries (e.g., Chart.js, D3) or server-side metric dependencies. Keep binary statically compiled (`CGO_ENABLED=0`) with Alpine.js and Tailwind CSS.

---

## 3. Architecture & Data Flow

```text
  [Browser UI: Metrics Tab]  <--->  GET /api/v1/queue/stats  <--->  [HTTP Router]
  [CLI: simple-relay queue]                                                │
                                                                           ▼
                                                                [RequestRepository]
                                                                GetQueueStats(ctx)
                                                                           │
                                                                           ▼
                                                                  [SQLite Storage]
                                                            Single Round-Trip Query:
                                                            4 Scalar Subqueries using
                                                            Partial Indexes
```

---

## 4. Detailed Component Design

### 4.1. Domain Model ([`internal/model/request.go`](file:///workspace/simple-relay/internal/model/request.go))

Define `QueueStats`:
```go
// QueueStats represents aggregate counts of requests across delivery lifecycle states.
type QueueStats struct {
	Pending    int64 `json:"pending"`
	InFlight   int64 `json:"in_flight"`
	Delayed    int64 `json:"delayed"`
	DLQ        int64 `json:"dlq"`
	QueueDepth int64 `json:"queue_depth"`
}
```

### 4.2. Storage Interface ([`internal/storage/storage.go`](file:///workspace/simple-relay/internal/storage/storage.go))

Update `RequestRepository`:
```go
type RequestRepository interface {
	// ... existing methods ...
	GetQueueStats(ctx context.Context) (*model.QueueStats, error)
}
```

### 4.3. SQLite Storage Implementation & Indexing ([`internal/storage/sqlite/request_repo.go`](file:///workspace/simple-relay/internal/storage/sqlite/request_repo.go))

1. Ensure partial index on DLQ in `migrations.go`:
   ```sql
   CREATE INDEX IF NOT EXISTS idx_requests_dlq ON requests (status) WHERE status = 'dlq';
   ```
2. Single round-trip scalar subquery execution:
```go
func (r *RequestRepo) GetQueueStats(ctx context.Context) (*model.QueueStats, error) {
	now := time.Now().UTC()
	query := `
		SELECT
			(SELECT COUNT(*) FROM requests WHERE (status = 'pending' AND (next_attempt_at IS NULL OR next_attempt_at <= ?)) OR (status = 'failed' AND next_attempt_at IS NOT NULL AND next_attempt_at <= ?)),
			(SELECT COUNT(*) FROM requests WHERE status = 'processing'),
			(SELECT COUNT(*) FROM requests WHERE status = 'failed' AND next_attempt_at > ?),
			(SELECT COUNT(*) FROM requests WHERE status = 'dlq');
	`
	var stats model.QueueStats
	err := r.db.Reader().QueryRowContext(ctx, query, now, now, now).Scan(
		&stats.Pending,
		&stats.InFlight,
		&stats.Delayed,
		&stats.DLQ,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate queue stats: %w", err)
	}

	stats.QueueDepth = stats.Pending + stats.InFlight
	return &stats, nil
}
```
*Note on index efficiency:*
- Subquery 1 & 3 hit `idx_requests_dispatcher` (`status IN ('pending', 'failed')`).
- Subquery 2 hits `idx_requests_processing` (`status = 'processing'`).
- Subquery 4 hits `idx_requests_dlq` (`status = 'dlq'`).
- Historical `success` rows are never scanned.

### 4.4. HTTP Router Handler ([`internal/api/router.go`](file:///workspace/simple-relay/internal/api/router.go))

Update `GET /api/v1/queue/stats`:
```go
mux.HandleFunc("GET /api/v1/queue/stats", func(w http.ResponseWriter, r *http.Request) {
	stats, err := opt.RequestRepo.GetQueueStats(r.Context())
	if err != nil {
		logger.Error("failed to get queue stats", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to get queue stats"})
		return
	}
	status := "HEALTHY"
	if stats.QueueDepth >= 1000 {
		status = "BACKLOG"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"pending":     stats.Pending,
		"in_flight":   stats.InFlight,
		"delayed":     stats.Delayed,
		"dlq":         stats.DLQ,
		"queue_depth": stats.QueueDepth,
		"status":      status,
	})
})
```

### 4.5. CLI Client Compatibility ([`internal/cli/client.go`](file:///workspace/simple-relay/internal/cli/client.go))

Update `queueStatsResp` in `RelayClient.GetQueueStats`:
```go
type queueStatsResp struct {
	Pending    int64  `json:"pending"`
	InFlight   int64  `json:"in_flight"`
	Delayed    int64  `json:"delayed"`
	DLQ        int64  `json:"dlq"`
	QueueDepth int64  `json:"queue_depth"`
	Status     string `json:"status"`
}
```
Existing CLI commands (`simple-relay queue`) consume `Pending`, `InFlight`, and `QueueDepth` seamlessly.

### 4.6. Embedded Dashboard UI ([`ui/index.html`](file:///workspace/simple-relay/ui/index.html))

1. **Sidebar Navigation:**
   - Add `Metrics` nav button at the top (`setTab('metrics')`), default `currentTab: 'metrics'`.
   - Reorder items: `Metrics` -> `Requests` -> `Dead letter` -> `Routes` -> `API Keys`.
   - Remove `<span x-text="...">` counter badges from all nav items.
2. **Metrics Section (`x-show="currentTab === 'metrics'"`):**
   - Header with System Status Badge (`HEALTHY` in green, `BACKLOG` in red) and Manual Refresh button.
   - 4 Interactive Metric Cards:
     - **Pending:** Shows count, clock icon, subtext "Ready for delivery". Click -> `filterRequestsByStatus('pending')`.
     - **In-Flight:** Shows count, pulse icon, subtext "Actively running workers". Click -> `filterRequestsByStatus('processing')`.
     - **Retrying / Delayed:** Shows count, backoff icon, subtext "Awaiting retry backoff". Click -> `filterRequestsByStatus('failed')`.
     - **Dead Letter:** Shows count, warning icon, subtext "Max attempts exhausted". Click -> `setTab('dlq')`.
   - **Queue Depth Summary Card / Banner:** Highlights total `pending + in_flight`.
   - **Recent Activity Table:** Displays the 30 most recent requests across all routes with status badges, method, and timestamps. Clicking any request navigates to the Requests tab and opens the inspection drawer (`goToRequest(id)`).
3. **Alpine.js State & Functions:**
   - Add `queueStats` and `recentRequests: []`.
   - Add `fetchQueueStats()` method fetching stats and latest 30 requests via `Promise.all`.
   - Add `goToRequest(id)` helper: navigates to Requests tab and opens request inspection drawer.
   - Add helper `filterRequestsByStatus(status)`: sets `currentTab = 'requests'`, `reqFilter.status = status`, and triggers `fetchRequests(0)`.
   - In `init()` and periodic polling, invoke `fetchQueueStats()`.

---

## 5. Verification Plan

1. **Unit & Repository Tests:**
   - Test `GetQueueStats` in [`internal/storage/sqlite/request_repo_test.go`](file:///workspace/simple-relay/internal/storage/sqlite/request_repo_test.go) verifying counts for `pending`, `processing`, future `failed` (`delayed`), expired `failed` (`pending`), settled sync failures (`failed` with `next_attempt_at = nil`), and `dlq`.
   - Update engine mock repositories in [`internal/engine/`](file:///workspace/simple-relay/internal/engine/).
   - Update router tests in [`internal/api/router_test.go`](file:///workspace/simple-relay/internal/api/router_test.go).
   - Update CLI client tests in [`internal/cli/client_test.go`](file:///workspace/simple-relay/internal/cli/client_test.go).
2. **Command Verification:**
   - `go test -v ./...`
   - `go test -race ./...`
   - `CGO_ENABLED=0 go build -o /dev/null ./cmd/relay`
