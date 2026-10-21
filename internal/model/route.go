package model

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

type RouteType string

const (
	RouteTypeInbound  RouteType = "inbound"
	RouteTypeOutbound RouteType = "outbound"
)

type RouteMode string

const (
	RouteModeSync  RouteMode = "sync"
	RouteModeAsync RouteMode = "async"
)

type SignatureProvider string

const (
	SignatureProviderNone       SignatureProvider = "none"
	SignatureProviderStripe     SignatureProvider = "stripe"
	SignatureProviderGitHub     SignatureProvider = "github"
	SignatureProviderHMACSHA256 SignatureProvider = "hmac_sha256"
	SignatureProviderEd25519    SignatureProvider = "ed25519"
	SignatureProviderRSASHA256  SignatureProvider = "rsa_sha256"
)

type RuleSource string

const (
	RuleSourceHeader   RuleSource = "header"
	RuleSourceQuery    RuleSource = "query"
	RuleSourceBodyJSON RuleSource = "body_json"
)

type RuleOperator string

const (
	RuleOperatorEquals RuleOperator = "equals"
	RuleOperatorExists RuleOperator = "exists"
)

// ValidationRule defines an incoming pre-persistence validation constraint.
type ValidationRule struct {
	Source   RuleSource   `json:"source"`
	Key      string       `json:"key"`
	Operator RuleOperator `json:"operator"`
	Value    string       `json:"value,omitempty"`
}

type RetryStrategy string

const (
	RetryStrategyExponential RetryStrategy = "exp"
	RetryStrategyLinear      RetryStrategy = "linear"
)

// Route represents the routing and verification configuration.
type Route struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	Type                 RouteType         `json:"type"`
	Mode                 RouteMode         `json:"mode"`
	Path                 string            `json:"path"`
	DestinationURL       string            `json:"destination_url"`
	SignatureProvider    SignatureProvider `json:"signature_provider"`
	SignatureSecret      string            `json:"signature_secret,omitempty"`
	ValidationRules      []ValidationRule  `json:"validation_rules,omitempty"`
	MaxAttempts          int               `json:"max_attempts"`
	TimeoutMS            int               `json:"timeout_ms"`
	RetryBackoffStrategy RetryStrategy     `json:"retry_backoff_strategy"`
	BaseBackoffMS        int               `json:"base_backoff_ms"`
	MaxBackoffMS         int               `json:"max_backoff_ms"`
	RetryNonIdempotent   bool              `json:"retry_non_idempotent"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
}

var (
	ErrRouteNameRequired               = errors.New("route name is required")
	ErrRoutePathRequired               = errors.New("route path is required")
	ErrRouteDestinationRequired        = errors.New("destination_url is required")
	ErrRouteInvalidURL                 = errors.New("destination_url must be a valid absolute HTTP/HTTPS URL")
	ErrRouteInvalidType                = errors.New("invalid route type; must be 'inbound' or 'outbound'")
	ErrRouteInvalidMode                = errors.New("invalid route mode; must be 'sync' or 'async'")
	ErrRouteInvalidMaxAttempts         = errors.New("max_attempts must be at least 1")
	ErrRouteInvalidTimeout             = errors.New("timeout_ms must be greater than 0")
	ErrRouteInvalidBaseBackoff         = errors.New("base_backoff_ms must be greater than or equal to 0")
	ErrRouteInvalidMaxBackoff          = errors.New("max_backoff_ms must be greater than or equal to 0")
	ErrRouteBackoffRange               = errors.New("base_backoff_ms cannot be greater than max_backoff_ms")
	ErrRouteSignatureSecretRequired    = errors.New("signature_secret is required when signature_provider is enabled")
	ErrRouteInvalidSignatureProvider   = errors.New("invalid signature_provider")
	ErrRouteValidationRulesInboundOnly = errors.New("validation_rules are only supported on inbound routes")
	ErrRouteInvalidRuleSource          = errors.New("invalid validation rule source; must be 'header', 'query', or 'body_json'")
	ErrRouteInvalidRuleOperator        = errors.New("invalid validation rule operator; must be 'equals' or 'exists'")
	ErrRouteInvalidRuleKey             = errors.New("validation rule key is required")
)

// Validate checks the route's fields for logical correctness.
func (r *Route) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return ErrRouteNameRequired
	}
	if strings.TrimSpace(r.Path) == "" {
		return ErrRoutePathRequired
	}
	if strings.TrimSpace(r.DestinationURL) == "" {
		return ErrRouteDestinationRequired
	}
	u, err := url.ParseRequestURI(r.DestinationURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ErrRouteInvalidURL
	}
	if r.Type != RouteTypeInbound && r.Type != RouteTypeOutbound {
		return ErrRouteInvalidType
	}
	if r.Mode != RouteModeSync && r.Mode != RouteModeAsync {
		return ErrRouteInvalidMode
	}
	if r.MaxAttempts < 1 {
		return ErrRouteInvalidMaxAttempts
	}
	if r.TimeoutMS <= 0 {
		return ErrRouteInvalidTimeout
	}
	if r.BaseBackoffMS < 0 || time.Duration(r.BaseBackoffMS)*time.Millisecond < 0 {
		return ErrRouteInvalidBaseBackoff
	}
	if r.MaxBackoffMS < 0 || time.Duration(r.MaxBackoffMS)*time.Millisecond < 0 {
		return ErrRouteInvalidMaxBackoff
	}
	if r.BaseBackoffMS > 0 && r.MaxBackoffMS > 0 && r.BaseBackoffMS > r.MaxBackoffMS {
		return ErrRouteBackoffRange
	}

	if len(r.ValidationRules) > 0 {
		if r.Type != RouteTypeInbound {
			return ErrRouteValidationRulesInboundOnly
		}
		for _, rule := range r.ValidationRules {
			if strings.TrimSpace(rule.Key) == "" {
				return ErrRouteInvalidRuleKey
			}
			switch rule.Source {
			case RuleSourceHeader, RuleSourceQuery, RuleSourceBodyJSON:
			default:
				return ErrRouteInvalidRuleSource
			}
			switch rule.Operator {
			case RuleOperatorEquals, RuleOperatorExists:
			default:
				return ErrRouteInvalidRuleOperator
			}
		}
	}

	if r.Type == RouteTypeInbound {
		switch r.SignatureProvider {
		case SignatureProviderNone, "":
			// Valid: no signature verification required
		case SignatureProviderStripe, SignatureProviderGitHub, SignatureProviderHMACSHA256, SignatureProviderEd25519:
			if strings.TrimSpace(r.SignatureSecret) == "" {
				return ErrRouteSignatureSecretRequired
			}
		case SignatureProviderRSASHA256:
			// SignatureSecret is optional for RSA-SHA256 (defaults to dynamic AWS SNS cert download with cache)
		default:
			return ErrRouteInvalidSignatureProvider
		}
	}
	return nil
}
