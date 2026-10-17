package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/simple-relay/simple-relay/scripts/simulator/web"
	"github.com/simple-relay/simple-relay/ui"
)

func main() {
	port := flag.Int("port", 9099, "Simulator server port")
	relayURL := flag.String("relay-url", "http://localhost:8080", "Simple Relay base URL")
	flag.Parse()

	store := NewEventStore(100)
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	mux := SetupMux(store, hub, httpClient, *relayURL)

	addr := fmt.Sprintf("0.0.0.0:%d", *port)
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[simulator] server listening at http://%s", addr)
		log.Printf("[simulator] destination url: http://localhost:%d/destination", *port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[simulator] listen error: %v", err)
		}
	}()

	<-stopChan
	log.Println("[simulator] shutting down gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
	log.Println("[simulator] stopped")
}

// SetupMux builds the HTTP routing ServeMux for the simulator server.
func SetupMux(store *EventStore, hub *Hub, httpClient *http.Client, relayURL string) *http.ServeMux {
	mux := http.NewServeMux()

	// 0. Embedded UI static assets (vendored Tailwind CSS and Alpine.js for offline air-gapped delivery)
	mux.Handle("GET /ui/static/", http.StripPrefix("/ui/", http.FileServer(http.FS(ui.FS))))

	// 1. Embedded Web UI (exact match only via /{$} to prevent ServeMux subtree conflict)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		data, err := web.WebFS.ReadFile("index.html")
		if err != nil {
			http.Error(w, "failed to read embedded ui", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})

	// 2. WebSocket live stream
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		hub.ServeWS(w, r)
	})

	// 3. Mock Destination endpoint (receives forwarded requests from Simple Relay)
	mux.HandleFunc("/destination", func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 10*1024*1024))

		// Extract Query Parameters
		queryParams := make(map[string]string)
		for k, vv := range r.URL.Query() {
			queryParams[k] = strings.Join(vv, ", ")
		}

		// Extract Headers
		headers := make(map[string]string)
		for k, vv := range r.Header {
			headers[k] = strings.Join(vv, ", ")
		}

		mockCfg := store.GetMockConfig()
		if mockCfg.DelayMS > 0 {
			time.Sleep(time.Duration(mockCfg.DelayMS) * time.Millisecond)
		}

		statusCode := mockCfg.StatusCode
		if statusCode < 100 || statusCode > 599 {
			statusCode = http.StatusOK
		}

		evt := &Event{
			ID:                     fmt.Sprintf("evt_%d", time.Now().UnixNano()),
			ReceivedAt:             time.Now().UTC(),
			Method:                 r.Method,
			Path:                   r.URL.Path,
			QueryParams:            queryParams,
			Headers:                headers,
			Body:                   string(bodyBytes),
			ResponseStatusReturned: statusCode,
			ResponseDelayMS:        mockCfg.DelayMS,
		}

		store.Add(evt)

		// Broadcast event over WebSocket
		if evtJSON, err := json.Marshal(evt); err == nil {
			hub.Broadcast(evtJSON)
		}

		// Write custom mock response headers
		w.Header().Set("Content-Type", "application/json")
		if statusCode == http.StatusTooManyRequests {
			if _, exists := mockCfg.Headers["Retry-After"]; !exists {
				w.Header().Set("Retry-After", "3")
			}
		}
		for k, v := range mockCfg.Headers {
			w.Header().Set(k, v)
		}

		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"received":    true,
			"mock_status": statusCode,
			"id":          evt.ID,
		})
	})

	// 4. Dispatch proxy endpoint
	mux.HandleFunc("POST /api/send", func(w http.ResponseWriter, r *http.Request) {
		var req SendRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request json", http.StatusBadRequest)
			return
		}

		resp, err := Dispatch(httpClient, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	// 5. Mock config endpoints
	mux.HandleFunc("GET /api/mock-config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(store.GetMockConfig())
	})

	mux.HandleFunc("POST /api/mock-config", func(w http.ResponseWriter, r *http.Request) {
		var cfg MockConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		store.SetMockConfig(cfg)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(store.GetMockConfig())
	})

	// 6. Events buffer endpoints
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(store.GetAll())
	})

	mux.HandleFunc("DELETE /api/events", func(w http.ResponseWriter, r *http.Request) {
		store.Clear()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "cleared"})
	})

	// 7. Templates endpoint
	mux.HandleFunc("GET /api/templates", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(GetDefaultTemplates(relayURL))
	})

	// 8. Healthz
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	return mux
}
