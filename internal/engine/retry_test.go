package engine

import (
	"context"
	"errors"
	"math"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
)

func TestParseRetryAfter(t *testing.T) {
	e := NewRetryEngine()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		header     string
		wantDelay  time.Duration
		wantParsed bool
	}{
		{
			name:       "empty header",
			header:     "",
			wantDelay:  0,
			wantParsed: false,
		},
		{
			name:       "valid integer seconds",
			header:     "5",
			wantDelay:  5 * time.Second,
			wantParsed: true,
		},
		{
			name:       "zero integer seconds",
			header:     "0",
			wantDelay:  0,
			wantParsed: true,
		},
		{
			name:       "negative integer seconds",
			header:     "-5",
			wantDelay:  0,
			wantParsed: false,
		},
		{
			name:       "valid RFC1123 date",
			header:     "Thu, 08 Oct 2026 12:00:15 GMT",
			wantDelay:  15 * time.Second,
			wantParsed: true,
		},
		{
			name:       "valid RFC850 date",
			header:     "Thursday, 08-Oct-26 12:00:20 GMT",
			wantDelay:  20 * time.Second,
			wantParsed: true,
		},
		{
			name:       "valid ANSIC date",
			header:     "Thu Oct  8 12:00:25 2026",
			wantDelay:  25 * time.Second,
			wantParsed: true,
		},
		{
			name:       "past date",
			header:     "Thu, 08 Oct 2026 11:59:00 GMT",
			wantDelay:  0,
			wantParsed: true,
		},
		{
			name:       "huge integer seconds does not overflow int64",
			header:     "9223372037",
			wantDelay:  time.Duration(math.MaxInt64),
			wantParsed: true,
		},
		{
			name:       "malformed string",
			header:     "invalid-delay",
			wantDelay:  0,
			wantParsed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delay, parsed := e.ParseRetryAfter(tt.header, now)
			if parsed != tt.wantParsed {
				t.Fatalf("ParseRetryAfter() parsed = %v, want %v", parsed, tt.wantParsed)
			}
			if delay != tt.wantDelay {
				t.Fatalf("ParseRetryAfter() delay = %v, want %v", delay, tt.wantDelay)
			}
		})
	}
}

func TestCalculateBackoffExponential(t *testing.T) {
	cfg := RetryConfig{
		BaseBackoff:   100 * time.Millisecond,
		MaxBackoff:    10 * time.Second,
		MaxRetryAfter: 30 * time.Second,
	}
	e := NewRetryEngine(cfg)

	// Since Full Jitter is applied, duration must be in [0, Min(MaxBackoff, BaseBackoff * 2^(attempt-1))]
	for attempt := 1; attempt <= 10; attempt++ {
		var maxExpected time.Duration
		switch attempt {
		case 1:
			maxExpected = 100 * time.Millisecond
		case 2:
			maxExpected = 200 * time.Millisecond
		case 3:
			maxExpected = 400 * time.Millisecond
		case 4:
			maxExpected = 800 * time.Millisecond
		default:
			maxExpected = 10 * time.Second // capped at MaxBackoff
		}

		for i := 0; i < 50; i++ {
			delay := e.CalculateBackoff(attempt, model.RetryStrategyExponential, "")
			if delay < 0 || delay > maxExpected {
				t.Fatalf("attempt %d: delay %v out of range [0, %v]", attempt, delay, maxExpected)
			}
		}
	}
}

func TestCalculateBackoffLinear(t *testing.T) {
	cfg := RetryConfig{
		BaseBackoff:   100 * time.Millisecond,
		MaxBackoff:    10 * time.Second,
		MaxRetryAfter: 30 * time.Second,
	}
	e := NewRetryEngine(cfg)

	for attempt := 1; attempt <= 5; attempt++ {
		maxExpected := time.Duration(attempt) * 100 * time.Millisecond
		for i := 0; i < 50; i++ {
			delay := e.CalculateBackoff(attempt, model.RetryStrategyLinear, "")
			if delay < 0 || delay > maxExpected {
				t.Fatalf("linear attempt %d: delay %v out of range [0, %v]", attempt, delay, maxExpected)
			}
		}
	}
}

func TestCalculateBackoffWithRetryAfter(t *testing.T) {
	cfg := RetryConfig{
		BaseBackoff:   100 * time.Millisecond,
		MaxBackoff:    10 * time.Second,
		MaxRetryAfter: 30 * time.Second,
	}
	e := NewRetryEngine(cfg)

	// Retry-After of 5 seconds should be respected
	delay := e.CalculateBackoff(1, model.RetryStrategyExponential, "5")
	if delay != 5*time.Second {
		t.Fatalf("expected delay 5s from Retry-After, got %v", delay)
	}

	// Retry-After exceeding MaxRetryAfter (30s) must be clamped to 30s
	delayClamped := e.CalculateBackoff(1, model.RetryStrategyExponential, "120")
	if delayClamped != 30*time.Second {
		t.Fatalf("expected delay clamped to 30s, got %v", delayClamped)
	}
}

func TestIsRetryableStatus(t *testing.T) {
	e := NewRetryEngine()

	retryable := []int{429, 500, 502, 503, 504}
	for _, code := range retryable {
		if !e.IsRetryableStatus(code) {
			t.Errorf("status %d should be retryable", code)
		}
	}

	nonRetryable := []int{200, 201, 204, 301, 302, 400, 401, 403, 404, 405, 422}
	for _, code := range nonRetryable {
		if e.IsRetryableStatus(code) {
			t.Errorf("status %d should NOT be retryable", code)
		}
	}
}

func TestIsRetryableError(t *testing.T) {
	e := NewRetryEngine()

	// nil error
	if e.IsRetryableError(nil) {
		t.Error("nil error should not be retryable")
	}

	// Client cancellation must NOT be retryable
	if e.IsRetryableError(context.Canceled) {
		t.Error("context.Canceled must not be retryable")
	}

	// Per-attempt timeout must be retryable
	if !e.IsRetryableError(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded should be retryable")
	}

	// Network temporary / timeout error
	timeoutNetErr := &net.DNSError{IsTimeout: true}
	if !e.IsRetryableError(timeoutNetErr) {
		t.Error("net timeout error should be retryable")
	}

	// Connection refused
	connRefused := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: syscall.ECONNREFUSED,
	}
	if !e.IsRetryableError(connRefused) {
		t.Error("connection refused should be retryable")
	}

	// Connection reset
	connReset := &net.OpError{
		Op:  "read",
		Net: "tcp",
		Err: syscall.ECONNRESET,
	}
	if !e.IsRetryableError(connReset) {
		t.Error("connection reset should be retryable")
	}

	// Generic unrelated error
	if e.IsRetryableError(errors.New("unrelated business error")) {
		t.Error("unrelated generic error should not be retryable")
	}
}

func TestNewAsyncRetryEngine_Defaults(t *testing.T) {
	e := NewAsyncRetryEngine()
	if e.config.BaseBackoff != DefaultAsyncBaseBackoff {
		t.Fatalf("expected DefaultAsyncBaseBackoff 5s, got %v", e.config.BaseBackoff)
	}
	if e.config.MaxBackoff != DefaultAsyncMaxBackoff {
		t.Fatalf("expected DefaultAsyncMaxBackoff 15m, got %v", e.config.MaxBackoff)
	}
	if e.config.MaxRetryAfter != DefaultAsyncMaxRetryAfter {
		t.Fatalf("expected DefaultAsyncMaxRetryAfter 15m, got %v", e.config.MaxRetryAfter)
	}

	// Retry-After of 120s should be respected in async engine (not clamped to 30s)
	if delay := e.CalculateBackoff(1, model.RetryStrategyExponential, "120"); delay != 120*time.Second {
		t.Fatalf("expected 120s delay for async Retry-After, got %v", delay)
	}
}

func TestCalculateBackoffWithOverrides(t *testing.T) {
	e := NewRetryEngine() // sync defaults: 100ms base, 10s max

	// 1. Custom base (500ms) and max (2000ms)
	for i := 0; i < 50; i++ {
		d1 := e.CalculateBackoffWithOverrides(1, model.RetryStrategyExponential, "", 500, 2000)
		if d1 < 0 || d1 > 500*time.Millisecond {
			t.Fatalf("attempt 1 override delay %v out of [0, 500ms]", d1)
		}
		d5 := e.CalculateBackoffWithOverrides(5, model.RetryStrategyExponential, "", 500, 2000)
		if d5 < 0 || d5 > 2*time.Second {
			t.Fatalf("attempt 5 override delay %v out of [0, 2s]", d5)
		}
	}

	// 2. Only base_backoff_ms overridden above engine's default MaxBackoff (15000ms > 10s)
	for i := 0; i < 20; i++ {
		d := e.CalculateBackoffWithOverrides(1, model.RetryStrategyExponential, "", 15000, 0)
		if d < 0 || d > 15*time.Second {
			t.Fatalf("expected clamp to 15s when base_backoff_ms=15000 and max_backoff_ms=0, got %v", d)
		}
	}

	// 3. MaxBackoff override > DefaultMaxRetryAfter (60s > 30s) expands Retry-After cap
	dRetryAfter := e.CalculateBackoffWithOverrides(1, model.RetryStrategyExponential, "45", 1000, 60000)
	if dRetryAfter != 45*time.Second {
		t.Fatalf("expected Retry-After of 45s allowed when maxBackoffMS=60000, got %v", dRetryAfter)
	}

	// 4. Async engine with only max_backoff_ms overridden below DefaultAsyncBaseBackoff (2000ms < 5000ms)
	asyncEngine := NewAsyncRetryEngine()
	for i := 0; i < 20; i++ {
		d := asyncEngine.CalculateBackoffWithOverrides(1, model.RetryStrategyExponential, "", 0, 2000)
		if d < 0 || d > 2*time.Second {
			t.Fatalf("expected async delay clamped to explicit max_backoff_ms=2s, got %v", d)
		}
	}

	// 5. High attempt count (attempt=35) with large base_backoff_ms (60000ms) must not overflow int64 into 0
	var nonZeroSeen bool
	for i := 0; i < 50; i++ {
		d := asyncEngine.CalculateBackoffWithOverrides(35, model.RetryStrategyExponential, "", 60000, 900000)
		if d < 0 || d > 900*time.Second {
			t.Fatalf("attempt 35 overflowed or exceeded maxBackoff: %v", d)
		}
		if d > 0 {
			nonZeroSeen = true
		}
	}
	if !nonZeroSeen {
		t.Fatal("expected non-zero jittered backoff on attempt 35 with base_backoff_ms=60000, got all 0s (int64 overflow)")
	}

	// 6. MaxBackoff override < DefaultMaxRetryAfter (1000ms < 30s) bounds Retry-After to 1s
	dClampedRoute := e.CalculateBackoffWithOverrides(1, model.RetryStrategyExponential, "20", 100, 1000)
	if dClampedRoute != 1*time.Second {
		t.Fatalf("expected Retry-After clamped to route maxBackoffMS=1000ms (1s), got %v", dClampedRoute)
	}

	// 7. Huge Retry-After value (overflowing int64 seconds) clamped to maxRetryAfter
	dHugeRetryAfter := e.CalculateBackoffWithOverrides(1, model.RetryStrategyExponential, "9223372037", 100, 10000)
	if dHugeRetryAfter != 10*time.Second {
		t.Fatalf("expected huge Retry-After clamped to 10s, got %v", dHugeRetryAfter)
	}
}
