package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

type mockRequestRepo struct {
	mu           sync.Mutex
	created      []*model.Request
	batchesCount int
	batchErr     error
}

func (m *mockRequestRepo) CreateRequest(ctx context.Context, req *model.Request) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.created = append(m.created, req)
	return nil
}

func (m *mockRequestRepo) CreateRequestsBatch(ctx context.Context, reqs []*model.Request) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.batchErr != nil {
		return m.batchErr
	}
	m.batchesCount++
	m.created = append(m.created, reqs...)
	return nil
}

func (m *mockRequestRepo) GetRequestByID(ctx context.Context, id string) (*model.Request, error) {
	return nil, nil
}

func (m *mockRequestRepo) ListPendingRequests(ctx context.Context, limit int, dueBefore time.Time) ([]*model.Request, error) {
	return nil, nil
}

func (m *mockRequestRepo) UpdateRequestStatus(ctx context.Context, id string, status model.RequestStatus, attemptsCount int, nextAttemptAt *time.Time, lastError *string) error {
	return nil
}

func (m *mockRequestRepo) ClaimPendingRequests(ctx context.Context, limit int, dueBefore time.Time) ([]*model.Request, error) {
	return nil, nil
}

func (m *mockRequestRepo) RecoverStuckRequests(ctx context.Context, stuckBefore time.Time) (int64, error) {
	return 0, nil
}

func (m *mockRequestRepo) ListDLQRequests(ctx context.Context, routeID string, limit, offset int) ([]*model.Request, int64, error) {
	return nil, 0, nil
}

func (m *mockRequestRepo) ListRequests(ctx context.Context, filter storage.RequestFilter) ([]*model.RequestMetadata, int64, error) {
	return nil, 0, nil
}

func (m *mockRequestRepo) ReplayDLQRequest(ctx context.Context, id string) (*model.Request, error) {
	return nil, nil
}

func (m *mockRequestRepo) ReplayRequest(ctx context.Context, id string) (*model.Request, error) {
	return nil, nil
}

func (m *mockRequestRepo) ReplayAllDLQRequests(ctx context.Context, routeID string, limit int) ([]string, error) {
	return nil, nil
}

func (m *mockRequestRepo) GetQueueStats(ctx context.Context) (*model.QueueStats, error) {
	return &model.QueueStats{}, nil
}

func TestBatchWriter_BatchSizeFlush(t *testing.T) {
	repo := &mockRequestRepo{}
	var wakeCalled int32

	bw := NewBatchWriter(repo, BatchWriterConfig{
		BatchSize:     5,
		FlushInterval: 1 * time.Second, // Long interval so flush only happens on batch size
		QueueCapacity: 50,
		WakeFn: func() {
			atomic.AddInt32(&wakeCalled, 1)
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bw.Start(ctx)
	defer bw.Close()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := &model.Request{
				ID:      model.NewULID(),
				RouteID: "route-1",
				Method:  "POST",
				URL:     "https://api.example.com",
				Status:  model.RequestStatusPending,
			}
			err := bw.Enqueue(context.Background(), req)
			if err != nil {
				t.Errorf("Enqueue failed: %v", err)
			}
		}(i)
	}

	wg.Wait()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.created) != 5 {
		t.Fatalf("expected 5 items created, got %d", len(repo.created))
	}
	if repo.batchesCount != 1 {
		t.Errorf("expected 1 batch commit, got %d", repo.batchesCount)
	}
	if atomic.LoadInt32(&wakeCalled) != 1 {
		t.Errorf("expected wakeFn called once, got %d", atomic.LoadInt32(&wakeCalled))
	}
}

func TestBatchWriter_TimerFlush(t *testing.T) {
	repo := &mockRequestRepo{}
	bw := NewBatchWriter(repo, BatchWriterConfig{
		BatchSize:     50,
		FlushInterval: 20 * time.Millisecond,
		QueueCapacity: 50,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bw.Start(ctx)
	defer bw.Close()

	req := &model.Request{
		ID:      model.NewULID(),
		RouteID: "route-1",
		Method:  "POST",
		URL:     "https://api.example.com",
		Status:  model.RequestStatusPending,
	}

	start := time.Now()
	err := bw.Enqueue(context.Background(), req)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected flush within ~20ms, took %v", elapsed)
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.created) != 1 {
		t.Fatalf("expected 1 item created, got %d", len(repo.created))
	}
}

func TestBatchWriter_BackpressureAndCancel(t *testing.T) {
	repo := &mockRequestRepo{}
	// Create BatchWriter but DO NOT call Start, so queue will fill up
	bw := NewBatchWriter(repo, BatchWriterConfig{
		BatchSize:     5,
		FlushInterval: 10 * time.Millisecond,
		QueueCapacity: 2,
	})
	defer bw.Close()

	// Fill queue to capacity (2 items)
	ctxBg := context.Background()
	done1 := make(chan error, 1)
	done2 := make(chan error, 1)

	go func() {
		done1 <- bw.Enqueue(ctxBg, &model.Request{ID: "1", RouteID: "r", Method: "POST", URL: "u", Status: model.RequestStatusPending})
	}()
	go func() {
		done2 <- bw.Enqueue(ctxBg, &model.Request{ID: "2", RouteID: "r", Method: "POST", URL: "u", Status: model.RequestStatusPending})
	}()

	time.Sleep(20 * time.Millisecond)

	// Third enqueue with a quick timeout should fail with context deadline exceeded
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := bw.Enqueue(timeoutCtx, &model.Request{ID: "3", RouteID: "r", Method: "POST", URL: "u", Status: model.RequestStatusPending})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

func TestBatchWriter_BatchError(t *testing.T) {
	expectedErr := errors.New("db error")
	repo := &mockRequestRepo{batchErr: expectedErr}

	bw := NewBatchWriter(repo, BatchWriterConfig{
		BatchSize:     1,
		FlushInterval: 10 * time.Millisecond,
		QueueCapacity: 10,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bw.Start(ctx)
	defer bw.Close()

	req := &model.Request{
		ID:      model.NewULID(),
		RouteID: "route-1",
		Method:  "POST",
		URL:     "https://api.example.com",
		Status:  model.RequestStatusPending,
	}

	err := bw.Enqueue(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error wrapping db error, got %v", err)
	}
}
