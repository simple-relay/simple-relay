package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

func TestAPIKeyRepo_CRUD(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	repo := db.APIKeys()

	// 1. Initial counts should be 0
	total, err := repo.CountTotalKeys(ctx)
	if err != nil || total != 0 {
		t.Fatalf("expected 0 total keys, got %d, err: %v", total, err)
	}

	// 2. Create key
	key := &model.APIKey{
		ID:        model.NewULID(),
		Name:      "default",
		KeyHash:   "hash1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		MaskedKey: "sr_live_1234*****abcd",
		CreatedAt: time.Now().UTC(),
	}
	if err := repo.CreateAPIKey(ctx, key); err != nil {
		t.Fatalf("failed to create api key: %v", err)
	}

	// 3. Count total & active
	total, _ = repo.CountTotalKeys(ctx)
	active, _ := repo.CountActiveKeys(ctx)
	if total != 1 || active != 1 {
		t.Fatalf("expected 1 total and 1 active key, got %d total, %d active", total, active)
	}

	// 4. Get by hash
	fetched, err := repo.GetAPIKeyByHash(ctx, key.KeyHash)
	if err != nil {
		t.Fatalf("failed to get api key by hash: %v", err)
	}
	if fetched.ID != key.ID || fetched.Name != key.Name || fetched.MaskedKey != key.MaskedKey {
		t.Fatalf("fetched key mismatch: %+v", fetched)
	}

	// 5. Unique hash constraint
	duplicate := &model.APIKey{
		ID:        model.NewULID(),
		Name:      "duplicate",
		KeyHash:   key.KeyHash,
		MaskedKey: "sr_live_5678*****efgh",
		CreatedAt: time.Now().UTC(),
	}
	if err := repo.CreateAPIKey(ctx, duplicate); err != storage.ErrConflict {
		t.Fatalf("expected ErrConflict on duplicate hash, got: %v", err)
	}

	// 6. List keys
	keys, err := repo.ListAPIKeys(ctx)
	if err != nil || len(keys) != 1 {
		t.Fatalf("expected 1 listed key, got %d, err: %v", len(keys), err)
	}

	// 7. Revoke key
	if err := repo.RevokeAPIKey(ctx, key.ID); err != nil {
		t.Fatalf("failed to revoke key: %v", err)
	}

	// 8. Verify active count is 0 while total count remains 1
	total, _ = repo.CountTotalKeys(ctx)
	active, _ = repo.CountActiveKeys(ctx)
	if total != 1 || active != 0 {
		t.Fatalf("expected 1 total and 0 active after revoke, got total=%d active=%d", total, active)
	}

	// 9. Lookup revoked key by hash should return ErrNotFound
	_, err = repo.GetAPIKeyByHash(ctx, key.KeyHash)
	if err != storage.ErrNotFound {
		t.Fatalf("expected ErrNotFound for revoked key, got: %v", err)
	}
}

func TestAPIKeyRepo_CacheInvalidationOnRevocation(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	repo := db.APIKeys()

	key := &model.APIKey{
		ID:        model.NewULID(),
		Name:      "cached-key",
		KeyHash:   "cachedhash1234567890abcdef1234567890abcdef1234567890abcdef12345678",
		MaskedKey: "sr_live_cach*****abcd",
		CreatedAt: time.Now().UTC(),
	}
	if err := repo.CreateAPIKey(ctx, key); err != nil {
		t.Fatalf("failed to create api key: %v", err)
	}

	// First lookup (cache miss -> loads from DB into cache)
	k1, err := repo.GetAPIKeyByHash(ctx, key.KeyHash)
	if err != nil {
		t.Fatalf("expected key on first lookup, got error: %v", err)
	}
	if k1.ID != key.ID {
		t.Fatalf("key id mismatch: %s != %s", k1.ID, key.ID)
	}

	// Second lookup (cache hit)
	k2, err := repo.GetAPIKeyByHash(ctx, key.KeyHash)
	if err != nil {
		t.Fatalf("expected key on second lookup, got error: %v", err)
	}
	if k2.ID != key.ID {
		t.Fatalf("key id mismatch: %s != %s", k2.ID, key.ID)
	}

	// Revoke key (must invalidate in-memory cache)
	if err := repo.RevokeAPIKey(ctx, key.ID); err != nil {
		t.Fatalf("failed to revoke key: %v", err)
	}

	// Third lookup must return ErrNotFound, not stale cached key
	_, err = repo.GetAPIKeyByHash(ctx, key.KeyHash)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after revocation, got: %v", err)
	}
}

func TestAPIKeyRepo_EdgeCases(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	repo := db.APIKeys()

	// 1. Revoke non-existent key
	if err := repo.RevokeAPIKey(ctx, "non-existent-id"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-existent key revocation, got: %v", err)
	}

	// 2. Insert with standard sqlite datetime format
	id := model.NewULID()
	hash := "sqldatehash1234567890abcdef1234567890abcdef1234567890abcdef123456"
	_, err := db.Writer().ExecContext(ctx, `
		INSERT INTO api_keys (id, name, key_hash, masked_key, created_at, revoked_at)
		VALUES (?, ?, ?, ?, '2026-01-01 12:00:00', '2026-01-02 12:00:00');
	`, id, "legacy-date-key", hash, "sr_live_lega*****abcd")
	if err != nil {
		t.Fatalf("failed to insert raw legacy datetime key: %v", err)
	}

	// ListAPIKeys parses both created_at and revoked_at in legacy format
	keys, err := repo.ListAPIKeys(ctx)
	if err != nil || len(keys) != 1 {
		t.Fatalf("expected 1 key from ListAPIKeys, got %d, err: %v", len(keys), err)
	}
	if keys[0].CreatedAt.Year() != 2026 || keys[0].RevokedAt == nil || keys[0].RevokedAt.Year() != 2026 {
		t.Fatalf("unexpected parsed legacy dates: %+v", keys[0])
	}
}
