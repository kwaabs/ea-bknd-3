package pnsconsumption

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

// parseFilters is the single place query params become FilterParams.
// region/district/tariff are accepted under those names (not
// regionId/districtId/tariffCategory) for consistency with every other
// source's query params, even though the values themselves are opaque
// codes here rather than names — see the package doc comment.
func parseFilters(q url.Values) (FilterParams, error) {
	from, err := httpx.Date(q, "dateFrom")
	if err != nil {
		return FilterParams{}, err
	}
	to, err := httpx.Date(q, "dateTo")
	if err != nil {
		return FilterParams{}, err
	}
	return FilterParams{
		RegionID:       httpx.CSV(q, "region"),
		DistrictID:     httpx.CSV(q, "district"),
		TariffCategory: httpx.CSV(q, "tariff"),
		BillMonth:      httpx.CSV(q, "billMonth"),
		ServiceID:      httpx.CSV(q, "serviceId"),
		Search:         q.Get("search"),
		DateFrom:       from,
		DateTo:         to,
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
		h.log.Error("pns consumption detail failed", zap.Error(err))
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
		h.log.Error("pns consumption aggregate failed", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}
