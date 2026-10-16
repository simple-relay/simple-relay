package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/simple-relay/simple-relay/internal/storage"
)


// SessionStore manages active admin sessions in memory.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]time.Time
	ttl      time.Duration
}

// NewSessionStore creates an initialized in-memory SessionStore.
func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &SessionStore{
		sessions: make(map[string]time.Time),
		ttl:      ttl,
	}
}

// Create generates a cryptographically random 32-byte token and stores it.
func (s *SessionStore) Create() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[token] = time.Now().Add(s.ttl)
	return token, nil
}

// Validate checks whether a given session token is active and not expired.
func (s *SessionStore) Validate(token string) bool {
	if token == "" {
		return false
	}
	s.mu.RLock()
	expiresAt, exists := s.sessions[token]
	s.mu.RUnlock()

	if !exists {
		return false
	}
	if time.Now().After(expiresAt) {
		s.Revoke(token)
		return false
	}
	return true
}

// Revoke invalidates and removes an active session token.
func (s *SessionStore) Revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

// Cleanup evicts expired session tokens from memory.
func (s *SessionStore) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for token, expiresAt := range s.sessions {
		if now.After(expiresAt) {
			delete(s.sessions, token)
		}
	}
}

// StartAutoCleanup starts a periodic background worker to clean up expired sessions.
// It stops when ctx is cancelled.
func (s *SessionStore) StartAutoCleanup(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.Cleanup()
			}
		}
	}()
}

type ipAttempt struct {
	failures    []time.Time
	inFlight    int
	lockedUntil time.Time
}

// RateLimiter enforces brute-force lockout on failed login attempts per IP.
type RateLimiter struct {
	mu          sync.Mutex
	attempts    map[string]*ipAttempt
	maxAttempts int
	window      time.Duration
}

// NewRateLimiter creates an initialized in-memory RateLimiter.
func NewRateLimiter(maxAttempts int, window time.Duration) *RateLimiter {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if window <= 0 {
		window = 5 * time.Minute
	}
	return &RateLimiter{
		attempts:    make(map[string]*ipAttempt),
		maxAttempts: maxAttempts,
		window:      window,
	}
}

// Allow checks if the given IP address is allowed to attempt login and reserves an in-flight attempt.
func (rl *RateLimiter) Allow(ip string) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, exists := rl.attempts[ip]
	if !exists {
		entry = &ipAttempt{}
		rl.attempts[ip] = entry
	}

	now := time.Now()
	if !entry.lockedUntil.IsZero() {
		if now.Before(entry.lockedUntil) {
			return false, entry.lockedUntil.Sub(now)
		}
		// Lock expired, reset entry
		entry.lockedUntil = time.Time{}
		entry.failures = nil
		entry.inFlight = 0
	}

	// Prune failures older than sliding window
	valid := 0
	for _, t := range entry.failures {
		if now.Sub(t) <= rl.window {
			entry.failures[valid] = t
			valid++
		}
	}
	entry.failures = entry.failures[:valid]

	if len(entry.failures)+entry.inFlight >= rl.maxAttempts {
		wait := rl.window
		if len(entry.failures) >= rl.maxAttempts {
			if entry.lockedUntil.IsZero() {
				entry.lockedUntil = now.Add(rl.window)
			}
			wait = entry.lockedUntil.Sub(now)
		}
		return false, wait
	}

	entry.inFlight++
	return true, 0
}

// RecordFailure registers a failed login attempt for the given IP and decrements in-flight count.
func (rl *RateLimiter) RecordFailure(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, exists := rl.attempts[ip]
	if !exists {
		entry = &ipAttempt{}
		rl.attempts[ip] = entry
	}

	if entry.inFlight > 0 {
		entry.inFlight--
	}
	now := time.Now()
	entry.failures = append(entry.failures, now)
	if len(entry.failures) >= rl.maxAttempts {
		entry.lockedUntil = now.Add(rl.window)
	}
}

// ReleaseInFlight decrements in-flight counter when a request does not complete authentication (e.g. malformed body).
func (rl *RateLimiter) ReleaseInFlight(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if entry, exists := rl.attempts[ip]; exists && entry.inFlight > 0 {
		entry.inFlight--
	}
}

// RecordSuccess clears any failure records for the given IP upon successful authentication.
func (rl *RateLimiter) RecordSuccess(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.attempts, ip)
}

// Cleanup evicts expired rate limiter entries.
func (rl *RateLimiter) Cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	for ip, entry := range rl.attempts {
		// Prune expired failures
		valid := 0
		for _, t := range entry.failures {
			if now.Sub(t) <= rl.window {
				entry.failures[valid] = t
				valid++
			}
		}
		entry.failures = entry.failures[:valid]

		isLocked := !entry.lockedUntil.IsZero() && now.Before(entry.lockedUntil)
		if !isLocked && len(entry.failures) == 0 && entry.inFlight == 0 {
			delete(rl.attempts, ip)
		}
	}
}

// StartAutoCleanup starts a periodic background worker to clean up expired rate limiter entries.
// It stops when ctx is cancelled.
func (rl *RateLimiter) StartAutoCleanup(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rl.Cleanup()
			}
		}
	}()
}

// ExtractClientIP extracts the host IP from request headers or RemoteAddr.
// It prioritizes X-Forwarded-For (first entry) and X-Real-IP before RemoteAddr.
func ExtractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			clientIP := strings.TrimSpace(ips[0])
			if clientIP != "" {
				if host, _, err := net.SplitHostPort(clientIP); err == nil {
					return host
				}
				return clientIP
			}
		}
	}

	if xRealIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); xRealIP != "" {
		if host, _, err := net.SplitHostPort(xRealIP); err == nil {
			return host
		}
		return xRealIP
	}

	remote := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(remote); err == nil {
		return host
	}
	return remote
}

// AuthHandler handles admin login, logout, and session status.
type AuthHandler struct {
	adminPassword string
	sessionStore  *SessionStore
	rateLimiter   *RateLimiter
	apiKeyRepo    storage.APIKeyRepository
	logger        *slog.Logger
}

// NewAuthHandler constructs an AuthHandler instance.
func NewAuthHandler(adminPassword string, sessionStore *SessionStore, rateLimiter *RateLimiter, logger *slog.Logger) *AuthHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthHandler{
		adminPassword: adminPassword,
		sessionStore:  sessionStore,
		rateLimiter:   rateLimiter,
		logger:        logger,
	}
}

// WithAPIKeyRepo sets the API key repository for credential verification.
func (h *AuthHandler) WithAPIKeyRepo(repo storage.APIKeyRepository) *AuthHandler {
	h.apiKeyRepo = repo
	return h
}

func (h *AuthHandler) isAuthenticated(r *http.Request) bool {
	if h.apiKeyRepo != nil {
		if key := extractAPIKey(r); key != "" {
			hash := HashKey(key)
			if apiKey, err := h.apiKeyRepo.GetAPIKeyByHash(r.Context(), hash); err == nil && apiKey != nil && apiKey.RevokedAt == nil {
				return true
			}
			return false
		}
	}

	if h.adminPassword == "" {
		return true
	}
	cookie, err := r.Cookie("relay_session")
	if err != nil || cookie == nil {
		return false
	}
	return h.sessionStore.Validate(cookie.Value)
}


// Status returns whether admin auth is required and if the current client is authenticated.
func (h *AuthHandler) Status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"auth_required": h.adminPassword != "",
		"authenticated": h.isAuthenticated(r),
	})
}

// Login authenticates an admin with the configured RELAY_ADMIN_PASSWORD.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.adminPassword == "" {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "authenticated"})
		return
	}

	ip := ExtractClientIP(r)
	if allowed, wait := h.rateLimiter.Allow(ip); !allowed {
		retryAfterSeconds := int(math.Ceil(wait.Seconds()))
		if retryAfterSeconds <= 0 {
			retryAfterSeconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":       "Too many failed login attempts. Please try again later.",
			"retry_after": retryAfterSeconds,
		})
		return
	}

	// Restrict login request body to 16 KB max to prevent DoS memory bloat
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.rateLimiter.ReleaseInFlight(ip)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	// Constant-time password comparison to prevent timing attacks
	if subtle.ConstantTimeCompare([]byte(req.Password), []byte(h.adminPassword)) != 1 {
		h.rateLimiter.RecordFailure(ip)
		h.logger.Warn("admin login failed: invalid password", "client_ip", ip)
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid password"})
		return
	}

	// Login successful
	h.rateLimiter.RecordSuccess(ip)
	token, err := h.sessionStore.Create()
	if err != nil {
		h.logger.Error("failed to create admin session", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Internal server error"})
		return
	}

	isSecure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     "relay_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(h.sessionStore.ttl.Seconds()),
	})

	h.logger.Info("admin login successful", "client_ip", ip)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "authenticated"})
}

// Logout clears the current admin session cookie and revokes the session token.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("relay_session"); err == nil && cookie != nil {
		h.sessionStore.Revoke(cookie.Value)
	}

	isSecure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     "relay_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "logged_out"})
}
