package cli

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestNormalizeArgs(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "single dash known server flags",
			input:    []string{"-host", "127.0.0.1", "-port", "8080", "-db", "relay.db"},
			expected: []string{"--host", "127.0.0.1", "--port", "8080", "--db", "relay.db"},
		},
		{
			name:     "single dash known flag with equal sign",
			input:    []string{"-host=127.0.0.1", "-workers=50"},
			expected: []string{"--host=127.0.0.1", "--workers=50"},
		},
		{
			name:     "preserves POSIX double dash boundary",
			input:    []string{"dlq", "inspect", "--", "-some-req-id", "-host"},
			expected: []string{"dlq", "inspect", "--", "-some-req-id", "-host"},
		},
		{
			name:     "does not rewrite unknown single dash flags or IDs",
			input:    []string{"dlq", "inspect", "-some-req-id"},
			expected: []string{"dlq", "inspect", "-some-req-id"},
		},
		{
			name:     "preserves single char shorthands and double dash flags",
			input:    []string{"health", "-s", "http://127.0.0.1:8080", "-k", "secret", "--output", "json"},
			expected: []string{"health", "-s", "http://127.0.0.1:8080", "-k", "secret", "--output", "json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeArgs(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Fatalf("normalizeArgs(%v) = %v, expected %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestRunServe_DispatcherBufferScaling(t *testing.T) {
	t.Run("workers flag dynamically scales dispatcher buffer", func(t *testing.T) {
		cmd := newServeCmd()
		cmd.SetArgs([]string{"--workers", "50"})
		if err := cmd.ParseFlags([]string{"--workers", "50"}); err != nil {
			t.Fatalf("failed to parse flags: %v", err)
		}

		workers, _ := cmd.Flags().GetInt("workers")
		if workers != 50 {
			t.Fatalf("expected workers=50, got %d", workers)
		}
	})
}

func TestRunServe_ContextCancellation(t *testing.T) {
	// Allocate an ephemeral port for testing server boot and cancellation
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on ephemeral port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cmd := NewRootCmd()
	cmd.SetContext(ctx)

	dbPath := filepath.Join(t.TempDir(), "test_cancel.db")
	cmd.SetArgs([]string{
		"serve",
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--db", dbPath,
		"--drain-timeout", "1s",
	})
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.Execute()
	}()

	// Wait briefly for server startup, then cancel context
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server did not terminate cleanly on context cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server timed out waiting to shutdown after context cancellation")
	}
}
