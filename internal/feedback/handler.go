package feedback

import (
	"bknd-3/internal/httpx"
	"encoding/json"
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

// CreateFeedback handles POST /api/v1/feedback
func (h *Handler) CreateFeedback(w http.ResponseWriter, r *http.Request) {
	var req CreateFeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.JSON(w, http.StatusBadRequest, MessageResponse{
			Success: false,
			Message: "Invalid request body",
		})
		return
	}

	// Validate required fields
	if req.Email == "" {
		httpx.JSON(w, http.StatusBadRequest, MessageResponse{
			Success: false,
			Message: "Email is required",
		})
		return
	}

	if req.Comments == "" {
		httpx.JSON(w, http.StatusBadRequest, MessageResponse{
			Success: false,
			Message: "Comments are required",
		})
		return
	}

	// Validate type for top-level comments
	if req.ParentID == nil {
		if req.Type == "" {
			httpx.JSON(w, http.StatusBadRequest, MessageResponse{
				Success: false,
				Message: "Type is required for top-level comments",
			})
			return
		}
		if req.Type != "COMMENT" && req.Type != "COMPLAINT" {
			httpx.JSON(w, http.StatusBadRequest, MessageResponse{
				Success: false,
				Message: "Type must be COMMENT or COMPLAINT",
			})
			return
		}
	}

	if err := h.service.CreateFeedback(r.Context(), &req); err != nil {
		h.logr.Error("failed to create feedback", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, MessageResponse{
			Success: false,
			Message: "Failed to create feedback",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, MessageResponse{
		Success: true,
		Message: "Feedback submitted",
	})
}

// GetAllFeedback handles GET /api/v1/feedback
func (h *Handler) GetAllFeedback(w http.ResponseWriter, r *http.Request) {
	// Parse pagination parameters
	limit := 100
	offset := 0

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	feedbacks, total, err := h.service.GetAllFeedback(r.Context(), limit, offset)
	if err != nil {
		h.logr.Error("failed to fetch feedback", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, MessageResponse{
			Success: false,
			Message: "Failed to retrieve feedback",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, FeedbackListResponse{
		Success: true,
		Count:   total,
		Limit:   limit,
		Offset:  offset,
		Data:    feedbacks,
	})
}

// GetFeedbackByEmail handles GET /api/v1/feedback/user/{email}
func (h *Handler) GetFeedbackByEmail(w http.ResponseWriter, r *http.Request) {
	email := chi.URLParam(r, "email")
	if email == "" {
		httpx.JSON(w, http.StatusBadRequest, MessageResponse{
			Success: false,
			Message: "Email is required",
		})
		return
	}

	feedbacks, err := h.service.GetFeedbackByEmail(r.Context(), email)
	if err != nil {
		h.logr.Error("failed to fetch feedback by email", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, MessageResponse{
			Success: false,
			Message: "Failed to retrieve feedback",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, FeedbackListResponse{
		Success: true,
		Count:   len(feedbacks),
		Limit:   100,
		Offset:  0,
		Data:    feedbacks,
	})
}

// DeleteFeedback handles DELETE /api/v1/feedback/{id}
func (h *Handler) DeleteFeedback(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, MessageResponse{
			Success: false,
			Message: "Invalid feedback ID",
		})
		return
	}

	if err := h.service.DeleteFeedback(r.Context(), id); err != nil {
		h.logr.Error("failed to delete feedback", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, MessageResponse{
			Success: false,
			Message: "Failed to delete feedback",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, MessageResponse{
		Success: true,
		Message: "Deleted successfully",
	})
}

// UpdateFeedbackStatus handles PATCH /api/v1/feedback/{id}/status
func (h *Handler) UpdateFeedbackStatus(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, MessageResponse{
			Success: false,
			Message: "Invalid feedback ID",
		})
		return
	}

	var req UpdateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.JSON(w, http.StatusBadRequest, MessageResponse{
			Success: false,
			Message: "Invalid request body",
		})
		return
	}

	if req.Status == "" {
		httpx.JSON(w, http.StatusBadRequest, MessageResponse{
			Success: false,
			Message: "Status is required",
		})
		return
	}

	// Validate status against enum values
	validStatuses := map[string]bool{
		StatusPending:    true,
		StatusInProgress: true,
		StatusResolved:   true,
	}

	if !validStatuses[req.Status] {
		httpx.JSON(w, http.StatusBadRequest, MessageResponse{
			Success: false,
			Message: "Status must be PENDING, IN_PROGRESS, or RESOLVED",
		})
		return
	}

	feedback, err := h.service.UpdateFeedbackStatus(r.Context(), id, req.Status)
	if err != nil {
		h.logr.Error("failed to update feedback status", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, MessageResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	httpx.JSON(w, http.StatusOK, StatusUpdateResponse{
		Success: true,
		Message: "Status updated",
		Data:    feedback,
	})
}

// GetFeedbackByID retrieves a single feedback with its replies
func (h *Handler) GetFeedbackByID(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, MessageResponse{
			Success: false,
			Message: "Invalid feedback ID",
		})
		return
	}

	feedback, err := h.service.GetFeedbackByID(r.Context(), id)
	if err != nil {
		h.logr.Error("failed to fetch feedback", zap.Error(err))
		httpx.JSON(w, http.StatusNotFound, MessageResponse{
			Success: false,
			Message: "Feedback not found",
		})
		return
	}

	// For JSON response, nil pointers will be omitted or shown as null
	// If you want to omit status for replies completely, you can add omitempty
	httpx.JSON(w, http.StatusOK, FeedbackResponse{
		Success: true,
		Data:    feedback,
	})
}
