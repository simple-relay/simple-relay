package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

// APIKeysHandler manages CRUD endpoints for API keys.
type APIKeysHandler struct {
	repo   storage.APIKeyRepository
	logger *slog.Logger
}

// NewAPIKeysHandler initializes a new APIKeysHandler.
func NewAPIKeysHandler(repo storage.APIKeyRepository, logger *slog.Logger) *APIKeysHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &APIKeysHandler{
		repo:   repo,
		logger: logger,
	}
}

// CreateKey generates a new API key, persists its hash, and returns the raw key once.
func (h *APIKeysHandler) CreateKey(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "unnamed"
	}

	rawKey, apiKey, err := GenerateRawKey(name)
	if err != nil {
		h.logger.Error("failed to generate raw api key", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to generate api key"})
		return
	}

	if err := h.repo.CreateAPIKey(r.Context(), apiKey); err != nil {
		h.logger.Error("failed to persist api key", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to persist api key"})
		return
	}

	resp := model.CreateKeyResponse{
		ID:        apiKey.ID,
		Name:      apiKey.Name,
		Key:       rawKey,
		MaskedKey: apiKey.MaskedKey,
		CreatedAt: apiKey.CreatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// ListKeys returns metadata for all API keys (never exposing raw tokens).
func (h *APIKeysHandler) ListKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.repo.ListAPIKeys(r.Context())
	if err != nil {
		h.logger.Error("failed to list api keys", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to query api keys"})
		return
	}

	if keys == nil {
		keys = []*model.APIKey{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(keys)
}

// RevokeKey marks an active API key as revoked.
func (h *APIKeysHandler) RevokeKey(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing api key id"})
		return
	}

	if err := h.repo.RevokeAPIKey(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "api key not found or already revoked"})
			return
		}
		h.logger.Error("failed to revoke api key", "id", id, "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to revoke api key"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"id":     id,
		"status": "revoked",
	})
}
