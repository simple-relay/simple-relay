package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

type mockRouteRepo struct {
	routes []*model.Route
}

func (m *mockRouteRepo) CreateRoute(ctx context.Context, route *model.Route) error {
	m.routes = append(m.routes, route)
	return nil
}

func (m *mockRouteRepo) GetRouteByID(ctx context.Context, id string) (*model.Route, error) {
	for _, r := range m.routes {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, storage.ErrNotFound
}

func (m *mockRouteRepo) GetRouteByName(ctx context.Context, name string) (*model.Route, error) {
	for _, r := range m.routes {
		if r.Name == name {
			return r, nil
		}
	}
	return nil, storage.ErrNotFound
}

func (m *mockRouteRepo) GetRouteByPath(ctx context.Context, path string) (*model.Route, error) {
	for _, r := range m.routes {
		if r.Path == path {
			return r, nil
		}
	}
	return nil, storage.ErrNotFound
}

func (m *mockRouteRepo) ListRoutes(ctx context.Context) ([]*model.Route, error) {
	return m.routes, nil
}

func (m *mockRouteRepo) UpdateRoute(ctx context.Context, route *model.Route) error {
	return nil
}

func (m *mockRouteRepo) DeleteRoute(ctx context.Context, id string) error {
	return nil
}

type mockWorkerRepo struct {
	mu           sync.Mutex
	statusUpdate map[string]struct {
		status        model.RequestStatus
		attemptsCount int
		nextAttemptAt *time.Time
		lastError     *string
	}
	attempts []*model.Attempt
}

func newMockWorkerRepo() *mockWorkerRepo {
	return &mockWorkerRepo{
		statusUpdate: make(map[string]struct {
			status        model.RequestStatus
			attemptsCount int
			nextAttemptAt *time.Time
			lastError     *string
		}),
	}
}

func (m *mockWorkerRepo) CreateRequest(ctx context.Context, req *model.Request) error { return nil }
func (m *mockWorkerRepo) CreateRequestsBatch(ctx context.Context, reqs []*model.Request) error {
	return nil
}
func (m *mockWorkerRepo) GetRequestByID(ctx context.Context, id string) (*model.Request, error) {
	return nil, nil
}
func (m *mockWorkerRepo) ListPendingRequests(ctx context.Context, limit int, dueBefore time.Time) ([]*model.Request, error) {
	return nil, nil
}
func (m *mockWorkerRepo) ClaimPendingRequests(ctx context.Context, limit int, dueBefore time.Time) ([]*model.Request, error) {
	return nil, nil
}
func (m *mockWorkerRepo) RecoverStuckRequests(ctx context.Context, stuckBefore time.Time) (int64, error) {
	return 0, nil
}
func (m *mockWorkerRepo) ListDLQRequests(ctx context.Context, routeID string, limit, offset int) ([]*model.Request, int64, error) {
	return nil, 0, nil
}
func (m *mockWorkerRepo) ListRequests(ctx context.Context, filter storage.RequestFilter) ([]*model.RequestMetadata, int64, error) {
	return nil, 0, nil
}
func (m *mockWorkerRepo) ReplayDLQRequest(ctx context.Context, id string) (*model.Request, error) {
	return nil, nil
}
func (m *mockWorkerRepo) ReplayAllDLQRequests(ctx context.Context, routeID string, limit int) ([]string, error) {
	return nil, nil
}
func (m *mockWorkerRepo) GetQueueStats(ctx context.Context) (int64, int64, error) {
	return 0, 0, nil
}

func (m *mockWorkerRepo) UpdateRequestStatus(ctx context.Context, id string, status model.RequestStatus, attemptsCount int, nextAttemptAt *time.Time, lastError *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statusUpdate[id] = struct {
		status        model.RequestStatus
		attemptsCount int
		nextAttemptAt *time.Time
		lastError     *string
	}{
		status:        status,
		attemptsCount: attemptsCount,
		nextAttemptAt: nextAttemptAt,
		lastError:     lastError,
	}
	return nil
}

func (m *mockWorkerRepo) RecordAttempt(ctx context.Context, attempt *model.Attempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts = append(m.attempts, attempt)
	return nil
}

func (m *mockWorkerRepo) ListAttemptsByRequestID(ctx context.Context, requestID string) ([]*model.Attempt, error) {
	return nil, nil
}

func TestWorkerPool_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Relay-Request-ID") != "req-1" {
			t.Errorf("expected X-Relay-Request-ID header")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	defer ts.Close()

	route := &model.Route{
		ID:                   "r-1",
		Name:                 "service-a",
		DestinationURL:       ts.URL,
		MaxAttempts:          3,
		TimeoutMS:            2000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	routeRepo := &mockRouteRepo{routes: []*model.Route{route}}
	registry := NewRouteRegistry(routeRepo)
	if err := registry.Init(context.Background()); err != nil {
		t.Fatalf("registry.Init failed: %v", err)
	}

	repo := newMockWorkerRepo()
	jobsCh := make(chan *model.Request, 5)
	retryEngine := NewRetryEngine(RetryConfig{})

	wp := NewWorkerPool(jobsCh, registry, repo, repo, retryEngine, WorkerPoolConfig{
		Concurrency: 2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wp.Start(ctx)

	req := &model.Request{
		ID:            "req-1",
		RouteID:       "r-1",
		Method:        "POST",
		URL:           ts.URL,
		Headers:       map[string]string{"Content-Type": "application/json"},
		Body:          []byte(`{"hello": "world"}`),
		AttemptsCount: 0,
	}

	jobsCh <- req
	close(jobsCh)

	// Wait for worker to finish
	_ = wp.Close()

	repo.mu.Lock()
	defer repo.mu.Unlock()

	up, ok := repo.statusUpdate["req-1"]
	if !ok {
		t.Fatal("expected status update for req-1")
	}
	if up.status != model.RequestStatusSuccess {
		t.Errorf("expected success status, got %s", up.status)
	}
	if up.attemptsCount != 1 {
		t.Errorf("expected attemptsCount 1, got %d", up.attemptsCount)
	}
	if len(repo.attempts) != 1 {
		t.Errorf("expected 1 attempt logged, got %d", len(repo.attempts))
	}
}

func TestWorkerPool_RetryableFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer ts.Close()

	route := &model.Route{
		ID:                   "r-1",
		Name:                 "service-a",
		DestinationURL:       ts.URL,
		MaxAttempts:          3,
		TimeoutMS:            2000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	routeRepo := &mockRouteRepo{routes: []*model.Route{route}}
	registry := NewRouteRegistry(routeRepo)
	if err := registry.Init(context.Background()); err != nil {
		t.Fatalf("registry.Init failed: %v", err)
	}

	repo := newMockWorkerRepo()
	jobsCh := make(chan *model.Request, 5)
	retryEngine := NewRetryEngine(RetryConfig{})

	wp := NewWorkerPool(jobsCh, registry, repo, repo, retryEngine, WorkerPoolConfig{Concurrency: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wp.Start(ctx)

	req := &model.Request{
		ID:            "req-1",
		RouteID:       "r-1",
		Method:        "POST",
		URL:           ts.URL,
		AttemptsCount: 0,
	}

	jobsCh <- req
	close(jobsCh)
	_ = wp.Close()

	repo.mu.Lock()
	defer repo.mu.Unlock()

	up, ok := repo.statusUpdate["req-1"]
	if !ok {
		t.Fatal("expected status update for req-1")
	}
	if up.status != model.RequestStatusFailed {
		t.Errorf("expected failed status, got %s", up.status)
	}
	if up.attemptsCount != 1 {
		t.Errorf("expected attemptsCount 1, got %d", up.attemptsCount)
	}
	if up.nextAttemptAt == nil {
		t.Error("expected nextAttemptAt to be set")
	}
}

func TestWorkerPool_MaxAttemptsToDLQ(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer ts.Close()

	route := &model.Route{
		ID:                   "r-1",
		Name:                 "service-a",
		DestinationURL:       ts.URL,
		MaxAttempts:          2,
		TimeoutMS:            2000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	routeRepo := &mockRouteRepo{routes: []*model.Route{route}}
	registry := NewRouteRegistry(routeRepo)
	if err := registry.Init(context.Background()); err != nil {
		t.Fatalf("registry.Init failed: %v", err)
	}

	repo := newMockWorkerRepo()
	jobsCh := make(chan *model.Request, 5)
	retryEngine := NewRetryEngine(RetryConfig{})

	wp := NewWorkerPool(jobsCh, registry, repo, repo, retryEngine, WorkerPoolConfig{Concurrency: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wp.Start(ctx)

	// AttemptsCount is already 1, route MaxAttempts is 2 -> next attempt will reach max
	req := &model.Request{
		ID:            "req-dlq",
		RouteID:       "r-1",
		Method:        "POST",
		URL:           ts.URL,
		AttemptsCount: 1,
	}

	jobsCh <- req
	close(jobsCh)
	_ = wp.Close()

	repo.mu.Lock()
	defer repo.mu.Unlock()

	up, ok := repo.statusUpdate["req-dlq"]
	if !ok {
		t.Fatal("expected status update for req-dlq")
	}
	if up.status != model.RequestStatusDLQ {
		t.Errorf("expected DLQ status, got %s", up.status)
	}
	if up.attemptsCount != 2 {
		t.Errorf("expected attemptsCount 2, got %d", up.attemptsCount)
	}
}

func TestWorkerPool_TruncateLargeResponseBody(t *testing.T) {
	largeBody := strings.Repeat("A", 100*1024) // 100 KB
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(largeBody))
	}))
	defer ts.Close()

	route := &model.Route{
		ID:                   "r-1",
		Name:                 "service-a",
		DestinationURL:       ts.URL,
		MaxAttempts:          3,
		TimeoutMS:            2000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	routeRepo := &mockRouteRepo{routes: []*model.Route{route}}
	registry := NewRouteRegistry(routeRepo)
	if err := registry.Init(context.Background()); err != nil {
		t.Fatalf("registry.Init failed: %v", err)
	}

	repo := newMockWorkerRepo()
	jobsCh := make(chan *model.Request, 5)
	retryEngine := NewRetryEngine(RetryConfig{})

	wp := NewWorkerPool(jobsCh, registry, repo, repo, retryEngine, WorkerPoolConfig{Concurrency: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wp.Start(ctx)

	jobsCh <- &model.Request{ID: "req-trunc", RouteID: "r-1", Method: "POST", URL: ts.URL}
	close(jobsCh)
	_ = wp.Close()

	repo.mu.Lock()
	defer repo.mu.Unlock()

	if len(repo.attempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(repo.attempts))
	}
	if repo.attempts[0].ResponseBody == nil {
		t.Fatal("expected response body, got nil")
	}
	// Max allowed truncated size is 32KB (32768 bytes)
	if len(*repo.attempts[0].ResponseBody) > 32*1024 {
		t.Errorf("response body exceeded 32KB limit, length: %d", len(*repo.attempts[0].ResponseBody))
	}
}

func TestWorkerPool_DefaultAsyncEngineAndRouteBackoffOverrides(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "45")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	route := &model.Route{
		ID:                   "r-override",
		Name:                 "async-override",
		DestinationURL:       ts.URL,
		MaxAttempts:          5,
		TimeoutMS:            2000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
		BaseBackoffMS:        10000,
		MaxBackoffMS:         60000,
	}
	routeRepo := &mockRouteRepo{routes: []*model.Route{route}}
	registry := NewRouteRegistry(routeRepo)
	if err := registry.Init(context.Background()); err != nil {
		t.Fatalf("registry.Init failed: %v", err)
	}

	repo := newMockWorkerRepo()
	jobsCh := make(chan *model.Request, 2)

	// Pass nil retryEngine -> should default to NewAsyncRetryEngine() (5s base, 15m max)
	wp := NewWorkerPool(jobsCh, registry, repo, repo, nil, WorkerPoolConfig{Concurrency: 1})
	if wp.retryEngine == nil || wp.retryEngine.config.BaseBackoff != DefaultAsyncBaseBackoff {
		t.Fatalf("expected WorkerPool nil retryEngine to default to DefaultAsyncBaseBackoff (5s), got %+v", wp.retryEngine)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wp.Start(ctx)

	before := time.Now().UTC()
	jobsCh <- &model.Request{
		ID:            "req-override",
		RouteID:       "r-override",
		Method:        "POST",
		URL:           ts.URL,
		AttemptsCount: 0,
	}
	close(jobsCh)
	_ = wp.Close()

	repo.mu.Lock()
	defer repo.mu.Unlock()

	up, ok := repo.statusUpdate["req-override"]
	if !ok || up.nextAttemptAt == nil {
		t.Fatalf("expected status update with nextAttemptAt for req-override")
	}
	delay := up.nextAttemptAt.Sub(before)
	if delay < 44*time.Second || delay > 47*time.Second {
		t.Fatalf("expected ~45s delay from Retry-After with async engine, got %v", delay)
	}
}
