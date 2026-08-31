package salessummary

import (
	"net/http"
	"strings"

	"bknd-3/internal/httpx"

	"go.uber.org/zap"
)

type Handler struct {
	svc *Service
	log *zap.Logger
}

func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	category := Category(strings.ToLower(strings.TrimSpace(q.Get("category"))))
	if category != Prepaid && category != Postpaid {
		httpx.Error(w, http.StatusBadRequest, "category must be 'prepaid' or 'postpaid'")
		return
	}

	from, err := httpx.Date(q, "dateFrom")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid date: use YYYY-MM-DD")
		return
	}
	to, err := httpx.Date(q, "dateTo")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid date: use YYYY-MM-DD")
		return
	}

	groupBy := strings.ToLower(strings.TrimSpace(q.Get("groupBy")))

	f := CommonFilters{
		DateFrom: from,
		DateTo:   to,
		Region:   httpx.CSV(q, "region"),
		District: httpx.CSV(q, "district"),
	}

	result, err := h.svc.Summary(r.Context(), category, f, groupBy)
	if err != nil {
		h.log.Error("sales summary failed", zap.Error(err), zap.String("category", string(category)))
		httpx.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}
