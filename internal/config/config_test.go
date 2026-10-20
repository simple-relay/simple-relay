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
	if cfg.WorkerConcurrency != 20 {
		t.Fatalf("expected default WorkerConcurrency 20, got %d", cfg.WorkerConcurrency)
	}
	if cfg.DispatcherPollMS != 500 {
		t.Fatalf("expected default DispatcherPollMS 500, got %d", cfg.DispatcherPollMS)
	}
	if cfg.CleanerInterval.Hours() != 1 {
		t.Fatalf("expected default CleanerInterval 1h, got %v", cfg.CleanerInterval)
	}
	if cfg.DrainTimeout.Seconds() != 30 {
		t.Fatalf("expected default DrainTimeout 30s, got %v", cfg.DrainTimeout)
	}
	if cfg.TrustProxy {
		t.Fatalf("expected default TrustProxy false, got true")
	}
	if cfg.SQLiteSync != "NORMAL" {
		t.Fatalf("expected default SQLiteSync NORMAL, got %s", cfg.SQLiteSync)
	}
	if cfg.MaxBodySizeMB != 10 {
		t.Fatalf("expected default MaxBodySizeMB 10, got %d", cfg.MaxBodySizeMB)
	}
}

func TestLoadFromEnv(t *testing.T) {
	os.Setenv("RELAY_PORT", "9090")
	os.Setenv("RELAY_HOST", "127.0.0.1")
	os.Setenv("RELAY_LOG_LEVEL", "debug")
	os.Setenv("RELAY_DB_PATH", "/tmp/test.db")
	os.Setenv("RELAY_WORKER_CONCURRENCY", "100")
	os.Setenv("RELAY_DISPATCHER_BUFFER", "300")
	os.Setenv("RELAY_DISPATCHER_POLL_MS", "250")
	os.Setenv("RELAY_CLEANER_INTERVAL", "30m")
	os.Setenv("RELAY_DRAIN_TIMEOUT", "45s")
	os.Setenv("RELAY_TRUST_PROXY", "true")
	os.Setenv("RELAY_SQLITE_SYNC", "full")
	os.Setenv("RELAY_MAX_BODY_SIZE_MB", "25")
	defer func() {
		os.Unsetenv("RELAY_PORT")
		os.Unsetenv("RELAY_HOST")
		os.Unsetenv("RELAY_LOG_LEVEL")
		os.Unsetenv("RELAY_DB_PATH")
		os.Unsetenv("RELAY_WORKER_CONCURRENCY")
		os.Unsetenv("RELAY_DISPATCHER_BUFFER")
		os.Unsetenv("RELAY_DISPATCHER_POLL_MS")
		os.Unsetenv("RELAY_CLEANER_INTERVAL")
		os.Unsetenv("RELAY_DRAIN_TIMEOUT")
		os.Unsetenv("RELAY_TRUST_PROXY")
		os.Unsetenv("RELAY_SQLITE_SYNC")
		os.Unsetenv("RELAY_MAX_BODY_SIZE_MB")
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
	if cfg.WorkerConcurrency != 100 {
		t.Fatalf("expected WorkerConcurrency 100, got %d", cfg.WorkerConcurrency)
	}
	if cfg.DispatcherBuffer != 300 {
		t.Fatalf("expected DispatcherBuffer 300, got %d", cfg.DispatcherBuffer)
	}
	if cfg.DispatcherPollMS != 250 {
		t.Fatalf("expected DispatcherPollMS 250, got %d", cfg.DispatcherPollMS)
	}
	if cfg.SlogLevel() != slog.LevelDebug {
		t.Fatalf("expected SlogLevel Debug, got %v", cfg.SlogLevel())
	}
	if cfg.CleanerInterval.Minutes() != 30 {
		t.Fatalf("expected CleanerInterval 30m, got %v", cfg.CleanerInterval)
	}
	if cfg.DrainTimeout.Seconds() != 45 {
		t.Fatalf("expected DrainTimeout 45s, got %v", cfg.DrainTimeout)
	}
	if !cfg.TrustProxy {
		t.Fatalf("expected TrustProxy true when RELAY_TRUST_PROXY=true")
	}
	if cfg.SQLiteSync != "FULL" {
		t.Fatalf("expected SQLiteSync FULL, got %s", cfg.SQLiteSync)
	}
	if cfg.MaxBodySizeMB != 25 {
		t.Fatalf("expected MaxBodySizeMB 25, got %d", cfg.MaxBodySizeMB)
	}
}

func TestConfig_DynamicDispatcherBuffer(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorkerConcurrency = 50
	cfg.DispatcherBuffer = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DispatcherBuffer != 100 {
		t.Fatalf("expected dynamic DispatcherBuffer 100 (2x50), got %d", cfg.DispatcherBuffer)
	}

	// Explicit buffer remains unchanged
	cfg.DispatcherBuffer = 150
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DispatcherBuffer != 150 {
		t.Fatalf("expected explicit DispatcherBuffer 150, got %d", cfg.DispatcherBuffer)
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
	cfg.WorkerConcurrency = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero worker concurrency")
	}

	cfg = DefaultConfig()
	cfg.DispatcherPollMS = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero dispatcher poll interval")
	}

	cfg = DefaultConfig()
	cfg.CleanerInterval = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero cleaner interval")
	}

	cfg = DefaultConfig()
	cfg.DrainTimeout = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero drain timeout")
	}

	cfg = DefaultConfig()
	cfg.AdminPassword = "short"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for admin password shorter than 10 characters")
	}

	cfg = DefaultConfig()
	cfg.AdminPassword = "adminsecret123"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error for valid admin password: %v", err)
	}

	cfg = DefaultConfig()
	cfg.SQLiteSync = "OFF"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid SQLiteSync OFF")
	}

	cfg = DefaultConfig()
	cfg.MaxBodySizeMB = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero MaxBodySizeMB")
	}

	os.Setenv("RELAY_MAX_BODY_SIZE_MB", "not-a-number")
	defer os.Unsetenv("RELAY_MAX_BODY_SIZE_MB")
	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected error when RELAY_MAX_BODY_SIZE_MB is invalid")
	}
}
