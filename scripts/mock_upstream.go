package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

var (
	port      = flag.Int("port", 9191, "mock server port")
	delayMS   = flag.Int("delay-ms", 0, "artificial delay in milliseconds")
	received  atomic.Int64
	firstTime atomic.Int64
	lastTime  atomic.Int64
)

func setupHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UnixNano()
		received.Add(1)
		firstTime.CompareAndSwap(0, now)
		lastTime.Store(now)

		if *delayMS > 0 {
			time.Sleep(time.Duration(*delayMS) * time.Millisecond)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"received":true}`))
	})

	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		count := received.Load()
		first := firstTime.Load()
		last := lastTime.Load()

		var durationSec float64
		var rps float64
		if count > 1 && last > first {
			durationSec = float64(last-first) / 1e9
			if durationSec > 0 {
				rps = float64(count) / durationSec
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"received_count": count,
			"duration_sec":   durationSec,
			"effective_rps":  rps,
		})
	})

	mux.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		received.Store(0)
		firstTime.Store(0)
		lastTime.Store(0)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"reset":true}`))
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`ok`))
	})

	return mux
}

func main() {
	flag.Parse()

	handler := setupHandler()
	addr := fmt.Sprintf("0.0.0.0:%d", *port)
	log.Printf("[mock-upstream] listening on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}
