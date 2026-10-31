package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestStaticAssetsAndWebUI(t *testing.T) {
	store := NewEventStore(10)
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	mux := SetupMux(store, hub, http.DefaultClient, "http://localhost:8080")

	// 1. Root UI GET /
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for GET /, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html content type, got %s", ct)
	}

	// 2. Static CSS GET /ui/static/css/tailwind.min.css
	req = httptest.NewRequest("GET", "/ui/static/css/tailwind.min.css", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for GET /ui/static/css/tailwind.min.css, got %d", rr.Code)
	}

	// 3. Static JS GET /ui/static/js/alpine.min.js
	req = httptest.NewRequest("GET", "/ui/static/js/alpine.min.js", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for GET /ui/static/js/alpine.min.js, got %d", rr.Code)
	}

	// 4. Static SVG GET /ui/static/logo.svg
	req = httptest.NewRequest("GET", "/ui/static/logo.svg", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for GET /ui/static/logo.svg, got %d", rr.Code)
	}
}

func TestNewEventStore_DefaultMaxEvents(t *testing.T) {
	s1 := NewEventStore(0)
	if s1.maxEvents != 100 {
		t.Fatalf("expected 100, got %d", s1.maxEvents)
	}
	s2 := NewEventStore(-10)
	if s2.maxEvents != 100 {
		t.Fatalf("expected 100, got %d", s2.maxEvents)
	}
}

func TestSetMockConfig_Normalization(t *testing.T) {
	s := NewEventStore(10)
	s.SetMockConfig(MockConfig{
		StatusCode: 99,
		DelayMS:    -50,
	})
	cfg := s.GetMockConfig()
	if cfg.StatusCode != 200 {
		t.Fatalf("expected status 200, got %d", cfg.StatusCode)
	}
	if cfg.DelayMS != 0 {
		t.Fatalf("expected delay 0, got %d", cfg.DelayMS)
	}

	s.SetMockConfig(MockConfig{
		StatusCode: 600,
		DelayMS:    0,
	})
	cfg = s.GetMockConfig()
	if cfg.StatusCode != 200 {
		t.Fatalf("expected status 200, got %d", cfg.StatusCode)
	}
}

func TestSetupMux_Endpoints(t *testing.T) {
	store := NewEventStore(10)
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	client := &http.Client{Timeout: 2 * time.Second}
	mux := SetupMux(store, hub, client, "http://localhost:8080")

	// 1. Healthz
	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "ok" {
		t.Fatalf("unexpected healthz response: %d, body: %s", rr.Code, rr.Body.String())
	}

	// 2. Templates
	req = httptest.NewRequest("GET", "/api/templates", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected templates code: %d", rr.Code)
	}
	var tmpls []TemplatePreset
	if err := json.Unmarshal(rr.Body.Bytes(), &tmpls); err != nil || len(tmpls) == 0 {
		t.Fatalf("failed to decode templates: %v", err)
	}

	// 3. Mock config GET & POST
	req = httptest.NewRequest("GET", "/api/mock-config", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected mock config get code: %d", rr.Code)
	}

	// POST valid mock config
	cfgPayload := `{"status_code":429,"delay_ms":10,"headers":{"X-Custom":"test"}}`
	req = httptest.NewRequest("POST", "/api/mock-config", strings.NewReader(cfgPayload))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected mock config post code: %d", rr.Code)
	}

	// POST invalid mock config JSON
	req = httptest.NewRequest("POST", "/api/mock-config", strings.NewReader(`{invalid json`))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid mock config json, got %d", rr.Code)
	}

	// 4. Events GET & DELETE
	store.Add(&Event{ID: "evt_1", Method: "POST"})
	req = httptest.NewRequest("GET", "/api/events", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected events get code: %d", rr.Code)
	}

	req = httptest.NewRequest("DELETE", "/api/events", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected events delete code: %d", rr.Code)
	}
	if store.Count() != 0 {
		t.Fatalf("expected 0 events after delete, got %d", store.Count())
	}

	// 5. POST /api/send invalid json
	req = httptest.NewRequest("POST", "/api/send", strings.NewReader(`bad-json`))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad send json, got %d", rr.Code)
	}

	// 6. POST /api/send with client connection failure (result.Error populated, 200 response)
	req = httptest.NewRequest("POST", "/api/send", strings.NewReader(`{"url":"http://127.0.0.1:99999/invalid","method":"POST"}`))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for connection failure, got %d", rr.Code)
	}
	var sendResp SendResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &sendResp); err != nil || sendResp.Error == "" {
		t.Fatalf("expected populated error in send response, got: %+v", sendResp)
	}

	// 7. POST /api/send with invalid method triggering NewRequest error (500 response)
	req = httptest.NewRequest("POST", "/api/send", strings.NewReader(`{"url":"http://localhost","method":"INVALID METHOD"}`))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for invalid method error, got %d", rr.Code)
	}
}

func TestSetupMux_Destination_Variants(t *testing.T) {
	store := NewEventStore(10)
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	mux := SetupMux(store, hub, http.DefaultClient, "http://localhost:8080")

	// 1. Destination with 429 and default Retry-After
	store.SetMockConfig(MockConfig{
		StatusCode: 429,
		DelayMS:    5,
		Headers:    map[string]string{"X-Test": "val"},
	})

	req := httptest.NewRequest("POST", "/destination?foo=bar&baz=qux", strings.NewReader(`{"hello":"world"}`))
	req.Header.Set("X-Custom-Header", "custom-value")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}
	if rr.Header().Get("Retry-After") != "3" {
		t.Fatalf("expected Retry-After 3, got %s", rr.Header().Get("Retry-After"))
	}
	if rr.Header().Get("X-Test") != "val" {
		t.Fatalf("expected X-Test val, got %s", rr.Header().Get("X-Test"))
	}
	if store.Count() != 1 {
		t.Fatalf("expected 1 event, got %d", store.Count())
	}
	evt := store.GetAll()[0]
	if evt.QueryParams["foo"] != "bar" || evt.Headers["X-Custom-Header"] != "custom-value" {
		t.Fatalf("unexpected stored event: %+v", evt)
	}
}

func TestHub_WebSocketAndFrames(t *testing.T) {
	// 1. Test encodeTextFrame lengths
	// < 126
	f1, err := encodeTextFrame([]byte("hello"))
	if err != nil || len(f1) != 7 || f1[0] != 0x81 || f1[1] != 5 {
		t.Fatalf("unexpected short frame: %v, err: %v", f1, err)
	}

	// 126 <= len <= 65535
	medPayload := bytes.Repeat([]byte("A"), 250)
	f2, err := encodeTextFrame(medPayload)
	if err != nil || len(f2) != 254 || f2[1] != 126 {
		t.Fatalf("unexpected medium frame: %v, err: %v", len(f2), err)
	}

	// > 65535
	largePayload := bytes.Repeat([]byte("B"), 70000)
	f3, err := encodeTextFrame(largePayload)
	if err != nil || len(f3) != 70010 || f3[1] != 127 {
		t.Fatalf("unexpected large frame: %v, err: %v", len(f3), err)
	}

	// 2. Test ServeWS bad requests
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	// Missing upgrade header
	req := httptest.NewRequest("GET", "/ws", nil)
	rr := httptest.NewRecorder()
	hub.ServeWS(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-websocket upgrade, got %d", rr.Code)
	}

	// Missing Sec-WebSocket-Key
	req = httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	rr = httptest.NewRecorder()
	hub.ServeWS(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing key, got %d", rr.Code)
	}

	// 3. Test real WebSocket connection through httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		hub.ServeWS(w, r)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Connect via TCP
	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	reqStr := "GET /ws HTTP/1.1\r\n" +
		"Host: " + ts.Listener.Addr().String() + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"

	if _, err := conn.Write([]byte(reqStr)); err != nil {
		t.Fatalf("failed to write handshake: %v", err)
	}

	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(statusLine, "101") {
		t.Fatalf("expected 101 Switching Protocols, got: %s, err: %v", statusLine, err)
	}

	// Drain handshake headers until empty line
	for {
		line, err := reader.ReadString('\n')
		if err != nil || line == "\r\n" {
			break
		}
	}

	// Wait for registration
	time.Sleep(30 * time.Millisecond)
	if count := hub.ClientCount(); count != 1 {
		t.Fatalf("expected 1 connected client, got %d", count)
	}

	// Test broadcast
	hub.Broadcast([]byte(`{"test":"message"}`))

	// Read frame from conn
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		t.Fatalf("failed to read frame header: %v", err)
	}
	payloadLen := int(header[1] & 0x7F)
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatalf("failed to read payload: %v", err)
	}
	if string(payload) != `{"test":"message"}` {
		t.Fatalf("unexpected message: %s", string(payload))
	}

	// Send close frame from client
	closeFrame := []byte{0x88, 0x00}
	_, _ = conn.Write(closeFrame)

	time.Sleep(30 * time.Millisecond)
	if count := hub.ClientCount(); count != 0 {
		t.Fatalf("expected 0 clients after close, got %d", count)
	}
}
