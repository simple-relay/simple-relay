package config

import (
	"log/slog"
	"os"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Port != 8080 {
		t.Fatalf("expected default port 8080, got %d", cfg.Port)
	}
	if cfg.Host != "0.0.0.0" {
		t.Fatalf("expected default host 0.0.0.0, got %s", cfg.Host)
	}
	if cfg.Address() != "0.0.0.0:8080" {
		t.Fatalf("expected address 0.0.0.0:8080, got %s", cfg.Address())
	}
	if cfg.SlogLevel() != slog.LevelInfo {
		t.Fatalf("expected SlogLevel Info, got %v", cfg.SlogLevel())
	}
	if cfg.CleanerInterval.Hours() != 1 {
		t.Fatalf("expected default CleanerInterval 1h, got %v", cfg.CleanerInterval)
	}
}

func TestLoadFromEnv(t *testing.T) {
	os.Setenv("RELAY_PORT", "9090")
	os.Setenv("RELAY_HOST", "127.0.0.1")
	os.Setenv("RELAY_LOG_LEVEL", "debug")
	os.Setenv("RELAY_DB_PATH", "/tmp/test.db")
	os.Setenv("RELAY_CLEANER_INTERVAL", "30m")
	defer func() {
		os.Unsetenv("RELAY_PORT")
		os.Unsetenv("RELAY_HOST")
		os.Unsetenv("RELAY_LOG_LEVEL")
		os.Unsetenv("RELAY_DB_PATH")
		os.Unsetenv("RELAY_CLEANER_INTERVAL")
	}()

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("unexpected error loading config: %v", err)
	}

	if cfg.Port != 9090 {
		t.Fatalf("expected port 9090, got %d", cfg.Port)
	}
	if cfg.Host != "127.0.0.1" {
		t.Fatalf("expected host 127.0.0.1, got %s", cfg.Host)
	}
	if cfg.DBPath != "/tmp/test.db" {
		t.Fatalf("expected DBPath /tmp/test.db, got %s", cfg.DBPath)
	}
	if cfg.SlogLevel() != slog.LevelDebug {
		t.Fatalf("expected SlogLevel Debug, got %v", cfg.SlogLevel())
	}
	if cfg.CleanerInterval.Minutes() != 30 {
		t.Fatalf("expected CleanerInterval 30m, got %v", cfg.CleanerInterval)
	}
}

func TestConfig_Validate(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Port = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for negative port")
	}

	cfg = DefaultConfig()
	cfg.DBPath = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty db path")
	}

	cfg = DefaultConfig()
	cfg.WorkerPoolSize = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero worker pool size")
	}

	cfg = DefaultConfig()
	cfg.CleanerInterval = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero cleaner interval")
	}
}
