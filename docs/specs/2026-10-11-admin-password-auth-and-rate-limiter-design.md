# Design Specification: Admin Password Authentication & Login Rate Limiter

- **Issue:** [#14](https://github.com/simple-relay/simple-relay/issues/14)
- **Date:** 2026-10-11
- **Status:** Approved

---

## 1. Overview & Problem Statement

Simple Relay provides an embedded Web UI and administrative management APIs (`/api/v1/routes`, `/api/v1/requests`, `/api/v1/dlq`). In production environments, unauthorized users should not be able to inspect payloads, modify routes, or trigger DLQ replays.

This specification details the design for:
1. **Admin Password Authentication:** Protecting Web UI routes and administrative APIs using a session cookie authenticated against the configuration variable `RELAY_ADMIN_PASSWORD`.
2. **Login Rate Limiter:** Protecting the login endpoint against brute-force password guessing by enforcing IP-based rate limiting.

### Scope Boundaries & Clarifications
- **CSRF Token Validation:** Excluded from scope per team requirement.
- **Opt-in / Zero-Config by Default:** When `RELAY_ADMIN_PASSWORD` is empty (`""`), authentication is disabled entirely. The system remains zero-config and openly accessible for local development.

---

## 2. Goals & Non-Goals

### Goals
- **Opt-in Security:** If `RELAY_ADMIN_PASSWORD` is non-empty, require authentication for the Web UI and administrative management APIs.
- **Zero Disruptions to Machine Traffic:** Webhook ingestion (`/api/v1/webhook/*`), outbound proxy endpoints (`/api/v1/relay/*`, `/api/v1/jobs/*`), health checks (`/healthz`), and static frontend assets (`/ui/static/*`) must remain fully accessible without session cookies.
- **Timing-Attack Resistance:** Password comparison MUST use constant-time comparison (`crypto/subtle.ConstantTimeCompare`).
- **Secure Cookie Sessions:** Standard HTTP-only, SameSite=Lax cookie (`relay_session`) storing a cryptographically random token with 24-hour expiration.
- **Brute-Force Protection:** Rate limit failed login attempts per client IP (default: max 5 failed attempts per 5 minutes). Return `429 Too Many Requests` with a `Retry-After` header.
- **Seamless UI Experience:** Embedded Alpine.js modal in `ui/index.html` allowing login, logout, displaying rate-limiting countdowns, and gracefully handling session expiration (HTTP 401).

### Non-Goals
- **CSRF Token Validation:** Excluded from requirements.
- **Multi-user / RBAC:** Only a single shared admin password is supported.
- **Persistent Sessions in Database:** Sessions are stored in memory with TTL. Server restarts invalidate active sessions (acceptable for a single-admin appliance).

---

## 3. Architecture & Request Routing

```text
                                Incoming HTTP Request
                                         │
                    ┌────────────────────┴────────────────────┐
                    │                                         │
        Public / Machine Endpoints                Protected Web UI & Admin APIs
        • /healthz                                • / (Dashboard HTML)
        • /ui/static/* (CSS, JS)                  • /ui, /ui/
        • /api/v1/auth/status                     • /api/v1/routes* (CRUD)
        • /api/v1/auth/login                      • /api/v1/requests* (Inspect)
        • /api/v1/auth/logout                     • /api/v1/dlq* (Inspect & Replay)
        • /api/v1/webhook/* (Inbound)                         │
        • /api/v1/relay/* (Sync Proxy)                        ▼
        • /api/v1/jobs/* (Async Jobs)               Admin Auth Middleware
                    │                                (Validate relay_session)
                    │                                         │
                    ▼                                         ▼
             Direct Handler                            Target Handler
```

---

## 4. Detailed Component Design

### 4.1. Session Store (`internal/api/auth.go`)

- **Token Generation:** 32 cryptographically secure random bytes formatted as a 64-character hex string (`crypto/rand`).
- **Data Structure:**
  ```go
  type Session struct {
      Token     string
      CreatedAt time.Time
      ExpiresAt time.Time
  }

  type SessionStore struct {
      mu       sync.RWMutex
      sessions map[string]Session
      ttl      time.Duration
  }
  ```
- **Operations:**
  - `NewSessionStore(ttl time.Duration) *SessionStore`
  - `Create() (*Session, error)`: Generates a new session token, records expiry (default: 24h), and stores it.
  - `Validate(token string) bool`: Checks if token exists and `ExpiresAt > time.Now()`.
  - `Revoke(token string)`: Deletes session from the store on logout.
  - `Cleanup()`: Periodic garbage collection of expired sessions to prevent memory bloat.

### 4.2. Login Rate Limiter (`internal/api/rate_limiter.go`)

- **Objective:** Prevent brute-force password cracking on `POST /api/v1/auth/login`.
- **Keying:** Client IP address extracted from `r.RemoteAddr` (stripped of port) or `X-Forwarded-For` (first IP).
- **Parameters:**
  - `maxAttempts`: 5 failed attempts
  - `window`: 5 minutes (`300 * time.Second`)
- **State Machine:**
  ```go
  type ipAttempt struct {
      failures    int
      firstFail   time.Time
      lockedUntil time.Time
  }

  type RateLimiter struct {
      mu          sync.Mutex
      attempts    map[string]*ipAttempt
      maxAttempts int
      window      time.Duration
  }
  ```
- **Behavior:**
  1. `Allow(ip string) (bool, time.Duration)`: Returns `false` and remaining lock duration if the IP is currently locked.
  2. `RecordFailure(ip string)`: Increments failed attempt count. If count >= 5, sets `lockedUntil = time.Now().Add(5 * time.Minute)`.
  3. `RecordSuccess(ip string)`: Clears failure record for the IP upon successful login.
  4. `Cleanup()`: Evicts expired entries older than the window.

### 4.3. HTTP Endpoints & Auth Handlers

#### 1. `GET /api/v1/auth/status`
Public endpoint queried by Web UI on initialization.
- **Response `200 OK`:**
  ```json
  {
    "auth_required": true,
    "authenticated": true
  }
  ```

#### 2. `POST /api/v1/auth/login`
Public endpoint protected by `RateLimiter`.
- **Request Body:**
  ```json
  {
    "password": "secret-password"
  }
  ```
- **Flow:**
  1. Check rate limiter for client IP. If locked, return `429 Too Many Requests` with `Retry-After: <seconds>`:
     ```json
     {
       "error": "Too many failed login attempts. Please try again later.",
       "retry_after": 240
     }
     ```
  2. Compare password:
     ```go
     if subtle.ConstantTimeCompare([]byte(req.Password), []byte(adminPassword)) != 1 {
         rateLimiter.RecordFailure(clientIP)
         return 401 Unauthorized {"error": "Invalid password"}
     }
     ```
  3. On success, call `rateLimiter.RecordSuccess(clientIP)`.
  4. Create new session in `SessionStore`.
  5. Set HTTP cookie:
     ```http
     Set-Cookie: relay_session=<token>; Path=/; HttpOnly; SameSite=Lax; Max-Age=86400
     ```
  6. Return `200 OK` `{"status": "authenticated"}`.

#### 3. `POST /api/v1/auth/logout`
- Revokes session token in `SessionStore`.
- Clears `relay_session` cookie (`MaxAge: -1`).
- Return `200 OK` `{"status": "logged_out"}`.

### 4.4. Auth Middleware (`AuthMiddleware`)

- If `cfg.AdminPassword == ""`, the middleware is a no-op (calls `next.ServeHTTP(w, r)`).
- If `cfg.AdminPassword != ""`:
  - Read `relay_session` cookie from request.
  - If missing or invalid according to `SessionStore`:
    - For HTML UI requests (`GET /`, `GET /ui`, `GET /ui/`): Allow rendering index.html so the client-side Alpine.js script can detect `authenticated: false` from `/api/v1/auth/status` and display the login modal.
    - For API requests (`/api/v1/routes*`, `/api/v1/requests*`, `/api/v1/dlq*`): Return `401 Unauthorized` with JSON:
      ```json
      {
        "error": "Unauthorized: admin session required"
      }
      ```

### 4.5. Embedded Web UI Updates (`ui/index.html`)

- **State Additions in `relayApp()`:**
  - `authRequired`: boolean
  - `isAuthenticated`: boolean
  - `loginPassword`: string
  - `loginError`: string
  - `loginLoading`: boolean
  - `rateLimitCountdown`: number
- **Initialization:**
  - `init()` calls `checkAuthStatus()`.
  - If `authRequired && !isAuthenticated`, show login modal overlay and suspend background polling.
- **Login Modal:**
  - Clean, centered dark modal with password field, show/hide toggle, submit button, and error alert box.
  - Displays countdown timer if HTTP 429 response is encountered.
- **Sidebar Integration:**
  - When `authRequired && isAuthenticated`, display a "Logout" action in the sidebar footer.
- **Session Expiry Recovery:**
  - If any API request responds with `401 Unauthorized`, automatically reset `isAuthenticated = false` and reopen the login modal.

---

## 5. Verification & Testing Strategy

1. **Unit Tests:**
   - `internal/api/auth_test.go`:
     - Test session creation, validation, expiration, and revocation.
     - Test password validation with constant-time comparison.
     - Test login handler success and failure responses.
     - Test logout handler and cookie clearing.
   - `internal/api/rate_limiter_test.go`:
     - Test failure accumulation and lock activation upon 5 failed attempts.
     - Test `Retry-After` calculation.
     - Test lock release after window duration.
     - Test counter reset on successful login.
     - Test concurrent requests for race safety (`-race`).
   - `internal/api/router_test.go`:
     - Test router with `AdminPassword` empty (all endpoints accessible).
     - Test router with `AdminPassword` set:
       - Public endpoints (`/healthz`, `/ui/static/*`, `/api/v1/webhook/*`, `/api/v1/relay/*`) succeed without cookie.
       - Admin API endpoints (`/api/v1/routes`, `/api/v1/dlq`) return 401 without cookie.
       - Admin API endpoints succeed with valid session cookie.
2. **End-to-End & Compilation Verification:**
   - `go test -v -race ./...`
   - Static compilation check: `CGO_ENABLED=0 go build -o /dev/null ./cmd/relay`
