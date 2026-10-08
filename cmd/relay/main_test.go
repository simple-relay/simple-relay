package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/config"
)

func TestRun_GracefulShutdown(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = 18081 // Choose non-conflicting test port
	cfg.DBPath = filepath.Join(t.TempDir(), "test_main.db")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, cfg, logger)
	}()

	// Wait for server to be responsive
	var ready bool
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		resp, err := http.Get("http://127.0.0.1:18081/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
	}

	if !ready {
		cancel()
		t.Fatal("server failed to start and respond on /healthz within timeout")
	}

	// Trigger shutdown
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run returned error on graceful shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server failed to shut down within timeout")
	}
}
