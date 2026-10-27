---
title: "Recipe: Handling 429 & Rate Limits"
description: "Gracefully survive upstream API rate limits with automatic Retry-After parsing and exponential jitter."
section: "Recipes & Guides"
order: 7
---

Modern SaaS APIs (e.g., OpenAI, Anthropic, Shopify, Twitter, Slack) strictly enforce rate limits. When your application exceeds quota, upstream returns:

```http
HTTP/1.1 429 Too Many Requests
Retry-After: 12
```

Failing to respect rate limits or immediately retrying in a tight loop can lead to temporary account suspension or permanent IP throttling.

---

## How Simple Relay Handles 429 Responses

When routing outbound traffic through `/api/v1/relay/:route_name` (synchronous proxy) or `/api/v1/jobs/:route_name` (asynchronous background delivery):

1. **Automatic Status Detection:** Responses with status code `429` are flagged as transient retryable conditions.
2. **`Retry-After` Header Parsing:**
   - **Integer Seconds:** e.g., `Retry-After: 30` -> Simple Relay sleeps for 30 seconds (plus subtle uniform random jitter to avoid synchronicity). Capped at `30s` by default for sync proxy and `15m` for async delivery (or extended by route `max_backoff_ms`).
   - **HTTP-Date:** e.g., `Retry-After: Wed, 21 Oct 2026 07:28:00 GMT` -> Simple Relay calculates the exact duration delta between current clock time and target timestamp.
3. **Fallback to Exponential Backoff:** If the upstream server returns `429` without a `Retry-After` header, Simple Relay automatically falls back to standard exponential backoff with full jitter (`100ms`–`10s` default for sync, `5s`–`15m` default for async, or per-route `base_backoff_ms` / `max_backoff_ms`).
4. **Idempotency Guardrail on Sync Retries:** For non-idempotent HTTP methods (`POST`, `PATCH`), retries on `/api/v1/relay/:route_name` require an `Idempotency-Key` / `X-Relay-Idempotency-Key` header or `"retry_non_idempotent": true` on the route.
5. **Context Respect:** If the synchronous caller terminates connection or hits its total timeout, Simple Relay cancels the upstream wait immediately.

---

## Route Configuration Example

When registering a route with sync retry proxying via `POST /api/v1/routes`:

```bash
curl -X POST http://localhost:8080/api/v1/routes \
  -H "X-API-Key: sr_live_..." \
  -H "Content-Type: application/json" \
  -d '{
    "name": "openai-proxy",
    "type": "outbound",
    "mode": "sync",
    "path": "/api/v1/relay/openai",
    "destination_url": "https://api.openai.com/v1/chat/completions",
    "max_attempts": 8,
    "timeout_ms": 60000,
    "retry_backoff_strategy": "exp",
    "base_backoff_ms": 200,
    "max_backoff_ms": 15000,
    "retry_non_idempotent": true
  }'
```

---

## Practical Example: Calling OpenAI Through Simple Relay

```bash
curl -X POST "http://localhost:8080/api/v1/relay/openai" \
  -H "X-API-Key: sr_live_..." \
  -H "Idempotency-Key: chat_req_12345" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Explain HTTP idempotency in one sentence."}]
  }'
```

If OpenAI returns a `429` due to organization TPM/RPM limits, Simple Relay holds the connection, sleeps for the duration requested by OpenAI's `Retry-After` header, and retries seamlessly. Your backend application receives a clean `200 OK` as if the rate limit never happened.
