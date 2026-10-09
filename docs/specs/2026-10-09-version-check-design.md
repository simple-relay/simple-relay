# In-App Version Update Checker — Technical Design Specification

> **Date:** 2026-10-09  
> **Status:** Approved  
> **Author:** pmlonng (<phanminlongg@gmail.com>)  
> **Target Milestone:** Phase 3 — Milestone 3.3: Task 3.3.3  
> **Related Epics:** Release Automation (Release Please + GoReleaser), Embedded Web UI  

---

## 1. Overview & Problem Statement

Simple HTTP Relay is distributed as a single, self-hosted static binary without central infrastructure or mandatory cloud telemetry. Operators deploying instances on standalone servers, VPSs, or local infrastructure often run outdated builds, missing out on critical bug fixes, performance optimizations, and security patches.

When repository `https://github.com/simple-relay/simple-relay` is public and utilizes **Release Please** for automated changelog generation and Git tagging (e.g., `v1.2.0`), GitHub creates official Releases.

This specification details the **In-App Version Update Checker**, enabling the application to:
1. Autonomously query upstream release metadata from the public GitHub repository.
2. Determine if a newer stable version is available using standard Semantic Versioning (SemVer).
3. Safely cache and expose update status to operators via structured logs, system endpoints, and the embedded Web UI banner.
4. Provide complete operational safety: strict non-blocking execution, zero rate-limit hazards, air-gapped opt-out configurations, and hard request timeouts.

---

## 2. Core Architectural Decisions

### 2.1 Dual-Strategy Upstream Discovery (API vs. Redirect Trick)
To balance rich release notes with resilience against GitHub API rate limits (60 req/hr per IP for unauthenticated calls), the checker implements a **dual-strategy** mechanism:

1. **Primary Strategy (GitHub REST API):**
   - Query: `GET https://api.github.com/repos/simple-relay/simple-relay/releases/latest`
   - Headers: `User-Agent: simple-relay/<current_version>`, `Accept: application/vnd.github.v3+json`
   - Payload: Extracts `tag_name`, `html_url`, `published_at`, and markdown `body` (changelog summary).
   - If response is `403 Forbidden` (rate limit exceeded / `x-ratelimit-remaining: 0`), fall back immediately to the Redirect Strategy.

2. **Fallback / Lightweight Strategy (HTTP HEAD Redirect):**
   - Query: `HEAD https://github.com/simple-relay/simple-relay/releases/latest`
   - Config: Configure `http.Client.CheckRedirect` to return `http.ErrUseLastResponse` (do not follow 302 redirect).
   - Extraction: Read `Location` response header (e.g., `https://github.com/simple-relay/simple-relay/releases/tag/v1.2.0`). Extract the final path component `v1.2.0`.
   - Advantage: Completely bypasses GitHub REST API rate limits with near-zero latency and minimal bandwidth.

### 2.2 Strict Non-Blocking & Asynchronous Execution
- **Never Block Startup:** The version check routine must **never** delay `main()` execution, HTTP server binding, or request ingestion.
- **Background Worker:** Runs in an isolated background goroutine spawned after the HTTP server starts listening.
- **Initial Jitter Delay:** Spawns with an initial randomized delay (e.g., 5 to 15 seconds) so that application bootstrap completes immediately.
- **Check Cadence:** Checks periodically every 24 hours (`time.NewTicker(24 * time.Hour)`).
- **Strict Timeout:** Each check attempt enforces a hard `context.WithTimeout(ctx, 3*time.Second)` to prevent socket hangs during network instability.

### 2.3 Semantic Version Comparison
- Uses the standard Go module SemVer package: `golang.org/x/mod/semver`.
- Avoids lexicographical string comparisons (which fail on cases like `v0.10.0` vs. `v0.9.0`).
- Treats development builds (`dev`, `dirty`, or untagged commits) appropriately:
  - If current version is `"dev"` or empty, update checks run but do not raise disruptive alerts, or can be flagged as `dev-build`.
- Only flags an update when `semver.Compare(latestTag, currentVersion) > 0`.

### 2.4 Privacy & Air-Gapped Environments
- Self-hosted environments may run in private intranets or air-gapped networks where outbound traffic is blocked or audited.
- Configuration flag: `RELAY_CHECK_UPDATES` (default: `true`). When set to `false`, the background worker is completely disabled and zero outbound HTTP requests are emitted.
- Configurable upstream URL: `RELAY_UPDATE_REPO_URL` (default: `https://github.com/simple-relay/simple-relay`) allowing enterprise mirrors or private forks.

### 2.5 In-Memory State & Caching
- Store the latest check result in an in-memory struct protected by `sync.RWMutex`.
- Prevents multiple concurrent HTTP requests to GitHub when users navigate or refresh the dashboard.

---

## 3. Component Design & Interfaces

### 3.1 Package Structure (`internal/version`)

```
internal/version/
├── checker.go       # Core version checking logic, GitHub client & redirect fallback
├── checker_test.go  # Unit tests with mock HTTP servers
├── version.go       # Version variables and Info data structures
```

### 3.2 Data Models

```go
package version

import (
    "sync"
    "time"
)

// Injected at build time via -ldflags
var (
    Version   = "dev"
    GitCommit = "unknown"
    BuildDate = "unknown"
)

// Info holds the latest version check state.
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

// Checker coordinates background version checks and caches results.
type Checker struct {
    mu             sync.RWMutex
    currentVersion string
    repoURL        string
    enabled        bool
    interval       time.Duration
    httpClient     *http.Client
    latestInfo     Info
}
```

### 3.3 Core Methods

```go
func NewChecker(cfg Config) *Checker { ... }

// Start starts the background ticker loop in a dedicated goroutine.
func (c *Checker) Start(ctx context.Context) { ... }

// CheckNow performs an immediate version check (thread-safe, updates cache).
func (c *Checker) CheckNow(ctx context.Context) (Info, error) { ... }

// GetInfo returns the cached version information.
func (c *Checker) GetInfo() Info {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.latestInfo
}
```

---

## 4. Upstream Query Implementation Details

### 4.1 REST API Check with Fallback

```go
func (c *Checker) fetchLatestRelease(ctx context.Context) (*releaseMetadata, error) {
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL(), nil)
    if err != nil {
        return nil, err
    }
    req.Header.Set("User-Agent", "simple-relay/"+c.currentVersion)
    req.Header.Set("Accept", "application/vnd.github.v3+json")

    resp, err := c.httpClient.Do(req)
    if err != nil {
        // Fall back to HEAD redirect trick on network error or connect failure
        return c.fetchViaHeadRedirect(ctx)
    }
    defer resp.Body.Close()

    if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
        // Rate limited: fallback to HEAD redirect
        return c.fetchViaHeadRedirect(ctx)
    }

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("unexpected status from github api: %d", resp.StatusCode)
    }

    var payload struct {
        TagName     string    `json:"tag_name"`
        HTMLURL     string    `json:"html_url"`
        Body        string    `json:"body"`
        PublishedAt time.Time `json:"published_at"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
        return nil, err
    }

    return &releaseMetadata{
        Tag:         payload.TagName,
        URL:         payload.HTMLURL,
        Notes:       payload.Body,
        PublishedAt: payload.PublishedAt,
    }, nil
}
```

### 4.2 HEAD Redirect Trick (Rate-Limit Free)

```go
func (c *Checker) fetchViaHeadRedirect(ctx context.Context) (*releaseMetadata, error) {
    client := &http.Client{
        Timeout: 3 * time.Second,
        CheckRedirect: func(req *http.Request, via []*http.Request) error {
            return http.ErrUseLastResponse // Stop redirect traversal
        },
    }

    latestURL := fmt.Sprintf("%s/releases/latest", c.repoURL)
    req, err := http.NewRequestWithContext(ctx, http.MethodHead, latestURL, nil)
    if err != nil {
        return nil, err
    }
    req.Header.Set("User-Agent", "simple-relay/"+c.currentVersion)

    resp, err := client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusMovedPermanently {
        return nil, fmt.Errorf("expected redirect 302/301, got: %d", resp.StatusCode)
    }

    location := resp.Header.Get("Location")
    // Example: https://github.com/simple-relay/simple-relay/releases/tag/v1.2.0
    tag := path.Base(location)
    if tag == "" || tag == "latest" {
        return nil, fmt.Errorf("unable to extract tag from location: %s", location)
    }

    return &releaseMetadata{
        Tag: tag,
        URL: location,
    }, nil
}
```

---

## 5. System Integration Points

### 5.1 System API Endpoint: `GET /api/v1/system/version`
Exposes the version status to programmatic consumers and the embedded dashboard:

```json
{
  "current_version": "v1.1.0",
  "latest_version": "v1.2.0",
  "has_update": true,
  "release_url": "https://github.com/simple-relay/simple-relay/releases/tag/v1.2.0",
  "last_checked_at": "2026-10-09T14:30:00Z"
}
```

### 5.2 Embedded Web UI Integration (Phase 3.1)
- The Web UI top navigation bar displays a subtle notification badge if `has_update == true`.
- Clicking the badge opens a modal showing the new version, release date, and a direct link to GitHub Releases.
- If running the latest version, displays `v1.2.0 (Up to date)`.

### 5.3 Startup Structured Log Output
When an update is detected, log a concise warning at `INFO` or `WARN` level:
```
level=INFO msg="A new version of Simple HTTP Relay is available" current=v1.1.0 latest=v1.2.0 url=https://github.com/simple-relay/simple-relay/releases/tag/v1.2.0
```

---

## 6. Verification & Test Plan

1. **Unit Tests (`internal/version/checker_test.go`):**
   - Mock GitHub API server returning valid JSON response with newer tag (`v1.2.0` vs `v1.1.0`). Assert `HasUpdate == true`.
   - Current version matches or is higher (`v1.2.0` vs `v1.2.0` or `v2.0.0` vs `v1.2.0`). Assert `HasUpdate == false`.
   - Mock GitHub API returning HTTP 403 (rate limited); verify automatic fallback to HEAD redirect request.
   - Mock network timeout / failure: verify graceful error recording without panicking or blocking.
   - Config disabled (`enabled = false`): verify no HTTP calls are dispatched.

2. **Race Condition Testing:**
   - Execute test suite with race detector: `go test -race ./internal/version/...`.
   - Verify concurrent reads (`GetInfo()`) while a background check is writing to cache.

3. **Binary Build Verification:**
   - Verify compile compatibility under `CGO_ENABLED=0 go build -o /dev/null ./cmd/relay`.
