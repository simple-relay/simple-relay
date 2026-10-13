//go:build !windows

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/config"
	"github.com/simple-relay/simple-relay/internal/upgrade"
)

func TestRun_InheritedListenerAndReadySignal(t *testing.T) {
	origLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer origLn.Close()

	tcpLn, ok := origLn.(*net.TCPListener)
	if !ok {
		t.Fatalf("expected *net.TCPListener")
	}
	f, err := tcpLn.File()
	if err != nil {
		t.Fatalf("failed to extract listener file descriptor: %v", err)
	}
	defer f.Close()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()

	t.Setenv(upgrade.EnvInheritFD, strconv.Itoa(int(f.Fd())))
	t.Setenv(upgrade.EnvReadyFD, strconv.Itoa(int(w.Fd())))

	cfg := config.DefaultConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = origLn.Addr().(*net.TCPAddr).Port
	cfg.DBPath = filepath.Join(t.TempDir(), "test_inherited.db")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, cfg, logger)
	}()

	// Read readiness signal from pipe
	buf := make([]byte, 16)
	_ = r.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := r.Read(buf)
	if err != nil {
		cancel()
		t.Fatalf("failed to read from readiness pipe: %v", err)
	}
	if string(buf[:n]) != "1\n" {
		cancel()
		t.Fatalf("expected readiness signal '1\\n', got %q", string(buf[:n]))
	}

	// Verify server responds on the inherited port
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", cfg.Port))
	if err != nil {
		cancel()
		t.Fatalf("failed to query /healthz on inherited port: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("expected 200 OK from /healthz, got %d", resp.StatusCode)
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run returned error on shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server failed to shut down within timeout")
	}
}
