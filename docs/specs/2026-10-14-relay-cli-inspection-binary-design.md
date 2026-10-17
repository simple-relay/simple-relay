# CLI Inspection Subcommands (`cmd/relay`) Design Specification

- **Target Issue:** [#21](https://github.com/simple-relay/simple-relay/issues/21) (Roadmap Phase 4)
- **Dependency Issue:** [#60](https://github.com/simple-relay/simple-relay/issues/60) (Permit `spf13/cobra` for tooling binaries)
- **Related Issue:** [#23](https://github.com/simple-relay/simple-relay/issues/23) (API Key Authentication)
- **Author:** `pmlonng`
- **Date:** 2026-10-14
- **Status:** Approved for Implementation Planning

---

## 1. Intent & Context

### 1.1 The "Why"
Simple Relay is deployed as a standalone, self-hosted HTTP reliability layer in containerized or headless Linux environments (Docker, Kubernetes, VPS). Currently, runtime inspection requires either:
1. Accessing the embedded Web UI (`/ui/`), which is impractical in headless, bastion, or private VPC environments without port-forwarding.
2. Manually crafting cURL requests against REST API endpoints, which is cumbersome and prone to operator error during production incident triage.

`relay` provides ergonomic, Unix-friendly command-line subcommands directly inside the single binary (`cmd/relay`) enabling DevOps engineers and SREs to inspect health, monitor queue depth, diagnose failed jobs, and trigger Dead Letter Queue (DLQ) re-deliveries directly from their terminal or automated scripts.

---

## 2. Goals, Non-Goals & Anti-Goals

### 2.1 Goals
- **Headless Observability:** Rapid inspection of daemon health (`/healthz`) and queue lag directly from shell sessions.
- **DLQ Triage & Recovery:** List, inspect attempt history, and replay failed messages individually or in bulk without browser access.
- **Scriptability & Machine Output:** Provide `--output json` (`-o json`) with standardized POSIX exit codes (`0`, `1`, `2`) for seamless integration with `jq`, cronjobs, and Kubernetes container probes.
- **Safety Guardrails:** Enforce explicit interactive confirmation prompts on destructive or high-throughput operations (`dlq replay-all`), requiring `-y` / `--yes` in non-interactive/scripted environments.
- **Auth-Ready Transport:** Support `--api-key` and `RELAY_API_KEY` out of the box, forwarding `X-API-Key` headers and handling HTTP `401 Unauthorized` with actionable diagnostic messages.

### 2.2 Non-Goals
- **Zero Server Changes (Pure Client Mode for Subcommands):** Subcommands function as an HTTP client against existing daemon endpoints. No modifications to `internal/api` or `internal/storage` are required.
- **Configuration CRUD in v1:** Route creation, updates, and deletions remain managed via YAML configuration file or Web UI in this initial milestone.
- **Direct SQLite File Access:** CLI will **never** read or write directly to `relay.db`. All interactions strictly pass through the daemon's REST API.

### 2.3 Anti-Goals
- **Database Concurrency Violations:** Bypassing `cmd/relay` to touch SQLite files directly would violate Simple Relay's single-writer WAL invariant and risk `SQLITE_BUSY` contention or stale memory cache states.
- **Interactive TUI Bloat:** No heavy terminal UI libraries (e.g. bubbletea or termui); the tool is an imperative Unix-style CLI.

---

## 3. Constraints & Technical Choices

### 3.1 Concurrency & Persistence Model
- **Pure REST Client:** Interacts exclusively with `cmd/relay` via HTTP/JSON.
- **Zero Daemon Contention:** Relies entirely on existing thread-safe handlers (`internal/api`) and the daemon's asynchronous batch writer.

### 3.2 CLI Framework & Dependencies
- **Framework:** `github.com/spf13/cobra` (v1.8.1+) and `github.com/spf13/pflag` (as tracked in [#60](https://github.com/simple-relay/simple-relay/issues/60)).
- **Single Binary Architecture:** Embedded directly into the single `cmd/relay` binary. Running `relay` without subcommands defaults to starting the server daemon (`serve`), while operator subcommands provide POSIX flags, command hierarchies, shell autocompletion, and validation.
- **Static Binary Compatibility:** Must compile with `CGO_ENABLED=0` to preserve single-binary distribution.

---

## 4. Command Hierarchy & UX Specification

### 4.1 Global Flags & Environment Variables

| Flag | Shorthand | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- | :--- |
| `--server` | `-s` | `RELAY_SERVER_URL` | `http://127.0.0.1:8080` | Simple Relay daemon base URL. |
| `--api-key` | `-k` | `RELAY_API_KEY` | `""` | Optional admin API key forwarded via `X-API-Key`. |
| `--output` | `-o` | `RELAY_OUTPUT` | `table` | Output format: `table` (human-readable) or `json`. |
| `--timeout` | | `RELAY_TIMEOUT` | `10s` | HTTP client request timeout. |

### 4.2 Exit Codes
- `0` (Success): Operation succeeded or health check is healthy.
- `1` (Client / Connection / Auth Error): Server unreachable, flag parsing error, 401 Unauthorized, or request rejected.
- `2` (Unhealthy / Warning): Service returned degraded status (e.g. `/healthz` returned status `degraded`, or queue depth exceeds threshold $\ge 1,000$).

---

### 4.3 Subcommand Specifications

```text
relay
├── serve (default daemon mode)
├── health (alias: status)
├── queue
└── dlq
    ├── list
    ├── inspect <request_id>
    ├── replay <request_id>
    └── replay-all
```

#### 4.3.1 `relay health` (alias: `status`)
- **API Target:** `GET /healthz`
- **Description:** Verifies server responsiveness and round-trip latency.
- **Output (Table):**
  ```text
  STATUS    LATENCY    SERVER TIME             URL
  ok        1.42ms     2026-10-14T14:00:00Z    http://127.0.0.1:8080
  ```
- **Exit Code:** `0` if `status == "ok"`, `2` if non-200 or status != "ok", `1` if connection refused.

#### 4.3.2 `relay queue`
- **Pure Client Metric Calculation:**
  - Queries `GET /api/v1/requests?status=pending&limit=1` -> reads response `.total` as `PendingCount`.
  - Queries `GET /api/v1/requests?status=processing&limit=1` -> reads response `.total` as `InFlightCount`.
  - Calculates `QueueDepth = PendingCount + InFlightCount`.
- **Health Evaluation Logic:**
  - If `QueueDepth < 1000`: Status is `HEALTHY`, exit code is `0`.
  - If `QueueDepth >= 1000`: Status is `BACKLOG`, exit code is `2`.
- **Output (Table):**
  ```text
  QUEUE DEPTH    PENDING    IN-FLIGHT    STATUS
  12             10         2            HEALTHY
  ```

#### 4.3.3 `relay dlq list`
- **API Target:** `GET /api/v1/dlq?limit=<limit>`
- **Route Name Resolution (Pure Client):**
  - Fetches `GET /api/v1/routes` to build an in-memory lookup map (`route_id -> route_name`).
  - Resolves each DLQ item's `route_id` to its human-readable `route_name`.
  - Falls back to displaying `route_id` (truncated to 10 chars) if the route has since been deleted.
- **Flags:**
  - `--limit` (default: `50`): Maximum number of failed records to fetch.
- **Output (Table):**
  ```text
  REQUEST ID                  ROUTE             ATTEMPTS    FAILED AT               LAST ERROR
  01ja2b3c4d5e6f7g8h9j0k1m2n  stripe-inbound    5           2026-10-14 13:45:10     502 Bad Gateway: connection timeout
  01ja2b3c4d5e6f7g8h9j0k1m2p  slack-events      3           2026-10-14 13:50:22     429 Too Many Requests: rate limited
  ```

#### 4.3.4 `relay dlq inspect <request_id>`
- **API Target:** `GET /api/v1/requests/<request_id>`
- **Description:** Shows comprehensive job metadata, payload preview, and attempt history breakdown.
- **Output (Table):**
  - Section 1: Request Metadata (ID, Route, Status, Created At, Destination).
  - Section 2: Attempt History table (`#`, `Attempted At`, `Status Code`, `Duration`, `Error`).

#### 4.3.5 `relay dlq replay <request_id>`
- **API Target:** `POST /api/v1/dlq/<request_id>/replay`
- **Description:** Immediately transitions the specified DLQ request back to `pending` and wakes the dispatcher.
- **Output:**
  ```text
  Successfully requeued request 01ja2b3c4d5e6f7g8h9j0k1m2n for delivery.
  ```

#### 4.3.6 `relay dlq replay-all`
- **API Target:** `POST /api/v1/dlq/replay-all`
- **Flags:**
  - `-y, --yes`: Bypass interactive confirmation.
- **Safety Mechanism:**
  - If interactive terminal (TTY): Queries `GET /api/v1/dlq` to get total failed items. Prompts:
    `Found 142 items in DLQ. Requeue all for delivery? [y/N]: `
    Requires explicit `y` or `Y` to continue. Default is cancel.
  - If non-interactive (cron/CI without TTY) and `--yes` is **not** provided: Aborts immediately with exit code `1` and error:
    `Error: non-interactive terminal detected. Must specify --yes (-y) to confirm bulk replay.`
- **Client Chunked Pagination Loop (Server Cap Compliance):**
  - The server caps each replay call at `1,000` items (`limit = 1000`).
  - If total items to replay > 1,000, `relay` executes requests in chunks with `{"limit": 1000}` until all items are requeued, displaying incremental progress.
- **Output:**
  ```text
  Successfully requeued 142 DLQ requests for delivery.
  ```

---

## 5. Architectural Layout & Code Structure

```text
simple-relay/
├── cmd/
│   └── relay/
│       └── main.go                 # App entry point, delegates to cli.Execute()
├── internal/
│   ├── cli/
│   │   ├── root.go                 # Root command, persistent flags, config binding
│   │   ├── serve.go                # 'serve' daemon command execution & lifecycle
│   │   ├── client.go               # HTTP client wrapper, auth header injection, error parser
│   │   ├── formatter.go            # Tabwriter table and JSON output formatting utilities
│   │   ├── health.go               # 'health' / 'status' command implementation
│   │   ├── queue.go                # 'queue' command implementation
│   │   └── dlq.go                  # 'dlq' command tree (list, inspect, replay, replay-all)
```

### 5.1 HTTP Client Abstraction (`internal/cli/client.go`)
```go
type RelayClient struct {
    baseURL    string
    apiKey     string
    httpClient *http.Client
}

func NewClient(baseURL, apiKey string, timeout time.Duration) *RelayClient
func (c *RelayClient) Health(ctx context.Context) (*HealthResponse, time.Duration, error)
func (c *RelayClient) GetQueueStats(ctx context.Context) (pending, inFlight int64, err error)
func (c *RelayClient) ListRoutes(ctx context.Context) (map[string]string, error)
func (c *RelayClient) ListDLQ(ctx context.Context, limit int) ([]DLQItemResponse, error)
func (c *RelayClient) InspectRequest(ctx context.Context, id string) (*RequestDetailResponse, error)
func (c *RelayClient) ReplayDLQ(ctx context.Context, id string) error
func (c *RelayClient) ReplayAllDLQ(ctx context.Context) (int, error)
```

All outgoing requests automatically append:
- `X-API-Key: <apiKey>` (if non-empty)
- `Accept: application/json`

When receiving HTTP `401 Unauthorized`, `client.go` returns typed error:
`ErrUnauthorized = errors.New("authentication required: please provide --api-key or set RELAY_API_KEY")`

---

## 6. What is Left to the Builder

1. **Table Column Truncation:** Builder settles exact column widths and truncation threshold for errors in `dlq list` (e.g. 50 characters with `...` ellipsis).
2. **Color Highlights:** Builder applies ANSI escape codes for statuses (`\033[32m` green for `ok`/`healthy`, `\033[33m` yellow for `pending`/`in_flight`, `\033[31m` red for `failed`/`backlog`), with automatic color disabling when `stdout` is redirected or when `NO_COLOR=1` is set.
3. **Integration Test Suite:** Builder implements unit and mock HTTP server tests (`httptest.Server`) in `internal/cli/dlq_test.go`, verifying flag overrides, non-TTY rejection, exit codes, and JSON serialization.

---

## 7. Verification Strategy

1. **Unit & CLI Tests:**
   `go test -v -race ./internal/cli/...`
2. **Static Compilation:**
   `CGO_ENABLED=0 go build -o /dev/null ./cmd/relay`
3. **End-to-End CLI Smoke Test:**
   - Boot temporary relay daemon in test background.
   - Run `relay health` -> verify exit code 0 and valid status output.
   - Run `relay queue` -> verify queue counts.
   - Run `relay dlq replay-all` in non-TTY mode without `-y` -> verify exit code 1.
   - Run with `-y` -> verify exit code 0.
