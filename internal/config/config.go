package config

import (
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configurations for the relay service.
type Config struct {
	Host                 string
	Port                 int
	DBPath               string
	LogLevel             string
	WorkerPoolSize       int
	RetentionSuccessDays int
	RetentionFailedDays  int
	CleanerInterval      time.Duration
	AdminPassword        string
	MaxBatchSize         int
	BatchTimeoutMS       int
}

// DefaultConfig provides sensible production defaults.
func DefaultConfig() *Config {
	return &Config{
		Host:                 "0.0.0.0",
		Port:                 8080,
		DBPath:               "relay.db",
		LogLevel:             "info",
		WorkerPoolSize:       20,
		RetentionSuccessDays: 7,
		RetentionFailedDays:  30,
		CleanerInterval:      1 * time.Hour,
		AdminPassword:        "",
		MaxBatchSize:         50,
		BatchTimeoutMS:       10,
	}
}

// Load reads configuration from environment variables and CLI flags.
func Load() (*Config, error) {
	cfg := DefaultConfig()
	cfg.loadFromEnv()

	// Parse command line flags if provided
	fs := flag.NewFlagSet("relay", flag.ContinueOnError)
	fs.StringVar(&cfg.Host, "host", cfg.Host, "Server host address")
	fs.IntVar(&cfg.Port, "port", cfg.Port, "Server HTTP port")
	fs.StringVar(&cfg.DBPath, "db", cfg.DBPath, "Path to SQLite database file")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "Log level: debug, info, warn, error")
	fs.DurationVar(&cfg.CleanerInterval, "cleaner-interval", cfg.CleanerInterval, "Storage retention cleaner execution interval")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, fmt.Errorf("failed to parse command line flags: %w", err)
	}

	return cfg, cfg.Validate()
}

// LoadFromEnv initializes Config purely from environment variables (useful for testing).
func LoadFromEnv() (*Config, error) {
	cfg := DefaultConfig()
	cfg.loadFromEnv()
	return cfg, cfg.Validate()
}

func (c *Config) loadFromEnv() {
	if val := os.Getenv("RELAY_HOST"); val != "" {
		c.Host = val
	}
	if val := os.Getenv("RELAY_PORT"); val != "" {
		if p, err := strconv.Atoi(val); err == nil && p > 0 {
			c.Port = p
		}
	} else if val := os.Getenv("PORT"); val != "" { // Cloud platforms standard
		if p, err := strconv.Atoi(val); err == nil && p > 0 {
			c.Port = p
		}
	}
	if val := os.Getenv("RELAY_DB_PATH"); val != "" {
		c.DBPath = val
	}
	if val := os.Getenv("RELAY_LOG_LEVEL"); val != "" {
		c.LogLevel = strings.ToLower(val)
	}
	if val := os.Getenv("RELAY_WORKER_POOL_SIZE"); val != "" {
		if s, err := strconv.Atoi(val); err == nil && s > 0 {
			c.WorkerPoolSize = s
		}
	}
	if val := os.Getenv("RELAY_RETENTION_SUCCESS_DAYS"); val != "" {
		if d, err := strconv.Atoi(val); err == nil && d >= 0 {
			c.RetentionSuccessDays = d
		}
	}
	if val := os.Getenv("RELAY_RETENTION_FAILED_DAYS"); val != "" {
		if d, err := strconv.Atoi(val); err == nil && d >= 0 {
			c.RetentionFailedDays = d
		}
	}
	if val := os.Getenv("RELAY_CLEANER_INTERVAL"); val != "" {
		if d, err := time.ParseDuration(val); err == nil && d > 0 {
			c.CleanerInterval = d
		}
	}
	if val := os.Getenv("RELAY_ADMIN_PASSWORD"); val != "" {
		c.AdminPassword = val
	}
	if val := os.Getenv("RELAY_BATCH_SIZE"); val != "" {
		if b, err := strconv.Atoi(val); err == nil && b > 0 {
			c.MaxBatchSize = b
		}
	}
	if val := os.Getenv("RELAY_BATCH_TIMEOUT_MS"); val != "" {
		if t, err := strconv.Atoi(val); err == nil && t > 0 {
			c.BatchTimeoutMS = t
		}
	}
}

// Validate ensures configuration values are within acceptable bounds.
func (c *Config) Validate() error {
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("invalid port: %d (must be between 1 and 65535)", c.Port)
	}
	if strings.TrimSpace(c.DBPath) == "" {
		return fmt.Errorf("db path cannot be empty")
	}
	if c.WorkerPoolSize < 1 {
		return fmt.Errorf("worker pool size must be >= 1")
	}
	if c.CleanerInterval <= 0 {
		return fmt.Errorf("cleaner interval must be > 0")
	}
	return nil
}

// Address returns formatted host:port string.
func (c *Config) Address() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// SlogLevel parses the string log level to slog.Level.
func (c *Config) SlogLevel() slog.Level {
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
