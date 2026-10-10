# Signature Verification Adapters Design Specification

> **Document:** Design Specification  
> **Status:** Approved  
> **Author:** pmlonng  
> **Date:** 2026-10-09  
> **Target Package:** `internal/signature`, `internal/api`, `internal/model`  
> **Related Issue / Task:** Task 2.1.2 (`docs/roadmap.md`)  

---

## 1. Executive Summary

This specification defines the cryptographic signature verification architecture for the inbound webhook ingestion pipeline of **Simple Relay**.

The primary objective is to guarantee payload authenticity and tamper-resistance **before** any disk persistence or downstream processing takes place, strictly maintaining the **Durability Golden Rule**:

$$\text{Receive Raw Bytes} \longrightarrow \mathbf{Validate\ Signature} \longrightarrow \text{Persist to SQLite WAL} \longrightarrow \text{ACK 200} \longrightarrow \text{Deliver}$$

Invalid, forged, or unauthenticated requests are rejected immediately at the HTTP boundary with `401 Unauthorized`, shielding SQLite WAL storage and background dispatchers from fraudulent payload bloat and denial-of-service attempts.

---

## 2. Core Architectural Decisions & Invariants

1. **Zero External Cryptographic Dependencies:**
   - Use Go standard library (`crypto/hmac`, `crypto/sha256`, `crypto/subtle`, `encoding/hex`, `encoding/base64`).
   - Zero CGO (`CGO_ENABLED=0`).
2. **Timing Attack Protection:**
   - All signature comparisons MUST use `crypto/subtle.ConstantTimeCompare` to prevent timing side-channel vulnerabilities.
3. **Unmutated Raw Byte Preservations:**
   - Signatures are verified against the exact raw byte slice read from the network (`http.MaxBytesReader`), preserving carriage returns, formatting, and raw byte ordering.
4. **Adapter Pattern with Factory Registry:**
   - Isolated in `internal/signature`.
   - Clear contract decouples verification mechanics from HTTP handlers.
5. **Fail-Fast Validation:**
   - `model.Route.Validate()` strictly disallows routes with `signature_provider != "none"` when `signature_secret` is blank.
   - If encountered at runtime, handler aborts with `500 Internal Server Error` to prevent unauthenticated data ingestion due to misconfiguration.
6. **Replay Window Policy:**
   - For Stripe, verify HMAC hash validity across candidate signatures (`v1`) without enforcing an arbitrary timestamp tolerance window, eliminating false dropouts caused by clock drift or delayed webhooks.

---

## 3. Package Architecture & Interfaces (`internal/signature`)

### 3.1. Package Structure
```
internal/signature/
├── verifier.go          # Core Verifier interface, sentinel errors, and factory
├── stripe.go            # Stripe HMAC-SHA256 adapter
├── github.go            # GitHub HMAC-SHA256 adapter (X-Hub-Signature-256)
├── generic.go           # Generic HMAC-SHA256 adapter (Hex/Base64, header auto-detect)
└── verifier_test.go     # RFC test vectors & boundary unit tests
```

### 3.2. Interface Definition & Sentinel Errors
```go
package signature

import (
	"errors"
	"net/http"

	"github.com/simple-relay/simple-relay/internal/model"
)

var (
	ErrMissingSignatureHeader = errors.New("missing signature header")
	ErrInvalidSignatureFormat = errors.New("invalid signature format")
	ErrSignatureMismatch      = errors.New("signature mismatch")
	ErrUnsupportedProvider    = errors.New("unsupported signature provider")
	ErrMissingSecret          = errors.New("signature secret is required")
)

// Verifier defines the contract for verifying webhook authenticity against raw payloads.
type Verifier interface {
	Verify(header http.Header, secret string, rawBody []byte) error
}
```

### 3.3. Factory Function
```go
// ForProvider returns the Verifier implementation corresponding to the given provider.
func ForProvider(provider model.SignatureProvider) (Verifier, error) {
	switch provider {
	case model.SignatureProviderNone, "":
		return nil, nil
	case model.SignatureProviderStripe:
		return defaultStripeVerifier, nil
	case model.SignatureProviderGitHub:
		return defaultGitHubVerifier, nil
	case model.SignatureProviderHMACSHA256:
		return defaultGenericHMACVerifier, nil
	default:
		return nil, ErrUnsupportedProvider
	}
}
```

---

## 4. Adapters Implementation Specification

### 4.1. Stripe Adapter (`StripeVerifier`)
- **Header:** `Stripe-Signature` (case-insensitive lookup via `header.Get("Stripe-Signature")`).
- **Header Format:** Key-value pairs delimited by commas:
  ```
  t=1614295982,v1=5257a869e7ece225950ee3a396d229e3b79600673313d4037238432903323d4f,v0=...
  ```
- **Algorithm:**
  1. If header is empty, return `ErrMissingSignatureHeader`.
  2. Parse key-value tokens splitting by comma `,`. Collect `t` (timestamp string) and all `v1` values (supports multiple `v1` signatures during key rotation).
  3. If `t` is missing or no `v1` signatures exist, return `ErrInvalidSignatureFormat`.
  4. Compute expected HMAC:
     $$\text{signedPayload} = t + \text{"."} + \text{rawBody}$$
     $$\text{expectedMac} = \text{hex}(\text{HMAC-SHA256}(\text{secret}, \text{signedPayload}))$$
  5. Iterate over candidate `v1` signatures. Perform comparison using `subtle.ConstantTimeCompare([]byte(expectedMac), []byte(candidate))`.
  6. If at least one match equals 1, return `nil`. Otherwise, return `ErrSignatureMismatch`.

### 4.2. GitHub Adapter (`GitHubVerifier`)
- **Header:** `X-Hub-Signature-256` (fallback to `X-Hub-Signature` only if SHA256 header absent).
- **Header Format:** `sha256=<64-char-hex-digest>`.
- **Algorithm:**
  1. Retrieve header value. If missing, return `ErrMissingSignatureHeader`.
  2. Validate prefix `sha256=`. Strip prefix to get hex digest. If prefix missing or hex length != 64, return `ErrInvalidSignatureFormat`.
  3. Compute HMAC:
     $$\text{expectedMac} = \text{hex}(\text{HMAC-SHA256}(\text{secret}, \text{rawBody}))$$
  4. Compare with `subtle.ConstantTimeCompare([]byte(expectedMac), []byte(hexDigest))`. If 1, return `nil`; else return `ErrSignatureMismatch`.

### 4.3. Generic HMAC-SHA256 Adapter (`GenericHMACVerifier`)
- **Candidate Headers (Priority Order):**
  1. `X-Signature`
  2. `X-Hub-Signature-256`
  3. `X-Signature-SHA256`
- **Format Auto-Detection:**
  1. Retrieve first present header. If none present, return `ErrMissingSignatureHeader`.
  2. Strip optional algorithm prefixes: `sha256=`, `SHA256=`.
  3. Trim whitespace.
  4. Compute raw expected HMAC bytes:
     $$\text{expectedBytes} = \text{HMAC-SHA256}(\text{secret}, \text{rawBody})$$
  5. Signature comparison:
     - **Hex Mode:** If candidate is 64 hex characters:
       $$\text{expectedHex} = \text{hex}(\text{expectedBytes})$$
       Compare using `subtle.ConstantTimeCompare([]byte(expectedHex), []byte(strings.ToLower(candidate)))`.
     - **Base64 Mode:** If candidate decodes as 32-byte Base64 (Standard or URL-Safe):
       Decode candidate into `candidateBytes`.
       Compare using `subtle.ConstantTimeCompare(expectedBytes, candidateBytes)`.
     - If neither valid, return `ErrInvalidSignatureFormat`.
  6. Return `nil` if match found, else `ErrSignatureMismatch`.

---

## 5. Pipeline Integration (`internal/api` & `internal/model`)

### 5.1. Route Model Validation (`internal/model/route.go`)
Update `Route.Validate()`:
```go
if r.Type == RouteTypeInbound {
    switch r.SignatureProvider {
    case SignatureProviderNone, "":
        // No signature required
    case SignatureProviderStripe, SignatureProviderGitHub, SignatureProviderHMACSHA256:
        if strings.TrimSpace(r.SignatureSecret) == "" {
            return ErrRouteSignatureSecretRequired
        }
    default:
        return ErrRouteInvalidSignatureProvider
    }
}
```

### 5.2. Inbound Webhook Ingestion Handler (`internal/api/handler_webhook.go`)
Insert verification gate immediately after body extraction and before batch persistence:

```go
// Verify signature if required
if route.SignatureProvider != model.SignatureProviderNone && route.SignatureProvider != "" {
    if strings.TrimSpace(route.SignatureSecret) == "" {
        logger.Error("route misconfiguration: missing signature secret", "route", routeName)
        writeJSONError(w, http.StatusInternalServerError, "route signature misconfiguration")
        return
    }

    verifier, err := signature.ForProvider(route.SignatureProvider)
    if err != nil {
        logger.Error("unsupported signature provider configured on route", "provider", route.SignatureProvider, "route", routeName)
        writeJSONError(w, http.StatusInternalServerError, "unsupported signature provider")
        return
    }

    if err := verifier.Verify(r.Header, route.SignatureSecret, bodyBytes); err != nil {
        logger.Warn("webhook signature verification failed",
            "route", routeName,
            "provider", route.SignatureProvider,
            "error", err,
        )
        writeJSONError(w, http.StatusUnauthorized, "invalid webhook signature")
        return
    }
}
```

---

## 6. Future Provider Roadmap Expansion (`docs/roadmap.md`)

The following providers are planned for upcoming milestones and are tracked in the project backlog:
1. **Shopify Adapter:** `X-Shopify-Hmac-Sha256` (Base64-encoded HMAC-SHA256).
2. **Slack Adapter:** `X-Slack-Signature` (`v0=<hex>` over `v0:${timestamp}:${body}`).
3. **Standard Webhooks / Svix Adapter:** `webhook-signature` specification.
4. **Asymmetric Signatures (v1.0):** Public key verification (Ed25519, RSA SHA-256) for AWS SNS and DocuSign.

---

## 7. Testing & Verification Plan

### 7.1. Unit Testing (`internal/signature/verifier_test.go`)
- **Stripe:**
  - Standard payload with single `v1` signature $\rightarrow$ PASS.
  - Multi-key rotation scenario (`v1=old,v1=new`) $\rightarrow$ PASS.
  - Tampered payload or wrong secret $\rightarrow$ `ErrSignatureMismatch`.
  - Malformed header (missing `t` or missing `v1`) $\rightarrow$ `ErrInvalidSignatureFormat`.
  - Missing header $\rightarrow$ `ErrMissingSignatureHeader`.
- **GitHub:**
  - Official GitHub test vectors with `sha256=` prefix $\rightarrow$ PASS.
  - Invalid hash $\rightarrow$ `ErrSignatureMismatch`.
  - Missing prefix or wrong length $\rightarrow$ `ErrInvalidSignatureFormat`.
  - Missing header $\rightarrow$ `ErrMissingSignatureHeader`.
- **Generic HMAC-SHA256:**
  - Hex lowercase and uppercase with/without `sha256=` prefix $\rightarrow$ PASS.
  - Base64 standard and URL-safe encodings $\rightarrow$ PASS.
  - Multi-header precedence resolution (`X-Signature` over `X-Hub-Signature-256`).
- **Constant-Time Verification:** Ensure constant-time guarantees without leaking execution time.

### 7.2. Integration & End-to-End Testing (`internal/api/handler_webhook_test.go`)
- Valid signature $\rightarrow$ `200 OK`, persisted to SQLite WAL.
- Invalid signature $\rightarrow$ `401 Unauthorized`, zero rows inserted into `requests` table.
- Missing signature header on secured route $\rightarrow$ `401 Unauthorized`.
- Empty `signature_secret` route $\rightarrow$ `500 Internal Server Error`.
- Unsecured route (`none`) $\rightarrow$ Accepts payloads without headers.

---

## 8. Verification Commands
```bash
# Run all tests with race detector
go test -race -v ./...

# Verify zero-CGO static compilation
CGO_ENABLED=0 go build -o /dev/null ./cmd/relay
```
