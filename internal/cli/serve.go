package cli

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

	"github.com/spf13/cobra"

	"github.com/simple-relay/simple-relay/internal/api"
	"github.com/simple-relay/simple-relay/internal/config"
	"github.com/simple-relay/simple-relay/internal/engine"
	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage/sqlite"
	"github.com/simple-relay/simple-relay/internal/upgrade"
	"github.com/simple-relay/simple-relay/internal/version"
)

type ServerOptions struct {
	Host              string
	Port              int
	DBPath            string
	LogLevel          string
	WorkerConcurrency int
	DispatcherBuffer  int
	DispatcherPollMS  int
	CleanerInterval   time.Duration
	DrainTimeout      time.Duration
}

func addServerFlags(cmd *cobra.Command, opts *ServerOptions) {
	cmd.Flags().StringVar(&opts.Host, "host", "0.0.0.0", "Server host address (env: RELAY_HOST)")
	cmd.Flags().IntVar(&opts.Port, "port", 8080, "Server HTTP port (env: RELAY_PORT)")
	cmd.Flags().StringVar(&opts.DBPath, "db", "relay.db", "Path to SQLite database file (env: RELAY_DB_PATH)")
	cmd.Flags().StringVar(&opts.LogLevel, "log-level", "info", "Log level: debug, info, warn, error (env: RELAY_LOG_LEVEL)")
	cmd.Flags().IntVar(&opts.WorkerConcurrency, "workers", 20, "Outbound worker pool concurrency (env: RELAY_WORKER_CONCURRENCY)")
	cmd.Flags().IntVar(&opts.DispatcherBuffer, "dispatcher-buffer", 0, "Dispatcher jobs channel buffer capacity (env: RELAY_DISPATCHER_BUFFER)")
	cmd.Flags().IntVar(&opts.DispatcherPollMS, "dispatcher-poll-ms", 500, "Dispatcher polling interval in ms (env: RELAY_DISPATCHER_POLL_MS)")
	cmd.Flags().DurationVar(&opts.CleanerInterval, "cleaner-interval", 1*time.Hour, "Storage retention cleaner execution interval (env: RELAY_CLEANER_INTERVAL)")
	cmd.Flags().DurationVar(&opts.DrainTimeout, "drain-timeout", 30*time.Second, "Graceful shutdown drain timeout (env: RELAY_DRAIN_TIMEOUT)")
}

func newServeCmd() *cobra.Command {
	var opts ServerOptions
	cmd := &cobra.Command{
		Use:          "serve",
		Short:        "Start the relay daemon (default command)",
		SilenceUsage: true,
		RunE:         runServe,
	}
	addServerFlags(cmd, &opts)
	return cmd
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	if cmd.Flags().Changed("host") {
		cfg.Host, _ = cmd.Flags().GetString("host")
	}
	if cmd.Flags().Changed("port") {
		cfg.Port, _ = cmd.Flags().GetInt("port")
	}
	if cmd.Flags().Changed("db") {
		cfg.DBPath, _ = cmd.Flags().GetString("db")
	}
	if cmd.Flags().Changed("log-level") {
		cfg.LogLevel, _ = cmd.Flags().GetString("log-level")
	}
	if cmd.Flags().Changed("workers") {
		cfg.WorkerConcurrency, _ = cmd.Flags().GetInt("workers")
	}
	if cmd.Flags().Changed("dispatcher-buffer") {
		cfg.DispatcherBuffer, _ = cmd.Flags().GetInt("dispatcher-buffer")
	}
	if cmd.Flags().Changed("dispatcher-poll-ms") {
		cfg.DispatcherPollMS, _ = cmd.Flags().GetInt("dispatcher-poll-ms")
	}
	if cmd.Flags().Changed("cleaner-interval") {
		cfg.CleanerInterval, _ = cmd.Flags().GetDuration("cleaner-interval")
	}
	if cmd.Flags().Changed("drain-timeout") {
		cfg.DrainTimeout, _ = cmd.Flags().GetDuration("drain-timeout")
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.SlogLevel(),
	}))
	slog.SetDefault(logger)

	logger.Info("starting simple-relay",
		"version", version.Version,
		"commit", version.GitCommit,
		"build_date", version.BuildDate,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := RunServer(ctx, cfg, logger); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server terminated with error", "error", err)
		return &statusError{code: 1, err: err}
	}
	return nil
}

// RunServer executes the relay daemon HTTP server and background workers until ctx is canceled.
func RunServer(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	db, err := sqlite.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open sqlite database at %s: %w", cfg.DBPath, err)
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("failed to apply database migrations: %w", err)
	}

	// Auto-seed initial default API key on cold boot in dev mode if database has zero keys.
	if cfg.AdminPassword == "" {
		totalKeys, err := db.APIKeys().CountTotalKeys(ctx)
		if err != nil {
			logger.Error("failed to query api keys count", "error", err)
		} else if totalKeys == 0 {
			rawKey, apiKey, err := api.GenerateRawKey("default")
			if err != nil {
				logger.Error("failed to generate default api key", "error", err)
			} else if err := db.APIKeys().CreateAPIKey(ctx, apiKey); err != nil {
				logger.Error("failed to persist default api key", "error", err)
			} else {
				logger.Info("no API keys found; generated default bootstrap key for dev mode",
					"name", "default",
					"key", rawKey,
					"masked", apiKey.MaskedKey,
				)
			}
		}
	}

	registry := engine.NewRouteRegistry(db.RouteRepo)
	if err := registry.Init(ctx); err != nil {
		return fmt.Errorf("failed to initialize route registry: %w", err)
	}

	retryEngine := engine.NewRetryEngine()
	relayEngine := engine.NewRelayEngine(
		&http.Client{},
		retryEngine,
		db.RequestRepo,
		db.AttemptRepo,
		logger,
	)

	ln, inherited, err := upgrade.GetListener(cfg.Address())
	if err != nil {
		return fmt.Errorf("failed to bind or inherit listener on %s: %w", cfg.Address(), err)
	}
	defer ln.Close()

	jobsCh := make(chan *model.Request, cfg.DispatcherBuffer)
	dispatcher := engine.NewDispatcher(db.RequestRepo, jobsCh, engine.DispatcherConfig{
		PollInterval:          time.Duration(cfg.DispatcherPollMS) * time.Millisecond,
		SkipImmediateRecovery: inherited,
	})
	workerPool := engine.NewWorkerPool(jobsCh, registry, db.RequestRepo, db.AttemptRepo, retryEngine, engine.WorkerPoolConfig{
		Concurrency: cfg.WorkerConcurrency,
	})
	batchWriter := engine.NewBatchWriter(db.RequestRepo, engine.BatchWriterConfig{
		WakeFn: dispatcher.Wake,
	})

	batchWriter.Start(ctx)
	defer batchWriter.Close()

	dispatcher.Start(ctx)
	defer dispatcher.Close()

	workerPool.Start(ctx)
	defer workerPool.Close()

	cleaner := engine.NewCleaner(db.RetentionRepo, engine.CleanerConfig{
		Interval:             cfg.CleanerInterval,
		RetentionSuccessDays: cfg.RetentionSuccessDays,
		RetentionFailedDays:  cfg.RetentionFailedDays,
		BatchSize:            1000,
		VacuumPages:          1000,
	}, logger)
	cleaner.Start(ctx)
	defer cleaner.Close()

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	upgradeManager := upgrade.NewUpgradeManager(ln, func() {
		_ = dispatcher.Close()
		cancelRun()
	}, logger)
	upgradeManager.StartSignalListener(runCtx)

	handler := api.NewRouter(cfg, logger, api.RouterOptions{
		Context:       runCtx,
		RouteRepo:     db.RouteRepo,
		RouteRegistry: registry,
		RelayEngine:   relayEngine,
		BatchWriter:   batchWriter,
		RequestRepo:   db.RequestRepo,
		AttemptRepo:   db.AttemptRepo,
		WakeFn:        dispatcher.Wake,
		APIKeyRepo:    db.APIKeys(),
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
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	if err := upgrade.SignalReady(); err != nil {
		return fmt.Errorf("failed to signal upgrade readiness to parent: %w", err)
	}

	select {
	case err := <-serverErr:
		return fmt.Errorf("http server failure: %w", err)
	case <-runCtx.Done():
		logger.Info("shutting down server gracefully...")
	}

	drainTimeout := cfg.DrainTimeout
	if drainTimeout <= 0 {
		drainTimeout = 30 * time.Second
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}

	logger.Info("server stopped safely")
	return nil
}
