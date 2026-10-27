---
title: "Embedded Web UI"
description: "Inspect requests, analyze delivery attempts, and replay dead letters directly from the built-in browser dashboard."
section: "Reference"
order: 11
---

Simple Relay packages an administrative Web UI directly inside the Go binary via `go:embed`. It requires zero external Node.js dependencies or separate frontend server processes.

Access the dashboard at:

```text
http://localhost:8080/ui/
```

---

## Key Features of the Dashboard

### 1. Real-time Delivery Log
Inspect all inbound webhooks and outbound proxy calls:
- Chronologically sorted by monotonic ULID timestamp.
- Filter by status (`QUEUED`, `DELIVERING`, `DELIVERED`, `FAILED`).
- Visual latency badges showing duration per delivery attempt.

### 2. Payload & Header Inspector
Clicking any request opens an inspection modal showing:
- Bit-for-bit raw incoming request headers and payload body.
- Forwarded destination URL.
- Upstream response headers and response body (truncated to safe size limits).

### 3. Attempt History & Error Traces
When upstream services fail, inspect:
- Number of attempts made.
- Exact backoff delay calculated with full jitter.
- Error messages (e.g. connection refused, TLS handshake timeout, 502 Bad Gateway).

### 4. One-Click Request Replay & DLQ Triage
- **Replay Any Request:** Click the **Replay** button on any row in the Requests Explorer or inside the inspection drawer to requeue deliveries immediately. This allows re-running previously successful webhooks (`200 OK`) against your backend after making code adjustments—see the **[Local Webhook Inspection Recipe](/docs/local-webhook-inspection)**.
- **Dead Letter Queue (DLQ):** Deliveries that exhausted all retry policies are isolated in the DLQ view with individual and bulk replay buttons.

### 5. Authentication & Route Secret Masking
- **Loopback Dev Mode vs. Protected Mode:** When running locally on loopback (`127.0.0.1` / `localhost`) with `RELAY_ADMIN_PASSWORD` unset, the dashboard opens directly without a login prompt. When `RELAY_ADMIN_PASSWORD` is configured, the dashboard presents a rate-limited session login screen (`relay_session` HTTP-only cookie).
- **Masked Route Secrets:** Inbound route `signature_secret` values are automatically masked in API responses (`whse*****9a1b` or `********`). Editing a route in the UI safely preserves the existing secret unless you explicitly type a new secret to rotate it.

### 6. Interactive Route Configuration & Backoff Tuning
Create and modify routes dynamically:
- Configure per-route `base_backoff_ms` and `max_backoff_ms` overrides with mode-aware pre-filling (`100ms`–`10s` for sync proxy, `5s`–`15m` for async background delivery).
- Toggle `Retry Non-Idempotent Requests` on synchronous outbound routes to permit safe `POST`/`PATCH` retries.
- View the active backoff policy (`100ms–10s` or `5s–15m`) directly in the Routes table Policy column.
