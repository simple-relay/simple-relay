# Design Specification: Security Hardening, Outbound Auth Boundaries, Route Secret Masking & Proxy IP Spoofing Protection

- **Issue:** [#82](https://github.com/simple-relay/simple-relay/issues/82)
- **Date:** 2026-10-19
- **Status:** Approved

---

## 1. Overview & Problem Statement

A security audit of Simple Relay's default HTTP and configuration boundaries identified four hardening areas before exposing an instance beyond a trusted local developer workstation:

1. **Unauthenticated Remote Access in Dev Mode:** When `RELAY_ADMIN_PASSWORD` is unset (`""`) while binding to `0.0.0.0:8080`, all administrative endpoints (`/api/v1/routes`, `/api/v1/requests`, `/api/v1/dlq`, `/api/v1/api-keys`) were accessible to remote clients without credentials.
2. **Unprotected Outbound Relay & Job Enqueue Endpoints:** `POST /api/v1/jobs/{route_name}` and `ANY /api/v1/relay/{route_name}` were excluded from `isProtectedPath` (`internal/api/router.go`), allowing unauthenticated callers to enqueue background jobs or trigger outbound HTTP requests even when `RELAY_ADMIN_PASSWORD` or API keys were active.
3. **Plaintext Route Secrets in API Responses:** `GET /api/v1/routes` and `GET /api/v1/routes/{id}` returned `signature_secret` in plaintext JSON (`internal/model/route.go`), exposing HMAC secrets to anyone with read access to the UI or API.
4. **Unconditional `X-Forwarded-For` / `X-Real-IP` Trust:** `ExtractClientIP` (`internal/api/auth.go`) unconditionally trusted proxy headers, allowing an attacker connecting directly to Simple Relay to bypass the brute-force login rate limiter (`5 attempts / 5 minutes`) by rotating a spoofed `X-Forwarded-For` header on every request.

---

## 2. Goals & Non-Goals

### Goals
- **Loopback-Only Unauthenticated Dev Mode:** When `RELAY_ADMIN_PASSWORD` is empty, restrict unauthenticated fallback to loopback socket connections (`127.0.0.1`, `::1`, `localhost`). Require a valid API key (`Authorization: Bearer <key>` or `X-API-Key: <key>`) for non-loopback connections, and emit a structured startup warning when binding to `0.0.0.0` / `::` without an admin password.
- **Authenticated Outbound Endpoints:** Include `/api/v1/jobs` and `/api/v1/relay` in `isProtectedPath` so outbound synchronous proxying and asynchronous job submission require a valid session cookie, API key, or loopback dev-mode access.
- **Route Secret Masking & Safe Updates:** Mask `signature_secret` (`first4` + `*****` + `last4` characters, or `********` for secrets `<= 8` chars) in all `Route` HTTP JSON responses while keeping the raw secret intact in SQLite and `RouteRegistry` for HMAC verification. Preserve the existing secret during `PUT /api/v1/routes/{id}` when `signature_secret` is omitted or echoes back the masked placeholder.
- **Opt-In Proxy Header Trust (`RELAY_TRUST_PROXY`):** Add `--trust-proxy` CLI flag and `RELAY_TRUST_PROXY` environment variable (default `false`). Only read `X-Forwarded-For` and `X-Real-IP` in `ExtractClientIP` when `TrustProxy` is explicitly enabled.

### Non-Goals
- **Inbound Webhook Authentication Changes:** Inbound webhook receivers (`/api/v1/webhook/{route_name}`) continue to authenticate via route-level cryptographic signature verification (`HMAC-SHA256`, `Ed25519`, `RSA-SHA256`) and custom validation rules, not admin sessions or API keys.
- **Database-Level Secret Encryption at Rest:** Secrets remain stored in SQLite for runtime verification by `RouteRegistry`; masking applies strictly at the HTTP serialization boundary.

---

## 3. Architecture & Request Routing

```text
                                Incoming HTTP Request
                                         │
                    ┌────────────────────┴────────────────────┐
                    │                                         │
        Public / Webhook Endpoints               Protected Web UI, Admin & Outbound APIs
        • /healthz                                • / (Dashboard HTML), /ui, /ui/
        • /ui/static/* (CSS, JS)                  • /api/v1/routes* (CRUD, masked secrets)
        • /api/v1/auth/status                     • /api/v1/requests* (Inspect & Replay)
        • /api/v1/auth/login (Rate-Limited)       • /api/v1/dlq* (Inspect & Replay)
        • /api/v1/auth/logout                     • /api/v1/api-keys* (Manage Keys)
        • /api/v1/webhook/* (Signature Auth)      • /api/v1/relay/* (Sync Outbound Proxy)
                    │                             • /api/v1/jobs/* (Async Job Enqueue)
                    │                                         │
                    │                                         ▼
                    │                               Auth Middleware Check:
                    │                               1. Valid relay_session cookie?
                    │                               2. Valid Bearer / X-API-Key?
                    │                               3. Dev Mode (AdminPassword == "")
                    │                                  AND Loopback r.RemoteAddr?
                    │                                         │
                    ▼                                         ▼
             Direct Handler                            Target Handler
```

---

## 4. Detailed Component Design

### 4.1. Configuration (`internal/config/config.go` & `internal/cli/root.go`)

- Add `TrustProxy bool` to `config.Config`:
  - CLI Flag: `--trust-proxy` (default `false`)
  - Environment Variable: `RELAY_TRUST_PROXY` (`true`, `1`, `false`, `0`)
- Register `"trust-proxy": true` in `knownServerFlags` (`internal/cli/root.go`) so bare flag routing continues to work cleanly.
- In `RunServer` (`internal/cli/serve.go`), log a startup warning via `slog.Logger.Warn` if `cfg.AdminPassword == ""` and `cfg.Host` is non-loopback (`""`, `"0.0.0.0"`, `"::"`).

### 4.2. Client IP Extraction & Loopback Verification (`internal/api/auth.go`)

- **Loopback Check (`isLoopbackRequest`):**
  - Always inspects the direct socket peer `r.RemoteAddr` (never proxy headers) using `net.SplitHostPort` and `net.ParseIP(host).IsLoopback()` (or `host == "localhost"`).
  - Prevents remote clients from bypassing dev-mode restrictions via forged `X-Forwarded-For: 127.0.0.1` headers.
- **Proxy-Aware Client IP Extraction (`ExtractClientIP`):**
  - Signature: `ExtractClientIP(r *http.Request, trustProxy ...bool) string`
  - When `trustProxy` is `false` (default): ignores `X-Forwarded-For` and `X-Real-IP` and extracts the IP directly from `r.RemoteAddr`.
  - When `trustProxy` is `true`: checks the first comma-separated entry of `X-Forwarded-For`, then `X-Real-IP`, before falling back to `r.RemoteAddr`.

### 4.3. Authentication Boundaries (`internal/api/router.go` & `internal/api/auth.go`)

- **Protected Paths (`isProtectedPath`):**
  - Protects `/`, `/ui`, `/ui/`, `/api/v1/routes`, `/api/v1/requests`, `/api/v1/dlq`, `/api/v1/api-keys`, `/api/v1/jobs`, and `/api/v1/relay`.
- **Outbound Credential Stripping (`SanitizeInternalAuthHeaders`):**
  - Before persisting `requests.headers` to SQLite or forwarding requests to `route.DestinationURL` in `NewJobIngestHandler`, `RelayEngine.RelaySync`, and `WorkerPool.processJob`, Simple Relay strips its own internal authentication credentials (`X-API-Key`, `Authorization: Bearer sr_live_*`, and the `relay_session` cookie) while preserving non-relay `Authorization` headers (e.g., upstream `Bearer sk_live_...`) and application cookies.
- **Middleware & AuthHandler Dev Mode Behavior:**
  - When `AdminPassword == ""`:
    - Requests with a valid API key (`Authorization: Bearer` or `X-API-Key`) are authenticated regardless of source IP.
    - Requests without an API key are permitted **only** if `isLoopbackRequest(r)` is `true`.
    - Non-loopback unauthenticated requests receive `401 Unauthorized` (`{"error": "authentication required"}`).
  - `GET /api/v1/auth/status` returns `{"auth_required": true, "authenticated": false}` for unauthenticated non-loopback callers when `AdminPassword == ""`, prompting the UI to require credentials rather than silently failing downstream API calls.

### 4.4. Route Secret Masking (`internal/api/handler_routes.go`)

- **Masking Function (`MaskSecret`):**
  ```go
  func MaskSecret(secret string) string {
      if secret == "" {
          return ""
      }
      if len(secret) <= 8 {
          return "********"
      }
      return secret[:4] + "*****" + secret[len(secret)-4:]
  }
  ```
- **Read & Write Response Sanitization:**
  - `ListRoutes`, `GetRoute`, `CreateRoute`, and `UpdateRoute` return `maskRoute(route)` in HTTP JSON payloads while passing the unmasked `*model.Route` to `RouteRepository` and `RouteRegistry.Upsert`.
- **Safe Update Preservation (`PUT /api/v1/routes/{id}`):**
  - Before persisting an update, `UpdateRoute` fetches the existing route via `RouteRepo.GetRouteByID(ctx, id)`.
  - If `input.SignatureSecret == ""` or `input.SignatureSecret == MaskSecret(existing.SignatureSecret)`, `route.SignatureSecret` is restored to `existing.SignatureSecret`.
  - This allows the Web UI to load a route form (containing the masked secret) and submit updates to other fields (such as `target_url` or `max_attempts`) without overwriting the stored HMAC secret with asterisks.

---

## 5. Verification Plan

1. **Unit & Integration Tests:**
   - `internal/config/config_test.go`: Verify `RELAY_TRUST_PROXY` and `--trust-proxy` parsing and precedence.
   - `internal/api/auth_test.go`: Verify `ExtractClientIP` ignores `X-Forwarded-For` and `X-Real-IP` when `trustProxy=false` and honors them when `trustProxy=true`; verify login rate limiter blocks spoofed `X-Forwarded-For` brute-force attempts when `trustProxy=false`; verify dev mode rejects non-loopback requests unless a valid API key is supplied.
   - `internal/api/router_test.go`: Verify `/api/v1/jobs/*` and `/api/v1/relay/*` return `401 Unauthorized` when unauthenticated and succeed with session cookie or API key.
   - `internal/api/handler_routes_test.go`: Verify `MaskSecret` on `POST`, `GET`, and `PUT /api/v1/routes*`, confirm raw secret remains intact in storage/registry, and confirm `PUT` preserves the existing secret when omitted or when the masked string is echoed back.
2. **Standard Verification Suite:**
   - `go test -v ./...`
   - `go test -race ./...`
   - `CGO_ENABLED=0 go build -o /dev/null ./cmd/relay`
