package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

func setupTestDB(t *testing.T) (*DB, *RouteRepo) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_repo.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test sqlite: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate schema: %v", err)
	}

	return db, NewRouteRepo(db)
}

func TestRouteRepo_CRUD(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()

	route := &model.Route{
		Name:                 "stripe-inbound",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/stripe",
		DestinationURL:       "https://api.internal/stripe",
		SignatureProvider:    model.SignatureProviderStripe,
		SignatureSecret:      "whsec_test123",
		MaxAttempts:          5,
		TimeoutMS:            10000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}

	// 1. Create
	if err := repo.CreateRoute(ctx, route); err != nil {
		t.Fatalf("failed to create route: %v", err)
	}
	if route.ID == "" {
		t.Fatal("expected route ID to be generated")
	}

	// 2. Duplicate Name Conflict
	dupName := *route
	dupName.ID = ""
	dupName.Path = "/api/v1/webhook/stripe-2"
	if err := repo.CreateRoute(ctx, &dupName); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("expected ErrConflict on duplicate name, got: %v", err)
	}

	// 3. Duplicate Path Conflict
	dupPath := *route
	dupPath.ID = ""
	dupPath.Name = "stripe-inbound-2"
	if err := repo.CreateRoute(ctx, &dupPath); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("expected ErrConflict on duplicate path, got: %v", err)
	}

	// 4. Get by ID
	fetchedByID, err := repo.GetRouteByID(ctx, route.ID)
	if err != nil {
		t.Fatalf("failed to get route by ID: %v", err)
	}
	if fetchedByID.Name != route.Name || fetchedByID.SignatureSecret != route.SignatureSecret {
		t.Fatalf("fetched route mismatch: %+v", fetchedByID)
	}

	// 5. Get by Name
	fetchedByName, err := repo.GetRouteByName(ctx, route.Name)
	if err != nil {
		t.Fatalf("failed to get route by Name: %v", err)
	}
	if fetchedByName.ID != route.ID {
		t.Fatalf("expected ID %s, got %s", route.ID, fetchedByName.ID)
	}

	// 6. Get by Path
	fetchedByPath, err := repo.GetRouteByPath(ctx, route.Path)
	if err != nil {
		t.Fatalf("failed to get route by Path: %v", err)
	}
	if fetchedByPath.ID != route.ID {
		t.Fatalf("expected ID %s, got %s", route.ID, fetchedByPath.ID)
	}

	// 7. Update
	route.DestinationURL = "https://api.internal/stripe-v2"
	route.MaxAttempts = 8
	if err := repo.UpdateRoute(ctx, route); err != nil {
		t.Fatalf("failed to update route: %v", err)
	}

	updated, err := repo.GetRouteByID(ctx, route.ID)
	if err != nil {
		t.Fatalf("failed to fetch updated route: %v", err)
	}
	if updated.DestinationURL != "https://api.internal/stripe-v2" || updated.MaxAttempts != 8 {
		t.Fatalf("updated route values mismatch: %+v", updated)
	}

	// 8. List
	route2 := &model.Route{
		Name:                 "github-webhook",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/github",
		DestinationURL:       "https://api.internal/github",
		SignatureProvider:    model.SignatureProviderGitHub,
		SignatureSecret:      "gh_secret_test_123",
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyLinear,
	}
	if err := repo.CreateRoute(ctx, route2); err != nil {
		t.Fatalf("failed to create route2: %v", err)
	}

	allRoutes, err := repo.ListRoutes(ctx)
	if err != nil {
		t.Fatalf("failed to list routes: %v", err)
	}
	if len(allRoutes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(allRoutes))
	}

	// 9. Delete
	if err := repo.DeleteRoute(ctx, route.ID); err != nil {
		t.Fatalf("failed to delete route: %v", err)
	}

	_, err = repo.GetRouteByID(ctx, route.ID)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after deletion, got: %v", err)
	}

	// 10. Delete non-existent
	if err := repo.DeleteRoute(ctx, "non-existent-id"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound deleting non-existent, got: %v", err)
	}
}
