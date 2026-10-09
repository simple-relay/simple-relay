package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/simple-relay/simple-relay/internal/config"
	"github.com/simple-relay/simple-relay/internal/engine"
	"github.com/simple-relay/simple-relay/internal/storage"
)

// RouterOptions specifies optional subsystem dependencies for the router.
type RouterOptions struct {
	RouteRegistry *engine.RouteRegistry
	RelayEngine   *engine.RelayEngine
	BatchWriter   *engine.BatchWriter
	RequestRepo   storage.RequestRepository
	AttemptRepo   storage.AttemptRepository
	MaxBodyBytes  int64
}

// NewRouter creates and configures the HTTP routing using Go 1.22+ standard library ServeMux.
func NewRouter(cfg *config.Config, logger *slog.Logger, opts ...RouterOptions) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	mux := http.NewServeMux()

	// Health check endpoint for container orchestrators and monitoring
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "ok",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	})

	if len(opts) > 0 {
		opt := opts[0]
		if opt.RouteRegistry != nil && opt.RelayEngine != nil {
			relayHandler := NewRelayHandler(opt.RouteRegistry, opt.RelayEngine, logger, opt.MaxBodyBytes)
			mux.HandleFunc("/api/v1/relay/{route_name}", relayHandler)
		}
		if opt.RouteRegistry != nil && opt.BatchWriter != nil {
			ingestHandler := NewJobIngestHandler(opt.RouteRegistry, opt.BatchWriter, logger, opt.MaxBodyBytes)
			mux.HandleFunc("POST /api/v1/jobs/{route_name}", ingestHandler)

			webhookHandler := NewWebhookHandler(opt.RouteRegistry, opt.BatchWriter, logger, opt.MaxBodyBytes)
			mux.HandleFunc("/api/v1/webhook/{route_name}", webhookHandler)
		}
		if opt.RequestRepo != nil && opt.AttemptRepo != nil {
			inspectHandler := NewJobInspectHandler(opt.RequestRepo, opt.AttemptRepo, logger)
			mux.HandleFunc("GET /api/v1/jobs/{job_id}", inspectHandler)
		}
	}

	// Wrap mux with standard logging & panic recovery middleware
	return loggingMiddleware(logger, recoveryMiddleware(logger, mux))
}

func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(rw, r)

		logger.Debug("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.statusCode,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_addr", r.RemoteAddr,
		)
	})
}

func recoveryMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("panic recovered in http handler",
					"error", rec,
					"path", r.URL.Path,
				)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
