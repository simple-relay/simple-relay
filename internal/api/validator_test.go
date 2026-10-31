package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/simple-relay/simple-relay/internal/model"
)

func TestEvaluateHeaderQueryRules(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/test?token=secret123&env=prod", nil)
	req.Header.Set("X-Webhook-Key", "my-key-abc")
	req.Header.Set("X-Required-Flag", "true")

	t.Run("All rules pass", func(t *testing.T) {
		rules := []model.ValidationRule{
			{Source: model.RuleSourceHeader, Key: "X-Webhook-Key", Operator: model.RuleOperatorEquals, Value: "my-key-abc"},
			{Source: model.RuleSourceHeader, Key: "X-Required-Flag", Operator: model.RuleOperatorExists},
			{Source: model.RuleSourceQuery, Key: "token", Operator: model.RuleOperatorEquals, Value: "secret123"},
			{Source: model.RuleSourceQuery, Key: "env", Operator: model.RuleOperatorExists},
		}
		if err := EvaluateHeaderQueryRules(req, rules); err != nil {
			t.Fatalf("expected all rules to pass, got: %v", err)
		}
	})

	t.Run("Header value mismatch", func(t *testing.T) {
		rules := []model.ValidationRule{
			{Source: model.RuleSourceHeader, Key: "X-Webhook-Key", Operator: model.RuleOperatorEquals, Value: "wrong-key"},
		}
		if err := EvaluateHeaderQueryRules(req, rules); err != ErrValidationRuleFailed {
			t.Fatalf("expected ErrValidationRuleFailed, got: %v", err)
		}
	})

	t.Run("Header missing when exists required", func(t *testing.T) {
		rules := []model.ValidationRule{
			{Source: model.RuleSourceHeader, Key: "X-Missing-Header", Operator: model.RuleOperatorExists},
		}
		if err := EvaluateHeaderQueryRules(req, rules); err != ErrValidationRuleFailed {
			t.Fatalf("expected ErrValidationRuleFailed, got: %v", err)
		}
	})

	t.Run("Query param mismatch", func(t *testing.T) {
		rules := []model.ValidationRule{
			{Source: model.RuleSourceQuery, Key: "token", Operator: model.RuleOperatorEquals, Value: "wrong-token"},
		}
		if err := EvaluateHeaderQueryRules(req, rules); err != ErrValidationRuleFailed {
			t.Fatalf("expected ErrValidationRuleFailed, got: %v", err)
		}
	})

	t.Run("Query param missing when exists required", func(t *testing.T) {
		rules := []model.ValidationRule{
			{Source: model.RuleSourceQuery, Key: "missing_param", Operator: model.RuleOperatorExists},
		}
		if err := EvaluateHeaderQueryRules(req, rules); err != ErrValidationRuleFailed {
			t.Fatalf("expected ErrValidationRuleFailed, got: %v", err)
		}
	})
}

func TestEvaluateBodyJSONRules(t *testing.T) {
	rawJSON := []byte(`{
		"event": {
			"type": "payment.succeeded",
			"amount": 2500,
			"livemode": true,
			"customer": {
				"id": "cus_999",
				"email": "user@example.com"
			}
		}
	}`)

	t.Run("No body rules configured", func(t *testing.T) {
		rules := []model.ValidationRule{
			{Source: model.RuleSourceHeader, Key: "X-Test", Operator: model.RuleOperatorExists},
		}
		// Invalid JSON should not error if no body rules exist
		if err := EvaluateBodyJSONRules([]byte(`invalid-json`), rules); err != nil {
			t.Fatalf("expected nil when no body rules configured, got: %v", err)
		}
	})

	t.Run("Malformed JSON with body rule", func(t *testing.T) {
		rules := []model.ValidationRule{
			{Source: model.RuleSourceBodyJSON, Key: "event.type", Operator: model.RuleOperatorExists},
		}
		if err := EvaluateBodyJSONRules([]byte(`not a json {`), rules); err != ErrInvalidJSONPayload {
			t.Fatalf("expected ErrInvalidJSONPayload, got: %v", err)
		}
	})

	t.Run("Valid nested field equals", func(t *testing.T) {
		rules := []model.ValidationRule{
			{Source: model.RuleSourceBodyJSON, Key: "event.type", Operator: model.RuleOperatorEquals, Value: "payment.succeeded"},
			{Source: model.RuleSourceBodyJSON, Key: "event.amount", Operator: model.RuleOperatorEquals, Value: "2500"},
			{Source: model.RuleSourceBodyJSON, Key: "event.livemode", Operator: model.RuleOperatorEquals, Value: "true"},
			{Source: model.RuleSourceBodyJSON, Key: "event.customer.id", Operator: model.RuleOperatorEquals, Value: "cus_999"},
		}
		if err := EvaluateBodyJSONRules(rawJSON, rules); err != nil {
			t.Fatalf("expected valid evaluation, got: %v", err)
		}
	})

	t.Run("Nested field missing", func(t *testing.T) {
		rules := []model.ValidationRule{
			{Source: model.RuleSourceBodyJSON, Key: "event.nonexistent.field", Operator: model.RuleOperatorExists},
		}
		if err := EvaluateBodyJSONRules(rawJSON, rules); err != ErrValidationRuleFailed {
			t.Fatalf("expected ErrValidationRuleFailed, got: %v", err)
		}
	})

	t.Run("Nested field mismatch", func(t *testing.T) {
		rules := []model.ValidationRule{
			{Source: model.RuleSourceBodyJSON, Key: "event.type", Operator: model.RuleOperatorEquals, Value: "order.cancelled"},
		}
		if err := EvaluateBodyJSONRules(rawJSON, rules); err != ErrValidationRuleFailed {
			t.Fatalf("expected ErrValidationRuleFailed, got: %v", err)
		}
	})

	t.Run("Format and extract various JSON types", func(t *testing.T) {
		extendedJSON := []byte(`{
			"rate": 12.34,
			"null_val": null,
			"tags": ["prod", "us-east"],
			"nested_scalar": "simple string"
		}`)
		rules := []model.ValidationRule{
			{Source: model.RuleSourceBodyJSON, Key: "rate", Operator: model.RuleOperatorEquals, Value: "12.34"},
			{Source: model.RuleSourceBodyJSON, Key: "null_val", Operator: model.RuleOperatorEquals, Value: ""},
			{Source: model.RuleSourceBodyJSON, Key: "tags", Operator: model.RuleOperatorEquals, Value: `["prod","us-east"]`},
			{Source: model.RuleSourceBodyJSON, Key: "nested_scalar.invalid_child", Operator: model.RuleOperatorExists},
		}
		// The first 3 should pass, the 4th should fail because nested_scalar is not a map
		if err := EvaluateBodyJSONRules(extendedJSON, rules[:3]); err != nil {
			t.Fatalf("expected pass for types, got: %v", err)
		}
		if err := EvaluateBodyJSONRules(extendedJSON, rules[3:]); err != ErrValidationRuleFailed {
			t.Fatalf("expected ErrValidationRuleFailed when navigating child of scalar, got: %v", err)
		}
	})
}
