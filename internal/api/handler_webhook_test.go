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

	"github.com/simple-relay/simple-relay/internal/config"
	"github.com/simple-relay/simple-relay/internal/engine"
	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage/sqlite"
)

func setupTestWebhookServer(t *testing.T, maxBodyBytes int64) (*sqlite.DB, *engine.RouteRegistry, *engine.BatchWriter, http.Handler, *model.Route, *model.Route) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_webhook.db")

	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}

	routeRepo := sqlite.NewRouteRepo(db)
	reqRepo := sqlite.NewRequestRepo(db)
	attemptRepo := sqlite.NewAttemptRepo(db)

	registry := engine.NewRouteRegistry(routeRepo)
	inboundRoute := &model.Route{
		Name:                 "stripe-webhook",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/stripe-webhook",
		DestinationURL:       "https://internal.service/stripe",
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := registry.Create(ctx, inboundRoute); err != nil {
		t.Fatalf("failed to create inbound route: %v", err)
	}

	outboundRoute := &model.Route{
		Name:                 "outbound-service",
		Type:                 model.RouteTypeOutbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/jobs/outbound-service",
		DestinationURL:       "https://internal.service/outbound",
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := registry.Create(ctx, outboundRoute); err != nil {
		t.Fatalf("failed to create outbound route: %v", err)
	}

	batchWriter := engine.NewBatchWriter(reqRepo, engine.BatchWriterConfig{
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		QueueCapacity: 50,
	})
	batchWriter.Start(context.Background())

	cfg := &config.Config{Port: 8080}
	if maxBodyBytes <= 0 {
		maxBodyBytes = 1024 * 1024
	}

	handler := NewRouter(cfg, nil, RouterOptions{
		RouteRegistry: registry,
		BatchWriter:   batchWriter,
		RequestRepo:   reqRepo,
		AttemptRepo:   attemptRepo,
		MaxBodyBytes:  maxBodyBytes,
	})

	return db, registry, batchWriter, handler, inboundRoute, outboundRoute
}

func TestWebhookIngest_Success_Methods(t *testing.T) {
	db, _, bw, handler, inboundRoute, _ := setupTestWebhookServer(t, 1024*1024)
	defer db.Close()
	defer bw.Close()

	methods := []string{http.MethodPost, http.MethodPut, http.MethodPatch}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			payload := `{"event": "payment_intent.succeeded", "id": "evt_123"}`
			req := httptest.NewRequest(method, "/api/v1/webhook/"+inboundRoute.Name, bytes.NewBufferString(payload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Stripe-Signature", "t=1600000000,v1=abcdef123456")

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200 OK, got %d: %s", rec.Code, rec.Body.String())
			}

			var resp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			relayID, ok := resp["relay_id"].(string)
			if !ok || relayID == "" {
				t.Fatalf("expected relay_id in response, got %v", resp)
			}
			if resp["status"] != "persisted" {
				t.Fatalf("expected status 'persisted', got %v", resp["status"])
			}

			// Verify durable storage in database
			reqRepo := sqlite.NewRequestRepo(db)
			storedReq, err := reqRepo.GetRequestByID(context.Background(), relayID)
			if err != nil {
				t.Fatalf("failed to find stored request: %v", err)
			}
			if storedReq.Method != method {
				t.Errorf("expected stored method %s, got %s", method, storedReq.Method)
			}
			if string(storedReq.Body) != payload {
				t.Errorf("expected stored body %s, got %s", payload, string(storedReq.Body))
			}
			if storedReq.Headers["Stripe-Signature"] != "t=1600000000,v1=abcdef123456" {
				t.Errorf("expected Stripe-Signature header preserved, got %v", storedReq.Headers)
			}
		})
	}
}

func TestWebhookIngest_RouteNotFound(t *testing.T) {
	db, _, bw, handler, _, _ := setupTestWebhookServer(t, 1024*1024)
	defer db.Close()
	defer bw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/non-existent-route", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 Not Found, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWebhookIngest_RouteTypeMismatch(t *testing.T) {
	db, _, bw, handler, _, outboundRoute := setupTestWebhookServer(t, 1024*1024)
	defer db.Close()
	defer bw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/"+outboundRoute.Name, bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "route is not configured for inbound webhooks") {
		t.Fatalf("expected error message about inbound webhooks, got: %s", rec.Body.String())
	}
}

func TestWebhookIngest_MethodNotAllowed(t *testing.T) {
	db, _, bw, handler, inboundRoute, _ := setupTestWebhookServer(t, 1024*1024)
	defer db.Close()
	defer bw.Close()

	disallowedMethods := []string{http.MethodGet, http.MethodDelete, http.MethodHead, http.MethodOptions}
	for _, method := range disallowedMethods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/webhook/"+inboundRoute.Name, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("expected status 405 Method Not Allowed for %s, got %d: %s", method, rec.Code, rec.Body.String())
			}
			allowHeader := rec.Header().Get("Allow")
			if !strings.Contains(allowHeader, "POST") || !strings.Contains(allowHeader, "PUT") || !strings.Contains(allowHeader, "PATCH") {
				t.Fatalf("expected Allow header with POST, PUT, PATCH, got %s", allowHeader)
			}
		})
	}
}

func TestWebhookIngest_PayloadTooLarge(t *testing.T) {
	// Setup with 100 bytes limit
	db, _, bw, handler, inboundRoute, _ := setupTestWebhookServer(t, 100)
	defer db.Close()
	defer bw.Close()

	largePayload := strings.Repeat("A", 200)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/"+inboundRoute.Name, bytes.NewBufferString(largePayload))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413 Request Entity Too Large, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWebhookIngest_HeaderAndPayloadPreservation(t *testing.T) {
	db, _, bw, handler, inboundRoute, _ := setupTestWebhookServer(t, 1024*1024)
	defer db.Close()
	defer bw.Close()

	rawBinaryPayload := []byte{0x00, 0xFF, 0xFE, 0x01, 0x02, 0x03, 0x41, 0x42, 0x43}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/"+inboundRoute.Name, bytes.NewReader(rawBinaryPayload))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Custom-Webhook-Header", "custom-value")
	req.Header.Set("Connection", "keep-alive") // hop-by-hop header should be stripped

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	relayID := resp["relay_id"].(string)

	reqRepo := sqlite.NewRequestRepo(db)
	storedReq, err := reqRepo.GetRequestByID(context.Background(), relayID)
	if err != nil {
		t.Fatalf("failed to get stored request: %v", err)
	}

	if !bytes.Equal(storedReq.Body, rawBinaryPayload) {
		t.Fatalf("expected body %v, got %v", rawBinaryPayload, storedReq.Body)
	}
	if storedReq.Headers["X-Custom-Webhook-Header"] != "custom-value" {
		t.Errorf("expected custom header preserved, got %s", storedReq.Headers["X-Custom-Webhook-Header"])
	}
	if _, exists := storedReq.Headers["Connection"]; exists {
		t.Errorf("hop-by-hop Connection header should have been stripped")
	}
}
