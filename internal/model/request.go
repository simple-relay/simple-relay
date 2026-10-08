package model

import (
	"errors"
	"strings"
	"time"
)

type RequestStatus string

const (
	RequestStatusPending    RequestStatus = "pending"
	RequestStatusProcessing RequestStatus = "processing"
	RequestStatusSuccess    RequestStatus = "success"
	RequestStatusFailed     RequestStatus = "failed"
	RequestStatusDLQ        RequestStatus = "dlq"
)

// Request represents a durable HTTP payload and its execution state.
type Request struct {
	ID            string            `json:"id"`
	RouteID       string            `json:"route_id"`
	Method        string            `json:"method"`
	URL           string            `json:"url"`
	Headers       map[string]string `json:"headers"`
	Body          []byte            `json:"-"` // Raw bytes preserved, not directly dumped into standard JSON logs
	Status        RequestStatus     `json:"status"`
	AttemptsCount int               `json:"attempts_count"`
	NextAttemptAt *time.Time        `json:"next_attempt_at,omitempty"`
	LastError     *string           `json:"last_error,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

var (
	ErrRequestIDRequired      = errors.New("request id is required")
	ErrRequestRouteIDRequired = errors.New("route_id is required")
	ErrRequestMethodRequired  = errors.New("request method is required")
	ErrRequestURLRequired     = errors.New("request url is required")
	ErrRequestInvalidStatus   = errors.New("invalid request status")
)

// Validate checks the request's fields for logical correctness.
func (r *Request) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return ErrRequestIDRequired
	}
	if strings.TrimSpace(r.RouteID) == "" {
		return ErrRequestRouteIDRequired
	}
	if strings.TrimSpace(r.Method) == "" {
		return ErrRequestMethodRequired
	}
	if strings.TrimSpace(r.URL) == "" {
		return ErrRequestURLRequired
	}
	switch r.Status {
	case RequestStatusPending, RequestStatusProcessing, RequestStatusSuccess, RequestStatusFailed, RequestStatusDLQ:
	default:
		return ErrRequestInvalidStatus
	}
	return nil
}
