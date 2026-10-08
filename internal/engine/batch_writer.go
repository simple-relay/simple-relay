package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

var (
	// ErrBatchWriterClosed is returned when enqueueing to a closed BatchWriter.
	ErrBatchWriterClosed = errors.New("batch writer is closed")
)

type ingestionItem struct {
	req   *model.Request
	errCh chan error
}

// BatchWriterConfig contains configuration options for BatchWriter.
type BatchWriterConfig struct {
	BatchSize     int
	FlushInterval time.Duration
	QueueCapacity int
	WakeFn        func()
}

// BatchWriter implements single-writer micro-batching for durable request ingestion.
type BatchWriter struct {
	repo          storage.RequestRepository
	batchSize     int
	flushInterval time.Duration
	wakeFn        func()

	queue chan *ingestionItem
	done  chan struct{}
	wg    sync.WaitGroup
	mu    sync.RWMutex
	close bool
}

// NewBatchWriter constructs a new BatchWriter instance.
func NewBatchWriter(repo storage.RequestRepository, cfg BatchWriterConfig) *BatchWriter {
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 50
	}

	flushInterval := cfg.FlushInterval
	if flushInterval <= 0 {
		flushInterval = 10 * time.Millisecond
	}

	queueCap := cfg.QueueCapacity
	if queueCap <= 0 {
		queueCap = 1000
	}

	return &BatchWriter{
		repo:          repo,
		batchSize:     batchSize,
		flushInterval: flushInterval,
		wakeFn:        cfg.WakeFn,
		queue:         make(chan *ingestionItem, queueCap),
		done:          make(chan struct{}),
	}
}

// Start launches the single-writer background goroutine.
func (bw *BatchWriter) Start(ctx context.Context) {
	bw.wg.Add(1)
	go bw.run(ctx)
}

// Enqueue submits a request for durable batch ingestion.
// Caller blocks until the batch is committed to durable storage or the context expires.
func (bw *BatchWriter) Enqueue(ctx context.Context, req *model.Request) error {
	bw.mu.RLock()
	if bw.close {
		bw.mu.RUnlock()
		return ErrBatchWriterClosed
	}
	bw.mu.RUnlock()

	item := &ingestionItem{
		req:   req,
		errCh: make(chan error, 1),
	}

	// Natural backpressure: block until queue has room, context cancelled, or writer closed
	select {
	case bw.queue <- item:
	case <-ctx.Done():
		return ctx.Err()
	case <-bw.done:
		return ErrBatchWriterClosed
	}

	// Wait for durable write confirmation
	select {
	case err := <-item.errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-bw.done:
		return ErrBatchWriterClosed
	}
}

// run is the single-writer event loop grouping writes into atomic transactions.
func (bw *BatchWriter) run(ctx context.Context) {
	defer bw.wg.Done()

	ticker := time.NewTicker(bw.flushInterval)
	defer ticker.Stop()

	items := make([]*ingestionItem, 0, bw.batchSize)

	flush := func() {
		if len(items) == 0 {
			return
		}

		reqs := make([]*model.Request, len(items))
		for i, it := range items {
			reqs[i] = it.req
		}

		writeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := bw.repo.CreateRequestsBatch(writeCtx, reqs)
		cancel()

		for _, it := range items {
			if err != nil {
				it.errCh <- fmt.Errorf("batch write failed: %w", err)
			} else {
				it.errCh <- nil
			}
		}

		if err == nil && bw.wakeFn != nil {
			bw.wakeFn()
		}

		items = items[:0]
	}

	for {
		select {
		case item := <-bw.queue:
			items = append(items, item)
			if len(items) >= bw.batchSize {
				flush()
			}

		case <-ticker.C:
			flush()

		case <-ctx.Done():
			// Drain any remaining items in queue before shutting down
			for {
				select {
				case item := <-bw.queue:
					items = append(items, item)
					if len(items) >= bw.batchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}

		case <-bw.done:
			// Drain any remaining items in queue upon explicit Close
			for {
				select {
				case item := <-bw.queue:
					items = append(items, item)
					if len(items) >= bw.batchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

// Close gracefully closes the batch writer and flushes buffered items.
func (bw *BatchWriter) Close() error {
	bw.mu.Lock()
	if bw.close {
		bw.mu.Unlock()
		return nil
	}
	bw.close = true
	close(bw.done)
	bw.mu.Unlock()

	bw.wg.Wait()
	return nil
}
