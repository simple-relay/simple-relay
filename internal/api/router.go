package api

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/simple-relay/simple-relay/internal/config"
	"github.com/simple-relay/simple-relay/internal/engine"
	"github.com/simple-relay/simple-relay/internal/storage"
	"github.com/simple-relay/simple-relay/ui"
)

// RouterOptions specifies optional subsystem dependencies for the router.
type RouterOptions struct {
	RouteRepo     storage.RouteRepository
	RouteRegistry *engine.RouteRegistry
	RelayEngine   *engine.RelayEngine
	BatchWriter   *engine.BatchWriter
	RequestRepo   storage.RequestRepository
	AttemptRepo   storage.AttemptRepository
	WakeFn        func()
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

	// Mount embedded Web UI static assets and SPA HTML shell
	staticSub, err := fs.Sub(ui.FS, "static")
	if err == nil {
		mux.Handle("GET /ui/static/", http.StripPrefix("/ui/static/", http.FileServerFS(staticSub)))
	}

	uiHandler := func(w http.ResponseWriter, r *http.Request) {
		indexHTML, err := ui.FS.ReadFile("index.html")
		if err != nil {
			http.Error(w, "embedded UI index.html not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(indexHTML)
	}
	mux.HandleFunc("GET /{$}", uiHandler)
	mux.HandleFunc("GET /ui", uiHandler)
	mux.HandleFunc("GET /ui/", uiHandler)

	if len(opts) > 0 {
		opt := opts[0]
		if opt.RouteRepo != nil {
			routesHandler := NewRoutesHandler(opt.RouteRepo, opt.RouteRegistry, logger)
			mux.HandleFunc("GET /api/v1/routes", routesHandler.ListRoutes)
			mux.HandleFunc("POST /api/v1/routes", routesHandler.CreateRoute)
			mux.HandleFunc("GET /api/v1/routes/{id}", routesHandler.GetRoute)
			mux.HandleFunc("PUT /api/v1/routes/{id}", routesHandler.UpdateRoute)
			mux.HandleFunc("DELETE /api/v1/routes/{id}", routesHandler.DeleteRoute)
		}
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
		if opt.RequestRepo != nil {
			requestsHandler := NewRequestsHandler(opt.RequestRepo, opt.AttemptRepo, logger)
			mux.HandleFunc("GET /api/v1/requests", requestsHandler.ListRequests)
			mux.HandleFunc("GET /api/v1/requests/{id}", requestsHandler.GetRequest)

			mux.HandleFunc("GET /api/v1/queue/stats", func(w http.ResponseWriter, r *http.Request) {
				pending, inFlight, err := opt.RequestRepo.GetQueueStats(r.Context())
				if err != nil {
					logger.Error("failed to get queue stats", "error", err)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to get queue stats"})
					return
				}
				status := "HEALTHY"
				if pending+inFlight >= 1000 {
					status = "BACKLOG"
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"pending":     pending,
					"in_flight":   inFlight,
					"queue_depth": pending + inFlight,
					"status":      status,
				})
			})

			dlqHandler := NewDLQHandler(opt.RequestRepo, opt.RouteRegistry, opt.WakeFn, logger)
			mux.HandleFunc("GET /api/v1/dlq", dlqHandler.ListDLQ)
			mux.HandleFunc("POST /api/v1/dlq/{request_id}/replay", dlqHandler.ReplaySingle)
			mux.HandleFunc("POST /api/v1/dlq/replay-all", dlqHandler.ReplayAll)
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
