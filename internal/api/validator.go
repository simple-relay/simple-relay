package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/simple-relay/simple-relay/internal/model"
)

var (
	// ErrValidationRuleFailed is returned when an incoming request fails configured validation rules.
	ErrValidationRuleFailed = errors.New("validation rule failed")

	// ErrInvalidJSONPayload is returned when a JSON body rule cannot unmarshal payload bytes.
	ErrInvalidJSONPayload = errors.New("invalid json payload")
)

// EvaluateHeaderQueryRules performs Stage 1 cheap O(1) validation on headers and URL query parameters.
func EvaluateHeaderQueryRules(r *http.Request, rules []model.ValidationRule) error {
	for _, rule := range rules {
		switch rule.Source {
		case model.RuleSourceHeader:
			val := r.Header.Get(rule.Key)
			if err := checkRuleMatch(val, rule.Operator, rule.Value); err != nil {
				return err
			}
		case model.RuleSourceQuery:
			val := r.URL.Query().Get(rule.Key)
			if err := checkRuleMatch(val, rule.Operator, rule.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

// EvaluateBodyJSONRules performs Stage 3 validation inspecting dot-notation JSON body fields.
func EvaluateBodyJSONRules(rawBody []byte, rules []model.ValidationRule) error {
	var bodyRules []model.ValidationRule
	for _, rule := range rules {
		if rule.Source == model.RuleSourceBodyJSON {
			bodyRules = append(bodyRules, rule)
		}
	}

	if len(bodyRules) == 0 {
		return nil
	}

	var data any
	if err := json.Unmarshal(rawBody, &data); err != nil {
		return ErrInvalidJSONPayload
	}

	for _, rule := range bodyRules {
		val, exists := extractJSONValue(data, rule.Key)
		if !exists {
			return ErrValidationRuleFailed
		}

		if rule.Operator == model.RuleOperatorExists {
			continue
		}

		if rule.Operator == model.RuleOperatorEquals {
			valStr := formatJSONValue(val)
			if subtle.ConstantTimeCompare([]byte(valStr), []byte(rule.Value)) != 1 {
				return ErrValidationRuleFailed
			}
		}
	}

	return nil
}

func checkRuleMatch(gotVal string, op model.RuleOperator, expectedVal string) error {
	switch op {
	case model.RuleOperatorExists:
		if strings.TrimSpace(gotVal) == "" {
			return ErrValidationRuleFailed
		}
	case model.RuleOperatorEquals:
		if subtle.ConstantTimeCompare([]byte(gotVal), []byte(expectedVal)) != 1 {
			return ErrValidationRuleFailed
		}
	default:
		return ErrValidationRuleFailed
	}
	return nil
}

func extractJSONValue(root any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	current := root

	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		val, exists := m[part]
		if !exists {
			return nil, false
		}
		current = val
	}

	return current, true
}

func formatJSONValue(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case float64:
		// If float has no decimal part, format as integer
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	case bool:
		return fmt.Sprintf("%t", v)
	case nil:
		return ""
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}
