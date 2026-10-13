package engine

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

// DispatcherConfig encapsulates settings for the Dispatcher.
type DispatcherConfig struct {
	PollInterval          time.Duration
	LeaseTimeout          time.Duration
	LeaseReapInterval     time.Duration
	SkipImmediateRecovery bool
}

// Dispatcher orchestrates picking up due requests and distributing them to the WorkerPool.
type Dispatcher struct {
	repo                  storage.RequestRepository
	jobsCh                chan *model.Request
	wakeCh                chan struct{}
	pollInterval          time.Duration
	leaseTimeout          time.Duration
	leaseReapInterval     time.Duration
	skipImmediateRecovery bool

	done chan struct{}
	wg   sync.WaitGroup
	mu   sync.Mutex
	stop bool
}

// NewDispatcher creates a new Dispatcher instance.
func NewDispatcher(repo storage.RequestRepository, jobsCh chan *model.Request, cfg DispatcherConfig) *Dispatcher {
	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}

	leaseTimeout := cfg.LeaseTimeout
	if leaseTimeout <= 0 {
		leaseTimeout = 5 * time.Minute
	}

	leaseReapInterval := cfg.LeaseReapInterval
	if leaseReapInterval <= 0 {
		leaseReapInterval = 1 * time.Minute
	}

	return &Dispatcher{
		repo:                  repo,
		jobsCh:                jobsCh,
		wakeCh:                make(chan struct{}, 1),
		pollInterval:          pollInterval,
		leaseTimeout:          leaseTimeout,
		leaseReapInterval:     leaseReapInterval,
		skipImmediateRecovery: cfg.SkipImmediateRecovery,
		done:                  make(chan struct{}),
	}
}

// Wake triggers an immediate, non-blocking dispatch cycle.
func (d *Dispatcher) Wake() {
	select {
	case d.wakeCh <- struct{}{}:
	default:
	}
}

// Start launches the background dispatching loop.
func (d *Dispatcher) Start(ctx context.Context) {
	// Startup recovery: reclaim any requests left stuck in 'processing' by a previous instance.
	// When running as an inherited process during live binary upgrade, immediate recovery is
	// skipped to avoid stealing in-flight deliveries from the draining parent process ⚡.
	if !d.skipImmediateRecovery {
		recovered, err := d.repo.RecoverStuckRequests(ctx, time.Now().UTC())
		if err != nil {
			slog.Error("failed to recover stuck requests during dispatcher startup", "error", err)
		} else if recovered > 0 {
			slog.Info("recovered stuck requests on startup", "count", recovered)
		}
	}

	d.wg.Add(1)
	go d.run(ctx)
}

// run executes the multi-trigger event loop.
func (d *Dispatcher) run(ctx context.Context) {
	defer d.wg.Done()

	pollTicker := time.NewTicker(d.pollInterval)
	defer pollTicker.Stop()

	reapTicker := time.NewTicker(d.leaseReapInterval)
	defer reapTicker.Stop()

	// Initial dispatch on startup
	d.dispatch(ctx)

	for {
		select {
		case <-d.wakeCh:
			d.dispatch(ctx)

		case <-pollTicker.C:
			d.dispatch(ctx)

		case <-reapTicker.C:
			stuckBefore := time.Now().UTC().Add(-d.leaseTimeout)
			recovered, err := d.repo.RecoverStuckRequests(ctx, stuckBefore)
			if err != nil {
				slog.Error("failed to recover stuck requests in reaper", "error", err)
			} else if recovered > 0 {
				slog.Info("recovered stuck requests in reaper cycle", "count", recovered)
				d.dispatch(ctx)
			}

		case <-ctx.Done():
			return

		case <-d.done:
			return
		}
	}
}

// dispatch queries database for due requests up to available channel capacity and delivers them to jobsCh.
func (d *Dispatcher) dispatch(ctx context.Context) {
	avail := cap(d.jobsCh) - len(d.jobsCh)
	if avail <= 0 {
		// Worker queue is full, do not query DB to avoid unbound memory holding
		return
	}

	now := time.Now().UTC()
	claimed, err := d.repo.ClaimPendingRequests(ctx, avail, now)
	if err != nil {
		slog.Error("failed to claim pending requests", "error", err)
		return
	}

	for _, req := range claimed {
		select {
		case d.jobsCh <- req:
		case <-ctx.Done():
			return
		case <-d.done:
			return
		}
	}
}

// Close gracefully stops the dispatcher loop.
func (d *Dispatcher) Close() error {
	d.mu.Lock()
	if d.stop {
		d.mu.Unlock()
		return nil
	}
	d.stop = true
	close(d.done)
	d.mu.Unlock()

	d.wg.Wait()
	return nil
}
