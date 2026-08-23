package notifyemail

import (
	"encoding/json"
	"net/http"
	"strings"

	"bknd-3/internal/httpx"
	"bknd-3/internal/middleware"
	model "bknd-3/internal/models"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
	"go.uber.org/zap"
)

type Handler struct {
	service *Service
	db      *bun.DB
	logr    *zap.Logger
}

func NewHandler(svc *Service, db *bun.DB, logr *zap.Logger) *Handler {
	return &Handler{service: svc, db: db, logr: logr}
}

// requireNotifyEmail resolves the JWT-authenticated caller's email and
// checks it against the allowlist — managing who's on the allowlist is
// itself gated by already being on it, same self-referential model as
// meters.Handler.requireNotifyEmail (which this table now also backs).
func (h *Handler) requireNotifyEmail(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, _ := r.Context().Value(middleware.ContextUserIDKey).(string)
	if userID == "" {
		httpx.JSON(w, http.StatusUnauthorized, "authentication required")
		return "", false
	}
	var u model.User
	if err := h.db.NewSelect().Model(&u).Where("id = ?", userID).Scan(r.Context()); err != nil {
		h.logr.Error("failed to resolve caller", zap.Error(err), zap.String("user_id", userID))
		httpx.JSON(w, http.StatusInternalServerError, "failed to verify permissions")
		return "", false
	}
	allowed, err := h.service.IsAllowed(r.Context(), u.Email)
	if err != nil {
		h.logr.Error("failed to check notify-emails allowlist", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, "failed to verify permissions")
		return "", false
	}
	if !allowed {
		httpx.JSON(w, http.StatusForbidden, "you are not allowed to manage the notify-emails allowlist")
		return "", false
	}
	return u.Email, true
}

// Me handles GET /api/v1/notify-emails/me — no admin gate beyond being
// authenticated at all. This is what the frontend calls instead of
// checking its own hardcoded array, to decide whether to show admin-only
// nav/UI for the currently signed-in user.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.ContextUserIDKey).(string)
	if userID == "" {
		httpx.JSON(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var u model.User
	if err := h.db.NewSelect().Model(&u).Where("id = ?", userID).Scan(r.Context()); err != nil {
		h.logr.Error("failed to resolve caller", zap.Error(err), zap.String("user_id", userID))
		httpx.JSON(w, http.StatusInternalServerError, "failed to resolve caller")
		return
	}
	allowed, err := h.service.IsAllowed(r.Context(), u.Email)
	if err != nil {
		h.logr.Error("failed to check notify-emails allowlist", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, "failed to check allowlist")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"allowed": allowed})
}

// List handles GET /api/v1/notify-emails — admin-gated, the full list.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireNotifyEmail(w, r); !ok {
		return
	}
	rows, err := h.service.List(r.Context())
	if err != nil {
		h.logr.Error("failed to list notify emails", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, "failed to list allowlist")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

type addReq struct {
	Email string `json:"email"`
}

// Add handles POST /api/v1/notify-emails — admin-gated, add an email.
func (h *Handler) Add(w http.ResponseWriter, r *http.Request) {
	callerEmail, ok := h.requireNotifyEmail(w, r)
	if !ok {
		return
	}
	var req addReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Email) == "" {
		httpx.JSON(w, http.StatusBadRequest, "email is required")
		return
	}
	row, err := h.service.Add(r.Context(), req.Email, callerEmail)
	if err != nil {
		h.logr.Error("failed to add notify email", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, "failed to add email")
		return
	}
	httpx.JSON(w, http.StatusCreated, row)
}

// Remove handles DELETE /api/v1/notify-emails/{email} — admin-gated.
// Refuses to remove the last remaining allowlisted email: that would lock
// everyone, including whoever's removing it, out of every admin-gated
// route with no way back in short of a direct DB edit.
func (h *Handler) Remove(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireNotifyEmail(w, r); !ok {
		return
	}
	count, err := h.service.Count(r.Context())
	if err != nil {
		h.logr.Error("failed to count notify emails", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, "failed to verify allowlist size")
		return
	}
	if count <= 1 {
		httpx.JSON(w, http.StatusConflict, "cannot remove the last remaining notify-email — this would lock everyone out of admin routes")
		return
	}
	email := chi.URLParam(r, "email")
	if err := h.service.Remove(r.Context(), email); err != nil {
		h.logr.Error("failed to remove notify email", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, "failed to remove email")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
