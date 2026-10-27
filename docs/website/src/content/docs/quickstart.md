---
title: "Quickstart"
description: "Get Simple Relay up and running with a single Docker command or standalone binary in under a minute."
section: "Getting Started"
order: 2
---

Get Simple Relay running and test your first reliable HTTP delivery in under 60 seconds.

## 1. Run Simple Relay

### Option A: One-Line Install (Linux & macOS — Recommended)

Install the precompiled standalone static binary directly without Docker or Go toolchains:

```bash
# Download and install latest release to /usr/local/bin (or ~/.local/bin)
curl -fsSL https://raw.githubusercontent.com/simple-relay/simple-relay/main/install.sh | sh

# Start relay (development mode, zero config)
relay

# Or run with an admin password (production mode)
RELAY_ADMIN_PASSWORD="your-secure-password" relay
```

> [!NOTE] Windows Support
> `install.sh` supports Linux and macOS (`amd64`, `arm64`). For Windows, download precompiled binaries directly from **[GitHub Releases](https://github.com/simple-relay/simple-relay/releases)** or run with Docker.

### Option B: Using Docker

Run the container in daemon mode, mounting a local folder for persistence:

```bash
docker run -d \
  --name simple-relay \
  -p 8080:8080 \
  -v relay-data:/data \
  -e RELAY_DB_PATH=/data/relay.db \
  -e RELAY_ADMIN_PASSWORD=admin \
  ghcr.io/simple-relay/simple-relay:latest
```

### Option C: Precompiled Binaries (Manual / Windows)

Download the archive for your operating system and architecture from **[GitHub Releases](https://github.com/simple-relay/simple-relay/releases)**:
- **Linux:** `simple-relay_<version>_linux_<amd64|arm64>.tar.gz`
- **macOS:** `simple-relay_<version>_darwin_<amd64|arm64>.tar.gz`
- **Windows:** `simple-relay_<version>_windows_amd64.zip`

Extract the binary and execute:

```bash
# Linux / macOS
./simple-relay

# Windows (Command Prompt / PowerShell)
.\simple-relay.exe
```

### Option D: Build from Source (Git Clone or Go Install)

Clone the repository and compile the static binary with pure Go (no CGO required):

```bash
git clone https://github.com/simple-relay/simple-relay.git
cd simple-relay

# Pure Go static build (no CGO required)
CGO_ENABLED=0 go build -o relay ./cmd/relay

# Run the compiled binary
./relay
```

Or install via Go toolchain (Go 1.22+):

```bash
go install github.com/simple-relay/simple-relay/cmd/relay@latest
```

Verify that the relay is healthy:

```bash
curl http://localhost:8080/healthz
# {"status":"ok","version":"1.0.0"}
```

---

## 2. Ingest an Inbound Webhook

Simple Relay provides dedicated route endpoints under `/api/v1/webhook/:route`. Send a test payload:

```bash
curl -i -X POST http://localhost:8080/api/v1/webhook/orders \
  -H "Content-Type: application/json" \
  -d '{"order_id": "ord_99812", "amount": 4200, "currency": "usd"}'
```

Response:
```http
HTTP/1.1 200 OK
Content-Type: application/json

{"status":"queued","request_id":"01j9x0m1p00000000000000000"}
```

The raw bytes are durably persisted to storage before the `200 OK` response was sent. The delivery dispatcher is now forwarding this payload to your destination endpoint in the background.

---

## 3. Test Outbound Proxy with Smart Retries

Route an outgoing request to an upstream service through Simple Relay's synchronous retry proxy (`mode: "sync"`).

Register an outbound route pointing to httpbin:

```bash
curl -X POST http://localhost:8080/api/v1/routes \
  -H "Content-Type: application/json" \
  -d '{
    "name": "httpbin-test",
    "type": "outbound",
    "mode": "sync",
    "path": "/api/v1/relay/httpbin",
    "destination_url": "https://httpbin.org/status/200",
    "max_attempts": 3,
    "timeout_ms": 5000
  }'
```

Now dispatch an outgoing call through the proxy:

```bash
curl -i "http://localhost:8080/api/v1/relay/httpbin"
```

If the upstream server returns a `502 Bad Gateway` or `429 Too Many Requests`, Simple Relay automatically catches the failure and retries using exponential backoff with full jitter (`100ms` base, `10s` max default) until the request succeeds or max attempts are reached. Non-idempotent methods (`POST`, `PATCH`) are safeguarded against unintended duplicate side effects unless an `Idempotency-Key` is supplied or `retry_non_idempotent: true` is enabled on the route.

---

## 4. Open the Embedded Dashboard

Open your browser and navigate to:

```text
http://localhost:8080/ui/
```

You can view:
- Inbound and outbound delivery logs.
- Attempt durations and latency graphs.
- Raw request and response payloads.
- Dead Letter Queue (DLQ) entries with one-click re-delivery.

---

## What's Next?

- Configure **[HMAC Signatures](/docs/stripe-github)** for Stripe or GitHub.
- Set up **[Local Webhook Inspection with Cloudflare Tunnel](/docs/local-webhook-inspection)** for crash-resilient restarts and one-click replay during local development.
- Understand the **[Retries and Dead Letter Queue](/docs/retries-and-dlq)** mechanism.
- Inspect daemon health, queue backlog, and DLQ from your terminal using the **[CLI Reference](/docs/cli-reference)**.
- Check out the complete **[REST API Reference](/docs/api-reference)**.
