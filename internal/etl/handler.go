package etl

import (
	"bknd-3/internal/httpx"
	"bknd-3/internal/middleware"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

type Handler struct {
	service *Service
	logr    *zap.Logger
}

func NewHandler(svc *Service, logr *zap.Logger) *Handler {
	return &Handler{service: svc, logr: logr}
}

// requireNotifyEmail mirrors meters.Handler.requireNotifyEmail exactly —
// same allowlist gate, same shape, duplicated per package rather than
// shared (this codebase's existing convention; see that method's comment).
func (h *Handler) requireNotifyEmail(w http.ResponseWriter, r *http.Request) bool {
	userID, _ := r.Context().Value(middleware.ContextUserIDKey).(string)
	if userID == "" {
		httpx.JSON(w, http.StatusUnauthorized, "authentication required")
		return false
	}
	if _, err := h.service.ResolveNotifyEmail(r.Context(), userID); err != nil {
		if errors.Is(err, ErrForbidden) {
			httpx.JSON(w, http.StatusForbidden, "you are not allowed to manage the ETL engine")
			return false
		}
		h.logr.Error("failed to resolve notify email", zap.Error(err), zap.String("user_id", userID))
		httpx.JSON(w, http.StatusInternalServerError, "failed to verify permissions")
		return false
	}
	return true
}

func writeServiceErr(w http.ResponseWriter, logr *zap.Logger, action string, err error) {
	if errors.Is(err, ErrNotFound) {
		httpx.JSON(w, http.StatusNotFound, "not found")
		return
	}
	logr.Error(action, zap.Error(err))
	httpx.JSON(w, http.StatusBadRequest, err.Error())
}

// ---------------------------------------------------------------------
// Sources
// ---------------------------------------------------------------------

func (h *Handler) ListSources(w http.ResponseWriter, r *http.Request) {
	if !h.requireNotifyEmail(w, r) {
		return
	}
	sources, err := h.service.ListSources(r.Context())
	if err != nil {
		writeServiceErr(w, h.logr, "failed to list etl sources", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": sources})
}

func (h *Handler) CreateSource(w http.ResponseWriter, r *http.Request) {
	if !h.requireNotifyEmail(w, r) {
		return
	}
	var in SourceInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.JSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	src, err := h.service.CreateSource(r.Context(), in)
	if err != nil {
		writeServiceErr(w, h.logr, "failed to create etl source", err)
		return
	}
	httpx.JSON(w, http.StatusCreated, src)
}

func (h *Handler) UpdateSource(w http.ResponseWriter, r *http.Request) {
	if !h.requireNotifyEmail(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var in SourceInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.JSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	src, err := h.service.UpdateSource(r.Context(), id, in)
	if err != nil {
		writeServiceErr(w, h.logr, "failed to update etl source", err)
		return
	}
	httpx.JSON(w, http.StatusOK, src)
}

func (h *Handler) DeleteSource(w http.ResponseWriter, r *http.Request) {
	if !h.requireNotifyEmail(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.service.DeleteSource(r.Context(), id); err != nil {
		writeServiceErr(w, h.logr, "failed to delete etl source", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) TestSourceConnection(w http.ResponseWriter, r *http.Request) {
	if !h.requireNotifyEmail(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	elapsed, err := h.service.TestConnection(r.Context(), id)
	if err != nil {
		httpx.JSON(w, http.StatusOK, map[string]any{
			"ok":         false,
			"error":      err.Error(),
			"elapsed_ms": elapsed.Milliseconds(),
		})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"elapsed_ms": elapsed.Milliseconds(),
	})
}

// TestSourceConnectionDraft is TestSourceConnection's counterpart for a
// source that hasn't been saved yet — the "does this work" check while
// filling out the Add Source form, before it's ever a row in
// app.etl_sources.
func (h *Handler) TestSourceConnectionDraft(w http.ResponseWriter, r *http.Request) {
	if !h.requireNotifyEmail(w, r) {
		return
	}
	var in SourceInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.JSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	elapsed, err := h.service.TestConnectionDraft(r.Context(), in)
	if err != nil {
		httpx.JSON(w, http.StatusOK, map[string]any{
			"ok":         false,
			"error":      err.Error(),
			"elapsed_ms": elapsed.Milliseconds(),
		})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"elapsed_ms": elapsed.Milliseconds(),
	})
}

// ---------------------------------------------------------------------
// Jobs
// ---------------------------------------------------------------------

func (h *Handler) ListJobs(w http.ResponseWriter, r *http.Request) {
	if !h.requireNotifyEmail(w, r) {
		return
	}
	jobs, err := h.service.ListJobs(r.Context())
	if err != nil {
		writeServiceErr(w, h.logr, "failed to list etl jobs", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": jobs})
}

func (h *Handler) CreateJob(w http.ResponseWriter, r *http.Request) {
	if !h.requireNotifyEmail(w, r) {
		return
	}
	var in JobInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.JSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	job, err := h.service.CreateJob(r.Context(), in)
	if err != nil {
		writeServiceErr(w, h.logr, "failed to create etl job", err)
		return
	}
	httpx.JSON(w, http.StatusCreated, job)
}

func (h *Handler) UpdateJob(w http.ResponseWriter, r *http.Request) {
	if !h.requireNotifyEmail(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var in JobInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.JSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	job, err := h.service.UpdateJob(r.Context(), id, in)
	if err != nil {
		writeServiceErr(w, h.logr, "failed to update etl job", err)
		return
	}
	httpx.JSON(w, http.StatusOK, job)
}

func (h *Handler) DeleteJob(w http.ResponseWriter, r *http.Request) {
	if !h.requireNotifyEmail(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.service.DeleteJob(r.Context(), id); err != nil {
		writeServiceErr(w, h.logr, "failed to delete etl job", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) RunJobNow(w http.ResponseWriter, r *http.Request) {
	if !h.requireNotifyEmail(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	runID, err := h.service.RunNow(r.Context(), id)
	if err != nil {
		writeServiceErr(w, h.logr, "failed to trigger etl job run", err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{"run_id": runID})
}

func (h *Handler) ListJobRuns(w http.ResponseWriter, r *http.Request) {
	if !h.requireNotifyEmail(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := h.service.ListRuns(r.Context(), id, limit)
	if err != nil {
		writeServiceErr(w, h.logr, "failed to list etl job runs", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": runs})
}

func (h *Handler) GetJobState(w http.ResponseWriter, r *http.Request) {
	if !h.requireNotifyEmail(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	state, err := h.service.GetJobState(r.Context(), id)
	if err != nil {
		writeServiceErr(w, h.logr, "failed to load etl job state", err)
		return
	}
	httpx.JSON(w, http.StatusOK, state)
}

// ---------------------------------------------------------------------
// Ad-hoc test query
// ---------------------------------------------------------------------

type testQueryRequest struct {
	SourceID string `json:"source_id"`
	Query    string `json:"query"`
}

func (h *Handler) TestQuery(w http.ResponseWriter, r *http.Request) {
	if !h.requireNotifyEmail(w, r) {
		return
	}
	var in testQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.JSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := h.service.TestQuery(r.Context(), in.SourceID, in.Query)
	if err != nil {
		writeServiceErr(w, h.logr, "failed to run etl test query", err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}
