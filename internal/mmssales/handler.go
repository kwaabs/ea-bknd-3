package mmssales

import (
	"net/http"
	"net/url"
	"time"

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

// parseFilters is the single place query params become FilterParams —
// previously duplicated verbatim in GetDetail and GetAggregate.
func parseFilters(q url.Values) (FilterParams, error) {
	from, err := httpx.Date(q, "dateFrom", "dateTimeFrom")
	if err != nil {
		return FilterParams{}, err
	}
	to, err := httpx.Date(q, "dateTo", "dateTimeTo")
	if err != nil {
		return FilterParams{}, err
	}
	// Make dateTo inclusive of the whole end day. The old code compared
	// date_time <= midnight of dateTo, silently excluding almost all of that
	// day's rows — and the daily summary is whole-day by nature, so this also
	// keeps the raw and summary aggregate paths numerically identical.
	if !to.IsZero() {
		to = to.AddDate(0, 0, 1).Add(-time.Microsecond)
	}
	return FilterParams{
		Region:        httpx.CSV(q, "region"),
		District:      httpx.CSV(q, "district"),
		ContractType:  httpx.CSV(q, "contractType"),
		Tariff:        httpx.CSV(q, "tariff"),
		Manufacturer:  httpx.CSV(q, "manufacturer"),
		Model:         httpx.CSV(q, "model"),
		AccountNumber: httpx.CSV(q, "accountNumber"),
		MeterNumber:   httpx.CSV(q, "meterNumber"),
		Search:        q.Get("search"),
		DateTimeFrom:  from,
		DateTimeTo:    to,
	}, nil
}

func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	params, err := parseFilters(q)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid date: use YYYY-MM-DD")
		return
	}
	pg := httpx.ParsePagination(q, 50, 500)

	result, err := h.svc.Detail(r.Context(), params, pg)
	if err != nil {
		h.log.Error("mms sales detail failed", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

func (h *Handler) Aggregate(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	params, err := parseFilters(q)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid date: use YYYY-MM-DD")
		return
	}

	groupBy := httpx.CSV(q, "groupBy")
	if len(groupBy) == 0 {
		groupBy = []string{"region"}
	}

	result, err := h.svc.Aggregate(r.Context(), params, groupBy)
	if err != nil {
		h.log.Error("mms sales aggregate failed", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}
