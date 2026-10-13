package engine

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type mockRetentionRepo struct {
	mu              sync.Mutex
	pruneFn         func(ctx context.Context, successCutoff, failedCutoff time.Time, batchSize int) (int64, error)
	reclaimFn       func(ctx context.Context, maxPages int) error
	checkpointFn    func(ctx context.Context) error
	pruneCalls      int
	reclaimCalls    int
	checkpointCalls int
}

func (m *mockRetentionRepo) PruneExpiredRequests(ctx context.Context, successCutoff, failedCutoff time.Time, batchSize int) (int64, error) {
	m.mu.Lock()
	m.pruneCalls++
	fn := m.pruneFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, successCutoff, failedCutoff, batchSize)
	}
	return 0, nil
}

func (m *mockRetentionRepo) ReclaimStorage(ctx context.Context, maxPages int) error {
	m.mu.Lock()
	m.reclaimCalls++
	fn := m.reclaimFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, maxPages)
	}
	return nil
}

func (m *mockRetentionRepo) Checkpoint(ctx context.Context) error {
	m.mu.Lock()
	m.checkpointCalls++
	fn := m.checkpointFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx)
	}
	return nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCleaner_NewCleaner_Defaults(t *testing.T) {
	mockRepo := &mockRetentionRepo{}
	c := NewCleaner(mockRepo, CleanerConfig{}, nil)

	if c.cfg.Interval != 1*time.Hour {
		t.Errorf("expected default interval 1h, got %v", c.cfg.Interval)
	}
	if c.cfg.RetentionSuccessDays != 7 {
		t.Errorf("expected default success retention 7 days, got %d", c.cfg.RetentionSuccessDays)
	}
	if c.cfg.RetentionFailedDays != 30 {
		t.Errorf("expected default failed retention 30 days, got %d", c.cfg.RetentionFailedDays)
	}
	if c.cfg.BatchSize != 1000 {
		t.Errorf("expected default batch size 1000, got %d", c.cfg.BatchSize)
	}
	if c.cfg.VacuumPages != 1000 {
		t.Errorf("expected default vacuum pages 1000, got %d", c.cfg.VacuumPages)
	}
}

func TestCleaner_CleanOnce_SuccessWithVacuum(t *testing.T) {
	var capturedSuccess, capturedFailed time.Time
	var capturedBatch, capturedVacuum int

	mockRepo := &mockRetentionRepo{
		pruneFn: func(ctx context.Context, successCutoff, failedCutoff time.Time, batchSize int) (int64, error) {
			capturedSuccess = successCutoff
			capturedFailed = failedCutoff
			capturedBatch = batchSize
			return 15, nil
		},
		reclaimFn: func(ctx context.Context, maxPages int) error {
			capturedVacuum = maxPages
			return nil
		},
	}

	cleaner := NewCleaner(mockRepo, CleanerConfig{
		RetentionSuccessDays: 5,
		RetentionFailedDays:  15,
		BatchSize:            500,
		VacuumPages:          800,
	}, discardLogger())

	before := time.Now().UTC()
	res, err := cleaner.CleanOnce(context.Background())
	after := time.Now().UTC()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.PurgedCount != 15 {
		t.Errorf("expected PurgedCount 15, got %d", res.PurgedCount)
	}
	if !res.VacuumExecuted {
		t.Errorf("expected VacuumExecuted true, got false")
	}

	// Verify cutoffs roughly match before/after minus days
	expectedSuccessMin := before.AddDate(0, 0, -5).Add(-time.Second)
	expectedSuccessMax := after.AddDate(0, 0, -5).Add(time.Second)
	if capturedSuccess.Before(expectedSuccessMin) || capturedSuccess.After(expectedSuccessMax) {
		t.Errorf("success cutoff %v outside expected bounds [%v, %v]", capturedSuccess, expectedSuccessMin, expectedSuccessMax)
	}

	expectedFailedMin := before.AddDate(0, 0, -15).Add(-time.Second)
	expectedFailedMax := after.AddDate(0, 0, -15).Add(time.Second)
	if capturedFailed.Before(expectedFailedMin) || capturedFailed.After(expectedFailedMax) {
		t.Errorf("failed cutoff %v outside expected bounds [%v, %v]", capturedFailed, expectedFailedMin, expectedFailedMax)
	}

	if capturedBatch != 500 {
		t.Errorf("expected batch size 500, got %d", capturedBatch)
	}
	if capturedVacuum != 800 {
		t.Errorf("expected vacuum pages 800, got %d", capturedVacuum)
	}
	if mockRepo.reclaimCalls != 1 {
		t.Errorf("expected 1 reclaim call, got %d", mockRepo.reclaimCalls)
	}
}

func TestCleaner_CleanOnce_ZeroPurgedSkipsVacuum(t *testing.T) {
	mockRepo := &mockRetentionRepo{
		pruneFn: func(ctx context.Context, successCutoff, failedCutoff time.Time, batchSize int) (int64, error) {
			return 0, nil
		},
	}

	cleaner := NewCleaner(mockRepo, CleanerConfig{}, discardLogger())

	res, err := cleaner.CleanOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.PurgedCount != 0 {
		t.Errorf("expected 0 purged, got %d", res.PurgedCount)
	}
	if res.VacuumExecuted {
		t.Errorf("expected VacuumExecuted false when zero rows purged")
	}
	if mockRepo.reclaimCalls != 0 {
		t.Errorf("reclaim should not be called when zero rows purged, got %d calls", mockRepo.reclaimCalls)
	}
}

func TestCleaner_CleanOnce_ConcurrencyGuard(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})

	mockRepo := &mockRetentionRepo{
		pruneFn: func(ctx context.Context, successCutoff, failedCutoff time.Time, batchSize int) (int64, error) {
			close(started)
			<-unblock
			return 10, nil
		},
	}

	cleaner := NewCleaner(mockRepo, CleanerConfig{}, discardLogger())

	go func() {
		_, _ = cleaner.CleanOnce(context.Background())
	}()

	<-started

	// Second concurrent call should fail immediately with ErrCleanerAlreadyRunning
	_, err := cleaner.CleanOnce(context.Background())
	if !errors.Is(err, ErrCleanerAlreadyRunning) {
		t.Errorf("expected ErrCleanerAlreadyRunning, got %v", err)
	}

	close(unblock)
}

func TestCleaner_CleanOnce_PruneError(t *testing.T) {
	mockRepo := &mockRetentionRepo{
		pruneFn: func(ctx context.Context, successCutoff, failedCutoff time.Time, batchSize int) (int64, error) {
			return 0, errors.New("db disk I/O error")
		},
	}

	cleaner := NewCleaner(mockRepo, CleanerConfig{}, discardLogger())

	_, err := cleaner.CleanOnce(context.Background())
	if err == nil {
		t.Fatalf("expected error from CleanOnce when prune fails")
	}
}

func TestCleaner_CleanOnce_ReclaimErrorResilient(t *testing.T) {
	mockRepo := &mockRetentionRepo{
		pruneFn: func(ctx context.Context, successCutoff, failedCutoff time.Time, batchSize int) (int64, error) {
			return 5, nil
		},
		reclaimFn: func(ctx context.Context, maxPages int) error {
			return errors.New("vacuum failed")
		},
	}

	cleaner := NewCleaner(mockRepo, CleanerConfig{}, discardLogger())

	res, err := cleaner.CleanOnce(context.Background())
	if err != nil {
		t.Fatalf("CleanOnce should not fail overall when reclaim fails, got: %v", err)
	}
	if res.PurgedCount != 5 {
		t.Errorf("expected PurgedCount 5, got %d", res.PurgedCount)
	}
	if res.VacuumExecuted {
		t.Errorf("expected VacuumExecuted false on reclaim error")
	}
}

func TestCleaner_Lifecycle_StartAndClose(t *testing.T) {
	var callCount int
	var mu sync.Mutex

	mockRepo := &mockRetentionRepo{
		pruneFn: func(ctx context.Context, successCutoff, failedCutoff time.Time, batchSize int) (int64, error) {
			mu.Lock()
			callCount++
			mu.Unlock()
			return 0, nil
		},
	}

	cleaner := NewCleaner(mockRepo, CleanerConfig{
		Interval: 20 * time.Millisecond,
	}, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cleaner.Start(ctx)

	// Wait enough time for startup call + at least 1 ticker call
	time.Sleep(60 * time.Millisecond)

	if err := cleaner.Close(); err != nil {
		t.Fatalf("failed to close cleaner: %v", err)
	}

	mu.Lock()
	count := callCount
	mu.Unlock()

	if count < 2 {
		t.Errorf("expected at least 2 clean cycles (startup + ticker), got %d", count)
	}

	// Calling close a second time should be idempotent
	if err := cleaner.Close(); err != nil {
		t.Errorf("expected idempotent close, got: %v", err)
	}
}

func TestCleaner_CleanOnce_ExecutesCheckpoint(t *testing.T) {
	mockRepo := &mockRetentionRepo{
		pruneFn: func(ctx context.Context, successCutoff, failedCutoff time.Time, batchSize int) (int64, error) {
			return 2, nil
		},
	}

	cleaner := NewCleaner(mockRepo, CleanerConfig{}, discardLogger())
	res, err := cleaner.CleanOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.PurgedCount != 2 {
		t.Errorf("expected PurgedCount 2, got %d", res.PurgedCount)
	}

	mockRepo.mu.Lock()
	checkpointCalls := mockRepo.checkpointCalls
	mockRepo.mu.Unlock()

	if checkpointCalls != 1 {
		t.Errorf("expected 1 checkpoint call in CleanOnce, got %d", checkpointCalls)
	}
}

func TestCleaner_CleanOnce_CheckpointErrorResilient(t *testing.T) {
	mockRepo := &mockRetentionRepo{
		pruneFn: func(ctx context.Context, successCutoff, failedCutoff time.Time, batchSize int) (int64, error) {
			return 0, nil
		},
		checkpointFn: func(ctx context.Context) error {
			return errors.New("checkpoint sqlite busy")
		},
	}

	cleaner := NewCleaner(mockRepo, CleanerConfig{}, discardLogger())
	_, err := cleaner.CleanOnce(context.Background())
	if err != nil {
		t.Fatalf("CleanOnce should not fail when Checkpoint errors, got: %v", err)
	}
}

func TestCleaner_PeriodicCheckpoint(t *testing.T) {
	mockRepo := &mockRetentionRepo{
		pruneFn: func(ctx context.Context, successCutoff, failedCutoff time.Time, batchSize int) (int64, error) {
			return 0, nil
		},
	}

	cleaner := NewCleaner(mockRepo, CleanerConfig{
		Interval: 20 * time.Millisecond,
	}, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cleaner.Start(ctx)

	// Wait enough time for startup call + multiple ticker calls
	time.Sleep(60 * time.Millisecond)

	if err := cleaner.Close(); err != nil {
		t.Fatalf("failed to close cleaner: %v", err)
	}

	mockRepo.mu.Lock()
	checkpointCalls := mockRepo.checkpointCalls
	mockRepo.mu.Unlock()

	// Initial startup CleanOnce (1) + periodic checkpoint ticker (> 0)
	if checkpointCalls < 2 {
		t.Errorf("expected at least 2 checkpoint calls (startup + periodic), got %d", checkpointCalls)
	}
}
