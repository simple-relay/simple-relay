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

	// Signature provider validation tests
	missingSecretRoute := validRoute
	missingSecretRoute.SignatureProvider = SignatureProviderStripe
	missingSecretRoute.SignatureSecret = ""
	if err := missingSecretRoute.Validate(); err != ErrRouteSignatureSecretRequired {
		t.Fatalf("expected ErrRouteSignatureSecretRequired, got: %v", err)
	}

	invalidProviderRoute := validRoute
	invalidProviderRoute.SignatureProvider = "unsupported_provider"
	invalidProviderRoute.SignatureSecret = "secret123"
	if err := invalidProviderRoute.Validate(); err != ErrRouteInvalidSignatureProvider {
		t.Fatalf("expected ErrRouteInvalidSignatureProvider, got: %v", err)
	}

	validSecuredRoute := validRoute
	validSecuredRoute.SignatureProvider = SignatureProviderGitHub
	validSecuredRoute.SignatureSecret = "gh_secret_xyz"
	if err := validSecuredRoute.Validate(); err != nil {
		t.Fatalf("expected valid secured route, got: %v", err)
	}

	// Ed25519 provider tests
	ed25519Route := validRoute
	ed25519Route.SignatureProvider = SignatureProviderEd25519
	ed25519Route.SignatureSecret = ""
	if err := ed25519Route.Validate(); err != ErrRouteSignatureSecretRequired {
		t.Fatalf("expected ErrRouteSignatureSecretRequired for ed25519, got: %v", err)
	}
	ed25519Route.SignatureSecret = "base64orhexpubkey"
	if err := ed25519Route.Validate(); err != nil {
		t.Fatalf("expected valid ed25519 route, got: %v", err)
	}

	// RSA-SHA256 provider tests (secret is optional)
	rsaRoute := validRoute
	rsaRoute.SignatureProvider = SignatureProviderRSASHA256
	rsaRoute.SignatureSecret = ""
	if err := rsaRoute.Validate(); err != nil {
		t.Fatalf("expected valid rsa_sha256 route with empty secret, got: %v", err)
	}
	rsaRoute.SignatureSecret = "-----BEGIN PUBLIC KEY-----..."
	if err := rsaRoute.Validate(); err != nil {
		t.Fatalf("expected valid rsa_sha256 route with static secret, got: %v", err)
	}

	// ValidationRules tests
	outboundWithRules := validRoute
	outboundWithRules.Type = RouteTypeOutbound
	outboundWithRules.ValidationRules = []ValidationRule{
		{Source: RuleSourceHeader, Key: "X-Auth", Operator: RuleOperatorExists},
	}
	if err := outboundWithRules.Validate(); err != ErrRouteValidationRulesInboundOnly {
		t.Fatalf("expected ErrRouteValidationRulesInboundOnly, got: %v", err)
	}

	invalidRuleKeyRoute := validRoute
	invalidRuleKeyRoute.ValidationRules = []ValidationRule{
		{Source: RuleSourceHeader, Key: "", Operator: RuleOperatorExists},
	}
	if err := invalidRuleKeyRoute.Validate(); err != ErrRouteInvalidRuleKey {
		t.Fatalf("expected ErrRouteInvalidRuleKey, got: %v", err)
	}

	invalidRuleSourceRoute := validRoute
	invalidRuleSourceRoute.ValidationRules = []ValidationRule{
		{Source: "invalid_source", Key: "X-Test", Operator: RuleOperatorExists},
	}
	if err := invalidRuleSourceRoute.Validate(); err != ErrRouteInvalidRuleSource {
		t.Fatalf("expected ErrRouteInvalidRuleSource, got: %v", err)
	}

	invalidRuleOpRoute := validRoute
	invalidRuleOpRoute.ValidationRules = []ValidationRule{
		{Source: RuleSourceHeader, Key: "X-Test", Operator: "invalid_op"},
	}
	if err := invalidRuleOpRoute.Validate(); err != ErrRouteInvalidRuleOperator {
		t.Fatalf("expected ErrRouteInvalidRuleOperator, got: %v", err)
	}

	validRulesRoute := validRoute
	validRulesRoute.ValidationRules = []ValidationRule{
		{Source: RuleSourceHeader, Key: "X-Token", Operator: RuleOperatorEquals, Value: "secret"},
		{Source: RuleSourceQuery, Key: "sig", Operator: RuleOperatorExists},
		{Source: RuleSourceBodyJSON, Key: "event.type", Operator: RuleOperatorEquals, Value: "user.created"},
	}
	if err := validRulesRoute.Validate(); err != nil {
		t.Fatalf("expected valid rules route, got: %v", err)
	}

	// Backoff override & RetryNonIdempotent validation tests
	negativeBaseRoute := validRoute
	negativeBaseRoute.BaseBackoffMS = -100
	if err := negativeBaseRoute.Validate(); err != ErrRouteInvalidBaseBackoff {
		t.Fatalf("expected ErrRouteInvalidBaseBackoff, got: %v", err)
	}

	negativeMaxRoute := validRoute
	negativeMaxRoute.MaxBackoffMS = -1000
	if err := negativeMaxRoute.Validate(); err != ErrRouteInvalidMaxBackoff {
		t.Fatalf("expected ErrRouteInvalidMaxBackoff, got: %v", err)
	}

	invertedRangeRoute := validRoute
	invertedRangeRoute.BaseBackoffMS = 20000
	invertedRangeRoute.MaxBackoffMS = 5000
	if err := invertedRangeRoute.Validate(); err != ErrRouteBackoffRange {
		t.Fatalf("expected ErrRouteBackoffRange, got: %v", err)
	}

	validBackoffOverrideRoute := validRoute
	validBackoffOverrideRoute.BaseBackoffMS = 5000
	validBackoffOverrideRoute.MaxBackoffMS = 900000
	validBackoffOverrideRoute.RetryNonIdempotent = true
	if err := validBackoffOverrideRoute.Validate(); err != nil {
		t.Fatalf("expected valid route with backoff overrides, got: %v", err)
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

	emptyIDReq := validReq
	emptyIDReq.ID = ""
	if err := emptyIDReq.Validate(); err != ErrRequestIDRequired {
		t.Fatalf("expected ErrRequestIDRequired, got: %v", err)
	}

	emptyRouteIDReq := validReq
	emptyRouteIDReq.RouteID = "   "
	if err := emptyRouteIDReq.Validate(); err != ErrRequestRouteIDRequired {
		t.Fatalf("expected ErrRequestRouteIDRequired, got: %v", err)
	}

	emptyMethodReq := validReq
	emptyMethodReq.Method = ""
	if err := emptyMethodReq.Validate(); err != ErrRequestMethodRequired {
		t.Fatalf("expected ErrRequestMethodRequired, got: %v", err)
	}

	emptyURLReq := validReq
	emptyURLReq.URL = " "
	if err := emptyURLReq.Validate(); err != ErrRequestURLRequired {
		t.Fatalf("expected ErrRequestURLRequired, got: %v", err)
	}

	for _, st := range []RequestStatus{
		RequestStatusPending,
		RequestStatusProcessing,
		RequestStatusSuccess,
		RequestStatusFailed,
		RequestStatusDLQ,
	} {
		r := validReq
		r.Status = st
		if err := r.Validate(); err != nil {
			t.Fatalf("expected status %s to be valid, got: %v", st, err)
		}
	}
}

func TestAttempt_Validate(t *testing.T) {
	validAttempt := Attempt{
		ID:            NewULID(),
		RequestID:     NewULID(),
		AttemptNumber: 1,
		ExecutedAt:    time.Now(),
		DurationMS:    120,
	}

	if err := validAttempt.Validate(); err != nil {
		t.Fatalf("expected valid attempt, got: %v", err)
	}

	emptyIDAttempt := validAttempt
	emptyIDAttempt.ID = "  "
	if err := emptyIDAttempt.Validate(); err != ErrAttemptIDRequired {
		t.Fatalf("expected ErrAttemptIDRequired, got: %v", err)
	}

	emptyReqIDAttempt := validAttempt
	emptyReqIDAttempt.RequestID = ""
	if err := emptyReqIDAttempt.Validate(); err != ErrAttemptRequestIDRequired {
		t.Fatalf("expected ErrAttemptRequestIDRequired, got: %v", err)
	}

	zeroAttemptNum := validAttempt
	zeroAttemptNum.AttemptNumber = 0
	if err := zeroAttemptNum.Validate(); err != ErrAttemptNumberInvalid {
		t.Fatalf("expected ErrAttemptNumberInvalid, got: %v", err)
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
