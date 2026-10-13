package upgrade_test

import (
	"os"
	"testing"

	"github.com/simple-relay/simple-relay/internal/upgrade"
)

func TestResolveExecutable_EnvOverride(t *testing.T) {
	expected := "/usr/local/bin/custom-relay"
	t.Setenv(upgrade.EnvUpgradeBinary, expected)

	path, err := upgrade.ResolveExecutable()
	if err != nil {
		t.Fatalf("unexpected error resolving executable: %v", err)
	}

	if path != expected {
		t.Fatalf("expected executable %q, got %q", expected, path)
	}
}

func TestResolveExecutable_TrimDeletedSuffix(t *testing.T) {
	t.Setenv(upgrade.EnvUpgradeBinary, "/bin/simple-relay (deleted)")

	path, err := upgrade.ResolveExecutable()
	if err != nil {
		t.Fatalf("unexpected error resolving executable: %v", err)
	}

	expected := "/bin/simple-relay"
	if path != expected {
		t.Fatalf("expected trimmed executable %q, got %q", expected, path)
	}
}

func TestResolveExecutable_Default(t *testing.T) {
	t.Setenv(upgrade.EnvUpgradeBinary, "")

	path, err := upgrade.ResolveExecutable()
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
