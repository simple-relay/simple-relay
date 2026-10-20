# Design Specification: Decouple Async vs Sync Retry Backoff Windows, Per-Route Retry Overrides & SQLite Synchronous Mode

- **Issue:** [#83](https://github.com/simple-relay/simple-relay/issues/83)
- **Date:** 2026-10-20
- **Status:** Approved

---

## 1. Overview & Problem Statement

Currently, Simple Relay's delivery engine and SQLite storage configuration have three operational limitations in production environments:

1. **Shared Short Retry Window Across Sync & Async Paths:** `RunServer` (`internal/cli/serve.go`) wires a single `engine.NewRetryEngine()` (`DefaultBaseBackoff = 100ms`, `DefaultMaxBackoff = 10s`) into both `RelayEngine` (synchronous proxy) and `WorkerPool` (asynchronous webhook/job delivery). While a `100ms`–`10s` window is ideal for keeping open client HTTP connections responsive on `/api/v1/relay/{route_name}`, it causes asynchronous jobs with `max_attempts = 5` to exhaust all retries and land in `dlq` in under 30 seconds—too short to survive a standard 1–2 minute downstream container deployment or database restart.
2. **Lack of Per-Route Backoff Tuning & Non-Idempotent Sync Retry Guardrails:** `model.Route` only exposes `max_attempts`, `timeout_ms`, and `retry_backoff_strategy`. Operators cannot customize `base_backoff_ms` or `max_backoff_ms` per route. Furthermore, `RelayEngine.RelaySync` retries failed `POST` and `PATCH` requests unconditionally up to `max_attempts` even when the caller did not supply an `Idempotency-Key`, risking duplicate side effects (e.g., double charges) if an upstream times out after committing a transaction.
3. **Hardcoded SQLite `synchronous = NORMAL` & Unwired `RELAY_MAX_BODY_SIZE_MB`:** `sqlite.Open` hardcodes `PRAGMA synchronous = NORMAL;`, which is crash-safe across process failures (`SIGKILL`) but can lose uncommitted WAL frames on an OS kernel panic or power loss on single-node bare-metal hosts without UPS/Litestream. Additionally, `RELAY_MAX_BODY_SIZE_MB` is documented in `docs/architecture.md` but not exposed in `internal/config/config.go` or wired into `api.RouterOptions`.

---

## 2. Goals & Non-Goals

### Goals
- **Decoupled Default Retry Profiles:** Keep fast defaults (`100ms` base, `10s` max, `30s` max `Retry-After`) for synchronous `RelayEngine` and introduce a dedicated asynchronous retry profile (`5s` base, `15m` max, `15m` max `Retry-After`) for `WorkerPool`.
- **Per-Route Retry Timing Overrides:** Add optional `base_backoff_ms` and `max_backoff_ms` fields (`0` = use engine profile defaults) to `model.Route`, SQLite `routes` table, REST API, and Web UI route creation/editing drawer (with mode-aware UI pre-filling).
- **Idempotency-Aware Sync Proxy Retries (`retry_non_idempotent`):** Default `retry_non_idempotent` to `false` on `model.Route`. In `RelaySync`, always allow retries for idempotent HTTP methods (`GET`, `HEAD`, `PUT`, `DELETE`, `OPTIONS`, `TRACE`), and only retry non-idempotent methods (`POST`, `PATCH`, etc.) when either `route.RetryNonIdempotent == true` OR the request carries an `Idempotency-Key` / `X-Relay-Idempotency-Key` header.
- **Configurable SQLite Durability (`RELAY_SQLITE_SYNC`) & Body Size Limit (`RELAY_MAX_BODY_SIZE_MB`):** Expose `RELAY_SQLITE_SYNC` (`NORMAL` | `FULL`, default `NORMAL`) and `RELAY_MAX_BODY_SIZE_MB` (default `10`) via environment variables and CLI flags, pass `syncMode` explicitly to `sqlite.Open(dsn, syncMode)`, and wire `MaxBodyBytes` into `api.RouterOptions`.

### Non-Goals
- **Global Environment Variables for Backoff Timing:** Engine defaults (`100ms`/`10s` sync, `5s`/`15m` async) and per-route overrides (`base_backoff_ms`, `max_backoff_ms`) cover all tuning needs without adding extra global env vars.
- **Changing Asynchronous Retry Method Semantics:** Asynchronous webhooks (`/api/v1/webhook/{route_name}`) and jobs (`/api/v1/jobs/{route_name}`) are fire-and-forget at-least-once queues where `POST` is the primary verb; `WorkerPool` always retries up to `route.MaxAttempts` regardless of HTTP method.
- **Supporting `PRAGMA synchronous = OFF`:** Disallowing `OFF` / `0` preserves Simple Relay's Durability Golden Rule.

---

## 3. Architecture & Data Flow

```text
                       ┌─────────────────────────────────────────────┐
                       │                 model.Route                 │
                       │  • base_backoff_ms (0 = engine default)     │
                       │  • max_backoff_ms  (0 = engine default)     │
                       │  • retry_non_idempotent (default: false)    │
                       └──────────────────────┬──────────────────────┘
                                              │
                     ┌────────────────────────┴────────────────────────┐
                     ▼                                                 ▼
      ┌──────────────────────────────┐                  ┌──────────────────────────────┐
      │   Sync Proxy (RelayEngine)   │                  │   Async Queue (WorkerPool)   │
      │  Default Base: 100ms         │                  │  Default Base: 5s            │
      │  Default Max:  10s           │                  │  Default Max:  15m (900s)    │
      │                              │                  │                              │
      │  POST/PATCH Retry Gate:      │                  │  Always retries up to        │
      │  • Idempotency-Key present?  │                  │  route.MaxAttempts with      │
      │  • OR retry_non_idempotent?  │                  │  Full Jitter backoff         │
      │  If neither -> maxAttempts=1 │                  │                              │
      └──────────────────────────────┘                  └──────────────────────────────┘
```

---

## 4. Detailed Component Design

### 4.1. Retry Engine Profiles & Route Overrides (`internal/engine/retry.go`)

- **Default Constants:**
  ```go
  const (
      DefaultBaseBackoff        = 100 * time.Millisecond
      DefaultMaxBackoff         = 10 * time.Second
      DefaultMaxRetryAfter      = 30 * time.Second

      DefaultAsyncBaseBackoff   = 5 * time.Second
      DefaultAsyncMaxBackoff    = 15 * time.Minute
      DefaultAsyncMaxRetryAfter = 15 * time.Minute
  )
  ```
- **Helper Constructor for Async Worker Pool:**
  - `NewAsyncRetryEngine()` returns `NewRetryEngine(RetryConfig{BaseBackoff: DefaultAsyncBaseBackoff, MaxBackoff: DefaultAsyncMaxBackoff, MaxRetryAfter: DefaultAsyncMaxRetryAfter})`.
- **Per-Route Backoff Calculation (`CalculateBackoffWithOverrides`):**
  - Add `CalculateBackoffWithOverrides(attempt int, strategy model.RetryStrategy, retryAfterHeader string, baseBackoffMS, maxBackoffMS int) time.Duration`.
  - Existing `CalculateBackoff(attempt, strategy, retryAfterHeader)` delegates to `CalculateBackoffWithOverrides(attempt, strategy, retryAfterHeader, 0, 0)`.
  - Override resolution logic:
    1. `baseBackoff := e.config.BaseBackoff`; if `baseBackoffMS > 0`, `baseBackoff = time.Duration(baseBackoffMS) * time.Millisecond`.
    2. `maxBackoff := e.config.MaxBackoff`; if `maxBackoffMS > 0`, `maxBackoff = time.Duration(maxBackoffMS) * time.Millisecond`.
    3. If `baseBackoff > maxBackoff` (e.g., when only `base_backoff_ms` is overridden above the engine's default `MaxBackoff`), clamp `maxBackoff = baseBackoff`.
    4. `maxRetryAfter := e.config.MaxRetryAfter`; if `maxBackoff > maxRetryAfter`, set `maxRetryAfter = maxBackoff`.
    5. Compute exponential (`baseBackoff * 2^(attempt-1)`) or linear (`baseBackoff * attempt`) ceiling capped at `maxBackoff`, then apply Full Jitter in `[0, maxWait]`.

### 4.2. Domain Model & Validation (`internal/model/route.go`)

- Extend `model.Route`:
  ```go
  BaseBackoffMS      int  `json:"base_backoff_ms"`
  MaxBackoffMS       int  `json:"max_backoff_ms"`
  RetryNonIdempotent bool `json:"retry_non_idempotent"`
  ```
- Validation rules in `Route.Validate()`:
  - `ErrRouteInvalidBaseBackoff = errors.New("base_backoff_ms must be greater than or equal to 0")`
  - `ErrRouteInvalidMaxBackoff = errors.New("max_backoff_ms must be greater than or equal to 0")`
  - `ErrRouteBackoffRange = errors.New("base_backoff_ms cannot be greater than max_backoff_ms")`
  - If `r.BaseBackoffMS < 0` -> return `ErrRouteInvalidBaseBackoff`.
  - If `r.MaxBackoffMS < 0` -> return `ErrRouteInvalidMaxBackoff`.
  - If `r.BaseBackoffMS > 0 && r.MaxBackoffMS > 0 && r.BaseBackoffMS > r.MaxBackoffMS` -> return `ErrRouteBackoffRange`.

### 4.3. SQLite Schema Migration & Route Repository (`internal/storage/sqlite/`)

- **`migrations.go` (`schemaDDL` & `Migrate`):**
  - Include in `CREATE TABLE IF NOT EXISTS routes`:
    ```sql
    base_backoff_ms INTEGER NOT NULL DEFAULT 0,
    max_backoff_ms INTEGER NOT NULL DEFAULT 0,
    retry_non_idempotent INTEGER NOT NULL DEFAULT 0,
    ```
  - In `db.Migrate(ctx)`, inspect `pragma_table_info('routes')` and execute `ALTER TABLE routes ADD COLUMN ...` for `base_backoff_ms`, `max_backoff_ms`, and `retry_non_idempotent` if missing on existing databases.
- **`route_repo.go`:**
  - Persist and scan `BaseBackoffMS`, `MaxBackoffMS`, and `RetryNonIdempotent` (stored as integer `0`/`1` in SQLite) across `CreateRoute`, `GetRouteByID`, `GetRouteByName`, `GetRouteByPath`, `ListRoutes`, and `UpdateRoute`.

### 4.4. Sync Proxy Non-Idempotent Guard & Backoff Wiring (`internal/engine/relay.go` & `worker_pool.go`)

- **Idempotency Gate in `RelayEngine.RelaySync`:**
  - Define `isIdempotentMethod(method string) bool`: returns `true` for `GET`, `HEAD`, `PUT`, `DELETE`, `OPTIONS`, `TRACE` (case-insensitive), and `false` for `POST`, `PATCH`, and any other method.
  - Determine effective `maxAttempts`:
    ```go
    maxAttempts := route.MaxAttempts
    if maxAttempts <= 0 {
        maxAttempts = 1
    }
    hasIdempotencyKey := strings.TrimSpace(headers.Get("Idempotency-Key")) != "" ||
        strings.TrimSpace(headers.Get("X-Relay-Idempotency-Key")) != ""
    if !isIdempotentMethod(method) && !route.RetryNonIdempotent && !hasIdempotencyKey {
        maxAttempts = 1
    }
    ```
  - Use `e.retryEngine.CalculateBackoffWithOverrides(attempt, route.RetryBackoffStrategy, retryAfterHeader, route.BaseBackoffMS, route.MaxBackoffMS)` for retry sleeps.
- **Async Backoff in `NewWorkerPool` & `WorkerPool.processJob` (`internal/engine/worker_pool.go`):**
  - In `NewWorkerPool(...)`, if `retryEngine == nil`, initialize with `NewAsyncRetryEngine()` (matching the nil-safe constructor pattern in `NewRelayEngine`).
  - Use `wp.retryEngine.CalculateBackoffWithOverrides(newAttempts, route.RetryBackoffStrategy, retryAfterHeader, route.BaseBackoffMS, route.MaxBackoffMS)` when scheduling `nextAttemptAt`.

### 4.5. Web UI Route Form Pre-fill & Controls (`ui/index.html`)

- In the Route Drawer (`Retry & Timeout Policy` section):
  - Add numeric inputs for `Base Backoff (ms)` (`routeForm.base_backoff_ms`, `min="0"`) and `Max Backoff (ms)` (`routeForm.max_backoff_ms`, `min="0"`).
  - When `routeForm.type === 'outbound' && routeForm.mode === 'sync'`, display a checkbox toggle for `Retry Non-Idempotent Requests (POST / PATCH without Idempotency-Key)` bound to `routeForm.retry_non_idempotent`.
- **Smart Default Pre-filling on Create:**
  - In `openCreateRouteDrawer()` (default `type: 'inbound'`, `mode: 'async'`), pre-fill `base_backoff_ms: 5000`, `max_backoff_ms: 900000`, `retry_non_idempotent: false`.
  - In `onRouteTypeChange()` and `onRouteModeChange()`, when `!this.isEditingRoute`:
    - If `this.routeForm.mode === 'sync'`, set `base_backoff_ms = 100` and `max_backoff_ms = 10000`.
    - If `this.routeForm.mode === 'async'`, set `base_backoff_ms = 5000` and `max_backoff_ms = 900000`.
- **Payload Coercion on Save (`saveRoute()`):**
  - Coerce `payload.base_backoff_ms = Number(payload.base_backoff_ms) || 0`, `payload.max_backoff_ms = Number(payload.max_backoff_ms) || 0`, and `payload.retry_non_idempotent = !!payload.retry_non_idempotent` before `JSON.stringify` so cleared numeric inputs in Alpine.js never serialize as `""` or `null` against Go's strict `int` JSON decoding.
- **Routes Table Policy Column:**
  - Display effective backoff window cleanly alongside `max_attempts` and `timeout_ms` (falling back to engine defaults `100ms–10s` for sync and `5s–15m` for async when `base_backoff_ms` / `max_backoff_ms` are `0`).

### 4.6. SQLite Synchronous Mode & Max Body Size Config (`internal/config/`, `internal/storage/sqlite/`, `internal/cli/`)

- **`internal/config/config.go`:**
  - Add fields to `Config`:
    - `SQLiteSync string` (default `"NORMAL"`, env: `RELAY_SQLITE_SYNC`, flag: `-sqlite-sync`)
    - `MaxBodySizeMB int` (default `10`, env: `RELAY_MAX_BODY_SIZE_MB`, flag: `-max-body-size-mb`)
  - Bind `-sqlite-sync` and `-max-body-size-mb` in `config.Load()`'s `flag.NewFlagSet`.
  - In `loadFromEnv()`:
    - Read `RELAY_SQLITE_SYNC`: if non-empty, set `c.SQLiteSync = strings.ToUpper(strings.TrimSpace(val))`.
    - Read `RELAY_MAX_BODY_SIZE_MB`: if non-empty, parse integer; if `strconv.Atoi` fails or `<= 0`, set `c.MaxBodySizeMB = -1` so `Validate()` fails fast.
  - In `Validate()`:
    - Normalize `c.SQLiteSync = strings.ToUpper(strings.TrimSpace(c.SQLiteSync))`; if `""`, set to `"NORMAL"`.
    - Reject any value other than `"NORMAL"` or `"FULL"` with an explicit error.
    - Reject `c.MaxBodySizeMB <= 0` with an explicit error.
- **`internal/storage/sqlite/sqlite.go`:**
  - Update signature to `func Open(dsn string, syncMode string) (*DB, error)`.
  - Normalize `mode := strings.ToUpper(strings.TrimSpace(syncMode))`; if `mode == ""`, default to `"NORMAL"`.
  - Validate `mode == "NORMAL" || mode == "FULL"` before executing `fmt.Sprintf("PRAGMA synchronous = %s;", mode)` on `db.writer`; return an error on any other value.
  - Update all 20 callers of `sqlite.Open` across `internal/` and test files to pass the second argument (`cfg.SQLiteSync` in `serve.go`, `"NORMAL"` in tests).
- **`internal/cli/root.go` & `internal/cli/serve.go`:**
  - Register `"sqlite-sync": true` and `"max-body-size-mb": true` in `knownServerFlags` (`internal/cli/root.go`).
  - Add `--sqlite-sync` and `--max-body-size-mb` to `ServerOptions`, `addServerFlags`, and `runServe` flag overrides (applied before `cfg.Validate()`) in `internal/cli/serve.go`.
  - In `RunServer` (`internal/cli/serve.go`):
    - Call `sqlite.Open(cfg.DBPath, cfg.SQLiteSync)`.
    - Wire `syncRetryEngine := engine.NewRetryEngine()` into `RelayEngine` and `asyncRetryEngine := engine.NewAsyncRetryEngine()` into `WorkerPool`.
    - Pass `MaxBodyBytes: int64(cfg.MaxBodySizeMB) * 1024 * 1024` into `api.RouterOptions`.

### 4.7. Documentation Synchronization Checklist

#### A. Main Repository (`/workspace/pmlong-simple-relay`)
- **`docs/architecture.md`:**
  - Section 1 (Technical Stack — Database & Storage Adapter): Note configurable `PRAGMA synchronous` (`NORMAL` default, `FULL` optional via `RELAY_SQLITE_SYNC`).
  - Section 2 (Outbound HTTP Relay — Sync Proxy Mode): Note idempotency-aware retry gate for non-idempotent methods (`POST`, `PATCH`).
  - Section 6.1 (`routes` Table Schema): Add `validation_rules`, `base_backoff_ms`, `max_backoff_ms`, and `retry_non_idempotent` columns to match `migrations.go`.
  - Section 7 (Application Startup Configuration): Add `RELAY_SQLITE_SYNC` (`NORMAL` | `FULL`, default `NORMAL`) to the environment variable table.
- **`docs/project-summary.md`:**
  - Section 5.3 (Method-aware retry & Idempotency): Align non-idempotent method list to `(POST, PATCH)` per RFC 9110.
- **`docs/roadmap.md`:**
  - Record completion of Issue [#83](https://github.com/simple-relay/simple-relay/issues/83) and link to `docs/specs/2026-10-20-retry-backoff-profiles-and-sqlite-sync-design.md`.

#### B. Documentation Site Repository (`/workspace/pmlong-simplerelay.dev`)
- **`src/content/docs/configuration.md`:**
  - Add `--sqlite-sync`, `-sqlite-sync` (`RELAY_SQLITE_SYNC`, default `NORMAL`, valid `NORMAL` | `FULL`) and `--max-body-size-mb`, `-max-body-size-mb` (`RELAY_MAX_BODY_SIZE_MB`, default `10`) to the Server Daemon Settings Matrix.
  - Update the Dynamic Route Configuration (`POST /api/v1/routes`) example to include `base_backoff_ms`, `max_backoff_ms`, and `retry_non_idempotent`.
- **`src/content/docs/retries-and-dlq.md`:**
  - Document the decoupled default retry profiles (`100ms` base / `10s` max for synchronous proxy vs `5s` base / `15m` max for asynchronous worker pool) and replace stale `initial_interval_ms` / `max_interval_ms` field names with `base_backoff_ms` and `max_backoff_ms`.
- **`src/content/docs/outbound-proxy.md`:**
  - Document idempotency-aware synchronous retries (`POST` and `PATCH` require `Idempotency-Key` / `X-Relay-Idempotency-Key` or `retry_non_idempotent: true` on the route to retry).
- **`src/content/docs/api-reference.md`:**
  - Update `/api/v1/relay/:route_name` documentation with non-idempotent retry protection details and `RELAY_MAX_BODY_SIZE_MB` reference.
  - Update `GET /api/v1/routes` and `POST /api/v1/routes` payload examples with `base_backoff_ms`, `max_backoff_ms`, and `retry_non_idempotent`.
- **`src/content/docs/production-deploy.md`:**
  - Document `RELAY_SQLITE_SYNC=FULL` recommendation for single-node bare-metal deployments running without UPS or Litestream replication.
- **Build & LLM Context Sync (`public/llms-full.txt` & `dist/`):**
  - Run `npm run build` in `/workspace/pmlong-simplerelay.dev` so `scripts/generate-llms-full.mjs` regenerates `public/llms-full.txt` and `dist/` with the updated docs.

---

## 5. Verification Plan

1. **Unit & Integration Tests:**
   - `internal/engine/retry_test.go`: Verify `NewRetryEngine` (`100ms`/`10s`) vs `NewAsyncRetryEngine` (`5s`/`15m`) defaults and `CalculateBackoffWithOverrides` with per-route `base_backoff_ms` and `max_backoff_ms` (including `Retry-After` ceiling interaction).
   - `internal/engine/relay_test.go` & `internal/api/handler_relay_test.go`:
     - Update existing `POST` retry tests (`TestRelayEngine_RetryAndSuccessOnSecondAttempt`, `TestRelayEngine_ExhaustRetries`) to explicitly opt in via `RetryNonIdempotent: true` or `Idempotency-Key`.
     - Verify `POST` without `Idempotency-Key` and `retry_non_idempotent = false` executes only 1 attempt on upstream 5xx/timeout.
     - Verify `POST` with `Idempotency-Key` (or `X-Relay-Idempotency-Key`) retries up to `max_attempts`.
     - Verify `POST` with `retry_non_idempotent = true` retries up to `max_attempts` without `Idempotency-Key`.
     - Verify `GET` / `PUT` / `DELETE` always retry up to `max_attempts` when `retry_non_idempotent = false`.
   - `internal/engine/worker_pool_test.go`: Verify async worker uses `DefaultAsyncBaseBackoff` (`5s`) by default and respects route `base_backoff_ms` / `max_backoff_ms` overrides.
   - `internal/model/route_test.go` & `internal/storage/sqlite/route_repo_test.go` & `internal/api/handler_routes_test.go`: Verify validation, persistence, migration, and JSON round-tripping of `base_backoff_ms`, `max_backoff_ms`, and `retry_non_idempotent`.
   - `internal/config/config_test.go` & `internal/storage/sqlite/sqlite_test.go`: Verify `RELAY_SQLITE_SYNC` (`NORMAL`, `FULL`, invalid rejection), `RELAY_MAX_BODY_SIZE_MB` validation, and SQLite `PRAGMA synchronous` query verification on `db.Writer()` (`1` for NORMAL, `2` for FULL).
2. **Standard Verification Suite:**
   - `go test -v ./...`
   - `go test -race ./...`
   - `CGO_ENABLED=0 go build -o /dev/null ./cmd/relay`
