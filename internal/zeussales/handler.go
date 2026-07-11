package zeussales

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
// previously duplicated verbatim in GetDetail and GetAggregate. Note: unlike
// the old GetAggregate handler, this also honors accountNumber and
// servicePointNumber on the aggregate path — the old code silently dropped
// them there, which looked like an oversight rather than intent.
func parseFilters(q url.Values) (FilterParams, error) {
	lastBillFrom, err := httpx.Date(q, "lastBillDateFrom")
	if err != nil {
		return FilterParams{}, err
	}
	lastBillTo, err := httpx.Date(q, "lastBillDateTo")
	if err != nil {
		return FilterParams{}, err
	}
	lastReadingFrom, err := httpx.Date(q, "lastReadingDateFrom")
	if err != nil {
		return FilterParams{}, err
	}
	lastReadingTo, err := httpx.Date(q, "lastReadingDateTo")
	if err != nil {
		return FilterParams{}, err
	}
	// Make the "To" dates inclusive of the whole end day. lastbilldate and
	// lastreadingdate are timestamp columns; comparing <= midnight silently
	// excluded almost all of that day's rows.
	if !lastBillTo.IsZero() {
		lastBillTo = lastBillTo.AddDate(0, 0, 1).Add(-time.Microsecond)
	}
	if !lastReadingTo.IsZero() {
		lastReadingTo = lastReadingTo.AddDate(0, 0, 1).Add(-time.Microsecond)
	}

	return FilterParams{
		RegionName:          httpx.CSV(q, "region"),
		DistrictName:        httpx.CSV(q, "district"),
		ServiceType:         httpx.CSV(q, "serviceType"),
		ServiceClass:        httpx.CSV(q, "serviceClass"),
		TariffClassCode:     httpx.CSV(q, "tariffClassCode"),
		CustomerType:        httpx.CSV(q, "customerType"),
		AccountType:         httpx.CSV(q, "accountType"),
		ContractStatus:      httpx.CSV(q, "contractStatus"),
		BillMonth:           httpx.CSV(q, "billMonth"),
		IsAMR:               q.Get("isAmr"),
		Search:              q.Get("search"),
		AccountNumber:       httpx.CSV(q, "accountNumber"),
		ServicePointNumber:  httpx.CSV(q, "servicePointNumber"),
		LastBillDateFrom:    lastBillFrom,
		LastBillDateTo:      lastBillTo,
		LastReadingDateFrom: lastReadingFrom,
		LastReadingDateTo:   lastReadingTo,
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
		h.log.Error("zeus sales detail failed", zap.Error(err))
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
		groupBy = []string{"regionname"}
	}

	result, err := h.svc.Aggregate(r.Context(), params, groupBy)
	if err != nil {
		h.log.Error("zeus sales aggregate failed", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}
