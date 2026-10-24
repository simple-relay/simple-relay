# In-App Version Update Checker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an autonomous, non-blocking in-app version update checker (`internal/version`) querying upstream GitHub Releases (`simple-relay/simple-relay`) with dual discovery (REST API + HEAD redirect fallback), SemVer comparison, and expose update metadata via `GET /api/v1/system/version` and the embedded dashboard UI badge.

**Architecture:** Pure Go background worker routine running asynchronously with randomized startup jitter (5–15s) and 24-hour periodic check cadence; cached thread-safe state via `sync.RWMutex`; dual-strategy upstream release discovery (primary GitHub REST API with seamless fallback to rate-limit-free HTTP HEAD redirect); standard `golang.org/x/mod/semver` comparison; opt-out toggle (`RELAY_CHECK_UPDATES=false`) for air-gapped deployments; REST endpoint `GET /api/v1/system/version` and UI status badge adhering to `DESIGN.md`.

**Tech Stack:** Go 1.22+, `golang.org/x/mod/semver`, standard library `net/http`, `log/slog`, `sync`, Alpine.js, Tailwind CSS tokens.

**Spec:** [docs/specs/2026-10-09-version-check-design.md](file:///workspace/pmlong-simple-relay/docs/specs/2026-10-09-version-check-design.md)

---

## Global Constraints

- Must compile statically without CGO (`CGO_ENABLED=0`).
- Strict non-blocking startup: version checking must never delay daemon bootstrap, database initialization, HTTP listener binding, or webhook ingestion.
- Hard upstream timeout: 3 seconds maximum per outbound HTTP request context to avoid socket resource leaks.
- Zero rate-limit stalls: automatically fallback from GitHub REST API to HTTP HEAD redirect trick on HTTP 403, HTTP 429, or network errors.
- Air-gapped safety: setting `RELAY_CHECK_UPDATES=false` (or CLI flag `--check-updates=false`) completely disables background network polling.
- Concurrency safety: all cached state access protected by `sync.RWMutex`, verified with `go test -race ./...`.
- Design system compliance: Web UI additions in `ui/index.html` must strictly adhere to `DESIGN.md` (no external CDNs, utilitarian dark palette, tabular numbers, WCAG AAA contrast).

---

## Review Focus

1. **GitHub API Rate Limit (403/429):** When unauthenticated rate limits are exhausted (`x-ratelimit-remaining: 0`), the checker must automatically fall back to the HEAD redirect trick without panicking or marking the check as a fatal error.
2. **Air-Gapped & Offline Isolation:** With `RELAY_CHECK_UPDATES=false`, no background goroutine or HTTP request may ever be initiated.
3. **Development & Untagged Builds:** When `Version = "dev"` or contains a non-semver suffix, the checker records release metadata but does not flag disruptive false alarms (`HasUpdate = false`).
4. **Network Partition / Upstream Hang:** When GitHub is unreachable or drops connections, the 3s context timeout cancels cleanly, logs a debug message, records the error in cached `Info`, and the daemon continues uninterrupted.
5. **SemVer Normalization & Comparison:** Handles versions with or without leading `v` (e.g., `0.10.0` vs `v0.9.0`) using standard `golang.org/x/mod/semver`.

---

### Task 1: Add SemVer Dependency & Data Structures

**Files:**
- Modify: `go.mod`
- Modify: `internal/version/version.go`
- Modify: `internal/version/version_test.go`

**Interfaces:**
- Produces:
  ```go
  type Info struct {
      CurrentVersion string    `json:"current_version"`
      LatestVersion  string    `json:"latest_version,omitempty"`
      HasUpdate      bool      `json:"has_update"`
      ReleaseURL     string    `json:"release_url,omitempty"`
      ReleaseNotes   string    `json:"release_notes,omitempty"`
      PublishedAt    time.Time `json:"published_at,omitempty"`
      LastCheckedAt  time.Time `json:"last_checked_at,omitempty"`
      Error          string    `json:"error,omitempty"`
  }

  func NormalizeVersion(v string) string
  func IsNewerVersion(current, latest string) bool
  ```

- [ ] **Step 1: Add `golang.org/x/mod/semver` dependency**
  Run: `go get golang.org/x/mod@v0.21.0 && go mod tidy`

- [ ] **Step 2: Write unit tests for `NormalizeVersion` and `IsNewerVersion`**
  In `internal/version/version_test.go`:
  - `NormalizeVersion("v1.2.3")` -> `"v1.2.3"`.
  - `NormalizeVersion("1.2.3")` -> `"v1.2.3"`.
  - `IsNewerVersion("v1.0.0", "v1.1.0")` -> `true`.
  - `IsNewerVersion("v0.9.0", "v0.10.0")` -> `true`.
  - `IsNewerVersion("v1.2.0", "v1.2.0")` -> `false`.
  - `IsNewerVersion("v2.0.0", "v1.9.0")` -> `false`.
  - `IsNewerVersion("dev", "v1.0.0")` -> `false`.
  - `IsNewerVersion("unknown", "v1.0.0")` -> `false`.

- [ ] **Step 3: Run test to verify failure**
  Run: `go test -v ./internal/version/...`
  Expected: FAIL with undefined `Info`, `NormalizeVersion`, `IsNewerVersion`.

- [ ] **Step 4: Implement `Info` struct and SemVer comparison helpers**
  In `internal/version/version.go`:
  - Define `Info` struct with JSON tags.
  - Implement `NormalizeVersion(v string) string` ensuring leading `v`.
  - Implement `IsNewerVersion(current, latest string) bool` using `semver.Compare`.

- [ ] **Step 5: Run tests to verify they pass**
  Run: `go test -v -race ./internal/version/...`
  Expected: PASS

- [ ] **Step 6: Commit**
  ```bash
  git add go.mod go.sum internal/version/version.go internal/version/version_test.go
  git commit -m "feat(version): add SemVer dependency and version Info data structures"
  ```

---

### Task 2: Implement Checker Engine with Dual Discovery

**Files:**
- Create: `internal/version/checker.go`
- Create: `internal/version/checker_test.go`

**Interfaces:**
- Produces:
  ```go
  type Config struct {
      CurrentVersion string
      RepoURL        string
      Enabled        bool
      Interval       time.Duration
      HTTPClient     *http.Client
  }

  type Checker struct { ... }
  func NewChecker(cfg Config) *Checker
  func (c *Checker) Start(ctx context.Context)
  func (c *Checker) CheckNow(ctx context.Context) (Info, error)
  func (c *Checker) GetInfo() Info
  ```

- [ ] **Step 1: Write comprehensive unit tests for `Checker`**
  In `internal/version/checker_test.go`:
  - `TestChecker_RESTAPISuccess`: mock server returns valid GitHub Release JSON; verify parsed fields and `HasUpdate = true`.
  - `TestChecker_RateLimitFallbackToHead`: mock server returns 403 on REST API endpoint, then returns 302 on HEAD `/releases/latest` with `Location: .../tag/v1.5.0`; verify seamless fallback.
  - `TestChecker_DisabledOptOut`: with `Enabled: false`, verify `CheckNow` returns immediately without dispatching any HTTP requests.
  - `TestChecker_NetworkFailure`: mock server drops connection; verify `CheckNow` captures error in `Info.Error` without panicking.
  - `TestChecker_ConcurrentAccess`: spawn multiple goroutines calling `GetInfo()` while `CheckNow()` is updating the cache; verify thread safety with `-race`.

- [ ] **Step 2: Run test to verify failure**
  Run: `go test -v ./internal/version/...`
  Expected: FAIL with undefined `Checker`.

- [ ] **Step 3: Implement `Checker` in `internal/version/checker.go`**
  - Implement `NewChecker(cfg Config) *Checker` with default fallbacks (`RepoURL = "https://github.com/simple-relay/simple-relay"`, `Interval = 24 * time.Hour`).
  - Implement `CheckNow(ctx context.Context) (Info, error)`:
    - If `!c.enabled`, return cached info immediately.
    - Set 3-second hard timeout context.
    - Try REST API: `GET https://api.github.com/repos/{owner}/{repo}/releases/latest` with standard User-Agent.
    - On 403, 429, or network error, execute `fetchViaHeadRedirect`: `HEAD {RepoURL}/releases/latest` with `CheckRedirect: http.ErrUseLastResponse`. Extract tag from `Location` header.
    - Evaluate `IsNewerVersion(c.currentVersion, tag)`.
    - Update cached `latestInfo` under `c.mu.Lock()`.
  - Implement `GetInfo() Info` under `c.mu.RLock()`.
  - Implement `Start(ctx context.Context)`:
    - If `!c.enabled`, return immediately.
    - Sleep initial random jitter delay (5 to 15 seconds) or wait for `ctx.Done()`.
    - Perform initial `CheckNow(ctx)`.
    - Loop over `time.NewTicker(c.interval)` until `ctx.Done()`.

- [ ] **Step 4: Run tests to verify they pass**
  Run: `go test -v -race ./internal/version/...`
  Expected: PASS

- [ ] **Step 5: Commit**
  ```bash
  git add internal/version/checker.go internal/version/checker_test.go
  git commit -m "feat(version): implement dual discovery version checker engine"
  ```

---

### Task 3: Configuration & CLI Flags

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/cli/serve.go`

**Interfaces:**
- Produces:
  ```go
  // In config.Config
  CheckUpdates   bool          // env: RELAY_CHECK_UPDATES, default: true
  UpdateRepoURL  string        // env: RELAY_UPDATE_REPO_URL, default: "https://github.com/simple-relay/simple-relay"
  UpdateInterval time.Duration // env: RELAY_UPDATE_INTERVAL, default: 24h
  ```

- [ ] **Step 1: Write failing tests for configuration**
  In `internal/config/config_test.go`:
  - Test default values: `CheckUpdates == true`, `UpdateRepoURL == "https://github.com/simple-relay/simple-relay"`, `UpdateInterval == 24 * time.Hour`.
  - Test environment variables: `RELAY_CHECK_UPDATES=false`, `RELAY_UPDATE_REPO_URL=https://custom-repo.example.com`.
  - Test validation rules.

- [ ] **Step 2: Run test to verify failure**
  Run: `go test -v ./internal/config/...`
  Expected: FAIL with missing configuration fields.

- [ ] **Step 3: Implement config fields and CLI flag bindings**
  - In `internal/config/config.go`: add fields to `Config` struct, default initializer, and `loadFromEnv()`.
  - In `internal/cli/serve.go`: add `--check-updates` and `--update-repo-url` flags to `addServerFlags()`, bind in `runServe()`.

- [ ] **Step 4: Run tests to verify they pass**
  Run: `go test -v -race ./internal/config/... ./internal/cli/...`
  Expected: PASS

- [ ] **Step 5: Commit**
  ```bash
  git add internal/config/config.go internal/config/config_test.go internal/cli/serve.go
  git commit -m "feat(config): add update checker configuration and CLI flags"
  ```

---

### Task 4: System Version API Endpoint & Server Wiring

**Files:**
- Modify: `internal/api/router.go`
- Create: `internal/api/handler_system.go`
- Create: `internal/api/handler_system_test.go`
- Modify: `internal/cli/serve.go`

**Interfaces:**
- Produces:
  ```go
  // In internal/api/router.go
  type RouterOptions struct {
      ...
      VersionChecker *version.Checker
  }

  // GET /api/v1/system/version
  ```

- [ ] **Step 1: Write failing tests for `GET /api/v1/system/version`**
  In `internal/api/handler_system_test.go`:
  - Query `GET /api/v1/system/version` with mocked `version.Checker`.
  - Verify JSON status 200 and schema:
    ```json
    {
      "current_version": "v1.1.0",
      "latest_version": "v1.2.0",
      "has_update": true,
      "release_url": "https://github.com/simple-relay/simple-relay/releases/tag/v1.2.0",
      "last_checked_at": "..."
    }
    ```
  - Verify accessibility without mandatory authentication (public system status endpoint).

- [ ] **Step 2: Run test to verify failure**
  Run: `go test -v ./internal/api/...`
  Expected: FAIL (404 not found).

- [ ] **Step 3: Implement `handler_system.go` and mount route in `internal/api/router.go`**
  - Implement `NewSystemHandler(checker *version.Checker, logger *slog.Logger)`.
  - Register `GET /api/v1/system/version` in ServeMux.
  - Wire `version.NewChecker` into `internal/cli/serve.go`, start background routine with `runCtx`, and log `INFO` message if `has_update == true`.

- [ ] **Step 4: Run tests to verify they pass**
  Run: `go test -v -race ./internal/api/... ./internal/cli/...`
  Expected: PASS

- [ ] **Step 5: Commit**
  ```bash
  git add internal/api/router.go internal/api/handler_system.go internal/api/handler_system_test.go internal/cli/serve.go
  git commit -m "feat(api): expose GET /api/v1/system/version and wire background worker"
  ```

---

### Task 5: Web UI Notification Badge & Modal

**Files:**
- Modify: `ui/index.html`

- [ ] **Step 1: Add update checker state to Alpine.js `relayApp()`**
  In `ui/index.html`:
  - Add state properties: `updateInfo: null`, `showUpdateModal: false`.
  - In `init()`: call `this.fetchVersionInfo()`.
  - Implement `fetchVersionInfo()`: `fetch('/api/v1/system/version')` and store response in `this.updateInfo`.

- [ ] **Step 2: Render notification badge in sidebar footer**
  In `ui/index.html` sidebar footer (beside version link):
  - When `updateInfo && updateInfo.has_update`, display a subtle badge:
    ```html
    <template x-if="updateInfo && updateInfo.has_update">
      <button @click="showUpdateModal = true" class="inline-flex items-center gap-1 px-1.5 py-0.5 text-[10px] font-medium font-mono rounded bg-blue-950/60 text-blue-400 border border-blue-800 hover:bg-blue-900/60 transition-colors" title="New version available">
        <span class="w-1.5 h-1.5 rounded-full bg-blue-400 animate-pulse"></span>
        Update
      </button>
    </template>
    ```

- [ ] **Step 3: Render update details modal**
  In `ui/index.html`:
  - When `showUpdateModal` is true, render a modal displaying:
    - Current version vs. Latest version (`tabular-nums font-mono`).
    - Release date.
    - Link to GitHub Release notes (`release_url`).
    - Close button.
  - Strict compliance with `DESIGN.md` dark theme tokens (`#0b0f17`, `#121824`, `#243044`).

- [ ] **Step 4: Verify static binary compilation and UI embed**
  Run: `CGO_ENABLED=0 go build -o /dev/null ./cmd/relay`
  Expected: Build success with embedded UI assets.

- [ ] **Step 5: Commit**
  ```bash
  git add ui/index.html
  git commit -m "feat(ui): add version update notification badge and modal in dashboard"
  ```

---

### Task 6: Full Verification & Roadmap Sync

**Files:**
- Modify: `docs/roadmap.md`

- [ ] **Step 1: Run comprehensive verification test suites**
  - Run: `go test -v ./...`
  - Run: `go test -race ./...`
  - Run: `CGO_ENABLED=0 go build -o /dev/null ./cmd/relay`
  - Expected: All test suites PASS with zero race warnings and clean static binary build.

- [ ] **Step 2: Synchronize `docs/roadmap.md`**
  Mark milestone task complete:
  ```markdown
  - [x] **In-App Version Update Checker (`internal/version`)** ([#20](https://github.com/simple-relay/simple-relay/issues/20))
  ```

- [ ] **Step 3: Commit**
  ```bash
  git add docs/roadmap.md
  git commit -m "docs(roadmap): mark in-app version update checker completed"
  ```
