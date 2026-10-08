package engine

import (
	"context"
	"errors"
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
