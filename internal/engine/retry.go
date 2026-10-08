package engine

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
)

// Default retry timing parameters.
const (
	DefaultBaseBackoff   = 100 * time.Millisecond
	DefaultMaxBackoff    = 10 * time.Second
	DefaultMaxRetryAfter = 30 * time.Second
)

// RetryConfig encapsulates timing parameters for backoff calculation.
type RetryConfig struct {
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
	MaxRetryAfter time.Duration
}

// RetryEngine provides calculation and evaluation policies for retrying HTTP requests.
type RetryEngine struct {
	config RetryConfig
}

// NewRetryEngine initializes a new RetryEngine with optional custom configuration.
func NewRetryEngine(cfg ...RetryConfig) *RetryEngine {
	c := RetryConfig{
		BaseBackoff:   DefaultBaseBackoff,
		MaxBackoff:    DefaultMaxBackoff,
		MaxRetryAfter: DefaultMaxRetryAfter,
	}
	if len(cfg) > 0 {
		if cfg[0].BaseBackoff > 0 {
			c.BaseBackoff = cfg[0].BaseBackoff
		}
		if cfg[0].MaxBackoff > 0 {
			c.MaxBackoff = cfg[0].MaxBackoff
		}
		if cfg[0].MaxRetryAfter > 0 {
			c.MaxRetryAfter = cfg[0].MaxRetryAfter
		}
	}
	return &RetryEngine{config: c}
}

// CalculateBackoff calculates the wait duration before the next attempt.
// If retryAfterHeader is provided and valid, it takes precedence (capped at MaxRetryAfter).
// Otherwise, it computes backoff using the configured strategy with Full Jitter.
func (e *RetryEngine) CalculateBackoff(attempt int, strategy model.RetryStrategy, retryAfterHeader string) time.Duration {
	if retryAfterHeader != "" {
		if delay, ok := e.ParseRetryAfter(retryAfterHeader, time.Now().UTC()); ok {
			if delay > e.config.MaxRetryAfter {
				return e.config.MaxRetryAfter
			}
			return delay
		}
	}

	var maxWait time.Duration
	switch strategy {
	case model.RetryStrategyLinear:
		if attempt < 1 {
			attempt = 1
		}
		maxWait = e.config.BaseBackoff * time.Duration(attempt)
	default: // Exponential
		exp := attempt - 1
		if exp < 0 {
			exp = 0
		}
		if exp > 30 {
			exp = 30
		}
		maxWait = e.config.BaseBackoff * time.Duration(1<<exp)
	}

	if maxWait > e.config.MaxBackoff {
		maxWait = e.config.MaxBackoff
	}
	if maxWait <= 0 {
		return 0
	}

	// Full Jitter: Sleep duration uniformly distributed in [0, maxWait]
	// Using Go 1.22+ math/rand/v2 Int64N
	jittered := rand.Int64N(int64(maxWait) + 1)
	return time.Duration(jittered)
}

// ParseRetryAfter parses the HTTP Retry-After header which can be either seconds or an HTTP-date.
func (e *RetryEngine) ParseRetryAfter(header string, now time.Time) (time.Duration, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, false
	}

	// 1. Try parsing integer seconds
	if secs, err := strconv.Atoi(header); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}

	// 2. Try parsing HTTP-date formats (RFC 1123, RFC 850, ANSIC)
	formats := []string{
		http.TimeFormat,
		time.RFC850,
		time.ANSIC,
	}

	for _, format := range formats {
		if t, err := time.Parse(format, header); err == nil {
			diff := t.Sub(now)
			if diff <= 0 {
				return 0, true
			}
			return diff, true
		}
	}

	return 0, false
}

// IsRetryableStatus returns true if the HTTP status code is eligible for retry.
func (e *RetryEngine) IsRetryableStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable, // 503
		http.StatusGatewayTimeout:      // 504
		return true
	default:
		return false
	}
}

// IsRetryableError evaluates whether a network or client error qualifies for retrying.
// Note: Client disconnects (context.Canceled) must NEVER be retried.
func (e *RetryEngine) IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Downstream client cancellation must abort immediately without retry
	if errors.Is(err, context.Canceled) {
		return false
	}

	// Attempt timeout is retryable
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// Network temporary or timeout errors
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// DNS errors
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	// System connection errors (connection refused, reset, broken pipe)
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) ||
			errors.Is(opErr.Err, syscall.ECONNRESET) ||
			errors.Is(opErr.Err, syscall.EPIPE) {
			return true
		}
	}

	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}

	return false
}
