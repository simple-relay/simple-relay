package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

// RequestsHandler provides endpoints for querying requests and viewing full payloads.
type RequestsHandler struct {
	reqRepo     storage.RequestRepository
	attemptRepo storage.AttemptRepository
	wakeFn      func()
	logger      *slog.Logger
}

// NewRequestsHandler initializes a new requests query handler.
func NewRequestsHandler(
	reqRepo storage.RequestRepository,
	attemptRepo storage.AttemptRepository,
	logger *slog.Logger,
) *RequestsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &RequestsHandler{
		reqRepo:     reqRepo,
		attemptRepo: attemptRepo,
		logger:      logger,
	}
}

// WithWakeFn registers an immediate dispatcher wakeup callback.
func (h *RequestsHandler) WithWakeFn(wakeFn func()) *RequestsHandler {
	h.wakeFn = wakeFn
	return h
}

// RequestReplayResponse represents the result of requeuing any historical request.
type RequestReplayResponse struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Replayed   bool   `json:"replayed"`
	ReplayedAt string `json:"replayed_at"`
}

// RequestsListResponse represents paginated request metadata.
type RequestsListResponse struct {
	Items  []*model.RequestMetadata `json:"items"`
	Total  int64                    `json:"total"`
	Limit  int                      `json:"limit"`
	Offset int                      `json:"offset"`
}

// RequestDetailResponse represents complete request payload and attempt history.
type RequestDetailResponse struct {
	ID            string            `json:"id"`
	RouteID       string            `json:"route_id"`
	Method        string            `json:"method"`
	URL           string            `json:"url"`
	Headers       map[string]string `json:"headers"`
	Body          string            `json:"body"`
	Status        string            `json:"status"`
	AttemptsCount int               `json:"attempts_count"`
	LastError     *string           `json:"last_error,omitempty"`
	CreatedAt     string            `json:"created_at"`
	UpdatedAt     string            `json:"updated_at"`
	Attempts      []*model.Attempt  `json:"attempts"`
}

// ListRequests handles GET /api/v1/requests
func (h *RequestsHandler) ListRequests(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit := 50
	if l := q.Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 {
			limit = val
		}
	}
	if limit > 100 {
		limit = 100
	}

	offset := 0
	if o := q.Get("offset"); o != "" {
		if val, err := strconv.Atoi(o); err == nil && val >= 0 {
			offset = val
		}
	}

	filter := storage.RequestFilter{
		Status:  strings.TrimSpace(q.Get("status")),
		RouteID: strings.TrimSpace(q.Get("route_id")),
		Search:  strings.TrimSpace(q.Get("search")),
		Limit:   limit,
		Offset:  offset,
	}

	items, total, err := h.reqRepo.ListRequests(r.Context(), filter)
	if err != nil {
		h.logger.Error("failed to query requests", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to query requests")
		return
	}

	if items == nil {
		items = []*model.RequestMetadata{}
	}

	resp := RequestsListResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// GetRequest handles GET /api/v1/requests/{id}
func (h *RequestsHandler) GetRequest(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "missing request id")
		return
	}

	req, err := h.reqRepo.GetRequestByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "request not found")
			return
		}
		h.logger.Error("failed to fetch request", "id", id, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to fetch request")
		return
	}

	var attempts []*model.Attempt
	if h.attemptRepo != nil {
		attList, err := h.attemptRepo.ListAttemptsByRequestID(r.Context(), id)
		if err != nil {
			h.logger.Error("failed to fetch attempts for request", "id", id, "error", err)
			writeJSONError(w, http.StatusInternalServerError, "failed to fetch attempts")
			return
		}
		attempts = attList
	}
	if attempts == nil {
		attempts = []*model.Attempt{}
	}

	detail := RequestDetailResponse{
		ID:            req.ID,
		RouteID:       req.RouteID,
		Method:        req.Method,
		URL:           req.URL,
		Headers:       req.Headers,
		Body:          string(req.Body),
		Status:        string(req.Status),
		AttemptsCount: req.AttemptsCount,
		LastError:     req.LastError,
		CreatedAt:     req.CreatedAt.Format(http.TimeFormat),
		UpdatedAt:     req.UpdatedAt.Format(http.TimeFormat),
		Attempts:      attempts,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(detail)
}

// ReplayRequest handles POST /api/v1/requests/{id}/replay.
func (h *RequestsHandler) ReplayRequest(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("id")
	if requestID == "" {
		requestID = r.PathValue("request_id")
	}
	if requestID == "" {
		writeJSONError(w, http.StatusBadRequest, "request_id is required in URL path")
		return
	}

	req, err := h.reqRepo.ReplayRequest(r.Context(), requestID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "request not found")
			return
		}
		if errors.Is(err, storage.ErrConflict) {
			writeJSONError(w, http.StatusConflict, "request is currently pending or processing")
			return
		}
		h.logger.Error("failed to replay request", "request_id", requestID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to replay request")
		return
	}

	// Wake dispatcher immediately so delivery starts without delay ⚡
	if h.wakeFn != nil {
		h.wakeFn()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(RequestReplayResponse{
		ID:         req.ID,
		Status:     string(req.Status),
		Replayed:   true,
		ReplayedAt: time.Now().UTC().Format(time.RFC3339),
	})
}
