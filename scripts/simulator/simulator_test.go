package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/signature"
)

func TestSignatureGenerators(t *testing.T) {
	secret := "whsec_test_secret_12345"
	body := []byte(`{"id":"evt_001","type":"payment_intent.succeeded"}`)

	t.Run("Stripe Signature Verification", func(t *testing.T) {
		now := time.Now()
		sigVal := ComputeStripeSignature(secret, body, now)

		header := make(http.Header)
		header.Set("Stripe-Signature", sigVal)

		verifier, err := signature.ForProvider("stripe")
		if err != nil {
			t.Fatalf("failed to get stripe verifier: %v", err)
		}

		if err := verifier.Verify(header, secret, body); err != nil {
			t.Fatalf("generated stripe signature failed verification: %v", err)
		}
	})

	t.Run("GitHub Signature Verification", func(t *testing.T) {
		sigVal := ComputeGitHubSignature(secret, body)

		header := make(http.Header)
		header.Set("X-Hub-Signature-256", sigVal)

		verifier, err := signature.ForProvider("github")
		if err != nil {
			t.Fatalf("failed to get github verifier: %v", err)
		}

		if err := verifier.Verify(header, secret, body); err != nil {
			t.Fatalf("generated github signature failed verification: %v", err)
		}
	})
}

func TestEventStore(t *testing.T) {
	store := NewEventStore(3)

	for i := 1; i <= 5; i++ {
		store.Add(&Event{
			ID:     string(rune('A' + i - 1)),
			Method: "POST",
		})
	}

	if count := store.Count(); count != 3 {
		t.Fatalf("expected count 3, got %d", count)
	}

	events := store.GetAll()
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	// Oldest two should have been evicted; remaining should be C, D, E
	if events[0].ID != "C" || events[1].ID != "D" || events[2].ID != "E" {
		t.Fatalf("unexpected events ring order: %+v", events)
	}

	store.Clear()
	if count := store.Count(); count != 0 {
		t.Fatalf("expected count 0 after clear, got %d", count)
	}
}

func TestMockConfig(t *testing.T) {
	store := NewEventStore(10)

	cfg := store.GetMockConfig()
	if cfg.StatusCode != 200 {
		t.Fatalf("expected default status code 200, got %d", cfg.StatusCode)
	}

	store.SetMockConfig(MockConfig{
		StatusCode: 429,
		DelayMS:    50,
		Headers: map[string]string{
			"Retry-After": "5",
		},
	})

	updated := store.GetMockConfig()
	if updated.StatusCode != 429 {
		t.Fatalf("expected updated status code 429, got %d", updated.StatusCode)
	}
	if updated.DelayMS != 50 {
		t.Fatalf("expected updated delay 50, got %d", updated.DelayMS)
	}
	if updated.Headers["Retry-After"] != "5" {
		t.Fatalf("expected Retry-After header 5, got %s", updated.Headers["Retry-After"])
	}
}

func TestMockDestinationHandler(t *testing.T) {
	store := NewEventStore(10)
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)

		queryParams := make(map[string]string)
		for k, vv := range r.URL.Query() {
			queryParams[k] = vv[0]
		}

		headers := make(map[string]string)
		for k, vv := range r.Header {
			headers[k] = vv[0]
		}

		mockCfg := store.GetMockConfig()
		evt := &Event{
			ID:                     "evt_test",
			ReceivedAt:             time.Now().UTC(),
			Method:                 r.Method,
			Path:                   r.URL.Path,
			QueryParams:            queryParams,
			Headers:                headers,
			Body:                   string(bodyBytes),
			ResponseStatusReturned: mockCfg.StatusCode,
		}
		store.Add(evt)

		if mockCfg.StatusCode == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "3")
		}
		w.WriteHeader(mockCfg.StatusCode)
		_ = json.NewEncoder(w).Encode(map[string]any{"received": true})
	})

	t.Run("Standard 200 delivery", func(t *testing.T) {
		store.Clear()
		store.SetMockConfig(MockConfig{StatusCode: 200})

		req := httptest.NewRequest("POST", "/destination?token=test_token&env=staging", bytes.NewBufferString(`{"ping":"pong"}`))
		req.Header.Set("X-Relay-Request-ID", "01j9z5test001")
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}

		if store.Count() != 1 {
			t.Fatalf("expected 1 stored event, got %d", store.Count())
		}

		evt := store.GetAll()[0]
		if evt.QueryParams["token"] != "test_token" || evt.QueryParams["env"] != "staging" {
			t.Fatalf("unexpected query params: %+v", evt.QueryParams)
		}
		if evt.Headers["X-Relay-Request-Id"] != "01j9z5test001" {
			t.Fatalf("unexpected header: %+v", evt.Headers)
		}
		if evt.Body != `{"ping":"pong"}` {
			t.Fatalf("unexpected body: %s", evt.Body)
		}
	})

	t.Run("Rate limit 429 delivery", func(t *testing.T) {
		store.Clear()
		store.SetMockConfig(MockConfig{StatusCode: 429})

		req := httptest.NewRequest("POST", "/destination", bytes.NewBufferString(`{}`))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("expected status 429, got %d", rr.Code)
		}
		if rr.Header().Get("Retry-After") != "3" {
			t.Fatalf("expected Retry-After 3, got %s", rr.Header().Get("Retry-After"))
		}
	})
}

func TestDispatchProxy(t *testing.T) {
	// Mock Simple Relay server
	var receivedHeader string
	var receivedBody string

	mockRelay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("Stripe-Signature")
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"relay_id":"01j9test","status":"persisted"}`))
	}))
	defer mockRelay.Close()

	client := mockRelay.Client()

	sendReq := SendRequest{
		URL:          mockRelay.URL + "/api/v1/webhook/stripe-test",
		Method:       "POST",
		Headers:      map[string]string{"Content-Type": "application/json"},
		Body:         `{"test":"data"}`,
		SignProvider: "stripe",
		SignSecret:   "whsec_relay_secret",
	}

	resp, err := Dispatch(client, sendReq)
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !bytes.Contains([]byte(receivedHeader), []byte("v1=")) {
		t.Fatalf("expected Stripe-Signature header with v1=, got %s", receivedHeader)
	}
	if receivedBody != `{"test":"data"}` {
		t.Fatalf("expected received body %s, got %s", `{"test":"data"}`, receivedBody)
	}
}

func TestGetDefaultTemplates(t *testing.T) {
	templates := GetDefaultTemplates("http://127.0.0.1:8080")
	if len(templates) < 5 {
		t.Fatalf("expected at least 5 default templates, got %d", len(templates))
	}

	foundStripe := false
	foundGitHub := false
	foundQuery := false

	for _, tmpl := range templates {
		if tmpl.ID == "stripe_webhook" {
			foundStripe = true
		}
		if tmpl.ID == "github_webhook" {
			foundGitHub = true
		}
		if tmpl.ID == "query_param_auth" {
			foundQuery = true
		}
	}

	if !foundStripe || !foundGitHub || !foundQuery {
		t.Fatalf("missing expected template presets: stripe=%v github=%v query=%v", foundStripe, foundGitHub, foundQuery)
	}
}
