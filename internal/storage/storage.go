package storage

import (
	"context"
	"errors"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
)

var (
	// ErrNotFound is returned when the requested entity does not exist in storage.
	ErrNotFound = errors.New("resource not found")
	// ErrConflict is returned when a unique constraint or conflicting state is violated.
	ErrConflict = errors.New("resource conflict or already exists")
)

// RouteRepository defines the storage operations for Route configuration.
type RouteRepository interface {
	CreateRoute(ctx context.Context, route *model.Route) error
	GetRouteByID(ctx context.Context, id string) (*model.Route, error)
	GetRouteByName(ctx context.Context, name string) (*model.Route, error)
	GetRouteByPath(ctx context.Context, path string) (*model.Route, error)
	ListRoutes(ctx context.Context) ([]*model.Route, error)
	UpdateRoute(ctx context.Context, route *model.Route) error
	DeleteRoute(ctx context.Context, id string) error
}

// RequestFilter defines criteria for querying requests.
type RequestFilter struct {
	Status  string // Optional: 'pending', 'processing', 'success', 'failed', 'dlq'
	RouteID string // Optional: filter by specific route ID
	Search  string // Optional: ID prefix search
	Limit   int    // Default 50, maximum 100
	Offset  int    // Pagination offset
}

// RequestRepository defines the storage operations for durable HTTP requests and jobs.
type RequestRepository interface {
	CreateRequest(ctx context.Context, req *model.Request) error
	CreateRequestsBatch(ctx context.Context, reqs []*model.Request) error
	GetRequestByID(ctx context.Context, id string) (*model.Request, error)
	ListPendingRequests(ctx context.Context, limit int, dueBefore time.Time) ([]*model.Request, error)
	ListRequests(ctx context.Context, filter RequestFilter) ([]*model.RequestMetadata, int64, error)
	UpdateRequestStatus(ctx context.Context, id string, status model.RequestStatus, attemptsCount int, nextAttemptAt *time.Time, lastError *string) error
	ClaimPendingRequests(ctx context.Context, limit int, dueBefore time.Time) ([]*model.Request, error)
	RecoverStuckRequests(ctx context.Context, stuckBefore time.Time) (int64, error)
	ListDLQRequests(ctx context.Context, routeID string, limit, offset int) ([]*model.Request, int64, error)
	ReplayDLQRequest(ctx context.Context, id string) (*model.Request, error)
	ReplayAllDLQRequests(ctx context.Context, routeID string, limit int) ([]string, error)
}

// AttemptRepository defines the storage operations for attempt audit logs.
type AttemptRepository interface {
	RecordAttempt(ctx context.Context, attempt *model.Attempt) error
	ListAttemptsByRequestID(ctx context.Context, requestID string) ([]*model.Attempt, error)
}

// RetentionRepository defines maintenance operations for storage lifecycle management.
// Designed as an adapter interface to support SQLite, PostgreSQL, and MySQL backends.
type RetentionRepository interface {
	// PruneExpiredRequests removes expired requests matching retention policies in bounded batches.
	// It cascades deletion to related attempts and returns the total number of requests purged.
	PruneExpiredRequests(ctx context.Context, successCutoff, failedCutoff time.Time, batchSize int) (int64, error)

	// ReclaimStorage performs dialect-specific disk page reclamation.
	// In SQLite, this executes PRAGMA incremental_vacuum.
	// In PostgreSQL/MySQL, this is a no-op (managed by database daemon autovacuum).
	ReclaimStorage(ctx context.Context, maxPages int) error
}

// Storage encapsulates the complete storage engine interface.
type Storage interface {
	RouteRepository
	RequestRepository
	AttemptRepository
	RetentionRepository

	Ping(ctx context.Context) error
	Close() error
}
