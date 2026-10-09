package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/simple-relay/simple-relay/internal/engine"
	"github.com/simple-relay/simple-relay/internal/storage"
)

// DLQHandler provides HTTP endpoints for Dead Letter Queue inspection and replay.
type DLQHandler struct {
	repo     storage.RequestRepository
	registry *engine.RouteRegistry
	wakeFn   func()
	logger   *slog.Logger
}

// NewDLQHandler initializes a new DLQ handler instance.
func NewDLQHandler(
	repo storage.RequestRepository,
	registry *engine.RouteRegistry,
	wakeFn func(),
	logger *slog.Logger,
) *DLQHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &DLQHandler{
		repo:     repo,
		registry: registry,
		wakeFn:   wakeFn,
		logger:   logger,
	}
}

// DLQItemResponse represents an individual request item in DLQ inspection.
type DLQItemResponse struct {
	ID            string    `json:"id"`
	RouteID       string    `json:"route_id"`
	Method        string    `json:"method"`
	URL           string    `json:"url"`
	Status        string    `json:"status"`
	AttemptsCount int       `json:"attempts_count"`
	LastError     *string   `json:"last_error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// DLQListResponse represents paginated DLQ records.
type DLQListResponse struct {
	Items  []DLQItemResponse `json:"items"`
	Total  int64             `json:"total"`
	Limit  int               `json:"limit"`
	Offset int               `json:"offset"`
}

// ListDLQ handles GET /api/v1/dlq.
func (h *DLQHandler) ListDLQ(w http.ResponseWriter, r *http.Request) {
	var routeID string
	if routeName := r.URL.Query().Get("route_name"); routeName != "" {
		if h.registry != nil {
			route, found := h.registry.GetByName(routeName)
			if !found || route == nil {
				writeJSONError(w, http.StatusNotFound, "route not found")
				return
			}
			routeID = route.ID
		}
	}

	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		l, err := strconv.Atoi(lStr)
		if err != nil || l <= 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid limit parameter")
			return
		}
		if l > 500 {
			l = 500
		}
		limit = l
	}

	offset := 0
	if oStr := r.URL.Query().Get("offset"); oStr != "" {
		o, err := strconv.Atoi(oStr)
		if err != nil || o < 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid offset parameter")
			return
		}
		offset = o
	}

	items, total, err := h.repo.ListDLQRequests(r.Context(), routeID, limit, offset)
	if err != nil {
		h.logger.Error("failed to list dlq requests", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to query dlq requests")
		return
	}

	respItems := make([]DLQItemResponse, 0, len(items))
	for _, it := range items {
		respItems = append(respItems, DLQItemResponse{
			ID:            it.ID,
			RouteID:       it.RouteID,
			Method:        it.Method,
			URL:           it.URL,
			Status:        string(it.Status),
			AttemptsCount: it.AttemptsCount,
			LastError:     it.LastError,
			CreatedAt:     it.CreatedAt,
			UpdatedAt:     it.UpdatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(DLQListResponse{
		Items:  respItems,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// ReplaySingleResponse represents the payload returned after replaying a single request.
type ReplaySingleResponse struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Replayed   bool   `json:"replayed"`
	ReplayedAt string `json:"replayed_at"`
}

// ReplaySingle handles POST /api/v1/dlq/{request_id}/replay.
func (h *DLQHandler) ReplaySingle(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("request_id")
	if requestID == "" {
		writeJSONError(w, http.StatusBadRequest, "request_id is required in URL path")
		return
	}

	req, err := h.repo.ReplayDLQRequest(r.Context(), requestID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "request not found")
			return
		}
		if errors.Is(err, storage.ErrConflict) {
			writeJSONError(w, http.StatusConflict, "request is not in dlq status")
			return
		}
		h.logger.Error("failed to replay dlq request", "request_id", requestID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to replay request")
		return
	}

	// Wake dispatcher immediately so the job doesn't sit idle until next poll tick ⚡
	if h.wakeFn != nil {
		h.wakeFn()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(ReplaySingleResponse{
		ID:         req.ID,
		Status:     string(req.Status),
		Replayed:   true,
		ReplayedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// ReplayAllRequest represents optional filter parameters for batch DLQ replay.
type ReplayAllRequest struct {
	RouteName string `json:"route_name,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// ReplayAllResponse represents the outcome of a batch DLQ replay.
type ReplayAllResponse struct {
	ReplayedCount int      `json:"replayed_count"`
	RequestIDs    []string `json:"request_ids"`
}

// ReplayAll handles POST /api/v1/dlq/replay-all.
func (h *DLQHandler) ReplayAll(w http.ResponseWriter, r *http.Request) {
	var reqBody ReplayAllRequest
	if r.Body != nil {
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		if err != nil && !errors.Is(err, io.EOF) {
			writeJSONError(w, http.StatusBadRequest, "malformed request payload")
			return
		}
	}

	if reqBody.Limit < 0 {
		writeJSONError(w, http.StatusBadRequest, "limit cannot be negative")
		return
	}

	limit := reqBody.Limit
	if limit <= 0 {
		limit = 100
	} else if limit > 1000 {
		// Cap limit to 1000 to prevent sudden downstream thundering herd storms ⚡
		limit = 1000
	}

	var routeID string
	if reqBody.RouteName != "" {
		if h.registry != nil {
			route, found := h.registry.GetByName(reqBody.RouteName)
			if !found || route == nil {
				writeJSONError(w, http.StatusNotFound, "route not found")
				return
			}
			routeID = route.ID
		}
	}

	ids, err := h.repo.ReplayAllDLQRequests(r.Context(), routeID, limit)
	if err != nil {
		h.logger.Error("failed to batch replay dlq requests", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to batch replay requests")
		return
	}

	if len(ids) > 0 && h.wakeFn != nil {
		h.wakeFn()
	}

	if ids == nil {
		ids = []string{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(ReplayAllResponse{
		ReplayedCount: len(ids),
		RequestIDs:    ids,
	})
}
