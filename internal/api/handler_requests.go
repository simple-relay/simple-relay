package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage"
)

// RequestsHandler provides endpoints for querying requests and viewing full payloads.
type RequestsHandler struct {
	reqRepo     storage.RequestRepository
	attemptRepo storage.AttemptRepository
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
