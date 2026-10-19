package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/simple-relay/simple-relay/internal/engine"
	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

// RoutesHandler provides HTTP CRUD endpoints for Route configuration.
type RoutesHandler struct {
	repo     storage.RouteRepository
	registry *engine.RouteRegistry
	logger   *slog.Logger
}

// NewRoutesHandler initializes a new routes CRUD handler.
func NewRoutesHandler(
	repo storage.RouteRepository,
	registry *engine.RouteRegistry,
	logger *slog.Logger,
) *RoutesHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &RoutesHandler{
		repo:     repo,
		registry: registry,
		logger:   logger,
	}
}

// MaskSecret redacts a signature secret into a safe hint (e.g. "whse*****9a1b" or "********").
func MaskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 8 {
		return "********"
	}
	return secret[:4] + "*****" + secret[len(secret)-4:]
}

// maskRoute returns a shallow copy of the route with SignatureSecret masked for HTTP responses.
// Keeps the underlying model.Route in RouteRegistry untouched for zero-alloc HMAC verification ⚡.
func maskRoute(r *model.Route) *model.Route {
	if r == nil {
		return nil
	}
	cp := *r
	cp.SignatureSecret = MaskSecret(r.SignatureSecret)
	return &cp
}

// ListRoutes handles GET /api/v1/routes
func (h *RoutesHandler) ListRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := h.repo.ListRoutes(r.Context())
	if err != nil {
		h.logger.Error("failed to list routes", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to list routes")
		return
	}
	masked := make([]*model.Route, 0, len(routes))
	for _, rt := range routes {
		masked = append(masked, maskRoute(rt))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(masked)
}

// CreateRoute handles POST /api/v1/routes
func (h *RoutesHandler) CreateRoute(w http.ResponseWriter, r *http.Request) {
	var route model.Route
	if err := json.NewDecoder(r.Body).Decode(&route); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}

	if route.Mode == "" {
		route.Mode = model.RouteModeAsync
	}
	if route.MaxAttempts <= 0 {
		route.MaxAttempts = 5
	}
	if route.TimeoutMS <= 0 {
		route.TimeoutMS = 10000
	}
	if route.RetryBackoffStrategy == "" {
		route.RetryBackoffStrategy = model.RetryStrategyExponential
	}
	if route.SignatureProvider == "" {
		route.SignatureProvider = model.SignatureProviderNone
	}

	if err := route.Validate(); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.repo.CreateRoute(r.Context(), &route); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			writeJSONError(w, http.StatusConflict, "route name or path already exists")
			return
		}
		h.logger.Error("failed to create route", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to create route")
		return
	}

	// Hot reload in-memory cache
	if h.registry != nil {
		if err := h.registry.Reload(r.Context()); err != nil {
			h.logger.Warn("failed to reload route registry after create", "error", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(maskRoute(&route))
}

// GetRoute handles GET /api/v1/routes/{id}
func (h *RoutesHandler) GetRoute(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "missing route id")
		return
	}

	route, err := h.repo.GetRouteByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "route not found")
			return
		}
		h.logger.Error("failed to get route", "id", id, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to get route")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(maskRoute(route))
}

// UpdateRoute handles PUT /api/v1/routes/{id}
func (h *RoutesHandler) UpdateRoute(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "missing route id")
		return
	}

	var route model.Route
	if err := json.NewDecoder(r.Body).Decode(&route); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}

	existing, err := h.repo.GetRouteByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "route not found")
			return
		}
		h.logger.Error("failed to fetch existing route for update", "id", id, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to update route")
		return
	}

	route.ID = id
	if route.Mode == "" {
		route.Mode = model.RouteModeAsync
	}
	if route.MaxAttempts <= 0 {
		route.MaxAttempts = 5
	}
	if route.TimeoutMS <= 0 {
		route.TimeoutMS = 10000
	}
	if route.RetryBackoffStrategy == "" {
		route.RetryBackoffStrategy = model.RetryStrategyExponential
	}
	if route.SignatureProvider == "" {
		route.SignatureProvider = model.SignatureProviderNone
	}

	// Preserve existing secret when the caller omits signature_secret or echoes back the masked value
	if route.Type == model.RouteTypeInbound && route.SignatureProvider != model.SignatureProviderNone {
		if strings.TrimSpace(route.SignatureSecret) == "" || route.SignatureSecret == MaskSecret(existing.SignatureSecret) {
			route.SignatureSecret = existing.SignatureSecret
		}
	}

	if err := route.Validate(); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.repo.UpdateRoute(r.Context(), &route); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "route not found")
			return
		}
		if errors.Is(err, storage.ErrConflict) {
			writeJSONError(w, http.StatusConflict, "route name or path conflict")
			return
		}
		h.logger.Error("failed to update route", "id", id, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to update route")
		return
	}

	// Hot reload in-memory cache
	if h.registry != nil {
		if err := h.registry.Reload(r.Context()); err != nil {
			h.logger.Warn("failed to reload route registry after update", "error", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(maskRoute(&route))
}

// DeleteRoute handles DELETE /api/v1/routes/{id}
func (h *RoutesHandler) DeleteRoute(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "missing route id")
		return
	}

	if err := h.repo.DeleteRoute(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "route not found")
			return
		}
		h.logger.Error("failed to delete route", "id", id, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to delete route")
		return
	}

	// Hot reload in-memory cache
	if h.registry != nil {
		if err := h.registry.Reload(r.Context()); err != nil {
			h.logger.Warn("failed to reload route registry after delete", "error", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}
