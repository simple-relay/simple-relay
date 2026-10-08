package model

import (
	"strings"
	"testing"
	"time"
)

func TestRoute_Validate(t *testing.T) {
	validRoute := Route{
		Name:                 "stripe-test",
		Type:                 RouteTypeInbound,
		Mode:                 RouteModeAsync,
		Path:                 "/api/v1/webhook/stripe",
		DestinationURL:       "https://internal-api.service/webhook",
		MaxAttempts:          5,
		TimeoutMS:            10000,
		RetryBackoffStrategy: RetryStrategyExponential,
	}

	if err := validRoute.Validate(); err != nil {
		t.Fatalf("expected valid route, got error: %v", err)
	}

	invalidURLRoute := validRoute
	invalidURLRoute.DestinationURL = "invalid-url"
	if err := invalidURLRoute.Validate(); err != ErrRouteInvalidURL {
		t.Fatalf("expected ErrRouteInvalidURL, got: %v", err)
	}

	invalidAttemptsRoute := validRoute
	invalidAttemptsRoute.MaxAttempts = 0
	if err := invalidAttemptsRoute.Validate(); err != ErrRouteInvalidMaxAttempts {
		t.Fatalf("expected ErrRouteInvalidMaxAttempts, got: %v", err)
	}
}

func TestRequest_Validate(t *testing.T) {
	validReq := Request{
		ID:        NewULID(),
		RouteID:   NewULID(),
		Method:    "POST",
		URL:       "/api/v1/webhook/stripe",
		Status:    RequestStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := validReq.Validate(); err != nil {
		t.Fatalf("expected valid request, got error: %v", err)
	}

	invalidStatusReq := validReq
	invalidStatusReq.Status = "unknown_status"
	if err := invalidStatusReq.Validate(); err != ErrRequestInvalidStatus {
		t.Fatalf("expected ErrRequestInvalidStatus, got: %v", err)
	}
}

func TestTruncateResponseBody(t *testing.T) {
	shortBody := "hello world"
	if got := TruncateResponseBody(shortBody); got != shortBody {
		t.Fatalf("expected short body unmodified, got: %s", got)
	}

	exactLimitBody := strings.Repeat("a", MaxResponseBodyBytes)
	if got := TruncateResponseBody(exactLimitBody); len(got) != MaxResponseBodyBytes {
		t.Fatalf("expected len %d, got %d", MaxResponseBodyBytes, len(got))
	}

	oversizedBody := strings.Repeat("b", MaxResponseBodyBytes+5000)
	got := TruncateResponseBody(oversizedBody)
	if len(got) != MaxResponseBodyBytes {
		t.Fatalf("expected truncated to %d bytes, got %d", MaxResponseBodyBytes, len(got))
	}
}
