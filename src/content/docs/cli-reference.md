---
title: "CLI Reference"
description: "Embedded operator CLI subcommands for daemon health checks, queue depth monitoring, and automated DLQ replay."
section: "Reference"
order: 12
---

Operator inspection tooling is embedded directly into the primary `relay` binary. Running `relay` without subcommands (or `relay serve`) launches the reliability daemon; running subcommands (`health`, `status`, `queue`, `dlq`) executes operator inspection and triage tasks from the terminal without browser access.

Rather than opening or locking the SQLite database directly, CLI subcommands interact exclusively with Simple Relay's REST API over HTTP/HTTPS. This architecture prevents SQLite lock contention (`SQLITE_BUSY`), eliminates shared memory overhead, and allows operators to inspect or manage remote daemons across networks or from lightweight container environments.

---

## 1. Installation & Build

Simple Relay is distributed as a single static binary with CGO disabled:

### One-Line Install (Linux & macOS — Recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/simple-relay/simple-relay/main/install.sh | sh
```

### Compile from Source

```bash
# Build the unified static binary
CGO_ENABLED=0 go build -o relay ./cmd/relay
```

### Install via Go Toolchain

```bash
go install github.com/simple-relay/simple-relay/cmd/relay@latest
```

---

## 2. Global Flags & Environment Variables

All `relay` CLI subcommands accept global flags and support configuration through environment variables:

| CLI Flag | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `-s, --server` | `RELAY_SERVER_URL` | `http://127.0.0.1:8080` | Simple Relay daemon base URL. |
| `-k, --api-key` | `RELAY_API_KEY` | `""` | Admin API key for authenticated endpoints. |
| `-o, --output` | `RELAY_OUTPUT` | `table` | Output format: `table` (human-readable) or `json` (scripting). |
| `--timeout` | `RELAY_TIMEOUT` | `10s` | HTTP request client timeout duration (e.g., `5s`, `30s`). |

---

## 3. Core Commands & Usage

### 3.1 Daemon Health & Latency (`health` / `status`)

Probes the `/healthz` endpoint, measures end-to-end round-trip network latency, and returns standardized POSIX exit codes suitable for orchestration probes:

```bash
relay health
# Or alias
relay status
```

#### Example Output (Table):
```
STATUS   LATENCY   SERVER TIME                   URL
OK       0.45ms    2026-10-15T01:25:00.000000Z   http://127.0.0.1:8080
```

#### Example Output (JSON):
```bash
relay health -o json
```
```json
{
  "status": "ok",
  "latency_ms": 0,
  "timestamp": "2026-10-15T01:25:00.000000Z",
  "url": "http://127.0.0.1:8080"
}
```

#### Exit Codes:

| Exit Code | Condition | Meaning |
| :--- | :--- | :--- |
| `0` | **Healthy** | Daemon returned HTTP 200 and operational status `ok`. |
| `1` | **Unreachable** | Connection refused, DNS failure, or client timeout. |
| `2` | **Degraded** | Daemon reachable but reported unhealthy or degraded state. |

---

### 3.2 Queue Depth & Backlog Monitoring (`queue`)

Inspects the number of pending and active in-flight delivery jobs across background dispatcher workers:

```bash
relay queue
```

#### Example Output (Table):
```
QUEUE DEPTH   PENDING   IN-FLIGHT   STATUS
14            10        4           HEALTHY
```

#### Example Output (JSON):
```bash
relay queue -o json
```
```json
{
  "queue_depth": 14,
  "pending": 10,
  "in_flight": 4,
  "status": "HEALTHY"
}
```

> [!NOTE] Backlog Alerting
> If total queue depth (`pending + in_flight`) reaches or exceeds **1,000 requests**, `relay queue` marks status as `BACKLOG` and exits with **code 2**. This enables automated autoscaling or alert triggering in monitoring pipelines without custom parsing scripts.

---

### 3.3 Dead Letter Queue Inspection (`dlq`)

Inspect and triage dead-lettered requests that have exhausted all configured retry attempts.

#### List DLQ Records

```bash
relay dlq list
relay dlq list --limit 100
```

Example Output:
```
REQUEST ID                    ROUTE           ATTEMPTS   FAILED AT             LAST ERROR
01j9x0m1p00000000000000001   stripe-inbound  5          2026-10-15 01:12:04   upstream returned HTTP 500 Internal Server...
01j9x0z87q00000000000000002   github-webhook  5          2026-10-15 01:14:22   connection reset by peer
```

#### Inspect Detailed Record & Attempt Audit Trail

```bash
relay dlq inspect 01j9x0m1p00000000000000001
```

Example Output:
```
Request ID:    01j9x0m1p00000000000000001
Route ID:      stripe-inbound
Method:        POST
Destination:   http://internal-backend:8000/webhooks/stripe
Status:        FAILED
Created At:    2026-10-15T01:10:00Z
Last Error:    upstream returned HTTP 500 Internal Server Error

Attempt History:
#   ATTEMPTED AT          STATUS CODE   DURATION   ERROR
1   2026-10-15 01:10:01   500           23ms       upstream returned HTTP 500
2   2026-10-15 01:10:03   500           19ms       upstream returned HTTP 500
3   2026-10-15 01:10:07   500           21ms       upstream returned HTTP 500
4   2026-10-15 01:10:15   500           25ms       upstream returned HTTP 500
5   2026-10-15 01:10:31   500           20ms       upstream returned HTTP 500
```

---

### 3.4 Replaying DLQ Messages (`dlq replay`)

#### Replay Single Request

Requeues a specific dead-lettered request for immediate retry:

```bash
relay dlq replay 01j9x0m1p00000000000000001
```

```
Successfully requeued request 01j9x0m1p00000000000000001 for delivery.
```

#### Replay All Failed Requests (Bulk Replay)

Requeues all messages currently in the DLQ. When run in an interactive terminal, `relay` prompts for confirmation:

```bash
relay dlq replay-all
# Found 42 items in DLQ. Requeue all for delivery? [y/N]: y
# Successfully requeued 42 DLQ requests for delivery.
```

In CI/CD, cron jobs, or headless deployment scripts, pass `--yes` (`-y`) to bypass confirmation:

```bash
relay dlq replay-all -y
```

---

### 3.4 Request Replay for Local Debugging & Triage (`request replay`)

While `relay dlq replay` is intentionally restricted to exhausted messages in the Dead Letter Queue, `relay request replay` allows operators and developers to requeue **any historical request** regardless of its lifecycle status (`success`, `failed`, or `dlq`):

```bash
relay request replay <request_id>
# Or alias
relay requests replay <request_id>
```

#### Example Usage:
```bash
relay request replay 01j9y9success001
# Output:
# Successfully requeued request 01j9y9success001 for delivery.
```

#### Scripting with JSON Output:
```bash
relay request replay 01j9y9success001 -o json
```
```json
{
  "request_id": "01j9y9success001",
  "status": "requeued"
}
```

> [!TIP] Local Webhook Debugging
> Replaying `success` requests is particularly useful during local webhook development alongside Cloudflare Tunnel. When testing code modifications in your backend, you can re-run a previous webhook delivery with one CLI command without re-triggering new events from external SaaS dashboards. See the **[Cloudflare Tunnel Inspection Recipe](/docs/local-webhook-inspection)** for complete setup instructions.

---

## 4. Headless & Container Probe Recipes

Because Simple Relay compiles into a single, static CGO-free binary, container images only need the `relay` binary for both serving traffic and executing health/readiness probes (no `curl`, `wget`, or `jq` needed).

### Docker Container Health Check

Add to your `Dockerfile`:

```dockerfile
HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
  CMD ["/usr/local/bin/relay", "health"]
```

### Kubernetes Probes

Configure Kubernetes `livenessProbe` and `readinessProbe` directly in your Pod manifest:

```yaml
spec:
  containers:
    - name: relay
      image: ghcr.io/simple-relay/simple-relay:latest
      livenessProbe:
        exec:
          command:
            - /usr/local/bin/relay
            - health
        initialDelaySeconds: 3
        periodSeconds: 10
        timeoutSeconds: 2
        failureThreshold: 3
      readinessProbe:
        exec:
          command:
            - /usr/local/bin/relay
            - queue
        initialDelaySeconds: 2
        periodSeconds: 5
        timeoutSeconds: 2
        failureThreshold: 2
```

> [!TIP] Zero-Downtime Backlog Protection
> The `readinessProbe` using `relay queue` automatically fails (exit code 2) if queue depth exceeds 1,000 jobs, prompting Kubernetes Service routing to temporarily stop sending new inbound requests to an overloaded replica until its queue drains.
