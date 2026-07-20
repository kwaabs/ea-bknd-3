package loginstats

import (
	"bknd-3/internal/httpx"
	"net/http"
	"time"

	"go.uber.org/zap"
)

type Handler struct {
	service *Service
	logr    *zap.Logger
}

func NewHandler(svc *Service, logr *zap.Logger) *Handler {
	return &Handler{service: svc, logr: logr}
}

// GetStats handles GET /api/v1/admin/login-stats?from=YYYY-MM-DD&to=YYYY-MM-DD
// Defaults to the last 30 days when no range is given.
func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	to := time.Now().UTC().Add(24 * time.Hour)
	from := to.AddDate(0, 0, -30)

	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			from = t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			to = t.Add(24 * time.Hour)
		}
	}

	stats, err := h.service.GetStats(r.Context(), from, to)
	if err != nil {
		h.logr.Error("failed to fetch login stats", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Failed to retrieve login stats",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, stats)
}
