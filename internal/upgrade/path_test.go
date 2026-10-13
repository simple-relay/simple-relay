package upgrade

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveExecutable_EnvOverride(t *testing.T) {
	expected := "/usr/local/bin/custom-relay"
	t.Setenv(EnvUpgradeBinary, expected)

	path, err := ResolveExecutable()
	if err != nil {
		t.Fatalf("unexpected error resolving executable: %v", err)
	}

	if path != expected {
		t.Fatalf("expected executable %q, got %q", expected, path)
	}
}

func TestResolveExecutable_TrimDeletedSuffix(t *testing.T) {
	t.Setenv(EnvUpgradeBinary, "")

	origArgs := os.Args
	t.Cleanup(func() {
		os.Args = origArgs
	})
	os.Args = []string{"nonexistent-binary-fallback"}

	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "simple-relay")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("failed to create temp binary: %v", err)
	}

	origExec := executableFunc
	t.Cleanup(func() {
		executableFunc = origExec
	})
	executableFunc = func() (string, error) {
		return binPath + " (deleted)", nil
	}

	path, err := ResolveExecutable()
	if err != nil {
		t.Fatalf("unexpected error resolving executable: %v", err)
	}

	if path != binPath {
		t.Fatalf("expected trimmed executable %q, got %q", binPath, path)
	}
}

func TestResolveExecutable_StatFailure(t *testing.T) {
	t.Setenv(EnvUpgradeBinary, "")

	origArgs := os.Args
	t.Cleanup(func() {
		os.Args = origArgs
	})
	os.Args = []string{"nonexistent-binary-fallback"}

	origExec := executableFunc
	t.Cleanup(func() {
		executableFunc = origExec
	})
	executableFunc = func() (string, error) {
		return "/nonexistent/binary/path (deleted)", nil
	}

	_, err := ResolveExecutable()
	if err == nil {
		t.Fatalf("expected error when resolved binary does not exist, got nil")
	}
}

func TestResolveExecutable_Default(t *testing.T) {
	t.Setenv(EnvUpgradeBinary, "")

	path, err := ResolveExecutable()
	if err != nil {
		t.Fatalf("unexpected error resolving default executable: %v", err)
	}

	if path == "" {
		t.Fatalf("expected non-empty executable path")
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("resolved executable does not exist: %v", err)
	}
}
