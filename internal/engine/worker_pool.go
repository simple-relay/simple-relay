package engine

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

// WorkerPoolConfig holds configuration options for WorkerPool.
type WorkerPoolConfig struct {
	Concurrency int
	Client      *http.Client
}

// WorkerPool processes outbound delivery jobs concurrently.
type WorkerPool struct {
	concurrency int
	jobsCh      <-chan *model.Request
	routes      *RouteRegistry
	reqRepo     storage.RequestRepository
	attemptRepo storage.AttemptRepository
	retryEngine *RetryEngine
	client      *http.Client

	done chan struct{}
	wg   sync.WaitGroup
	mu   sync.Mutex
	stop bool
}

// NewWorkerPool creates a new WorkerPool instance.
func NewWorkerPool(
	jobsCh <-chan *model.Request,
	routes *RouteRegistry,
	reqRepo storage.RequestRepository,
	attemptRepo storage.AttemptRepository,
	retryEngine *RetryEngine,
	cfg WorkerPoolConfig,
) *WorkerPool {
	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 20
	}

	if retryEngine == nil {
		retryEngine = NewAsyncRetryEngine()
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
			},
		}
	}

	return &WorkerPool{
		concurrency: concurrency,
		jobsCh:      jobsCh,
		routes:      routes,
		reqRepo:     reqRepo,
		attemptRepo: attemptRepo,
		retryEngine: retryEngine,
		client:      client,
		done:        make(chan struct{}),
	}
}

// Start launches the pool of worker goroutines.
func (wp *WorkerPool) Start(ctx context.Context) {
	for i := 0; i < wp.concurrency; i++ {
		wp.wg.Add(1)
		go wp.worker(ctx)
	}
}

// worker is the main consumer loop for a single goroutine in the pool.
func (wp *WorkerPool) worker(ctx context.Context) {
	defer wp.wg.Done()

	for {
		select {
		case req, ok := <-wp.jobsCh:
			if !ok {
				return
			}
			wp.processJob(ctx, req)

		case <-ctx.Done():
			return

		case <-wp.done:
			for {
				select {
				case req, ok := <-wp.jobsCh:
					if !ok {
						return
					}
					wp.processJob(ctx, req)
				default:
					return
				}
			}
		}
	}
}

// processJob executes delivery for an individual request and updates its state.
func (wp *WorkerPool) processJob(ctx context.Context, req *model.Request) {
	route, ok := wp.routes.GetByID(req.RouteID)
	if !ok {
		// Route no longer exists; route job to DLQ
		errMsg := "associated route not found or deleted"
		_ = wp.reqRepo.UpdateRequestStatus(ctx, req.ID, model.RequestStatusDLQ, req.AttemptsCount, nil, &errMsg)
		return
	}

	// Determine attempt timeout
	timeout := time.Duration(route.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if overrideHeader, ok := req.Headers["X-Relay-Timeout"]; ok {
		if ms, err := strconv.Atoi(overrideHeader); err == nil && ms > 0 {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}

	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var bodyReader io.Reader
	if len(req.Body) > 0 {
		bodyReader = bytes.NewReader(req.Body)
	}

	outReq, err := http.NewRequestWithContext(attemptCtx, req.Method, req.URL, bodyReader)
	if err != nil {
		errMsg := err.Error()
		_ = wp.reqRepo.UpdateRequestStatus(ctx, req.ID, model.RequestStatusDLQ, req.AttemptsCount+1, nil, &errMsg)
		return
	}

	for k, v := range req.Headers {
		if isHopByHopHeader(k) || strings.EqualFold(k, "X-Relay-Timeout") {
			continue
		}
		cleaned, keep := SanitizeInternalAuthHeaders(k, []string{v})
		if !keep || len(cleaned) == 0 {
			continue
		}
		outReq.Header.Set(k, strings.Join(cleaned, ", "))
	}
	if outReq.Header.Get("X-Relay-Request-ID") == "" {
		outReq.Header.Set("X-Relay-Request-ID", req.ID)
	}
	outReq.Header.Set("X-Relay-Attempt", strconv.Itoa(req.AttemptsCount+1))

	start := time.Now()
	resp, doErr := wp.client.Do(outReq)
	durationMs := int(time.Since(start).Milliseconds())

	var statusCode *int
	var respBodyStr *string
	var retryAfterHeader string

	if resp != nil {
		code := resp.StatusCode
		statusCode = &code
		retryAfterHeader = resp.Header.Get("Retry-After")

		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		truncated := model.TruncateResponseBody(string(bodyBytes))
		respBodyStr = &truncated
	}

	var errMsg *string
	if doErr != nil {
		errStr := doErr.Error()
		errMsg = &errStr
	} else if statusCode != nil && *statusCode >= 400 {
		errStr := http.StatusText(*statusCode)
		errMsg = &errStr
	}

	// Persist attempt audit record
	attemptRecord := &model.Attempt{
		ID:                 model.NewULID(),
		RequestID:          req.ID,
		AttemptNumber:      req.AttemptsCount + 1,
		ResponseStatusCode: statusCode,
		ResponseBody:       respBodyStr,
		ErrorMessage:       errMsg,
		ExecutedAt:         time.Now().UTC(),
		DurationMS:         int64(durationMs),
	}
	if recErr := wp.attemptRepo.RecordAttempt(ctx, attemptRecord); recErr != nil {
		slog.Error("failed to record attempt audit", "request_id", req.ID, "error", recErr)
	}

	newAttempts := req.AttemptsCount + 1

	// Check if attempt succeeded
	sc := 0
	if statusCode != nil {
		sc = *statusCode
	}
	if doErr == nil && sc >= 200 && sc < 400 {
		_ = wp.reqRepo.UpdateRequestStatus(ctx, req.ID, model.RequestStatusSuccess, newAttempts, nil, nil)
		return
	}

	// Handle failure / retries
	if newAttempts < route.MaxAttempts {
		delay := wp.retryEngine.CalculateBackoffWithOverrides(newAttempts, route.RetryBackoffStrategy, retryAfterHeader, route.BaseBackoffMS, route.MaxBackoffMS)
		nextAttemptAt := time.Now().UTC().Add(delay)
		_ = wp.reqRepo.UpdateRequestStatus(ctx, req.ID, model.RequestStatusFailed, newAttempts, &nextAttemptAt, errMsg)
	} else {
		// Exhausted all retries -> DLQ
		_ = wp.reqRepo.UpdateRequestStatus(ctx, req.ID, model.RequestStatusDLQ, newAttempts, nil, errMsg)
	}
}

// Close gracefully terminates all workers in the pool.
func (wp *WorkerPool) Close() error {
	wp.mu.Lock()
	if wp.stop {
		wp.mu.Unlock()
		return nil
	}
	wp.stop = true
	close(wp.done)
	wp.mu.Unlock()

	wp.wg.Wait()
	return nil
}
