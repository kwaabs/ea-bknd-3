package comments

import (
	"bknd-3/internal/httpx"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

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

// userIDFromContext extracts the authenticated user's UUID from the request context.
// Adjust the key to match whatever your auth middleware uses.
func userIDFromContext(r *http.Request) (uuid.UUID, bool) {
	val := r.Context().Value("userID")
	if val == nil {
		return uuid.Nil, false
	}
	id, ok := val.(uuid.UUID)
	return id, ok
}

// ─── GET /api/comments ────────────────────────────────────────────────────────

func (h *Handler) ListComments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	params := CommentListParams{
		Page:  parseIntDefault(q.Get("page"), 1),
		Limit: parseIntDefault(q.Get("limit"), 20),
	}

	if raw := q.Get("resolved"); raw != "" {
		b := raw == "true" || raw == "1"
		params.Resolved = &b
	}

	comments, total, err := h.service.ListComments(r.Context(), params)
	if err != nil {
		h.logr.Error("ListComments failed", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "internal server error"})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"data":  comments,
		"total": total,
		"page":  params.Page,
	})
}

// ─── GET /api/comments/:id/replies ───────────────────────────────────────────

func (h *Handler) ListReplies(w http.ResponseWriter, r *http.Request) {
	parentID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid comment id"})
		return
	}

	replies, err := h.service.ListReplies(r.Context(), parentID)
	if err != nil {
		h.logr.Error("ListReplies failed", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "internal server error"})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"data": replies})
}

// ─── POST /api/comments ───────────────────────────────────────────────────────

func (h *Handler) CreateComment(w http.ResponseWriter, r *http.Request) {
	authorID, ok := userIDFromContext(r)
	if !ok {
		httpx.JSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}

	var body struct {
		Body     string      `json:"body"`
		Mentions []uuid.UUID `json:"mentions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Body == "" {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "body is required"})
		return
	}

	comment, err := h.service.CreateComment(r.Context(), authorID, body.Body, body.Mentions, nil)
	if err != nil {
		h.logr.Error("CreateComment failed", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "internal server error"})
		return
	}

	httpx.JSON(w, http.StatusCreated, comment)
}

// ─── POST /api/comments/:id/replies ──────────────────────────────────────────

func (h *Handler) CreateReply(w http.ResponseWriter, r *http.Request) {
	authorID, ok := userIDFromContext(r)
	if !ok {
		httpx.JSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}

	parentID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid comment id"})
		return
	}

	var body struct {
		Body     string      `json:"body"`
		Mentions []uuid.UUID `json:"mentions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Body == "" {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "body is required"})
		return
	}

	comment, err := h.service.CreateComment(r.Context(), authorID, body.Body, body.Mentions, &parentID)
	if err != nil {
		if err.Error() == "replies can only be one level deep" {
			httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrNotFound) {
			httpx.JSON(w, http.StatusNotFound, map[string]any{"error": "parent comment not found"})
			return
		}
		h.logr.Error("CreateReply failed", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "internal server error"})
		return
	}

	httpx.JSON(w, http.StatusCreated, comment)
}

// ─── PATCH /api/comments/:id ──────────────────────────────────────────────────

func (h *Handler) EditComment(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r)
	if !ok {
		httpx.JSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}

	commentID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid comment id"})
		return
	}

	var body struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Body == "" {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "body is required"})
		return
	}

	comment, err := h.service.EditComment(r.Context(), commentID, userID, body.Body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.JSON(w, http.StatusNotFound, map[string]any{"error": "comment not found"})
			return
		}
		if errors.Is(err, ErrForbidden) {
			httpx.JSON(w, http.StatusForbidden, map[string]any{"error": "you can only edit your own comments"})
			return
		}
		h.logr.Error("EditComment failed", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "internal server error"})
		return
	}

	httpx.JSON(w, http.StatusOK, comment)
}

// ─── DELETE /api/comments/:id ─────────────────────────────────────────────────

func (h *Handler) DeleteComment(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r)
	if !ok {
		httpx.JSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}

	commentID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid comment id"})
		return
	}

	err = h.service.DeleteComment(r.Context(), commentID, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.JSON(w, http.StatusNotFound, map[string]any{"error": "comment not found"})
			return
		}
		if errors.Is(err, ErrForbidden) {
			httpx.JSON(w, http.StatusForbidden, map[string]any{"error": "you can only delete your own comments"})
			return
		}
		h.logr.Error("DeleteComment failed", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "internal server error"})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"success": true})
}

// ─── POST /api/comments/:id/reactions ────────────────────────────────────────

func (h *Handler) ToggleReaction(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r)
	if !ok {
		httpx.JSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}

	commentID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid comment id"})
		return
	}

	var body struct {
		Emoji string `json:"emoji"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Emoji == "" {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "emoji is required"})
		return
	}

	comment, err := h.service.ToggleReaction(r.Context(), commentID, userID, body.Emoji)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.JSON(w, http.StatusNotFound, map[string]any{"error": "comment not found"})
			return
		}
		h.logr.Error("ToggleReaction failed", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "internal server error"})
		return
	}

	httpx.JSON(w, http.StatusOK, comment)
}

// ─── PATCH /api/comments/:id/resolve ─────────────────────────────────────────

func (h *Handler) ResolveComment(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r)
	if !ok {
		httpx.JSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}

	commentID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid comment id"})
		return
	}

	var body struct {
		Resolved bool `json:"resolved"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}

	comment, err := h.service.SetResolved(r.Context(), commentID, userID, body.Resolved)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.JSON(w, http.StatusNotFound, map[string]any{"error": "comment not found"})
			return
		}
		h.logr.Error("ResolveComment failed", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "internal server error"})
		return
	}

	httpx.JSON(w, http.StatusOK, comment)
}

// ─── GET /api/users/mentionable ───────────────────────────────────────────────

func (h *Handler) GetMentionableUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.service.GetMentionableUsers(r.Context())
	if err != nil {
		h.logr.Error("GetMentionableUsers failed", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "internal server error"})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"data": users})
}

// ─── local helpers ────────────────────────────────────────────────────────────

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
