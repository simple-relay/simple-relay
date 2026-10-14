# Asymmetric Signature Verification & Custom Validation Pipeline Design Specification

> **Document:** Design Specification  
> **Status:** Draft / Under Review  
> **Author:** phuong  
> **Date:** 2026-10-14  
> **Target Packages:** `internal/signature`, `internal/api`, `internal/model`, `internal/storage/sqlite`  
> **Related Issues:** [#30](https://github.com/simple-relay/simple-relay/issues/30), [#59](https://github.com/simple-relay/simple-relay/issues/59)  

---

## 1. Executive Summary & Intent

Simple Relay currently enforces webhook payload authenticity for symmetric HMAC providers (Stripe, GitHub, Generic HMAC-SHA256) at the HTTP boundary before disk persistence. However, modern webhook ecosystems increasingly employ:
1. **Asymmetric Public-Key Signatures ([#30](https://github.com/simple-relay/simple-relay/issues/30)):**
   - **Ed25519:** Modern, high-performance signatures standardized by Standard Webhooks (Svix, Clerk, Resend) and DocuSign v2.
   - **RSA-SHA256:** Asymmetric signatures used by enterprise event buses like AWS SNS with remote X.509 certificate verification.
2. **Custom Pre-Persistence Validation Rules ([#59](https://github.com/simple-relay/simple-relay/issues/59)):**
   - Ability for operators to enforce header tokens, query parameters, or specific JSON body payload fields before requests enter the durable SQLite WAL queue.

Without a unified pre-persistence validation pipeline, unauthorized or malformed requests cause unnecessary disk I/O, SQLite lock contention, and Dead Letter Queue (DLQ) noise.

This specification unifies **Cryptographic Verification (Symmetric & Asymmetric)** and **Custom Rule Validation** into a single, high-performance, fail-fast boundary pipeline.

### Pipeline Execution Order & DoS Defense

$$\text{HTTP Request} \longrightarrow \mathbf{Stage\ 1:\ Query/Header\ Rules} \longrightarrow \mathbf{Stage\ 2:\ Signature\ Verification} \longrightarrow \mathbf{Stage\ 3:\ JSON\ Body\ Rules} \longrightarrow \text{SQLite WAL Ingestion}$$

**Architectural Rationale for Stage Order:**
- **Stage 1 (Query & Header Rules):** Cheepest $O(1)$ string checks run first, dropping unauthorized probes with zero body read or CPU crypto overhead.
- **Stage 2 (Cryptographic Signature Verification):** Validates raw byte authenticity *before* any payload parsing. This prevents CPU/memory exhaustion DoS attacks where attackers send large (up to 10MB) unauthenticated JSON payloads to force expensive unmarshaling.
- **Stage 3 (JSON Body Rules):** Evaluated only after the payload is proven authentic by Stage 2.
- **Error Oracle Prevention:** Both validation rule failures and signature failures return identical `HTTP 401 Unauthorized` responses with generic body `{"error": "unauthorized"}`, preventing attackers from guessing internal rules.

Any failure in stages 1–3 immediately aborts ingestion without writing a single byte to SQLite WAL.

---

## 2. Goals, Non-Goals & Anti-Goals

### 2.1. Goals
- **Unified Pipeline:** Execute validation rules and cryptographic checks in strict fail-fast order (cheapest $O(1)$ string checks first, cryptographic verification second, JSON body parsing third).
- **Asymmetric Support:**
  - Implement Ed25519 verifier auto-detecting Standard Webhooks headers (`webhook-id`, `webhook-timestamp`, `webhook-signature`) and raw body single-header fallback (`X-Signature-Ed25519`, `X-Signature`).
  - Implement RSA-SHA256 verifier with AWS SNS payload parsing, HTTPS domain whitelist check, and in-memory TTL caching for X.509 certificates.
  - Support pre-configured static PEM/Base64 public keys to bypass remote certificate network fetching.
- **Custom Rules Flexibility:** Support rules across `header`, `query`, and `body_json` (dot-notation) with AND logic matching on `inbound` routes.
- **Timing Attack Resistance:** Perform constant-time comparisons (`crypto/subtle.ConstantTimeCompare`) for all secret values.
- **High Performance & Bloat Protection:** In-memory certificate cache ($< 1\mu s$ lookup) and zero disk writes on rejection.

### 2.2. Non-Goals
- Complex dynamic certificate revocation lists (CRL/OCSP) — standard TLS HTTPS verification and short TTL caching suffice.
- Support for arbitrary OR/nested boolean expressions in custom rules — simple AND list is sufficient.
- Schema migration scripts or backward compatibility shims — database is unreleased; update `schemaDDL` directly.

### 2.3. Anti-Goals
- Leaking internal rule details, field names, or expected secret lengths in error responses. Rejection responses must be generic (`HTTP 401 Unauthorized`).
- Performing outbound network dials to untrusted domains (SSRF vulnerability). Remote cert downloads MUST be strictly bounded to verified provider hostnames.
- Committing rejected requests to DLQ or attempts audit table. Rejections happen before ingestion.

---

## 3. Architecture & Data Model

### 3.1. Route Model Extension (`internal/model/route.go`)

Extend `model.SignatureProvider`:
```go
const (
	SignatureProviderNone       SignatureProvider = "none"
	SignatureProviderStripe     SignatureProvider = "stripe"
	SignatureProviderGitHub     SignatureProvider = "github"
	SignatureProviderHMACSHA256 SignatureProvider = "hmac_sha256"
	SignatureProviderEd25519    SignatureProvider = "ed25519"
	SignatureProviderRSASHA256  SignatureProvider = "rsa_sha256"
)
```

Define `ValidationRule`:
```go
type RuleSource string

const (
	RuleSourceHeader   RuleSource = "header"
	RuleSourceQuery    RuleSource = "query"
	RuleSourceBodyJSON RuleSource = "body_json"
)

type RuleOperator string

const (
	RuleOperatorEquals RuleOperator = "equals"
	RuleOperatorExists RuleOperator = "exists"
)

type ValidationRule struct {
	Source   RuleSource   `json:"source"`
	Key      string       `json:"key"`
	Operator RuleOperator `json:"operator"`
	Value    string       `json:"value,omitempty"`
}
```

Add `ValidationRules []ValidationRule` to `Route`:
```go
type Route struct {
	// ... existing fields ...
	ValidationRules      []ValidationRule  `json:"validation_rules"`
	// ...
}
```

Validation rules in `Route.Validate()`:
1. `ValidationRules`:
   - If `len(ValidationRules) > 0` and `Type != RouteTypeInbound`: return `errors.New("validation_rules are only supported on inbound routes")`.
   - For each rule:
     - `Source` must be `header`, `query`, or `body_json`.
     - `Key` must not be empty.
     - `Operator` must be `equals` or `exists`.
     - If `Operator == equals`, `Value` may be non-empty (or empty string if matching empty).
2. `SignatureProvider`:
   - If `ed25519`, `SignatureSecret` must be provided (valid 32-byte Ed25519 public key in Base64, Hex, or PEM).
   - If `rsa_sha256`, `SignatureSecret` is optional (if omitted, remote `SigningCertURL` from AWS SNS payload is used with cache; if provided, static PEM public key is used).

### 3.2. SQLite Schema (`internal/storage/sqlite/migrations.go`)

Directly update `schemaDDL` in `internal/storage/sqlite/migrations.go`:
```sql
CREATE TABLE IF NOT EXISTS routes (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    type TEXT NOT NULL,
    mode TEXT NOT NULL DEFAULT 'async',
    path TEXT NOT NULL UNIQUE,
    destination_url TEXT NOT NULL,
    signature_provider TEXT DEFAULT 'none',
    signature_secret TEXT,
    validation_rules TEXT DEFAULT '[]',
    max_attempts INTEGER NOT NULL DEFAULT 5,
    timeout_ms INTEGER NOT NULL DEFAULT 10000,
    retry_backoff_strategy TEXT DEFAULT 'exp',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

Update `route_repo.go` to scan and serialize `validation_rules` as JSON text (`json.Marshal` / `json.Unmarshal`).

---

## 4. Cryptographic Adapters Specification (`internal/signature`)

### 4.1. Request Context Contract Evolution

To support asymmetric signatures (which may inspect multiple headers or JSON fields), `Verifier` interface is extended to accept the verification context:

```go
type VerificationContext struct {
	Header   http.Header
	Query    url.Values
	RawBody  []byte
	Secret   string // Static secret or public key configured on route
}

type Verifier interface {
	Verify(ctx *VerificationContext) error
}
```

Existing adapters (`stripe`, `github`, `generic`) are wrapped/updated to satisfy this contract.

### 4.2. Ed25519 Adapter (`ed25519.go`)

- **Provider:** `ed25519`
- **Public Key Resolution:**
  - Parse `ctx.Secret` as:
    1. Standard PEM block (`BEGIN PUBLIC KEY` -> `x509.ParsePKIXPublicKey`).
    2. Base64 decoded 32 bytes (`ed25519.PublicKey`).
    3. Hex decoded 32 bytes (`ed25519.PublicKey`).
- **Signature & Payload Auto-Detection:**
  - **Path A: Standard Webhooks (Svix / Clerk / Resend):**
    - Checked if `webhook-signature` header is present.
    - Extract `msgId = header.Get("webhook-id")` and `timestamp = header.Get("webhook-timestamp")`.
    - If either is missing, return `ErrInvalidSignatureFormat`.
    - Signed content: `signedPayload = fmt.Sprintf("%s.%s.%s", msgId, timestamp, rawBody)`
    - Header `webhook-signature` contains space-separated tokens: `v1a,<base64_sig>`.
    - Iterate over candidate `v1a` signatures, decode Base64 (64 bytes), verify via `ed25519.Verify(pubKey, []byte(signedPayload), sigBytes)`.
    - Return `nil` on first match, else `ErrSignatureMismatch`.
  - **Path B: Generic Ed25519 (Raw Body):**
    - Checked if `webhook-signature` is absent.
    - Inspect headers in order: `X-Signature-Ed25519`, `X-Signature`, `X-Ed25519-Signature`.
    - If none found, return `ErrMissingSignatureHeader`.
    - Decode signature candidate from Base64 or Hex (64 bytes).
    - Verify via `ed25519.Verify(pubKey, rawBody, sigBytes)`.
    - Return `nil` if verified, else `ErrSignatureMismatch`.

### 4.3. RSA-SHA256 Adapter (`rsa.go`) & Certificate Cache (`cert_cache.go`)

- **Provider:** `rsa_sha256`
- **Payload Auto-Detection:**
  - **Case 1: AWS SNS Message (JSON Payload):**
    - Detected if `rawBody` is JSON containing `SigningCertURL` or `TopicArn`.
    - **Certificate / Key Resolution:**
      - If `ctx.Secret` is provided: Parse directly as RSA Public Key (PEM PKCS#1 or PKIX).
      - If `ctx.Secret` is empty:
        - Extract `SigningCertURL` from JSON body.
        - **SSRF Whitelist Validation:** URL must start with `https://` and host must match regex:
          `^sns\.[a-z0-9\-]+\.amazonaws\.com$`
        - **Certificate Cache Lookup:** Check in-memory cache (`sync.RWMutex`, TTL = 24h). On miss, fetch over HTTP (3s timeout, 1MB limit), parse X.509 cert, extract `*rsa.PublicKey`, store in cache.
    - **AWS SNS Canonical String Reconstruction:**
      - Required fields based on `Type`:
        - `Notification`: `Message`, `MessageId`, `Subject` (if present), `Timestamp`, `TopicArn`, `Type`.
        - `SubscriptionConfirmation` / `UnsubscribeConfirmation`: `Message`, `MessageId`, `SubscribeURL`, `Timestamp`, `Token`, `TopicArn`, `Type`.
      - Build canonical string in AWS alphabetical key order: `Key\nValue\n`.
      - Check `SignatureVersion`:
        - If `"1"`: Use `crypto.SHA1`.
        - If `"2"` or unspecified: Use `crypto.SHA256`.
      - Decode `Signature` field from Base64.
      - Verify signature using `rsa.VerifyPKCS1v15(pubKey, hashFunc, hashBytes, sigBytes)`.
  - **Case 2: Generic RSA-SHA256 (Raw Body):**
    - Detected if not an AWS SNS message and candidate header present (`X-Signature-RSA`, `X-Signature`).
    - Parse static public key from `ctx.Secret`.
    - Hash `rawBody` with `crypto.SHA256`.
    - Decode signature candidate from Base64 or Hex.
    - Verify with `rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hashBytes, sigBytes)`.

---

## 5. Custom Validation Rules Evaluator (`internal/api/validator.go`)

Function signatures:
```go
// EvaluateHeaderQueryRules evaluates fast O(1) header and query parameter rules.
func EvaluateHeaderQueryRules(r *http.Request, rules []model.ValidationRule) error

// EvaluateBodyJSONRules evaluates JSON body field rules on authenticated payloads.
func EvaluateBodyJSONRules(rawBody []byte, rules []model.ValidationRule) error
```

### 5.1. Evaluation Logic
1. **Header & Query Evaluation (Stage 1):**
   - For `header` rule:
     - `val := r.Header.Get(rule.Key)`
     - If `operator == exists`: pass if `val != ""`.
     - If `operator == equals`: pass if `subtle.ConstantTimeCompare([]byte(val), []byte(rule.Value)) == 1`.
   - For `query` rule:
     - `val := r.URL.Query().Get(rule.Key)`
     - If `operator == exists`: pass if `val != ""`.
     - If `operator == equals`: pass if `subtle.ConstantTimeCompare([]byte(val), []byte(rule.Value)) == 1`.
   - On failure: return `ErrValidationRuleFailed`.
2. **JSON Body Evaluation (Stage 3):**
   - If any `body_json` rule is configured:
     - Parse `rawBody` into `map[string]any`.
     - If JSON parsing fails, return `ErrInvalidJSONPayload`.
     - Traverse dot-notation path (e.g. `event.type`).
     - Compare value with `rule.Value` using string conversion and `subtle.ConstantTimeCompare`.
     - If rule fails, return `ErrValidationRuleFailed`.

---

## 6. HTTP Boundary Integration (`internal/api/handler_webhook.go`)

Updated `NewWebhookHandler` execution pipeline:

```
1. Method Check (POST, PUT, PATCH -> 405)
2. Route Resolution (byName or byPath -> 404)
3. Inbound Type Check (route.Type == "inbound" -> 400)
4. Stage 1: Evaluate Header & Query Rules -> 401 on mismatch
5. Read Raw Body (http.MaxBytesReader -> 413 or 400)
6. Stage 2: Cryptographic Signature Verification (Symmetric & Asymmetric) -> 401 on mismatch
7. Stage 3: Evaluate BodyJSON Rules -> 400 on malformed JSON, 401 on mismatch
8. Stage 4: Durable Ingestion (batchWriter.Enqueue -> 200 / 202)
```

Error responses:
- `ErrValidationRuleFailed`, `ErrSignatureMismatch`, `ErrMissingSignatureHeader`, `ErrInvalidSignatureFormat` -> `writeJSONError(w, http.StatusUnauthorized, "unauthorized")`
- `ErrInvalidJSONPayload` -> `writeJSONError(w, http.StatusBadRequest, "invalid json payload")`

---

## 7. What is Left to the Builder

The builder has full discretion over:
1. Exact internal cache data structure in `internal/signature/cert_cache.go` (e.g. `sync.RWMutex` map vs `sync.Map`).
2. Exact helper function for JSON dot-notation traversal in `internal/api/validator.go`.
3. Unit test vector generation (using standard crypto keys generated in test suites).

---

## 8. Verification & Testing Plan

### 8.1. Unit Testing
- **Ed25519 Adapter:**
  - Standard Webhooks vector with `webhook-id`, `webhook-timestamp`, `webhook-signature` (`v1a,...`).
  - Generic Ed25519 vector with `X-Signature-Ed25519`.
  - Key formats: Base64, Hex, PEM.
  - Invalid signature / tampered body -> `ErrSignatureMismatch`.
- **RSA-SHA256 Adapter:**
  - AWS SNS payload with mock certificate HTTP server and valid signature (`SignatureVersion: "1"` and `"2"`) -> PASS.
  - Non-whitelisted certificate URL -> rejected before HTTP fetch.
  - Certificate caching -> second request does NOT trigger HTTP fetch.
  - Static PEM public key configured on route -> ignores payload `SigningCertURL`.
  - Generic RSA raw body vector with `X-Signature-RSA` -> PASS.
- **Validation Rules Evaluator:**
  - Header exists & equals.
  - Query param exists & equals.
  - JSON body dot-notation (`data.user.id`).
  - Malformed JSON -> HTTP 400.
  - AND logic with multiple rules: 1 fails -> all fail.

### 8.2. Integration Testing
- Verify end-to-end webhook ingestion with all rules and signatures.
- Confirm SQLite WAL has ZERO inserts when validation or signature fails.

### 8.3. Verification Commands
```bash
go test -v ./...
go test -race ./...
CGO_ENABLED=0 go build -o /dev/null ./cmd/relay
```
