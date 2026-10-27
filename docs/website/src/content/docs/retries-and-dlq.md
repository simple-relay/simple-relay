---
title: "Retries & Dead Letter Queue"
description: "Exponential backoff algorithms, full uniform jitter, and Dead Letter Queue (DLQ) inspection and replay."
section: "Core Concepts"
order: 5
---

When external networks or downstream microservices fail, naive retry strategies (such as immediate looping or fixed intervals) frequently trigger **Thundering Herd** disasters that overwhelm recovering services.

Simple Relay incorporates battle-tested exponential backoff, randomized full jitter, and a built-in Dead Letter Queue (DLQ).

---

## 1. Exponential Backoff with Full Jitter

Simple Relay implements the standard Full Jitter algorithm recommended by distributed systems engineering:

```text
┌──────────────────────────────────────────────────────────────────┐
│ Full Jitter Exponential Backoff                                  │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│   cap   = min( max_interval, initial_interval * 2^attempt )     │
│   sleep = random_between( 0, cap )                               │
│                                                                  │
│   Example (initial = 1s, max = 60s):                             │
│   • Attempt 1: random(0s .. 2s)   [avg: ~1.0s]                  │
│   • Attempt 2: random(0s .. 4s)   [avg: ~2.0s]                  │
│   • Attempt 3: random(0s .. 8s)   [avg: ~4.0s]                  │
│   • Attempt 4: random(0s .. 16s)  [avg: ~8.0s]                  │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

### Why Full Jitter?

Without jitter, if an upstream API experiences a 10-second blip and 500 requests fail simultaneously, all 500 clients retry at exactly the same timestamps (2s, 4s, 8s), creating periodic traffic spikes that knock the recovering server offline again.

Full jitter scatters retry execution evenly across time, converting synchronized shockwaves into a smooth, manageable background trickle.

---

## 2. Retry Exhaustion and the Dead Letter Queue (DLQ)

Simple Relay uses separate default backoff windows for synchronous proxying versus asynchronous background delivery, and supports optional per-route overrides:
- **Synchronous Proxy (`mode: "sync"`):** `100ms` base backoff, `10s` max backoff (`30s` max `Retry-After`) so open client HTTP connections remain responsive.
- **Asynchronous Delivery (`mode: "async"`):** `5s` base backoff, `15m` (`900000ms`) max backoff (`15m` max `Retry-After`) so transient downstream container restarts or database failovers do not exhaust all attempts within seconds.

Every route enforces bounded execution:
- `max_attempts` (default: `5`, configurable up to any positive integer)
- `base_backoff_ms` (default: `0` = use engine profile default: `100ms` sync / `5000ms` async)
- `max_backoff_ms` (default: `0` = use engine profile default: `10000ms` sync / `900000ms` async)
- `retry_non_idempotent` (default: `false`; on sync routes, controls whether `POST`/`PATCH` calls without an `Idempotency-Key` header are retried)
- `timeout_ms` (per-attempt HTTP timeout, default: `10000` ms)

When all retry attempts are exhausted without receiving a successful response code (`2xx`), Simple Relay marks the delivery as `dlq` and places it in the **Dead Letter Queue (DLQ)**.

---

## 3. Replaying Deliveries from the DLQ

Simple Relay never discards failed payloads. You can inspect failures and trigger replays via the Web UI or REST API.

### Replay via Web UI

Navigate to `http://localhost:8080/ui/`, filter by status `FAILED` or navigate to the DLQ tab, select any delivery, inspect the error message and status code history, and click **Replay Delivery**.

### Replay a Single Delivery via REST API

```bash
curl -X POST http://localhost:8080/api/v1/requests/01j9x0m1p00000000000000000/replay
```

Response:
```json
{
  "status": "requeued",
  "request_id": "01j9x0m1p00000000000000000",
  "attempt_count": 0
}
```

### Bulk Replay All Failed Deliveries

Once downstream services have recovered from an outage:

```bash
curl -X POST http://localhost:8080/api/v1/dlq/replay \
  -H "Content-Type: application/json" \
  -d '{"route": "stripe-inbound", "limit": 100}'
```

### Replay via `relay` CLI

For terminal workflows, scripts, or automated remediation:

```bash
# Replay a specific dead-lettered message
relay dlq replay 01j9x0m1p00000000000000000

# Bulk replay all failed DLQ messages without confirmation prompt
relay dlq replay-all -y
```

---

## Next Steps

- Check out **[Handling 429 Rate Limits](/docs/rate-limits-429)**.
- Learn how to inspect and replay any request (including 200 OK) during local development in **[Local Webhook Inspection](/docs/local-webhook-inspection)**.
- Manage and triage DLQ via the terminal with the **[CLI Reference](/docs/cli-reference)**.
- Review **[REST API Specifications](/docs/api-reference)** for all DLQ endpoints.
