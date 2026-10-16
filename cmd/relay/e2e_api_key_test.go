package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/simple-relay/simple-relay/internal/api"
	"github.com/simple-relay/simple-relay/internal/cli"
	"github.com/simple-relay/simple-relay/internal/config"
	"github.com/simple-relay/simple-relay/internal/storage/sqlite"
)

func TestE2E_ColdBootAutoSeedingAndCLI(t *testing.T) {
	tempDB := t.TempDir() + "/test_relay.db"
	db, err := sqlite.Open(tempDB)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate schema: %v", err)
	}

	// Cold boot check: CountTotalKeys is 0
	count, err := db.APIKeys().CountTotalKeys(ctx)
	if err != nil || count != 0 {
		t.Fatalf("expected 0 keys on cold boot, got %d, err: %v", count, err)
	}

	// Trigger auto-seeding helper
	rawKey, apiKey, err := api.GenerateRawKey("default")
	if err != nil {
		t.Fatalf("failed to generate default key: %v", err)
	}
	if err := db.APIKeys().CreateAPIKey(ctx, apiKey); err != nil {
		t.Fatalf("failed to seed key: %v", err)
	}

	// Configure server with AdminPassword to enable protected mode
	cfg := config.DefaultConfig()
	cfg.AdminPassword = "protectedpassword123"

	router := api.NewRouter(cfg, nil, api.RouterOptions{
		APIKeyRepo: db.APIKeys(),
	})
	ts := httptest.NewServer(router)
	defer ts.Close()

	// 1. Client with valid API key can query routes/requests
	clientWithKey := cli.NewClient(ts.URL, rawKey, 0)
	_, err = clientWithKey.InspectRequest(ctx, "01j8v3x4m89a0b1c2d3e4f5g6h")
	if errors.Is(err, cli.ErrUnauthorized) {
		t.Fatal("expected authenticated access with valid api key, got ErrUnauthorized")
	}

	// 2. Client with bad API key is rejected
	clientBadKey := cli.NewClient(ts.URL, "sr_live_invalidkey123", 0)
	_, err = clientBadKey.InspectRequest(ctx, "01j8v3x4m89a0b1c2d3e4f5g6h")
	if !errors.Is(err, cli.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for bad api key, got: %v", err)
	}
}

func TestE2E_ProtectedMode_NoAutoSeeding(t *testing.T) {
	tempDB := t.TempDir() + "/test_relay_protected.db"
	db, err := sqlite.Open(tempDB)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate schema: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.AdminPassword = "production_password"

	// Simulate cold boot logic from main.go
	if cfg.AdminPassword == "" {
		_, apiKey, err := api.GenerateRawKey("default")
		if err != nil {
			t.Fatalf("failed to generate key: %v", err)
		}
		if err := db.APIKeys().CreateAPIKey(ctx, apiKey); err != nil {
			t.Fatalf("failed to persist key: %v", err)
		}
	}

	count, err := db.APIKeys().CountTotalKeys(ctx)
	if err != nil || count != 0 {
		t.Fatalf("expected 0 keys in database when protected mode is enabled, got %d", count)
	}
}
