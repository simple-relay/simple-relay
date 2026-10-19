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
	"github.com/simple-relay/simple-relay/internal/storage"
)

// hopByHopHeaders defines headers that should not be forwarded or stored.
var hopByHopHeaders = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailers":            true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

func isHopByHop(header string) bool {
	return hopByHopHeaders[strings.ToLower(header)]
}

// NewJobIngestHandler handles asynchronous job ingestion for POST /api/v1/jobs/{route_name}.
func NewJobIngestHandler(
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

		headers := make(map[string]string)
		for k, vv := range r.Header {
			if isHopByHop(k) {
				continue
			}
			cleaned, keep := engine.SanitizeInternalAuthHeaders(k, vv)
			if !keep || len(cleaned) == 0 {
				continue
			}
			headers[k] = strings.Join(cleaned, ", ")
		}

		jobID := r.Header.Get("X-Relay-Request-ID")
		if jobID == "" {
			jobID = model.NewULID()
		}
		headers["X-Relay-Request-ID"] = jobID

		now := time.Now().UTC()
		req := &model.Request{
			ID:            jobID,
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
			logger.Error("failed to enqueue job", "route", routeName, "error", err)
			writeJSONError(w, http.StatusInternalServerError, "failed to persist job")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"job_id": jobID,
			"status": "queued",
		})
	}
}

// JobInspectionResponse represents the full inspection response for a job.
type JobInspectionResponse struct {
	ID            string           `json:"id"`
	RouteID       string           `json:"route_id"`
	Method        string           `json:"method"`
	URL           string           `json:"url"`
	Status        string           `json:"status"`
	AttemptsCount int              `json:"attempts_count"`
	NextAttemptAt *time.Time       `json:"next_attempt_at,omitempty"`
	LastError     *string          `json:"last_error,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
	Attempts      []*model.Attempt `json:"attempts"`
}

// NewJobInspectHandler handles status inspection for GET /api/v1/jobs/{job_id}.
func NewJobInspectHandler(
	reqRepo storage.RequestRepository,
	attemptRepo storage.AttemptRepository,
	logger *slog.Logger,
) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}

	return func(w http.ResponseWriter, r *http.Request) {
		jobID := r.PathValue("job_id")
		if jobID == "" {
			writeJSONError(w, http.StatusBadRequest, "job_id is required in URL path")
			return
		}

		req, err := reqRepo.GetRequestByID(r.Context(), jobID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "job not found")
				return
			}
			logger.Error("failed to fetch job", "job_id", jobID, "error", err)
			writeJSONError(w, http.StatusInternalServerError, "failed to fetch job")
			return
		}

		attempts, err := attemptRepo.ListAttemptsByRequestID(r.Context(), jobID)
		if err != nil {
			logger.Error("failed to fetch attempts", "job_id", jobID, "error", err)
			writeJSONError(w, http.StatusInternalServerError, "failed to fetch job attempts")
			return
		}
		if attempts == nil {
			attempts = []*model.Attempt{}
		}

		resp := JobInspectionResponse{
			ID:            req.ID,
			RouteID:       req.RouteID,
			Method:        req.Method,
			URL:           req.URL,
			Status:        string(req.Status),
			AttemptsCount: req.AttemptsCount,
			NextAttemptAt: req.NextAttemptAt,
			LastError:     req.LastError,
			CreatedAt:     req.CreatedAt,
			UpdatedAt:     req.UpdatedAt,
			Attempts:      attempts,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}
}
