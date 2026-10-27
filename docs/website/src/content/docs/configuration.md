---
title: "Configuration Reference"
description: "Complete list of CLI flags and environment variables for Simple Relay."
section: "Reference"
order: 9
---

Simple Relay adheres to the 12-factor application methodology. Configuration options are supplied via command-line flags and environment variables. Command-line flags take precedence over environment variables, which override default values.

---

## Server Daemon Settings Matrix

When running Simple Relay as a background daemon (`relay` or `relay serve`), the following flags and environment variables are supported. Both standard POSIX flags (`--host`) and single-dash flags (`-host`) are accepted:

| CLI Flag | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `--host`, `-host` | `RELAY_HOST` | `0.0.0.0` | Server host address. |
| `--port`, `-port` | `RELAY_PORT` (or `PORT`) | `8080` | Server HTTP port. |
| `--db`, `-db` | `RELAY_DB_PATH` | `relay.db` | Path to persistent SQLite database file. |
| `--log-level`, `-log-level` | `RELAY_LOG_LEVEL` | `info` | Logging verbosity: `debug`, `info`, `warn`, `error`. |
| `--workers`, `-workers` | `RELAY_WORKER_CONCURRENCY` | `20` | Outbound worker pool concurrency. |
| `--dispatcher-buffer`, `-dispatcher-buffer` | `RELAY_DISPATCHER_BUFFER` | `0` (dynamic: `2 * workers`) | Dispatcher jobs channel buffer capacity. |
| `--dispatcher-poll-ms`, `-dispatcher-poll-ms` | `RELAY_DISPATCHER_POLL_MS` | `500` | Dispatcher polling interval in milliseconds. |
| `--cleaner-interval`, `-cleaner-interval` | `RELAY_CLEANER_INTERVAL` | `1h` | Storage retention cleaner execution interval. |
| `--drain-timeout`, `-drain-timeout` | `RELAY_DRAIN_TIMEOUT` | `30s` | Graceful shutdown drain timeout. |
| `--trust-proxy`, `-trust-proxy` | `RELAY_TRUST_PROXY` | `false` | Trust `X-Forwarded-For` and `X-Real-IP` headers from reverse proxy for client IP extraction and login rate limiting. |
| `--sqlite-sync`, `-sqlite-sync` | `RELAY_SQLITE_SYNC` | `NORMAL` | SQLite WAL `PRAGMA synchronous` durability mode (`NORMAL` or `FULL`). Set to `FULL` on single-node bare-metal hosts without UPS or Litestream replication. |
| `--max-body-size-mb`, `-max-body-size-mb` | `RELAY_MAX_BODY_SIZE_MB` | `10` | Maximum allowable HTTP request body size in megabytes. |
| — | `RELAY_RETENTION_SUCCESS_DAYS` | `7` | Retention period in days for successfully delivered records. |
| — | `RELAY_RETENTION_FAILED_DAYS` | `30` | Retention period in days for failed/dead-lettered records. |
| — | `RELAY_ADMIN_PASSWORD` | `""` | Admin authentication password. When empty (`""`), unauthenticated access is restricted to loopback (`127.0.0.1` / `::1`); remote callers must provide `X-API-Key`. |
| — | `RELAY_BATCH_SIZE` | `50` | Maximum requests grouped per SQLite WAL write transaction. |
| — | `RELAY_BATCH_TIMEOUT_MS` | `10` | Maximum batch linger duration before flushing to SQLite. |

---

## CLI Subcommands Settings Matrix

When executing operator subcommands (`relay health`, `relay queue`, `relay dlq`), the following global persistent flags and environment variables configure remote daemon connectivity and output formatting:

| CLI Flag | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `-s, --server` | `RELAY_SERVER_URL` | `http://127.0.0.1:8080` | Target Simple Relay daemon HTTP/HTTPS address. |
| `-k, --api-key` | `RELAY_API_KEY` | `""` | Admin API key for protected management endpoints. |
| `-o, --output` | `RELAY_OUTPUT` | `table` | Output format: `table` (human-readable) or `json` (scripting). |
| `--timeout` | `RELAY_TIMEOUT` | `10s` | HTTP client request timeout duration. |

> [!TIP] Detailed Subcommand Documentation
> For subcommand recipes, interactive confirmation overrides, and orchestration probe configurations, see the **[CLI Reference](/docs/cli-reference)**.

---

## Dynamic Route Configuration

Routes in Simple Relay are managed dynamically at runtime via the REST API (`/api/v1/routes`) or the embedded Web UI, avoiding static configuration files and service restarts. Routes are persisted in SQLite and hot-reloaded into memory.

### Route Creation Example (`POST /api/v1/routes`)

```bash
curl -X POST http://localhost:8080/api/v1/routes \
  -H "Content-Type: application/json" \
  -d '{
    "name": "stripe-inbound",
    "type": "inbound",
    "mode": "async",
    "path": "/api/v1/webhook/stripe",
    "destination_url": "http://internal-backend:8000/webhooks/stripe",
    "signature_provider": "stripe",
    "signature_secret": "whsec_...",
    "max_attempts": 10,
    "timeout_ms": 15000,
    "retry_backoff_strategy": "exp",
    "base_backoff_ms": 5000,
    "max_backoff_ms": 900000,
    "retry_non_idempotent": false
  }'
```
