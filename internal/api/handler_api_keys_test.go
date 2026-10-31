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

	db, err := sqlite.Open(dbPath, "NORMAL")
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

	// 4. Revoke already revoked key should return 404
	rrRevokeAgain := httptest.NewRecorder()
	handler.RevokeKey(rrRevokeAgain, reqRevoke)
	if rrRevokeAgain.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for already revoked key, got %d", rrRevokeAgain.Code)
	}
}

func TestAPIKeysHandler_Errors(t *testing.T) {
	db := setupTestAPIKeysDB(t)
	defer db.Close()

	handler := NewAPIKeysHandler(db.APIKeys(), nil)

	// Invalid JSON payload
	badReq := httptest.NewRequest(http.MethodPost, "/api/v1/api-keys", bytes.NewReader([]byte("{invalid-json")))
	rrBad := httptest.NewRecorder()
	handler.CreateKey(rrBad, badReq)
	if rrBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for malformed JSON, got %d", rrBad.Code)
	}

	// Empty body defaults to unnamed
	emptyReq := httptest.NewRequest(http.MethodPost, "/api/v1/api-keys", bytes.NewReader([]byte("{}")))
	rrEmpty := httptest.NewRecorder()
	handler.CreateKey(rrEmpty, emptyReq)
	if rrEmpty.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for empty body, got %d", rrEmpty.Code)
	}
	var created model.CreateKeyResponse
	if err := json.Unmarshal(rrEmpty.Body.Bytes(), &created); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if created.Name != "unnamed" {
		t.Fatalf("expected default name 'unnamed', got %q", created.Name)
	}

	// Revoke without ID
	missingIDReq := httptest.NewRequest(http.MethodDelete, "/api/v1/api-keys/", nil)
	rrMissing := httptest.NewRecorder()
	handler.RevokeKey(rrMissing, missingIDReq)
	if rrMissing.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for missing ID, got %d", rrMissing.Code)
	}

	// Revoke non-existent ID
	notFoundReq := httptest.NewRequest(http.MethodDelete, "/api/v1/api-keys/01ARZ3NDEKTSV4RRFFQ69G5FAV", nil)
	notFoundReq.SetPathValue("id", "01ARZ3NDEKTSV4RRFFQ69G5FAV")
	rrNotFound := httptest.NewRecorder()
	handler.RevokeKey(rrNotFound, notFoundReq)
	if rrNotFound.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found for non-existent ID, got %d", rrNotFound.Code)
	}
}

func TestMaskKey_Short(t *testing.T) {
	if got := MaskKey("short"); got != "*****" {
		t.Fatalf("expected *****, got %s", got)
	}
}
