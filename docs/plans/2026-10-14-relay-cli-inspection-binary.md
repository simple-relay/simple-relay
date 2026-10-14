# CLI Inspection Binary (`cmd/relay-cli`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a standalone, static CLI inspection binary (`cmd/relay-cli`) that interacts with the Simple Relay daemon via REST API to inspect health, monitor queue depth, and triage/replay Dead Letter Queue (DLQ) messages.

**Architecture:** Pure REST HTTP client using `spf13/cobra` for POSIX command hierarchies, with a decoupled `RelayClient` transport layer, `text/tabwriter` table formatting with ANSI status highlighting, and strict non-TTY safety guardrails on bulk operations.

**Tech Stack:** Go 1.22+, `github.com/spf13/cobra v1.8.1`, `github.com/spf13/pflag v1.0.5`, standard library `net/http` and `text/tabwriter`.

**Spec:** [docs/specs/2026-10-14-relay-cli-inspection-binary-design.md](file:///workspace/pmlong-simple-relay/docs/specs/2026-10-14-relay-cli-inspection-binary-design.md)

## Global Constraints

- Must compile statically without CGO (`CGO_ENABLED=0`).
- Core daemon (`cmd/relay`, `internal/api`, `internal/storage`) must not be modified (Pure client architecture).
- Global flags: `-s, --server` (default: `http://127.0.0.1:8080`, env: `RELAY_SERVER_URL`), `-k, --api-key` (env: `RELAY_API_KEY`), `-o, --output` (default: `table`), `--timeout` (default: `10s`).
- Exit codes: `0` for success/healthy, `1` for client/auth/connection error, `2` for server degraded/unhealthy.

## Review Focus

- Daemon unreachable / connection refused: CLI must output clean error message and exit with code 1 without panic or stack trace.
- HTTP 401 Unauthorized: CLI must output clear diagnostic guidance to provide `--api-key` or set `RELAY_API_KEY`.
- Non-interactive `dlq replay-all`: Must immediately abort with exit code 1 if `--yes` (`-y`) is not passed in non-TTY environments.
- Server bulk replay cap (1,000 items): Client must chunk replays with `{"limit": 1000}` when total failed items exceed 1,000.
- Deleted route handling: `dlq list` must fallback to truncated `route_id` if route lookup in `/api/v1/routes` misses.

---

### Task 1: Dependencies Setup & `RelayClient` HTTP Transport

**Files:**
- Modify: `go.mod`
- Create: `internal/cli/client.go`
- Create: `internal/cli/client_test.go`

**Interfaces:**
- Produces:
  ```go
  type RelayClient struct { ... }
  func NewClient(baseURL, apiKey string, timeout time.Duration) *RelayClient
  func (c *RelayClient) Health(ctx context.Context) (*HealthResponse, time.Duration, error)
  func (c *RelayClient) GetQueueStats(ctx context.Context) (pending, inFlight int64, err error)
  func (c *RelayClient) ListRoutes(ctx context.Context) (map[string]string, error)
  func (c *RelayClient) ListDLQ(ctx context.Context, limit int) ([]DLQItem, error)
  func (c *RelayClient) InspectRequest(ctx context.Context, id string) (*RequestDetail, error)
  func (c *RelayClient) ReplayDLQ(ctx context.Context, id string) error
  func (c *RelayClient) ReplayAllDLQ(ctx context.Context) (int, error)
  ```

- [ ] **Step 1: Add `spf13/cobra` and `spf13/pflag` to `go.mod`**
  Run: `go get github.com/spf13/cobra@v1.8.1 github.com/spf13/pflag@v1.0.5 && go mod tidy`

- [ ] **Step 2: Write failing unit tests for `RelayClient`**
  In `internal/cli/client_test.go`, test mock server for:
  - `Health()` returning 200 vs 503 vs network failure.
  - `GetQueueStats()` querying `status=pending` and `status=processing`.
  - `ListRoutes()` building route ID-to-name lookup map.
  - `ListDLQ()` parsing items.
  - `InspectRequest()` fetching request details and attempts.
  - `ReplayDLQ()` calling replay endpoint.
  - `ReplayAllDLQ()` chunked loop handling > 1000 items.
  - 401 Unauthorized typed error handling.

- [ ] **Step 3: Run test to verify failure**
  Run: `go test -v ./internal/cli/...`
  Expected: FAIL with undefined `RelayClient`.

- [ ] **Step 4: Implement `RelayClient` in `internal/cli/client.go`**
  Implement HTTP request builder with `X-API-Key` header, timeout context, and typed JSON decoding.

- [ ] **Step 5: Run tests to verify they pass**
  Run: `go test -v -race ./internal/cli/...`
  Expected: PASS

- [ ] **Step 6: Commit**
  ```bash
  git add go.mod go.sum internal/cli/client.go internal/cli/client_test.go
  git commit -m "feat(cli): implement RelayClient HTTP transport layer"
  ```

---

### Task 2: Output Formatting & Table/JSON Renderers

**Files:**
- Create: `internal/cli/formatter.go`
- Create: `internal/cli/formatter_test.go`

**Interfaces:**
- Consumes: Models/Responses from `internal/cli/client.go`.
- Produces:
  ```go
  func RenderTable(w io.Writer, headers []string, rows [][]string) error
  func RenderJSON(w io.Writer, data any) error
  func FormatStatus(status string, isTerminal bool) string
  func Truncate(s string, maxLen int) string
  ```

- [ ] **Step 1: Write failing tests for formatters**
  In `internal/cli/formatter_test.go`, test:
  - `RenderTable` with custom headers and tabwriter alignment.
  - `FormatStatus` with ANSI colors when `isTerminal=true`, plain text when `isTerminal=false`.
  - `RenderJSON` pretty-printed valid JSON serialization.
  - `Truncate` ellipsis string truncation.

- [ ] **Step 2: Run test to verify failure**
  Run: `go test -v ./internal/cli/formatter_test.go`
  Expected: FAIL

- [ ] **Step 3: Implement `internal/cli/formatter.go`**
  Implement tabwriter formatting, ANSI coloring (`\033[32m`, `\033[33m`, `\033[31m`), and JSON indentation.

- [ ] **Step 4: Run test to verify pass**
  Run: `go test -v -race ./internal/cli/formatter_test.go`
  Expected: PASS

- [ ] **Step 5: Commit**
  ```bash
  git add internal/cli/formatter.go internal/cli/formatter_test.go
  git commit -m "feat(cli): add tabwriter and JSON output formatters"
  ```

---

### Task 3: Command Tree Implementation (`root`, `health`, `queue`, `dlq`)

**Files:**
- Create: `internal/cli/root.go`
- Create: `internal/cli/health.go`
- Create: `internal/cli/queue.go`
- Create: `internal/cli/dlq.go`
- Create: `internal/cli/commands_test.go`

**Interfaces:**
- Produces:
  ```go
  func NewRootCmd() *cobra.Command
  func Execute() error
  ```

- [ ] **Step 1: Write failing tests for command executions**
  In `internal/cli/commands_test.go`:
  - Test `health` command output and exit codes.
  - Test `queue` command output and `HEALTHY` vs `BACKLOG` status calculation.
  - Test `dlq list` with route name resolution.
  - Test `dlq inspect <id>` showing request and attempts.
  - Test `dlq replay <id>`.
  - Test `dlq replay-all` non-TTY rejection without `-y` and success with `-y`.

- [ ] **Step 2: Run test to verify failure**
  Run: `go test -v ./internal/cli/commands_test.go`
  Expected: FAIL

- [ ] **Step 3: Implement Cobra command hierarchy**
  - `root.go`: Bind persistent flags and environment variables (`RELAY_SERVER_URL`, `RELAY_API_KEY`, etc.).
  - `health.go`: `healthCmd` / `statusCmd`.
  - `queue.go`: `queueCmd`.
  - `dlq.go`: `dlqCmd`, `dlqListCmd`, `dlqInspectCmd`, `dlqReplayCmd`, `dlqReplayAllCmd`.

- [ ] **Step 4: Run test to verify pass**
  Run: `go test -v -race ./internal/cli/commands_test.go`
  Expected: PASS

- [ ] **Step 5: Commit**
  ```bash
  git add internal/cli/root.go internal/cli/health.go internal/cli/queue.go internal/cli/dlq.go internal/cli/commands_test.go
  git commit -m "feat(cli): implement cobra command hierarchy for health, queue, and dlq"
  ```

---

### Task 4: Main Binary Entry Point & End-to-End CLI Verification

**Files:**
- Create: `cmd/relay-cli/main.go`
- Create: `internal/cli/e2e_cli_test.go`

- [ ] **Step 1: Create `cmd/relay-cli/main.go`**
  ```go
  package main

  import (
      "os"
      "github.com/simple-relay/simple-relay/internal/cli"
  )

  func main() {
      if err := cli.Execute(); err != nil {
          os.Exit(1)
      }
  }
  ```

- [ ] **Step 2: Verify static compilation**
  Run: `CGO_ENABLED=0 go build -o /dev/null ./cmd/relay-cli`
  Expected: PASS

- [ ] **Step 3: Write comprehensive e2e CLI test against real server in test suite**
  In `internal/cli/e2e_cli_test.go`, boot a full `api.NewRouter` test server with in-memory SQLite, ingest test jobs into DLQ, execute CLI commands through buffer, verify output and status codes.

- [ ] **Step 4: Run full test suite with race detection**
  Run: `go test -v -race ./internal/cli/...`
  Expected: PASS

- [ ] **Step 5: Commit**
  ```bash
  git add cmd/relay-cli/main.go internal/cli/e2e_cli_test.go
  git commit -m "feat(cli): create relay-cli main entry point and e2e tests"
  ```

---

### Task 5: Documentation, Roadmap Synchronization & Final Verification

**Files:**
- Modify: `docs/roadmap.md`
- Modify: `docs/architecture.md`

- [ ] **Step 1: Update `docs/roadmap.md`**
  Mark task [#21](https://github.com/simple-relay/simple-relay/issues/21) complete: `- [x] **CLI Inspection Binary (`cmd/relay-cli`)** ([#21](https://github.com/simple-relay/simple-relay/issues/21))`.

- [ ] **Step 2: Update `docs/architecture.md`**
  Add `cmd/relay-cli` and `internal/cli` to the project directory layout.

- [ ] **Step 3: Run repository-wide test & build verification suite**
  Run:
  - `go test -v ./...`
  - `go test -race ./...`
  - `CGO_ENABLED=0 go build -o /dev/null ./cmd/relay`
  - `CGO_ENABLED=0 go build -o /dev/null ./cmd/relay-cli`

- [ ] **Step 4: Commit and Push**
  ```bash
  git add docs/roadmap.md docs/architecture.md docs/specs/2026-10-14-relay-cli-inspection-binary-design.md
  git commit -m "docs(roadmap): mark CLI inspection binary task complete"
  ```
