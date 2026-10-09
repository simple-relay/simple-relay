package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

func TestWebhookIngest_SignatureVerification_Stripe(t *testing.T) {
	db, registry, bw, handler, _, _ := setupTestWebhookServer(t, 1024*1024)
	defer db.Close()
	defer bw.Close()

	ctx := context.Background()
	secret := "whsec_stripe_test_key_123"
	route := &model.Route{
		Name:                 "stripe-secured",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/stripe-secured",
		DestinationURL:       "https://internal.service/stripe",
		SignatureProvider:    model.SignatureProviderStripe,
		SignatureSecret:      secret,
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := registry.Create(ctx, route); err != nil {
		t.Fatalf("failed to create route: %v", err)
	}

	payload := `{"id":"evt_stripe_1","type":"invoice.paid"}`
	timestamp := "1690000000"

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write([]byte(payload))
	validSig := hex.EncodeToString(mac.Sum(nil))

	t.Run("Valid Stripe signature passes and persists", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/"+route.Name, bytes.NewBufferString(payload))
		req.Header.Set("Stripe-Signature", fmt.Sprintf("t=%s,v1=%s", timestamp, validSig))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("Invalid Stripe signature rejected with 401 and NOT persisted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/"+route.Name, bytes.NewBufferString(payload))
		req.Header.Set("Stripe-Signature", fmt.Sprintf("t=%s,v1=deadbeefdeadbeef", timestamp))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "invalid webhook signature") {
			t.Fatalf("expected invalid webhook signature message, got: %s", rec.Body.String())
		}
	})

	t.Run("Missing Stripe signature header rejected with 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/"+route.Name, bytes.NewBufferString(payload))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestWebhookIngest_SignatureVerification_GitHub(t *testing.T) {
	db, registry, bw, handler, _, _ := setupTestWebhookServer(t, 1024*1024)
	defer db.Close()
	defer bw.Close()

	ctx := context.Background()
	secret := "gh_webhook_secret_xyz"
	route := &model.Route{
		Name:                 "github-secured",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/github-secured",
		DestinationURL:       "https://internal.service/github",
		SignatureProvider:    model.SignatureProviderGitHub,
		SignatureSecret:      secret,
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := registry.Create(ctx, route); err != nil {
		t.Fatalf("failed to create route: %v", err)
	}

	payload := `{"ref":"refs/heads/main","after":"abc123"}`

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	validSig := hex.EncodeToString(mac.Sum(nil))

	t.Run("Valid GitHub signature passes and persists", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/"+route.Name, bytes.NewBufferString(payload))
		req.Header.Set("X-Hub-Signature-256", "sha256="+validSig)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("Invalid GitHub signature rejected with 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/"+route.Name, bytes.NewBufferString(payload))
		req.Header.Set("X-Hub-Signature-256", "sha256=0000000000000000000000000000000000000000000000000000000000000000")

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestWebhookIngest_SignatureVerification_GenericHMAC(t *testing.T) {
	db, registry, bw, handler, _, _ := setupTestWebhookServer(t, 1024*1024)
	defer db.Close()
	defer bw.Close()

	ctx := context.Background()
	secret := "generic_secret_key"
	route := &model.Route{
		Name:                 "generic-secured",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/generic-secured",
		DestinationURL:       "https://internal.service/generic",
		SignatureProvider:    model.SignatureProviderHMACSHA256,
		SignatureSecret:      secret,
		MaxAttempts:          3,
		TimeoutMS:            5000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
	}
	if err := registry.Create(ctx, route); err != nil {
		t.Fatalf("failed to create route: %v", err)
	}

	payload := `{"event":"order.completed"}`

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	validSig := hex.EncodeToString(mac.Sum(nil))

	t.Run("Valid Generic HMAC with X-Signature passes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/"+route.Name, bytes.NewBufferString(payload))
		req.Header.Set("X-Signature", validSig)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("Invalid Generic HMAC rejected with 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/"+route.Name, bytes.NewBufferString(payload))
		req.Header.Set("X-Signature", "invalid")

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestWebhookIngest_SignatureVerification_EmptySecretReturns500(t *testing.T) {
	db, registry, bw, handler, _, _ := setupTestWebhookServer(t, 1024*1024)
	defer db.Close()
	defer bw.Close()

	// Bypass Route.Validate() by inserting raw SQL directly to test runtime handler defense
	ctx := context.Background()
	routeID := model.NewULID()
	routeName := "misconfigured-route"
	_, err := db.Writer().ExecContext(ctx, `
		INSERT INTO routes (id, name, type, mode, path, destination_url, signature_provider, signature_secret, max_attempts, timeout_ms, retry_backoff_strategy)
		VALUES (?, ?, 'inbound', 'async', '/api/v1/webhook/misconfigured-route', 'https://internal.service/misconfigured', 'stripe', '', 3, 5000, 'exp')
	`, routeID, routeName)
	if err != nil {
		t.Fatalf("failed to insert corrupt route: %v", err)
	}
	if err := registry.Reload(ctx); err != nil {
		t.Fatalf("failed to reload registry: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/"+routeName, bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 Internal Server Error, got %d: %s", rec.Code, rec.Body.String())
	}
}
