package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/simple-relay/simple-relay/internal/api"
	"github.com/simple-relay/simple-relay/internal/config"
	"github.com/simple-relay/simple-relay/internal/engine"
	"github.com/simple-relay/simple-relay/internal/storage/sqlite"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.SlogLevel(),
	}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, logger); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server terminated with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	// Initialize SQLite storage
	db, err := sqlite.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open sqlite database at %s: %w", cfg.DBPath, err)
	}
	defer db.Close()

	// Apply database schema migrations
	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("failed to apply database migrations: %w", err)
	}

	// Initialize in-memory route registry
	registry := engine.NewRouteRegistry(db.RouteRepo)
	if err := registry.Init(ctx); err != nil {
		return fmt.Errorf("failed to initialize route registry: %w", err)
	}

	// Initialize delivery engines
	retryEngine := engine.NewRetryEngine()
	relayEngine := engine.NewRelayEngine(
		&http.Client{},
		retryEngine,
		db.RequestRepo,
		db.AttemptRepo,
		logger,
	)

	handler := api.NewRouter(cfg, logger, api.RouterOptions{
		RouteRegistry: registry,
		RelayEngine:   relayEngine,
	})

	srv := &http.Server{
		Addr:              cfg.Address(),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("starting simple-relay server",
			"address", cfg.Address(),
			"log_level", cfg.LogLevel,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("http server failure: %w", err)
	case <-ctx.Done():
		logger.Info("shutting down server gracefully...")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}

	logger.Info("server stopped safely")
	return nil
}
