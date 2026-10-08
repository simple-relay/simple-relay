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

// RequestRepository defines the storage operations for durable HTTP requests and jobs.
type RequestRepository interface {
	CreateRequest(ctx context.Context, req *model.Request) error
	CreateRequestsBatch(ctx context.Context, reqs []*model.Request) error
	GetRequestByID(ctx context.Context, id string) (*model.Request, error)
	ListPendingRequests(ctx context.Context, limit int, dueBefore time.Time) ([]*model.Request, error)
	UpdateRequestStatus(ctx context.Context, id string, status model.RequestStatus, attemptsCount int, nextAttemptAt *time.Time, lastError *string) error
}

// AttemptRepository defines the storage operations for attempt audit logs.
type AttemptRepository interface {
	RecordAttempt(ctx context.Context, attempt *model.Attempt) error
	ListAttemptsByRequestID(ctx context.Context, requestID string) ([]*model.Attempt, error)
}

// Storage encapsulates the complete storage engine interface.
type Storage interface {
	RouteRepository
	RequestRepository
	AttemptRepository

	Ping(ctx context.Context) error
	Close() error
}
