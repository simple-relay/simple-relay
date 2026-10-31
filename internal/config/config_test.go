package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
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

	cfg = DefaultConfig()
	cfg.MaxBodySizeMB = 101
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for MaxBodySizeMB > 100")
	}

	cfg = DefaultConfig()
	cfg.MaxBodySizeMB = 100
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error for MaxBodySizeMB = 100: %v", err)
	}

	os.Setenv("RELAY_MAX_BODY_SIZE_MB", "not-a-number")
	defer os.Unsetenv("RELAY_MAX_BODY_SIZE_MB")
	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected error when RELAY_MAX_BODY_SIZE_MB is invalid")
	}
}

func TestConfig_SlogLevel(t *testing.T) {
	tests := []struct {
		level string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"info", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
		{"", slog.LevelInfo},
	}
	for _, tt := range tests {
		cfg := &Config{LogLevel: tt.level}
		if got := cfg.SlogLevel(); got != tt.want {
			t.Errorf("LogLevel(%q) = %v, want %v", tt.level, got, tt.want)
		}
	}
}

func TestLoad_Flags(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	tmpDB := filepath.Join(t.TempDir(), "flag_test.db")
	os.Args = []string{
		"relay",
		"-host=127.0.0.1",
		"-port=8888",
		"-db=" + tmpDB,
		"-log-level=warn",
		"-workers=15",
		"-dispatcher-buffer=30",
		"-dispatcher-poll-ms=250",
		"-cleaner-interval=30m",
		"-drain-timeout=15s",
		"-trust-proxy=true",
		"-sqlite-sync=FULL",
		"-max-body-size-mb=25",
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected Load error: %v", err)
	}

	if cfg.Host != "127.0.0.1" || cfg.Port != 8888 {
		t.Errorf("unexpected host/port: %s:%d", cfg.Host, cfg.Port)
	}
	if cfg.DBPath != tmpDB {
		t.Errorf("unexpected DBPath: %s", cfg.DBPath)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("unexpected LogLevel: %s", cfg.LogLevel)
	}
	if cfg.WorkerConcurrency != 15 {
		t.Errorf("unexpected WorkerConcurrency: %d", cfg.WorkerConcurrency)
	}
	if cfg.DispatcherBuffer != 30 {
		t.Errorf("unexpected DispatcherBuffer: %d", cfg.DispatcherBuffer)
	}
	if cfg.DispatcherPollMS != 250 {
		t.Errorf("unexpected DispatcherPollMS: %d", cfg.DispatcherPollMS)
	}
	if cfg.CleanerInterval != 30*time.Minute {
		t.Errorf("unexpected CleanerInterval: %v", cfg.CleanerInterval)
	}
	if cfg.DrainTimeout != 15*time.Second {
		t.Errorf("unexpected DrainTimeout: %v", cfg.DrainTimeout)
	}
	if !cfg.TrustProxy {
		t.Errorf("expected TrustProxy true")
	}
	if cfg.SQLiteSync != "FULL" {
		t.Errorf("unexpected SQLiteSync: %s", cfg.SQLiteSync)
	}
	if cfg.MaxBodySizeMB != 25 {
		t.Errorf("unexpected MaxBodySizeMB: %d", cfg.MaxBodySizeMB)
	}
}

func TestLoad_FlagParseError(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{"relay", "-invalid-unknown-flag=true"}
	_, err := Load()
	if err == nil {
		t.Fatal("expected error on invalid CLI flag, got nil")
	}
}
