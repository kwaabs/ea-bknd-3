package alphaconsumption

import (
	"net/http"
	"net/url"
	"strconv"
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

// parseEnergyCodes parses a comma-separated list of integer energy codes —
// unlike httpx.CSV's plain string lists, these need to bind as integers
// for the "energycode IN (?)" filter in Service.base.
func parseEnergyCodes(q url.Values) []int {
	raw := httpx.CSV(q, "energyCode")
	if len(raw) == 0 {
		return nil
	}
	out := make([]int, 0, len(raw))
	for _, s := range raw {
		if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			out = append(out, v)
		}
	}
	return out
}

// parseFilters is the single place query params become FilterParams.
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
		Substation: httpx.CSV(q, "substation"),
		EnergyCode: parseEnergyCodes(q),
		Search:     q.Get("search"),
		DateFrom:   from,
		DateTo:     to,
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

	result, err := h.svc.Detail(r.Context(), params, pg, q.Get("sortBy"), q.Get("sortOrder"))
	if err != nil {
		h.log.Error("alpha consumption detail failed", zap.Error(err))
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
		groupBy = []string{"substation"}
	}

	result, err := h.svc.Aggregate(r.Context(), params, groupBy)
	if err != nil {
		h.log.Error("alpha consumption aggregate failed", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}
