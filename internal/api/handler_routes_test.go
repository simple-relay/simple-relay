package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/config"
	"github.com/simple-relay/simple-relay/internal/engine"
	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage/sqlite"
)

func setupTestRoutesRouter(t *testing.T) (http.Handler, *sqlite.DB, *engine.RouteRegistry) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_routes.db")

	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test sqlite: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate schema: %v", err)
	}

	registry := engine.NewRouteRegistry(db.RouteRepo)
	if err := registry.Init(ctx); err != nil {
		t.Fatalf("failed to init registry: %v", err)
	}

	cfg := &config.Config{Port: 8080}
	handler := NewRouter(cfg, nil, RouterOptions{
		RouteRepo:     db.RouteRepo,
		RouteRegistry: registry,
		RequestRepo:   db.RequestRepo,
		AttemptRepo:   db.AttemptRepo,
	})

	return handler, db, registry
}

func TestRoutesHandler_CRUD(t *testing.T) {
	router, db, registry := setupTestRoutesRouter(t)
	defer db.Close()

	// 1. Initial list should be empty
	req := httptest.NewRequest(http.MethodGet, "/api/v1/routes", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var routes []*model.Route
	if err := json.NewDecoder(rec.Body).Decode(&routes); err != nil {
		t.Fatalf("failed to decode routes list: %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("expected 0 routes initially, got %d", len(routes))
	}

	// 2. Create route
	createBody := map[string]any{
		"name":            "stripe-webhook",
		"type":            "inbound",
		"mode":            "async",
		"path":            "/api/v1/webhook/stripe",
		"destination_url": "https://api.internal.com/stripe",
		"max_attempts":    5,
		"timeout_ms":      10000,
	}
	bodyBytes, _ := json.Marshal(createBody)

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/routes", bytes.NewReader(bodyBytes))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", createRec.Code, createRec.Body.String())
	}

	var createdRoute model.Route
	if err := json.NewDecoder(createRec.Body).Decode(&createdRoute); err != nil {
		t.Fatalf("failed to decode created route: %v", err)
	}
	if createdRoute.ID == "" || createdRoute.Name != "stripe-webhook" {
		t.Fatalf("unexpected created route: %+v", createdRoute)
	}

	// Verify route registry immediately has it (hot-reloaded)
	cachedRoute, found := registry.GetByName("stripe-webhook")
	if !found || cachedRoute.ID != createdRoute.ID {
		t.Fatalf("expected route in registry after creation, got found=%v", found)
	}

	// 3. Duplicate creation should return 409 Conflict
	dupReq := httptest.NewRequest(http.MethodPost, "/api/v1/routes", bytes.NewReader(bodyBytes))
	dupReq.Header.Set("Content-Type", "application/json")
	dupRec := httptest.NewRecorder()
	router.ServeHTTP(dupRec, dupReq)

	if dupRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on duplicate route, got %d", dupRec.Code)
	}

	// 4. Invalid creation (missing name) should return 400 Bad Request
	badReq := httptest.NewRequest(http.MethodPost, "/api/v1/routes", bytes.NewReader([]byte(`{"path":"/foo"}`)))
	badRec := httptest.NewRecorder()
	router.ServeHTTP(badRec, badReq)

	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request on invalid route, got %d", badRec.Code)
	}

	// 5. Get route by ID
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/routes/"+createdRoute.ID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on get route, got %d", getRec.Code)
	}

	// 6. Update route
	updateBody := map[string]any{
		"name":            "stripe-webhook-updated",
		"type":            "inbound",
		"mode":            "async",
		"path":            "/api/v1/webhook/stripe-v2",
		"destination_url": "https://api.internal.com/stripe-v2",
		"max_attempts":    10,
		"timeout_ms":      15000,
	}
	updBytes, _ := json.Marshal(updateBody)
	updReq := httptest.NewRequest(http.MethodPut, "/api/v1/routes/"+createdRoute.ID, bytes.NewReader(updBytes))
	updRec := httptest.NewRecorder()
	router.ServeHTTP(updRec, updReq)

	if updRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on update route, got %d: %s", updRec.Code, updRec.Body.String())
	}

	// Verify registry reloaded
	_, oldFound := registry.GetByName("stripe-webhook")
	if oldFound {
		t.Fatalf("expected old route name to be removed from registry")
	}
	newCached, newFound := registry.GetByName("stripe-webhook-updated")
	if !newFound || newCached.TimeoutMS != 15000 {
		t.Fatalf("expected updated route in registry, got found=%v, route=%+v", newFound, newCached)
	}

	// 7. Delete route
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/routes/"+createdRoute.ID, nil)
	delRec := httptest.NewRecorder()
	router.ServeHTTP(delRec, delReq)

	if delRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on delete route, got %d: %s", delRec.Code, delRec.Body.String())
	}

	// Verify deleted from registry
	_, afterDelFound := registry.GetByName("stripe-webhook-updated")
	if afterDelFound {
		t.Fatalf("expected route to be deleted from registry")
	}

	// 8. Get deleted route should return 404
	getDelReq := httptest.NewRequest(http.MethodGet, "/api/v1/routes/"+createdRoute.ID, nil)
	getDelRec := httptest.NewRecorder()
	router.ServeHTTP(getDelRec, getDelReq)

	if getDelRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on deleted route, got %d", getDelRec.Code)
	}
}
