package zeusbilling

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

// intCSV parses a comma-separated query param into ints, skipping values
// that don't parse. Mirrors httpx.CSV's shape but for the two integer
// columns (billingyear/billingmonth) this table has instead of a single
// formatted billmonth string.
func intCSV(q url.Values, key string) []int {
	vals := httpx.CSV(q, key)
	if len(vals) == 0 {
		return nil
	}
	out := make([]int, 0, len(vals))
	for _, v := range vals {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// parseFilters is the single place query params become FilterParams.
func parseFilters(q url.Values) (FilterParams, error) {
	lastPaymentFrom, err := httpx.Date(q, "lastPaymentDateFrom")
	if err != nil {
		return FilterParams{}, err
	}
	lastPaymentTo, err := httpx.Date(q, "lastPaymentDateTo")
	if err != nil {
		return FilterParams{}, err
	}
	createdFrom, err := httpx.Date(q, "createdAtFrom")
	if err != nil {
		return FilterParams{}, err
	}
	createdTo, err := httpx.Date(q, "createdAtTo")
	if err != nil {
		return FilterParams{}, err
	}
	// Make the "To" dates inclusive of the whole end day.
	if !lastPaymentTo.IsZero() {
		lastPaymentTo = lastPaymentTo.AddDate(0, 0, 1).Add(-time.Microsecond)
	}
	if !createdTo.IsZero() {
		createdTo = createdTo.AddDate(0, 0, 1).Add(-time.Microsecond)
	}

	return FilterParams{
		RegionName:          httpx.CSV(q, "region"),
		DistrictName:        httpx.CSV(q, "district"),
		TariffClassCode:     httpx.CSV(q, "tariffClassCode"),
		ServiceClass:        httpx.CSV(q, "serviceClass"),
		AccountType:         httpx.CSV(q, "accountType"),
		BillStatus:          httpx.CSV(q, "billStatus"),
		BillConsumptionType: httpx.CSV(q, "billConsumptionType"),
		MeterModelType:      httpx.CSV(q, "meterModelType"),
		ServicePointStatus:  httpx.CSV(q, "servicePointStatus"),
		BillingYear:         intCSV(q, "billingYear"),
		BillingMonth:        intCSV(q, "billingMonth"),
		IsSensitive:         q.Get("isSensitive"),
		Search:              q.Get("search"),
		AccountCode:         httpx.CSV(q, "accountCode"),
		ServicePointCode:    httpx.CSV(q, "servicePointCode"),
		MeterCode:           httpx.CSV(q, "meterCode"),
		LastPaymentDateFrom: lastPaymentFrom,
		LastPaymentDateTo:   lastPaymentTo,
		CreatedAtFrom:       createdFrom,
		CreatedAtTo:         createdTo,
	}, nil
}

func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	params, err := parseFilters(q)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid date: use YYYY-MM-DD")
		return
	}
	// Per-request page size is capped; navigate ?page= to walk the full result set.
	pg := httpx.ParsePagination(q, 50, 500)
	sortBy := q.Get("sortBy")
	sortDir := q.Get("sortDir")

	result, err := h.svc.Detail(r.Context(), params, pg, sortBy, sortDir)
	if err != nil {
		h.log.Error("zeus billing detail failed", zap.Error(err))
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
		h.log.Error("zeus billing aggregate failed", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}
