---
title: "Recipe: Stripe & GitHub Webhook HMAC"
description: "Configure automatic cryptographic HMAC signature verification for Stripe and GitHub webhooks."
section: "Recipes & Guides"
order: 6
---

Simple Relay features native signature adapters for major webhook providers. It verifies incoming cryptographic signatures before saving the payload or sending acknowledgments, shielding your application from invalid or malicious requests.

---

## 1. Stripe Webhook Verification

Stripe secures webhooks using HMAC-SHA256 signatures passed in the `Stripe-Signature` header, which includes a UNIX timestamp `t` and signature `v1`.

### Route Configuration

Register the Stripe inbound route via `POST /api/v1/routes`:

```bash
curl -X POST http://localhost:8080/api/v1/routes \
  -H "Content-Type: application/json" \
  -d '{
    "name": "stripe-payments",
    "type": "inbound",
    "mode": "async",
    "path": "/api/v1/webhook/stripe",
    "destination_url": "http://app-backend:8000/webhooks/stripe",
    "signature_provider": "stripe",
    "signature_secret": "whsec_your_stripe_webhook_secret"
  }'
```

### How Simple Relay Verifies Stripe
1. Extracts `t` (timestamp) and `v1` (signature) from `Stripe-Signature`.
2. Validates that `|current_time - t| <= tolerance_seconds` (default 300s) to defeat replay attacks.
3. Computes `HMAC-SHA256(secret, t + "." + raw_body)`.
4. Executes constant-time equality check (`subtle.ConstantTimeCompare`) against `v1`.
5. If invalid, rejects with `401 Unauthorized` without touching your database or internal application.

---

## 2. GitHub Webhook Verification

GitHub transmits webhook signatures via the `X-Hub-Signature-256` header formatted as `sha256=<hex_digest>`.

### Route Configuration

Register the GitHub inbound route via `POST /api/v1/routes`:

```bash
curl -X POST http://localhost:8080/api/v1/routes \
  -H "Content-Type: application/json" \
  -d '{
    "name": "github-events",
    "type": "inbound",
    "mode": "async",
    "path": "/api/v1/webhook/github",
    "destination_url": "http://app-backend:8000/webhooks/github",
    "signature_provider": "github",
    "signature_secret": "your_github_webhook_secret"
  }'
```

### Verification Logic
Simple Relay calculates `HMAC-SHA256(secret, raw_body)` and matches it using constant-time comparison against the hex digest in `X-Hub-Signature-256`.

---

## 3. Generic HMAC-SHA256 Webhooks

For custom webhooks (e.g., Shopify, internal microservices), register a route using `hmac_sha256`:

```bash
curl -X POST http://localhost:8080/api/v1/routes \
  -H "Content-Type: application/json" \
  -d '{
    "name": "custom-events",
    "type": "inbound",
    "mode": "async",
    "path": "/api/v1/webhook/custom",
    "destination_url": "http://app-backend:8000/webhooks/custom",
    "signature_provider": "hmac_sha256",
    "signature_secret": "your_custom_secret"
  }'
```

---

## Testing Verification with `curl`

Generate a test HMAC-SHA256 signature and test your endpoint locally:

```bash
SECRET="whsec_test_secret_123"
BODY='{"event":"payment_intent.succeeded"}'
SIGNATURE=$(echo -n "$BODY" | openssl dgst -sha256 -hmac "$SECRET" | sed 's/^.* //')

curl -i -X POST http://localhost:8080/api/v1/webhook/custom \
  -H "X-Signature-SHA256: $SIGNATURE" \
  -H "Content-Type: application/json" \
  -d "$BODY"
```
