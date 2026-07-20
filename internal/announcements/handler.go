package announcements

import (
	"bknd-3/internal/httpx"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type Handler struct {
	service *Service
	logr    *zap.Logger
}

func NewHandler(svc *Service, logr *zap.Logger) *Handler {
	return &Handler{service: svc, logr: logr}
}

// ListActive handles GET /api/v1/announcements — visible to everyone.
func (h *Handler) ListActive(w http.ResponseWriter, r *http.Request) {
	rows, err := h.service.ListActive(r.Context(), 50)
	if err != nil {
		h.logr.Error("ListActive failed", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, MessageResponse{
			Success: false,
			Message: "Failed to retrieve announcements",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, ListResponse{
		Success: true,
		Count:   len(rows),
		Data:    rows,
	})
}

// Create handles POST /api/v1/announcements — notify-email allowlist only.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateAnnouncementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.JSON(w, http.StatusBadRequest, MessageResponse{
			Success: false,
			Message: "Invalid request body",
		})
		return
	}

	row, err := h.service.Create(r.Context(), &req)
	if err != nil {
		if errors.Is(err, ErrBadRequest) {
			httpx.JSON(w, http.StatusBadRequest, MessageResponse{
				Success: false,
				Message: "body and author_email are required",
			})
			return
		}
		if errors.Is(err, ErrForbidden) {
			httpx.JSON(w, http.StatusForbidden, MessageResponse{
				Success: false,
				Message: "You are not allowed to post announcements",
			})
			return
		}
		h.logr.Error("Create announcement failed", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, MessageResponse{
			Success: false,
			Message: "Failed to create announcement",
		})
		return
	}

	httpx.JSON(w, http.StatusCreated, SingleResponse{
		Success: true,
		Data:    row,
	})
}

// SoftDelete handles DELETE /api/v1/announcements/{id} — notify-email allowlist only.
func (h *Handler) SoftDelete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, MessageResponse{
			Success: false,
			Message: "Invalid announcement id",
		})
		return
	}

	var req DeleteAnnouncementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AuthorEmail == "" {
		// Also accept email via query for clients that can't send DELETE bodies.
		if q := r.URL.Query().Get("author_email"); q != "" {
			req.AuthorEmail = q
		}
	}
	if req.AuthorEmail == "" {
		httpx.JSON(w, http.StatusBadRequest, MessageResponse{
			Success: false,
			Message: "author_email is required",
		})
		return
	}

	if err := h.service.SoftDelete(r.Context(), id, req.AuthorEmail); err != nil {
		if errors.Is(err, ErrForbidden) {
			httpx.JSON(w, http.StatusForbidden, MessageResponse{
				Success: false,
				Message: "You are not allowed to remove announcements",
			})
			return
		}
		if errors.Is(err, ErrNotFound) {
			httpx.JSON(w, http.StatusNotFound, MessageResponse{
				Success: false,
				Message: "Announcement not found",
			})
			return
		}
		h.logr.Error("SoftDelete announcement failed", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, MessageResponse{
			Success: false,
			Message: "Failed to delete announcement",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, MessageResponse{
		Success: true,
		Message: "Announcement removed",
	})
}
