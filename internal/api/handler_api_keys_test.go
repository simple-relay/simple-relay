package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage/sqlite"
)

func setupTestAPIKeysDB(t *testing.T) *sqlite.DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_api_keys.db")

	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test sqlite: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate schema: %v", err)
	}

	return db
}

func TestAPIKeysHandler_CreateListRevoke(t *testing.T) {
	db := setupTestAPIKeysDB(t)
	defer db.Close()

	handler := NewAPIKeysHandler(db.APIKeys(), nil)

	// 1. Create key
	body, _ := json.Marshal(map[string]string{"name": "test-key"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/api-keys", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handler.CreateKey(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rr.Code, rr.Body.String())
	}

	var created model.CreateKeyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !strings.HasPrefix(created.Key, "sr_live_") {
		t.Fatalf("expected sr_live_ prefix, got: %s", created.Key)
	}
	if !strings.Contains(created.MaskedKey, "*****") {
		t.Fatalf("expected masked key format AAA*****BBBB, got: %s", created.MaskedKey)
	}

	// 2. List keys
	reqList := httptest.NewRequest(http.MethodGet, "/api/v1/api-keys", nil)
	rrList := httptest.NewRecorder()
	handler.ListKeys(rrList, reqList)

	if rrList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rrList.Code)
	}
	var keys []*model.APIKey
	_ = json.Unmarshal(rrList.Body.Bytes(), &keys)
	if len(keys) != 1 || keys[0].ID != created.ID {
		t.Fatalf("unexpected list output: %+v", keys)
	}

	// 3. Revoke key
	reqRevoke := httptest.NewRequest(http.MethodDelete, "/api/v1/api-keys/"+created.ID, nil)
	reqRevoke.SetPathValue("id", created.ID)
	rrRevoke := httptest.NewRecorder()
	handler.RevokeKey(rrRevoke, reqRevoke)

	if rrRevoke.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rrRevoke.Code)
	}
}
