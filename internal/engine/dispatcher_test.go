package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
)

type mockDispatcherRepo struct {
	mu             sync.Mutex
	pending        []*model.Request
	claimed        []*model.Request
	recoveredCount int64
}

func (m *mockDispatcherRepo) CreateRequest(ctx context.Context, req *model.Request) error {
	return nil
}
func (m *mockDispatcherRepo) CreateRequestsBatch(ctx context.Context, reqs []*model.Request) error {
	return nil
}
func (m *mockDispatcherRepo) GetRequestByID(ctx context.Context, id string) (*model.Request, error) {
	return nil, nil
}
func (m *mockDispatcherRepo) ListPendingRequests(ctx context.Context, limit int, dueBefore time.Time) ([]*model.Request, error) {
	return nil, nil
}
func (m *mockDispatcherRepo) UpdateRequestStatus(ctx context.Context, id string, status model.RequestStatus, attemptsCount int, nextAttemptAt *time.Time, lastError *string) error {
	return nil
}

func (m *mockDispatcherRepo) ClaimPendingRequests(ctx context.Context, limit int, dueBefore time.Time) ([]*model.Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []*model.Request
	var remaining []*model.Request

	for _, req := range m.pending {
		if len(result) < limit {
			req.Status = model.RequestStatusProcessing
			result = append(result, req)
			m.claimed = append(m.claimed, req)
		} else {
			remaining = append(remaining, req)
		}
	}
	m.pending = remaining
	return result, nil
}

func (m *mockDispatcherRepo) RecoverStuckRequests(ctx context.Context, stuckBefore time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.recoveredCount
	m.recoveredCount = 0
	return c, nil
}

func TestDispatcher_WakeupSignal(t *testing.T) {
	repo := &mockDispatcherRepo{
		pending: []*model.Request{
			{ID: "req-1", Status: model.RequestStatusPending},
			{ID: "req-2", Status: model.RequestStatusPending},
		},
	}

	jobsCh := make(chan *model.Request, 10)
	disp := NewDispatcher(repo, jobsCh, DispatcherConfig{
		PollInterval: 10 * time.Second, // Long poll interval
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	disp.Start(ctx)
	defer disp.Close()

	// Initial startup dispatch claims both
	select {
	case req := <-jobsCh:
		if req.ID != "req-1" {
			t.Errorf("expected req-1, got %s", req.ID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for initial job")
	}

	select {
	case req := <-jobsCh:
		if req.ID != "req-2" {
			t.Errorf("expected req-2, got %s", req.ID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for second initial job")
	}

	// Add new job to repo, and call Wake()
	repo.mu.Lock()
	repo.pending = append(repo.pending, &model.Request{ID: "req-3", Status: model.RequestStatusPending})
	repo.mu.Unlock()

	disp.Wake()

	select {
	case req := <-jobsCh:
		if req.ID != "req-3" {
			t.Errorf("expected req-3 after Wake, got %s", req.ID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for job after Wake()")
	}
}

func TestDispatcher_CapacityBounding(t *testing.T) {
	repo := &mockDispatcherRepo{
		pending: []*model.Request{
			{ID: "req-1", Status: model.RequestStatusPending},
			{ID: "req-2", Status: model.RequestStatusPending},
			{ID: "req-3", Status: model.RequestStatusPending},
		},
	}

	// Capacity 2
	jobsCh := make(chan *model.Request, 2)
	disp := NewDispatcher(repo, jobsCh, DispatcherConfig{
		PollInterval: 100 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	disp.Start(ctx)
	defer disp.Close()

	// Wait for channel to fill up to 2
	time.Sleep(50 * time.Millisecond)

	if len(jobsCh) != 2 {
		t.Fatalf("expected channel to have 2 items, got %d", len(jobsCh))
	}

	// 1 item must still be in pending (not claimed) because capacity was 2
	repo.mu.Lock()
	if len(repo.pending) != 1 {
		t.Errorf("expected 1 remaining pending item, got %d", len(repo.pending))
	}
	repo.mu.Unlock()

	// Drain 1 item
	<-jobsCh

	// Give a tick to dispatch the last one
	time.Sleep(150 * time.Millisecond)

	repo.mu.Lock()
	if len(repo.pending) != 0 {
		t.Errorf("expected 0 remaining pending items, got %d", len(repo.pending))
	}
	repo.mu.Unlock()
}
