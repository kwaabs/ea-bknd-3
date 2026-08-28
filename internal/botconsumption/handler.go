package botconsumption

import (
	"net/http"
	"net/url"

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

// parseFilters is the single place query params become FilterParams. No
// error path — unlike Zeus/MMS there are no date params to validate.
func parseFilters(q url.Values) FilterParams {
	return FilterParams{
		Region:      httpx.CSV(q, "region"),
		District:    httpx.CSV(q, "district"),
		Tariff:      httpx.CSV(q, "tariff"),
		BillMonth:   httpx.CSV(q, "billMonth"),
		MeterNumber: httpx.CSV(q, "meterNumber"),
		Search:      q.Get("search"),
	}
}

func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params := parseFilters(q)
	pg := httpx.ParsePagination(q, 50, 500)

	result, err := h.svc.Detail(r.Context(), params, pg)
	if err != nil {
		h.log.Error("bot consumption detail failed", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

func (h *Handler) Aggregate(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params := parseFilters(q)

	groupBy := httpx.CSV(q, "groupBy")
	if len(groupBy) == 0 {
		groupBy = []string{"region"}
	}

	result, err := h.svc.Aggregate(r.Context(), params, groupBy)
	if err != nil {
		h.log.Error("bot consumption aggregate failed", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}
