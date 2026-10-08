package engine

import (
	"context"
	"fmt"
	"sync"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

// RouteRegistry provides a high-performance in-memory cache for Route configurations
// backed by SQLite durable storage with immediate cache invalidation on mutations.
type RouteRegistry struct {
	repo storage.RouteRepository

	mu           sync.RWMutex
	routesByName map[string]*model.Route
	routesByPath map[string]*model.Route
	routesByID   map[string]*model.Route
	routesList   []*model.Route
}

// NewRouteRegistry constructs a new in-memory cache registry for routes.
func NewRouteRegistry(repo storage.RouteRepository) *RouteRegistry {
	return &RouteRegistry{
		repo:         repo,
		routesByName: make(map[string]*model.Route),
		routesByPath: make(map[string]*model.Route),
		routesByID:   make(map[string]*model.Route),
		routesList:   make([]*model.Route, 0),
	}
}

// Init loads all routes from the database into the memory cache.
func (r *RouteRegistry) Init(ctx context.Context) error {
	return r.Reload(ctx)
}

// Reload fetches the latest routes from SQLite and atomically replaces the in-memory cache.
func (r *RouteRegistry) Reload(ctx context.Context) error {
	routes, err := r.repo.ListRoutes(ctx)
	if err != nil {
		return fmt.Errorf("failed to reload routes from storage: %w", err)
	}

	byName := make(map[string]*model.Route, len(routes))
	byPath := make(map[string]*model.Route, len(routes))
	byID := make(map[string]*model.Route, len(routes))
	list := make([]*model.Route, len(routes))

	for i, rt := range routes {
		// Clone route struct to ensure cached entries are protected from outside mutation
		routeCopy := *rt
		byName[rt.Name] = &routeCopy
		byPath[rt.Path] = &routeCopy
		byID[rt.ID] = &routeCopy
		list[i] = &routeCopy
	}

	r.mu.Lock()
	r.routesByName = byName
	r.routesByPath = byPath
	r.routesByID = byID
	r.routesList = list
	r.mu.Unlock()

	return nil
}

// Invalidate triggers an immediate cache refresh from the database.
func (r *RouteRegistry) Invalidate(ctx context.Context) error {
	return r.Reload(ctx)
}

// GetByName returns a cached Route by its unique name with sub-microsecond O(1) latency.
func (r *RouteRegistry) GetByName(name string) (*model.Route, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rt, ok := r.routesByName[name]
	if !ok {
		return nil, false
	}
	routeCopy := *rt
	return &routeCopy, true
}

// GetByPath returns a cached Route by its URL path with sub-microsecond O(1) latency.
func (r *RouteRegistry) GetByPath(path string) (*model.Route, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rt, ok := r.routesByPath[path]
	if !ok {
		return nil, false
	}
	routeCopy := *rt
	return &routeCopy, true
}

// GetByID returns a cached Route by its ULID with sub-microsecond O(1) latency.
func (r *RouteRegistry) GetByID(id string) (*model.Route, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rt, ok := r.routesByID[id]
	if !ok {
		return nil, false
	}
	routeCopy := *rt
	return &routeCopy, true
}

// List returns a shallow slice copy of all currently cached routes.
func (r *RouteRegistry) List() []*model.Route {
	r.mu.RLock()
	defer r.mu.RUnlock()

	copied := make([]*model.Route, len(r.routesList))
	copy(copied, r.routesList)
	return copied
}

// Create persists a new route into SQLite and immediately refreshes the memory cache.
func (r *RouteRegistry) Create(ctx context.Context, route *model.Route) error {
	if err := r.repo.CreateRoute(ctx, route); err != nil {
		return err
	}
	return r.Reload(ctx)
}

// Update updates an existing route in SQLite and immediately refreshes the memory cache.
func (r *RouteRegistry) Update(ctx context.Context, route *model.Route) error {
	if err := r.repo.UpdateRoute(ctx, route); err != nil {
		return err
	}
	return r.Reload(ctx)
}

// Delete removes a route from SQLite and immediately refreshes the memory cache.
func (r *RouteRegistry) Delete(ctx context.Context, id string) error {
	if err := r.repo.DeleteRoute(ctx, id); err != nil {
		return err
	}
	return r.Reload(ctx)
}
