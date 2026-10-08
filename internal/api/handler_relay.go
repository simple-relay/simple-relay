package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/simple-relay/simple-relay/internal/engine"
	"github.com/simple-relay/simple-relay/internal/model"
)

// DefaultMaxBodyBytes sets the maximum allowed request body size to 10 MB.
const DefaultMaxBodyBytes = 10 * 1024 * 1024

// NewRelayHandler handles synchronous HTTP proxy requests for /api/v1/relay/{route_name}.
func NewRelayHandler(
	registry *engine.RouteRegistry,
	relayEngine *engine.RelayEngine,
	logger *slog.Logger,
	maxBodyBytes int64,
) http.HandlerFunc {
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxBodyBytes
	}
	if logger == nil {
		logger = slog.Default()
	}

	return func(w http.ResponseWriter, r *http.Request) {
		routeName := r.PathValue("route_name")
		if routeName == "" {
			writeJSONError(w, http.StatusBadRequest, "route_name is required in URL path")
			return
		}

		route, found := registry.GetByName(routeName)
		if !found || route == nil {
			writeJSONError(w, http.StatusNotFound, "route not found")
			return
		}

		if route.Mode == model.RouteModeAsync {
			writeJSONError(w, http.StatusBadRequest, "route mode is async, use /api/v1/jobs")
			return
		}

		// Read request body bounded by MaxBytesReader
		bodyBytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				writeJSONError(w, http.StatusRequestEntityTooLarge, "payload too large")
				return
			}
			writeJSONError(w, http.StatusBadRequest, "failed to read request body")
			return
		}

		// Parse timeout override if provided
		var timeoutOverrideMS *int
		if timeoutHeader := r.Header.Get("X-Relay-Timeout"); timeoutHeader != "" {
			if ms, err := strconv.Atoi(timeoutHeader); err == nil && ms > 0 {
				timeoutOverrideMS = &ms
			}
		}

		// Execute synchronous relay
		result, err := relayEngine.RelaySync(
			r.Context(),
			route,
			r.Method,
			r.URL.String(),
			r.Header,
			bodyBytes,
			timeoutOverrideMS,
		)

		if errors.Is(err, context.Canceled) {
			// Client disconnected - do not attempt to write headers or body
			return
		}

		if err != nil && (result == nil || result.StatusCode == 0) {
			logger.Warn("relay upstream error", "route", routeName, "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":   "upstream unreachable",
				"details": err.Error(),
			})
			return
		}

		// Propagate upstream response headers
		if result != nil {
			for k, vv := range result.Headers {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}

			// Add Relay diagnostic headers
			w.Header().Set("X-Relay-Request-ID", result.RequestID)
			w.Header().Set("X-Relay-Attempts", strconv.Itoa(result.Attempts))

			w.WriteHeader(result.StatusCode)
			_, _ = w.Write(result.Body)
		}
	}
}

func writeJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
