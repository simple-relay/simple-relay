package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrUnauthorized is returned when server rejects a request with HTTP 401.
	ErrUnauthorized = errors.New("authentication required: please provide --api-key or set RELAY_API_KEY")
)

// HealthResponse represents daemon health status.
type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
	Version   string `json:"version,omitempty"`
}

// DLQItem represents a failed request in the Dead Letter Queue.
type DLQItem struct {
	ID            string    `json:"id"`
	RouteID       string    `json:"route_id"`
	Method        string    `json:"method"`
	URL           string    `json:"url"`
	Status        string    `json:"status"`
	AttemptsCount int       `json:"attempts_count"`
	LastError     *string   `json:"last_error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// AttemptDetail represents an individual outbound delivery attempt.
type AttemptDetail struct {
	AttemptNumber      int       `json:"attempt_number"`
	ResponseStatusCode *int      `json:"response_status_code,omitempty"`
	StatusCode         int       `json:"status_code,omitempty"`
	DurationMS         int64     `json:"duration_ms"`
	ErrorMessage       *string   `json:"error_message,omitempty"`
	ExecutedAt         time.Time `json:"executed_at,omitempty"`
	AttemptedAt        time.Time `json:"attempted_at,omitempty"`
}

// GetStatusCode returns the HTTP status code of the attempt if present, or 0.
func (a *AttemptDetail) GetStatusCode() int {
	if a.ResponseStatusCode != nil && *a.ResponseStatusCode > 0 {
		return *a.ResponseStatusCode
	}
	return a.StatusCode
}

// GetAttemptedAt returns the timestamp when the attempt occurred.
func (a *AttemptDetail) GetAttemptedAt() time.Time {
	if !a.ExecutedAt.IsZero() {
		return a.ExecutedAt
	}
	return a.AttemptedAt
}

// RequestDetail represents full request metadata and audit attempt history.
type RequestDetail struct {
	ID            string            `json:"id"`
	RouteID       string            `json:"route_id"`
	Method        string            `json:"method"`
	URL           string            `json:"url"`
	Headers       map[string]string `json:"headers"`
	Body          string            `json:"body"`
	Status        string            `json:"status"`
	AttemptsCount int               `json:"attempts_count"`
	LastError     *string           `json:"last_error,omitempty"`
	CreatedAt     string            `json:"created_at"`
	UpdatedAt     string            `json:"updated_at"`
	Attempts      []*AttemptDetail  `json:"attempts"`
}

// RelayClient provides programmatic HTTP interactions with a Simple Relay daemon.
type RelayClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient initializes a new RelayClient instance.
func NewClient(baseURL, apiKey string, timeout time.Duration) *RelayClient {
	baseURL = strings.TrimRight(baseURL, "/")
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &RelayClient{
		baseURL: baseURL,
		apiKey:  strings.TrimSpace(apiKey),
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("stopped after 10 redirects")
				}
				if len(via) > 0 && req.URL.Host != via[0].URL.Host {
					req.Header.Del("X-API-Key")
					req.Header.Del("Authorization")
				}
				return nil
			},
		},
	}
}

func (c *RelayClient) doRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	url := fmt.Sprintf("%s%s", c.baseURL, path)
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request to %s: %w", url, err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		_ = resp.Body.Close()
		return nil, ErrUnauthorized
	}

	return resp, nil
}

// Health checks the responsiveness and latency of the daemon.
func (c *RelayClient) Health(ctx context.Context) (*HealthResponse, time.Duration, error) {
	start := time.Now()
	resp, err := c.doRequest(ctx, http.MethodGet, "/healthz", nil)
	latency := time.Since(start)
	if err != nil {
		return nil, latency, err
	}
	defer resp.Body.Close()

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, latency, fmt.Errorf("failed to decode health response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return &health, latency, fmt.Errorf("daemon reported non-OK status code %d", resp.StatusCode)
	}

	return &health, latency, nil
}

// GetQueueStats queries pending and processing counts.
// It prefers the single round-trip /api/v1/queue/stats endpoint, falling back to /api/v1/requests if running against an older server.
func (c *RelayClient) GetQueueStats(ctx context.Context) (pending, inFlight int64, err error) {
	type queueStatsResp struct {
		Pending    int64  `json:"pending"`
		InFlight   int64  `json:"in_flight"`
		QueueDepth int64  `json:"queue_depth"`
		Status     string `json:"status"`
	}

	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/queue/stats", nil)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var stats queueStatsResp
			if err := json.NewDecoder(resp.Body).Decode(&stats); err == nil {
				return stats.Pending, stats.InFlight, nil
			}
		} else if resp.StatusCode != http.StatusNotFound {
			return 0, 0, fmt.Errorf("query queue stats returned status %d", resp.StatusCode)
		}
	} else if !strings.Contains(err.Error(), "404") {
		return 0, 0, err
	}

	// Fallback to legacy requests queries if endpoint is not found
	type countResp struct {
		Total int64 `json:"total"`
	}

	// 1. Query pending
	respPending, err := c.doRequest(ctx, http.MethodGet, "/api/v1/requests?status=pending&limit=1", nil)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to query pending queue count: %w", err)
	}
	defer respPending.Body.Close()

	if respPending.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("query pending returned status %d", respPending.StatusCode)
	}
	var countP countResp
	if err := json.NewDecoder(respPending.Body).Decode(&countP); err != nil {
		return 0, 0, fmt.Errorf("failed to decode pending count: %w", err)
	}
	pending = countP.Total

	// 2. Query processing (in_flight)
	respProcessing, err := c.doRequest(ctx, http.MethodGet, "/api/v1/requests?status=processing&limit=1", nil)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to query in-flight queue count: %w", err)
	}
	defer respProcessing.Body.Close()

	if respProcessing.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("query in-flight returned status %d", respProcessing.StatusCode)
	}
	var countI countResp
	if err := json.NewDecoder(respProcessing.Body).Decode(&countI); err != nil {
		return 0, 0, fmt.Errorf("failed to decode in-flight count: %w", err)
	}
	inFlight = countI.Total

	return pending, inFlight, nil
}

// ListRoutes fetches active routes and builds an ID-to-Name map.
func (c *RelayClient) ListRoutes(ctx context.Context) (map[string]string, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/routes", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list routes: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list routes returned status %d", resp.StatusCode)
	}

	// Constrain memory consumption on route listing to 4 MB
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read routes response: %w", err)
	}

	result := make(map[string]string)

	// Try decoding as raw array []*Route
	var routeList []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(bodyBytes, &routeList); err == nil {
		for _, r := range routeList {
			result[r.ID] = r.Name
		}
		return result, nil
	}

	// Fallback to wrapped object {"routes": [...]}
	var data struct {
		Routes []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(bodyBytes, &data); err == nil {
		for _, r := range data.Routes {
			result[r.ID] = r.Name
		}
		return result, nil
	}

	return nil, fmt.Errorf("failed to decode routes response")
}

// ListDLQ fetches items currently present in the Dead Letter Queue.
func (c *RelayClient) ListDLQ(ctx context.Context, limit int) ([]DLQItem, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	path := fmt.Sprintf("/api/v1/dlq?limit=%d", limit)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list dlq: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("list dlq returned status %d", resp.StatusCode)
	}

	var data struct {
		Items []DLQItem `json:"items"`
		Total int64     `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, 0, fmt.Errorf("failed to decode dlq items: %w", err)
	}

	return data.Items, data.Total, nil
}

// InspectRequest fetches comprehensive details and attempts for a specific request.
func (c *RelayClient) InspectRequest(ctx context.Context, id string) (*RequestDetail, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("request id cannot be empty")
	}
	path := fmt.Sprintf("/api/v1/requests/%s", url.PathEscape(id))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect request %s: %w", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("request %s not found", id)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("inspect request returned status %d", resp.StatusCode)
	}

	var detail RequestDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return nil, fmt.Errorf("failed to decode request detail: %w", err)
	}

	return &detail, nil
}

// ReplayDLQ requeues a single failed message back to pending.
func (c *RelayClient) ReplayDLQ(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("request id cannot be empty")
	}
	path := fmt.Sprintf("/api/v1/dlq/%s/replay", url.PathEscape(id))
	resp, err := c.doRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		return fmt.Errorf("failed to replay dlq request %s: %w", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("replay request returned status %d", resp.StatusCode)
	}
	return nil
}

// ReplayRequest requeues any historical request (success, failed, or dlq) back to pending.
func (c *RelayClient) ReplayRequest(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("request id cannot be empty")
	}
	path := fmt.Sprintf("/api/v1/requests/%s/replay", url.PathEscape(id))
	resp, err := c.doRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		return fmt.Errorf("failed to replay request %s: %w", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("request %s not found", id)
	}
	if resp.StatusCode == http.StatusConflict {
		return fmt.Errorf("request %s is currently pending or processing", id)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("replay request returned status %d", resp.StatusCode)
	}
	return nil
}

// ReplayAllDLQ requeues all failed DLQ messages in chunked batches complying with the 1,000 server cap.
func (c *RelayClient) ReplayAllDLQ(ctx context.Context) (int, error) {
	const maxReplayBatches = 100
	totalReplayed := 0

	for batch := 0; batch < maxReplayBatches; batch++ {
		if err := ctx.Err(); err != nil {
			return totalReplayed, err
		}

		payload, err := json.Marshal(map[string]any{"limit": 1000})
		if err != nil {
			return totalReplayed, fmt.Errorf("failed to serialize replay request: %w", err)
		}

		resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/dlq/replay-all", bytes.NewReader(payload))
		if err != nil {
			return totalReplayed, fmt.Errorf("failed to batch replay dlq: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return totalReplayed, fmt.Errorf("batch replay dlq returned status %d", resp.StatusCode)
		}

		var result struct {
			ReplayedCount int      `json:"replayed_count"`
			RequestIDs    []string `json:"request_ids"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			_ = resp.Body.Close()
			return totalReplayed, fmt.Errorf("failed to decode replay-all response: %w", err)
		}
		_ = resp.Body.Close()

		totalReplayed += result.ReplayedCount

		// If this batch replayed less than 1,000, we have exhausted the queue
		if result.ReplayedCount < 1000 {
			break
		}
	}

	return totalReplayed, nil
}
