package model

import (
	"errors"
	"strings"
	"time"
)

// MaxResponseBodyBytes defines the 32 KB response truncation limit to prevent storage bloat.
const MaxResponseBodyBytes = 32 * 1024

// TruncateResponseBody truncates a response body string to MaxResponseBodyBytes (32 KB).
func TruncateResponseBody(body string) string {
	if len(body) <= MaxResponseBodyBytes {
		return body
	}
	return body[:MaxResponseBodyBytes]
}

// Attempt represents an execution attempt for a request.
type Attempt struct {
	ID                 string    `json:"id"`
	RequestID          string    `json:"request_id"`
	AttemptNumber      int       `json:"attempt_number"`
	ResponseStatusCode *int      `json:"response_status_code,omitempty"`
	ResponseBody       *string   `json:"response_body,omitempty"`
	ErrorMessage       *string   `json:"error_message,omitempty"`
	ExecutedAt         time.Time `json:"executed_at"`
	DurationMS         int64     `json:"duration_ms"`
}

var (
	ErrAttemptIDRequired        = errors.New("attempt id is required")
	ErrAttemptRequestIDRequired = errors.New("request_id is required")
	ErrAttemptNumberInvalid     = errors.New("attempt_number must be at least 1")
)

// Validate checks the attempt's fields for logical correctness.
func (a *Attempt) Validate() error {
	if strings.TrimSpace(a.ID) == "" {
		return ErrAttemptIDRequired
	}
	if strings.TrimSpace(a.RequestID) == "" {
		return ErrAttemptRequestIDRequired
	}
	if a.AttemptNumber < 1 {
		return ErrAttemptNumberInvalid
	}
	return nil
}
