package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/simple-relay/simple-relay/internal/engine"
	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/signature"
)

// NewWebhookHandler handles inbound webhook ingestion for ANY /api/v1/webhook/{route_name}.
func NewWebhookHandler(
	registry *engine.RouteRegistry,
	batchWriter *engine.BatchWriter,
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
		method := r.Method
		if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch {
			w.Header().Set("Allow", "POST, PUT, PATCH")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		routeName := r.PathValue("route_name")
		if routeName == "" {
			writeJSONError(w, http.StatusBadRequest, "route_name is required in URL path")
			return
		}

		route, found := registry.GetByName(routeName)
		if !found || route == nil {
			route, found = registry.GetByPath(r.URL.Path)
			if !found || route == nil {
				writeJSONError(w, http.StatusNotFound, "route not found")
				return
			}
		}

		if route.Type != model.RouteTypeInbound {
			writeJSONError(w, http.StatusBadRequest, "route is not configured for inbound webhooks")
			return
		}

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

		// Verify signature if required by route configuration
		if route.SignatureProvider != model.SignatureProviderNone && route.SignatureProvider != "" {
			if strings.TrimSpace(route.SignatureSecret) == "" {
				logger.Error("route misconfiguration: signature_secret is empty", "route", routeName, "provider", route.SignatureProvider)
				writeJSONError(w, http.StatusInternalServerError, "route signature misconfiguration")
				return
			}

			verifier, err := signature.ForProvider(route.SignatureProvider)
			if err != nil {
				logger.Error("unsupported signature provider configured on route", "provider", route.SignatureProvider, "route", routeName)
				writeJSONError(w, http.StatusInternalServerError, "unsupported signature provider")
				return
			}

			if verifier != nil {
				if err := verifier.Verify(r.Header, route.SignatureSecret, bodyBytes); err != nil {
					logger.Warn("inbound webhook signature verification failed",
						"route", routeName,
						"provider", route.SignatureProvider,
						"error", err,
					)
					writeJSONError(w, http.StatusUnauthorized, "invalid webhook signature")
					return
				}
			}
		}

		headers := make(map[string]string)
		for k, vv := range r.Header {
			if isHopByHop(k) {
				continue
			}
			headers[k] = strings.Join(vv, ", ")
		}

		relayID := r.Header.Get("X-Relay-Request-ID")
		if relayID == "" {
			relayID = model.NewULID()
		}
		headers["X-Relay-Request-ID"] = relayID

		now := time.Now().UTC()
		req := &model.Request{
			ID:            relayID,
			RouteID:       route.ID,
			Method:        r.Method,
			URL:           route.DestinationURL,
			Headers:       headers,
			Body:          bodyBytes,
			Status:        model.RequestStatusPending,
			AttemptsCount: 0,
			NextAttemptAt: &now,
			CreatedAt:     now,
			UpdatedAt:     now,
		}

		if err := batchWriter.Enqueue(r.Context(), req); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				writeJSONError(w, http.StatusGatewayTimeout, "ingestion timed out or client disconnected")
				return
			}
			logger.Error("failed to enqueue inbound webhook", "route", routeName, "error", err)
			writeJSONError(w, http.StatusInternalServerError, "failed to persist webhook")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"relay_id": relayID,
			"status":   "persisted",
		})
	}
}
