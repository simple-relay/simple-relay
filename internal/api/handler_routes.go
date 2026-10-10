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

// ListRoutes handles GET /api/v1/routes
func (h *RoutesHandler) ListRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := h.repo.ListRoutes(r.Context())
	if err != nil {
		h.logger.Error("failed to list routes", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to list routes")
		return
	}
	if routes == nil {
		routes = []*model.Route{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(routes)
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
	_ = json.NewEncoder(w).Encode(&route)
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
	_ = json.NewEncoder(w).Encode(route)
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
	_ = json.NewEncoder(w).Encode(&route)
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
