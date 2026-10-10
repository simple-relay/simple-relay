package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/simple-relay/simple-relay/internal/storage"
)

var (
	// ErrCleanerAlreadyRunning indicates an ad-hoc clean run was triggered while one was already in progress.
	ErrCleanerAlreadyRunning = errors.New("cleaner cycle is already in progress")
)

// CleanerConfig encapsulates settings for the Storage Retention Cleaner.
type CleanerConfig struct {
	Interval             time.Duration // Periodic execution interval (default: 1h)
	RetentionSuccessDays int           // Max days to keep successful requests (default: 7)
	RetentionFailedDays  int           // Max days to keep failed/dlq requests (default: 30)
	BatchSize            int           // Number of records deleted per batch transaction (default: 1000)
	VacuumPages          int           // Max pages reclaimed via incremental vacuum (default: 1000)
}

// CleanResult holds summary metrics from a single clean run.
type CleanResult struct {
	PurgedCount    int64
	VacuumExecuted bool
	Duration       time.Duration
}

// Cleaner periodically purges expired requests and audit logs, and reclaims disk space.
type Cleaner struct {
	repo       storage.RetentionRepository
	logger     *slog.Logger
	cfg        CleanerConfig
	isCleaning atomic.Bool

	done chan struct{}
	wg   sync.WaitGroup
	mu   sync.Mutex
	stop bool
}

// NewCleaner creates a new Storage Retention Cleaner instance.
func NewCleaner(repo storage.RetentionRepository, cfg CleanerConfig, logger *slog.Logger) *Cleaner {
	if cfg.Interval <= 0 {
		cfg.Interval = 1 * time.Hour
	}
	if cfg.RetentionSuccessDays <= 0 {
		cfg.RetentionSuccessDays = 7
	}
	if cfg.RetentionFailedDays <= 0 {
		cfg.RetentionFailedDays = 30
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 1000
	}
	if cfg.VacuumPages <= 0 {
		cfg.VacuumPages = 1000
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Cleaner{
		repo:   repo,
		logger: logger,
		cfg:    cfg,
		done:   make(chan struct{}),
	}
}

// CleanOnce performs a single retention purge and disk reclamation cycle.
// Can be called directly by CLI or test suites ⚡.
func (c *Cleaner) CleanOnce(ctx context.Context) (CleanResult, error) {
	if !c.isCleaning.CompareAndSwap(false, true) {
		return CleanResult{}, ErrCleanerAlreadyRunning
	}
	defer c.isCleaning.Store(false)

	startTime := time.Now()
	nowUTC := startTime.UTC()
	successCutoff := nowUTC.AddDate(0, 0, -c.cfg.RetentionSuccessDays)
	failedCutoff := nowUTC.AddDate(0, 0, -c.cfg.RetentionFailedDays)

	c.logger.Debug("starting storage retention prune cycle",
		"success_cutoff", successCutoff,
		"failed_cutoff", failedCutoff,
		"batch_size", c.cfg.BatchSize,
	)

	purged, err := c.repo.PruneExpiredRequests(ctx, successCutoff, failedCutoff, c.cfg.BatchSize)
	if err != nil {
		return CleanResult{
			PurgedCount: purged,
			Duration:    time.Since(startTime),
		}, fmt.Errorf("failed to prune expired requests: %w", err)
	}

	result := CleanResult{
		PurgedCount: purged,
	}

	// Only reclaim disk pages if at least one request was pruned
	if purged > 0 {
		if err := c.repo.ReclaimStorage(ctx, c.cfg.VacuumPages); err != nil {
			c.logger.Error("failed to reclaim storage after prune cycle", "error", err)
		} else {
			result.VacuumExecuted = true
		}
	}

	result.Duration = time.Since(startTime)

	if purged > 0 {
		c.logger.Info("storage retention cleanup completed",
			"purged_requests", result.PurgedCount,
			"vacuum_executed", result.VacuumExecuted,
			"duration_ms", result.Duration.Milliseconds(),
		)
	} else {
		c.logger.Debug("storage retention cleanup cycle completed with zero purges",
			"duration_ms", result.Duration.Milliseconds(),
		)
	}

	return result, nil
}

// Start launches the background cleaner loop.
func (c *Cleaner) Start(ctx context.Context) {
	c.wg.Add(1)
	go c.run(ctx)
}

// run executes initial cleanup and periodic timer triggers.
func (c *Cleaner) run(ctx context.Context) {
	defer c.wg.Done()

	// Initial cleanup pass on startup to clear backlog from any downtime
	if _, err := c.CleanOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		c.logger.Error("initial storage retention cleanup failed", "error", err)
	}

	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if _, err := c.CleanOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				c.logger.Error("periodic storage retention cleanup failed", "error", err)
			}
		case <-ctx.Done():
			return
		case <-c.done:
			return
		}
	}
}

// Close gracefully stops the background cleaner worker.
func (c *Cleaner) Close() error {
	c.mu.Lock()
	if c.stop {
		c.mu.Unlock()
		return nil
	}
	c.stop = true
	close(c.done)
	c.mu.Unlock()

	c.wg.Wait()
	return nil
}
