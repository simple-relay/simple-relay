package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestSessionStore_Lifecycle(t *testing.T) {
	ttl := 100 * time.Millisecond
	store := NewSessionStore(ttl)

	// Create session
	token, err := store.Create()
	if err != nil {
		t.Fatalf("unexpected error creating session: %v", err)
	}
	if len(token) != 64 {
		t.Fatalf("expected 64-character hex token, got %d chars: %s", len(token), token)
	}

	// Validate valid session
	if !store.Validate(token) {
		t.Fatalf("expected session token to be valid")
	}

	// Validate non-existent token
	if store.Validate("non-existent-token") {
		t.Fatalf("expected non-existent token to be invalid")
	}

	// Revoke token
	store.Revoke(token)
	if store.Validate(token) {
		t.Fatalf("expected revoked session to be invalid")
	}

	// Token expiration
	token2, err := store.Create()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	time.Sleep(120 * time.Millisecond)
	if store.Validate(token2) {
		t.Fatalf("expected session token to expire after TTL")
	}

	// Cleanup
	store.Cleanup()
	store.mu.RLock()
	count := len(store.sessions)
	store.mu.RUnlock()
	if count != 0 {
		t.Fatalf("expected 0 sessions after cleanup, got %d", count)
	}
}

func TestRateLimiter_Lifecycle(t *testing.T) {
	window := 200 * time.Millisecond
	rl := NewRateLimiter(5, window)
	ip := "192.168.1.100"

	// First 4 failed attempts should allow further attempts
	for i := 1; i <= 4; i++ {
		allowed, _ := rl.Allow(ip)
		if !allowed {
			t.Fatalf("expected attempt %d to be allowed", i)
		}
		rl.RecordFailure(ip)
	}

	// 5th failed attempt triggers the lockout
	allowed, _ := rl.Allow(ip)
	if !allowed {
		t.Fatalf("expected attempt 5 to be allowed before failure recorded")
	}
	rl.RecordFailure(ip)

	// Now should be locked
	allowed, wait := rl.Allow(ip)
	if allowed {
		t.Fatalf("expected IP to be locked after 5 failures")
	}
	if wait <= 0 || wait > window {
		t.Fatalf("expected positive wait time <= window, got %v", wait)
	}

	// Different IP should still be allowed
	otherIP := "192.168.1.101"
	allowedOther, _ := rl.Allow(otherIP)
	if !allowedOther {
		t.Fatalf("expected other IP to be allowed")
	}

	// Wait for window to expire
	time.Sleep(window + 10*time.Millisecond)
	allowedAfterWait, _ := rl.Allow(ip)
	if !allowedAfterWait {
		t.Fatalf("expected IP to be unlocked after window expiration")
	}

	// Record success resets failures
	rl.RecordFailure(ip)
	rl.RecordSuccess(ip)
	rl.mu.Lock()
	_, exists := rl.attempts[ip]
	rl.mu.Unlock()
	if exists {
		t.Fatalf("expected success to clear failure entry")
	}
}

func TestAuthHandler_Status(t *testing.T) {
	// Case 1: Admin password disabled
	store := NewSessionStore(24 * time.Hour)
	rl := NewRateLimiter(5, 5*time.Minute)
	hNoAuth := NewAuthHandler("", store, rl, nil)

	req := httptest.NewRequest("GET", "/api/v1/auth/status", nil)
	w := httptest.NewRecorder()
	hNoAuth.Status(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
	var res map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if res["auth_required"] != false || res["authenticated"] != true {
		t.Fatalf("expected auth_required=false, authenticated=true, got %+v", res)
	}

	// Case 2: Admin password enabled, not authenticated
	hAuth := NewAuthHandler("adminsecret123", store, rl, nil)
	w2 := httptest.NewRecorder()
	hAuth.Status(w2, req)

	var res2 map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &res2)
	if res2["auth_required"] != true || res2["authenticated"] != false {
		t.Fatalf("expected auth_required=true, authenticated=false, got %+v", res2)
	}

	// Case 3: Admin password enabled, valid cookie
	token, _ := store.Create()
	reqAuth := httptest.NewRequest("GET", "/api/v1/auth/status", nil)
	reqAuth.AddCookie(&http.Cookie{Name: "relay_session", Value: token})
	w3 := httptest.NewRecorder()
	hAuth.Status(w3, reqAuth)

	var res3 map[string]any
	_ = json.Unmarshal(w3.Body.Bytes(), &res3)
	if res3["auth_required"] != true || res3["authenticated"] != true {
		t.Fatalf("expected auth_required=true, authenticated=true, got %+v", res3)
	}
}

func TestAuthHandler_LoginAndLogout(t *testing.T) {
	adminPass := "supersecret123"
	store := NewSessionStore(24 * time.Hour)
	rl := NewRateLimiter(5, 5*time.Minute)
	h := NewAuthHandler(adminPass, store, rl, nil)

	// 1. Invalid Password
	bodyWrong, _ := json.Marshal(map[string]string{"password": "wrong"})
	reqWrong := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(bodyWrong))
	reqWrong.RemoteAddr = "10.0.0.1:12345"
	wWrong := httptest.NewRecorder()
	h.Login(wWrong, reqWrong)

	if wWrong.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", wWrong.Code)
	}

	// 2. Valid Password (with X-Forwarded-Proto: https -> Secure cookie)
	bodyRight, _ := json.Marshal(map[string]string{"password": adminPass})
	reqRight := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(bodyRight))
	reqRight.RemoteAddr = "10.0.0.1:12345"
	reqRight.Header.Set("X-Forwarded-Proto", "https")
	wRight := httptest.NewRecorder()
	h.Login(wRight, reqRight)

	if wRight.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", wRight.Code)
	}
	cookies := wRight.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "relay_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("expected relay_session cookie to be set")
	}
	if !sessionCookie.HttpOnly || !sessionCookie.Secure || sessionCookie.Path != "/" {
		t.Fatalf("invalid cookie properties: HttpOnly=%v, Secure=%v, Path=%v", sessionCookie.HttpOnly, sessionCookie.Secure, sessionCookie.Path)
	}
	if !store.Validate(sessionCookie.Value) {
		t.Fatalf("session cookie value not valid in store")
	}

	// 3. Logout
	reqLogout := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	reqLogout.Header.Set("X-Forwarded-Proto", "https")
	reqLogout.AddCookie(sessionCookie)
	wLogout := httptest.NewRecorder()
	h.Logout(wLogout, reqLogout)

	if wLogout.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on logout, got %d", wLogout.Code)
	}
	if store.Validate(sessionCookie.Value) {
		t.Fatalf("expected session token to be revoked after logout")
	}
	logoutCookies := wLogout.Result().Cookies()
	var clearedCookie *http.Cookie
	for _, c := range logoutCookies {
		if c.Name == "relay_session" {
			clearedCookie = c
			break
		}
	}
	if clearedCookie == nil || clearedCookie.MaxAge >= 0 || !clearedCookie.Secure {
		t.Fatalf("expected relay_session cookie to be cleared with Secure=true (MaxAge < 0)")
	}
}

func TestAuthHandler_RateLimitEnforcement(t *testing.T) {
	adminPass := "supersecret123"
	store := NewSessionStore(24 * time.Hour)
	rl := NewRateLimiter(5, 5*time.Minute)
	h := NewAuthHandler(adminPass, store, rl, nil)
	clientIP := "198.51.100.5:54321"

	// 5 failed login attempts
	for i := 1; i <= 5; i++ {
		body, _ := json.Marshal(map[string]string{"password": "wrong"})
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
		req.RemoteAddr = clientIP
		w := httptest.NewRecorder()
		h.Login(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i, w.Code)
		}
	}

	// 6th attempt should be blocked by rate limiter with 429
	body, _ := json.Marshal(map[string]string{"password": adminPass}) // even with correct pass!
	reqBlocked := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	reqBlocked.RemoteAddr = clientIP
	wBlocked := httptest.NewRecorder()
	h.Login(wBlocked, reqBlocked)

	if wBlocked.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests, got %d", wBlocked.Code)
	}
	retryAfter := wBlocked.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatalf("expected Retry-After header to be set")
	}
	seconds, err := strconv.Atoi(retryAfter)
	if err != nil || seconds <= 0 {
		t.Fatalf("expected positive integer in Retry-After, got %s", retryAfter)
	}
}

func TestRateLimiter_ConcurrentBurstBypass(t *testing.T) {
	window := 5 * time.Second
	rl := NewRateLimiter(5, window)
	ip := "192.0.2.1"

	// Fire 30 concurrent Allow() calls
	concurrency := 30
	results := make(chan bool, concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			allowed, _ := rl.Allow(ip)
			results <- allowed
		}()
	}

	allowedCount := 0
	deniedCount := 0
	for i := 0; i < concurrency; i++ {
		if <-results {
			allowedCount++
		} else {
			deniedCount++
		}
	}

	if allowedCount != 5 {
		t.Fatalf("expected exactly 5 attempts allowed during concurrent burst, got %d", allowedCount)
	}
	if deniedCount != 25 {
		t.Fatalf("expected 25 attempts throttled during concurrent burst, got %d", deniedCount)
	}
}

func TestRateLimiter_SlidingWindowAndCleanup(t *testing.T) {
	window := 100 * time.Millisecond
	rl := NewRateLimiter(3, window)
	ip := "192.0.2.2"

	// 2 failed attempts
	rl.Allow(ip)
	rl.RecordFailure(ip)
	rl.Allow(ip)
	rl.RecordFailure(ip)

	// Wait for window to expire
	time.Sleep(120 * time.Millisecond)

	// Cleanup should remove entry since failures expired and not locked
	rl.Cleanup()
	rl.mu.Lock()
	_, exists := rl.attempts[ip]
	rl.mu.Unlock()
	if exists {
		t.Fatalf("expected entry with expired failures to be evicted by Cleanup()")
	}

	// Next attempt should have clean state (1st attempt)
	allowed, _ := rl.Allow(ip)
	if !allowed {
		t.Fatalf("expected attempt after window expiry to be allowed")
	}
	rl.RecordFailure(ip)
}

func TestExtractClientIP(t *testing.T) {
	tests := []struct {
		name       string
		xff        string
		xRealIP    string
		remoteAddr string
		expected   string
	}{
		{
			name:       "X-Forwarded-For multiple IPs",
			xff:        " 203.0.113.195:8080 , 70.41.3.18, 150.172.238.178 ",
			remoteAddr: "10.0.0.1:12345",
			expected:   "203.0.113.195",
		},
		{
			name:       "X-Real-IP takes precedence over RemoteAddr",
			xRealIP:    "198.51.100.1:9090",
			remoteAddr: "10.0.0.1:12345",
			expected:   "198.51.100.1",
		},
		{
			name:       "Fallback to RemoteAddr",
			remoteAddr: "192.168.1.50:12345",
			expected:   "192.168.1.50",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xRealIP != "" {
				req.Header.Set("X-Real-IP", tc.xRealIP)
			}
			ip := ExtractClientIP(req)
			if ip != tc.expected {
				t.Fatalf("expected IP %q, got %q", tc.expected, ip)
			}
		})
	}
}

func TestAuthHandler_MaxRequestBody(t *testing.T) {
	store := NewSessionStore(24 * time.Hour)
	rl := NewRateLimiter(5, 5*time.Minute)
	h := NewAuthHandler("adminsecret123", store, rl, nil)

	// Create oversized payload (> 16 KB)
	largePassword := make([]byte, 20*1024)
	for i := range largePassword {
		largePassword[i] = 'a'
	}
	body, _ := json.Marshal(map[string]string{"password": string(largePassword)})

	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	h.Login(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for oversized payload, got %d", w.Code)
	}
}

func TestAutoCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := NewSessionStore(10 * time.Millisecond)
	rl := NewRateLimiter(5, 10*time.Millisecond)

	store.StartAutoCleanup(ctx, 20*time.Millisecond)
	rl.StartAutoCleanup(ctx, 20*time.Millisecond)

	_, _ = store.Create()
	rl.Allow("1.2.3.4")
	rl.RecordFailure("1.2.3.4")

	time.Sleep(50 * time.Millisecond)

	store.mu.RLock()
	sessionCount := len(store.sessions)
	store.mu.RUnlock()
	if sessionCount != 0 {
		t.Fatalf("expected auto cleanup to evict sessions, remaining: %d", sessionCount)
	}

	rl.mu.Lock()
	attemptCount := len(rl.attempts)
	rl.mu.Unlock()
	if attemptCount != 0 {
		t.Fatalf("expected auto cleanup to evict rate limiter entries, remaining: %d", attemptCount)
	}
}
