package engine

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage/sqlite"
)

func setupTestRegistry(t *testing.T) (*sqlite.DB, *RouteRegistry) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_registry.db")

	db, err := sqlite.Open(dbPath, "NORMAL")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	repo := sqlite.NewRouteRepo(db)
	registry := NewRouteRegistry(repo)

	return db, registry
}

func TestRouteRegistry_InitAndHotReload(t *testing.T) {
	db, registry := setupTestRegistry(t)
	defer db.Close()

	ctx := context.Background()

	// 1. Initial route in DB
	route1 := &model.Route{
		Name:                 "stripe",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/stripe",
		DestinationURL:       "https://internal.app/stripe",
		MaxAttempts:          5,
		TimeoutMS:            10000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}

	if err := registry.Create(ctx, route1); err != nil {
		t.Fatalf("failed to create route1: %v", err)
	}

	// 2. Query cache directly
	cached, ok := registry.GetByName("stripe")
	if !ok || cached.ID != route1.ID {
		t.Fatalf("expected to find route1 in cache by name, got: %+v", cached)
	}

	cachedByPath, ok := registry.GetByPath("/api/v1/webhook/stripe")
	if !ok || cachedByPath.ID != route1.ID {
		t.Fatalf("expected to find route1 in cache by path")
	}

	// 3. Hot-reload upon Update
	cached.DestinationURL = "https://internal.app/stripe-v2"
	if err := registry.Update(ctx, cached); err != nil {
		t.Fatalf("failed to update route: %v", err)
	}

	updated, ok := registry.GetByName("stripe")
	if !ok || updated.DestinationURL != "https://internal.app/stripe-v2" {
		t.Fatalf("expected immediate hot-reloaded URL, got: %+v", updated)
	}

	// 4. Hot-reload upon Delete
	if err := registry.Delete(ctx, updated.ID); err != nil {
		t.Fatalf("failed to delete route: %v", err)
	}

	_, ok = registry.GetByName("stripe")
	if ok {
		t.Fatalf("expected route to be evicted from cache after deletion")
	}
}

func TestRouteRegistry_ConcurrentAccess(t *testing.T) {
	db, registry := setupTestRegistry(t)
	defer db.Close()

	ctx := context.Background()

	// Seed 5 routes
	for i := 0; i < 5; i++ {
		name := string(rune('a' + i))
		err := registry.Create(ctx, &model.Route{
			Name:                 name,
			Type:                 model.RouteTypeInbound,
			Mode:                 model.RouteModeAsync,
			Path:                 "/webhook/" + name,
			DestinationURL:       "https://internal.app/" + name,
			MaxAttempts:          3,
			TimeoutMS:            5000,
			RetryBackoffStrategy: model.RetryStrategyLinear,
		})
		if err != nil {
			t.Fatalf("failed to create seed route %s: %v", name, err)
		}
	}

	var wg sync.WaitGroup
	const readers = 10
	const iterations = 100

	// Concurrent Readers
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			for iter := 0; iter < iterations; iter++ {
				_, _ = registry.GetByName("a")
				_, _ = registry.GetByPath("/webhook/b")
				_ = registry.List()
			}
		}(r)
	}

	// Concurrent Invalidator / Reloader
	wg.Add(1)
	go func() {
		defer wg.Done()
		for iter := 0; iter < 10; iter++ {
			_ = registry.Invalidate(ctx)
			time.Sleep(2 * time.Millisecond)
		}
	}()

	wg.Wait()
}
