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
)

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
	MaxAttempts          int               `json:"max_attempts"`
	TimeoutMS            int               `json:"timeout_ms"`
	RetryBackoffStrategy RetryStrategy     `json:"retry_backoff_strategy"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
}

var (
	ErrRouteNameRequired        = errors.New("route name is required")
	ErrRoutePathRequired        = errors.New("route path is required")
	ErrRouteDestinationRequired = errors.New("destination_url is required")
	ErrRouteInvalidURL          = errors.New("destination_url must be a valid absolute HTTP/HTTPS URL")
	ErrRouteInvalidType         = errors.New("invalid route type; must be 'inbound' or 'outbound'")
	ErrRouteInvalidMode         = errors.New("invalid route mode; must be 'sync' or 'async'")
	ErrRouteInvalidMaxAttempts  = errors.New("max_attempts must be at least 1")
	ErrRouteInvalidTimeout      = errors.New("timeout_ms must be greater than 0")
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
	return nil
}
