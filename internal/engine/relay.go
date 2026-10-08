package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

// RelayResult represents the finalized response of a synchronous relay operation.
type RelayResult struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
	Attempts   int
	RequestID  string
}

// RelayEngine orchestrates outbound HTTP execution with retries, jitter,
// client-disconnect cancellation, and audit persistence.
type RelayEngine struct {
	httpClient  *http.Client
	retryEngine *RetryEngine
	requestRepo storage.RequestRepository
	attemptRepo storage.AttemptRepository
	logger      *slog.Logger
}

// NewRelayEngine constructs a new RelayEngine.
func NewRelayEngine(
	httpClient *http.Client,
	retryEngine *RetryEngine,
	requestRepo storage.RequestRepository,
	attemptRepo storage.AttemptRepository,
	logger *slog.Logger,
) *RelayEngine {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	if retryEngine == nil {
		retryEngine = NewRetryEngine()
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &RelayEngine{
		httpClient:  httpClient,
		retryEngine: retryEngine,
		requestRepo: requestRepo,
		attemptRepo: attemptRepo,
		logger:      logger,
	}
}

// RelaySync synchronously executes an outbound request against the route's destination URL
// with bounded retries, full jitter backoff, and strict client-disconnect abort awareness.
func (e *RelayEngine) RelaySync(
	ctx context.Context,
	route *model.Route,
	method string,
	rawURL string,
	headers http.Header,
	body []byte,
	timeoutOverrideMS *int,
) (*RelayResult, error) {
	requestID := headers.Get("X-Relay-Request-ID")
	if requestID == "" {
		requestID = model.NewULID()
	}

	// Prepare headers map for SQLite storage
	headersMap := make(map[string]string)
	for k, v := range headers {
		if len(v) > 0 {
			headersMap[k] = v[0]
		}
	}

	// 1. Initial persistence of Request in SQLite (status: processing)
	reqModel := &model.Request{
		ID:            requestID,
		RouteID:       route.ID,
		Method:        method,
		URL:           route.DestinationURL,
		Headers:       headersMap,
		Body:          body,
		Status:        model.RequestStatusProcessing,
		AttemptsCount: 0,
	}

	if err := e.requestRepo.CreateRequest(ctx, reqModel); err != nil {
		e.logger.Error("failed to create initial request record", "request_id", requestID, "error", err)
		return nil, fmt.Errorf("failed to persist initial request: %w", err)
	}

	// 2. Determine per-attempt timeout
	attemptTimeout := time.Duration(route.TimeoutMS) * time.Millisecond
	if timeoutOverrideMS != nil && *timeoutOverrideMS > 0 {
		attemptTimeout = time.Duration(*timeoutOverrideMS) * time.Millisecond
	}
	if attemptTimeout <= 0 {
		attemptTimeout = 10 * time.Second
	}

	maxAttempts := route.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	var (
		lastStatusCode  int
		lastRespHeaders http.Header
		lastRespBody    []byte
		lastErr         error
		attemptsCount   int
	)

	// 3. Synchronous Retry Loop
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptsCount = attempt

		// Check if client disconnected before starting attempt
		if ctx.Err() != nil {
			lastErr = ctx.Err()
			break
		}

		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)

		// Create upstream request with fresh body reader
		upstreamReq, err := http.NewRequestWithContext(attemptCtx, method, route.DestinationURL, bytes.NewReader(body))
		if err != nil {
			cancel()
			lastErr = err
			break
		}

		// Copy headers, filtering hop-by-hop
		copyFilteredHeaders(upstreamReq.Header, headers)
		upstreamReq.Header.Set("X-Relay-Request-ID", requestID)

		// Idempotency propagation
		if idemKey := headers.Get("Idempotency-Key"); idemKey != "" {
			upstreamReq.Header.Set("Idempotency-Key", idemKey)
		} else if relayIdemKey := headers.Get("X-Relay-Idempotency-Key"); relayIdemKey != "" {
			upstreamReq.Header.Set("Idempotency-Key", relayIdemKey)
		}

		// Execute outbound HTTP call
		start := time.Now()
		resp, execErr := e.httpClient.Do(upstreamReq)
		durationMS := time.Since(start).Milliseconds()
		cancel() // cancel attemptCtx once call finishes

		if execErr != nil {
			lastErr = execErr
			errMsg := execErr.Error()

			// Record attempt failure
			attemptRecord := &model.Attempt{
				RequestID:     requestID,
				AttemptNumber: attempt,
				ErrorMessage:  &errMsg,
				ExecutedAt:    start.UTC(),
				DurationMS:    durationMS,
			}
			_ = e.attemptRepo.RecordAttempt(context.Background(), attemptRecord)

			// If client disconnected, abort immediately without retrying
			if errors.Is(ctx.Err(), context.Canceled) {
				lastErr = ctx.Err()
				break
			}

			// If error is retryable and we haven't reached max_attempts, sleep and retry
			if e.retryEngine.IsRetryableError(execErr) && attempt < maxAttempts {
				delay := e.retryEngine.CalculateBackoff(attempt, route.RetryBackoffStrategy, "")
				select {
				case <-time.After(delay):
					continue
				case <-ctx.Done():
					lastErr = ctx.Err()
					break
				}
			}
			break
		}

		// Response received
		defer resp.Body.Close()
		respBodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))

		lastStatusCode = resp.StatusCode
		lastRespHeaders = resp.Header.Clone()
		lastRespBody = respBodyBytes

		respBodyStr := string(respBodyBytes)
		attemptRecord := &model.Attempt{
			RequestID:          requestID,
			AttemptNumber:      attempt,
			ResponseStatusCode: &resp.StatusCode,
			ResponseBody:       &respBodyStr,
			ExecutedAt:         start.UTC(),
			DurationMS:         durationMS,
		}
		_ = e.attemptRepo.RecordAttempt(context.Background(), attemptRecord)

		// Check if response status is retryable
		if e.retryEngine.IsRetryableStatus(resp.StatusCode) {
			if attempt < maxAttempts {
				retryAfterHeader := resp.Header.Get("Retry-After")
				delay := e.retryEngine.CalculateBackoff(attempt, route.RetryBackoffStrategy, retryAfterHeader)
				select {
				case <-time.After(delay):
					continue
				case <-ctx.Done():
					break
				}
			}
			if ctx.Err() != nil {
				lastErr = ctx.Err()
				break
			}
			// Exhausted attempts on retryable status
			lastErr = fmt.Errorf("upstream returned status %d", resp.StatusCode)
			break
		}

		// Non-retryable status (2xx, 3xx, 4xx non-429) -> exit loop immediately
		lastErr = nil
		break
	}

	// 4. Update finalized request status in SQLite
	finalStatus := model.RequestStatusSuccess
	var lastErrStr *string
	if lastErr != nil || (lastStatusCode >= 400) {
		finalStatus = model.RequestStatusFailed
		if lastErr != nil {
			s := lastErr.Error()
			lastErrStr = &s
		} else {
			s := fmt.Sprintf("HTTP %d", lastStatusCode)
			lastErrStr = &s
		}
	}

	_ = e.requestRepo.UpdateRequestStatus(context.Background(), requestID, finalStatus, attemptsCount, nil, lastErrStr)

	// 5. Return result or error
	if errors.Is(lastErr, context.Canceled) {
		return nil, lastErr
	}

	// If no HTTP response was ever obtained (pure network failure)
	if lastStatusCode == 0 && lastErr != nil {
		return &RelayResult{
			RequestID: requestID,
			Attempts:  attemptsCount,
		}, lastErr
	}

	return &RelayResult{
		StatusCode: lastStatusCode,
		Headers:    lastRespHeaders,
		Body:       lastRespBody,
		Attempts:   attemptsCount,
		RequestID:  requestID,
	}, nil
}

// copyFilteredHeaders copies HTTP headers while stripping RFC 7230 hop-by-hop headers.
func copyFilteredHeaders(dst http.Header, src http.Header) {
	for k, vv := range src {
		if isHopByHopHeader(k) || strings.EqualFold(k, "X-Relay-Timeout") {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// isHopByHopHeader checks if a header is an RFC 7230 hop-by-hop header.
func isHopByHopHeader(header string) bool {
	switch strings.ToLower(header) {
	case "connection",
		"keep-alive",
		"proxy-authenticate",
		"proxy-authorization",
		"te",
		"trailers",
		"transfer-encoding",
		"upgrade":
		return true
	default:
		return false
	}
}
