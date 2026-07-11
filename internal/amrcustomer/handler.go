package amrcustomer

import (
	"bknd-3/internal/httpx"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Handler struct {
	service *Service
	logr    *zap.Logger
}

func NewHandler(svc *Service, logr *zap.Logger) *Handler {
	return &Handler{service: svc, logr: logr}
}

// ===================================================
// HELPERS
// ===================================================

func amrSplitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func amrParseDates(r *http.Request) (time.Time, time.Time, error) {
	const layout = "2006-01-02"
	dateFrom, err := time.Parse(layout, r.URL.Query().Get("dateFrom"))
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	dateTo, err := time.Parse(layout, r.URL.Query().Get("dateTo"))
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return dateFrom, dateTo, nil
}

func buildAmrFilterParams(r *http.Request, dateFrom, dateTo time.Time) AmrReadingFilterParams {
	q := r.URL.Query()
	return AmrReadingFilterParams{
		DateFrom:       dateFrom,
		DateTo:         dateTo,
		MeterNumber:    amrSplitCSV(q.Get("meterNumber")),
		Regions:        amrSplitCSV(q.Get("region")),
		Districts:      amrSplitCSV(q.Get("district")),
		Communities:    amrSplitCSV(q.Get("community")),
		TariffClass:    amrSplitCSV(q.Get("tariffClass")),
		CustomerType:   amrSplitCSV(q.Get("customerType")),
		AccountType:    amrSplitCSV(q.Get("accountType")),
		ContractStatus: amrSplitCSV(q.Get("contractStatus")),
		ServiceType:    amrSplitCSV(q.Get("serviceType")),
		AccountNo:      amrSplitCSV(q.Get("accountNo")),
		SPN:            amrSplitCSV(q.Get("spn")),
	}
}

// ===================================================
// DAILY CONSUMPTION
// ===================================================

// GET /api/v1/amr/consumption/daily
func (h *Handler) GetDailyConsumption(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	dateFrom, dateTo, err := amrParseDates(r)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid date format, expected YYYY-MM-DD",
		})
		return
	}

	if dateTo.Before(dateFrom) {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "dateTo must be after dateFrom",
		})
		return
	}

	q := r.URL.Query()

	systemName := q.Get("systemName")
	if systemName != "" && systemName != "import_kwh" && systemName != "export_kwh" {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid systemName, must be import_kwh or export_kwh",
		})
		return
	}

	page := 1
	if p := q.Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	limit := 100
	if l := q.Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 500 {
			limit = v
		}
	}

	params := AmrDailyConsumptionQueryParams{
		AmrReadingFilterParams: buildAmrFilterParams(r, dateFrom, dateTo),
		Page:                   page,
		Limit:                  limit,
		SystemName:             systemName,
	}

	result, err := h.service.GetDailyConsumption(ctx, params)
	if err != nil {
		h.logr.Error("amr: failed to get daily consumption", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve daily consumption",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    result,
	})
}

// ===================================================
// AGGREGATED CONSUMPTION
// ===================================================

// GET /api/v1/amr/consumption/aggregated
func (h *Handler) GetAggregatedConsumption(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	dateFrom, dateTo, err := amrParseDates(r)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid date format, expected YYYY-MM-DD",
		})
		return
	}

	if dateTo.Before(dateFrom) {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "dateTo must be after dateFrom",
		})
		return
	}

	groupBy := q.Get("groupBy")
	if groupBy == "" {
		groupBy = "day"
	}

	validGroupBy := map[string]bool{
		"day":   true,
		"month": true,
		"year":  true,
	}
	if !validGroupBy[groupBy] {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid groupBy, must be one of: day, month, year",
		})
		return
	}

	additionalGroups := amrSplitCSV(q.Get("group"))
	params := buildAmrFilterParams(r, dateFrom, dateTo)

	results, err := h.service.GetAggregatedConsumption(ctx, params, groupBy, additionalGroups)
	if err != nil {
		h.logr.Error("amr: failed to get aggregated consumption", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve aggregated consumption",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    results,
	})
}

// ===================================================
// METER STATUS
// ===================================================

// GET /api/v1/amr/status
func (h *Handler) GetMeterStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	dateFrom, dateTo, err := amrParseDates(r)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid date format, expected YYYY-MM-DD",
		})
		return
	}

	if dateTo.Before(dateFrom) {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "dateTo must be after dateFrom",
		})
		return
	}

	params := buildAmrFilterParams(r, dateFrom, dateTo)

	results, err := h.service.GetMeterStatus(ctx, params)
	if err != nil {
		h.logr.Error("amr: failed to get meter status", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve meter status",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    results,
	})
}

// GET /api/v1/amr/status/summary
func (h *Handler) GetMeterStatusSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	dateFrom, dateTo, err := amrParseDates(r)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid date format, expected YYYY-MM-DD",
		})
		return
	}

	if dateTo.Before(dateFrom) {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "dateTo must be after dateFrom",
		})
		return
	}

	params := buildAmrFilterParams(r, dateFrom, dateTo)

	summary, err := h.service.GetMeterStatusSummary(ctx, params)
	if err != nil {
		h.logr.Error("amr: failed to get meter status summary", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve meter status summary",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    summary,
	})
}

// GET /api/v1/amr/status/timeline
func (h *Handler) GetMeterStatusTimeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	dateFrom, dateTo, err := amrParseDates(r)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid date format, expected YYYY-MM-DD",
		})
		return
	}

	if dateTo.Before(dateFrom) {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "dateTo must be after dateFrom",
		})
		return
	}

	params := buildAmrFilterParams(r, dateFrom, dateTo)

	timeline, err := h.service.GetMeterStatusTimeline(ctx, params)
	if err != nil {
		h.logr.Error("amr: failed to get meter status timeline", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve meter status timeline",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    timeline,
	})
}

// GET /api/v1/amr/status/details
func (h *Handler) GetMeterStatusDetails(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	dateFrom, dateTo, err := amrParseDates(r)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid date format, expected YYYY-MM-DD",
		})
		return
	}

	if dateTo.Before(dateFrom) {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "dateTo must be after dateFrom",
		})
		return
	}

	// Pagination
	page := 1
	if p := q.Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	limit := 50
	if l := q.Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 200 {
			limit = v
		}
	}

	// Status filter
	status := strings.ToUpper(strings.TrimSpace(q.Get("status")))
	if status != "" && status != "ONLINE" && status != "OFFLINE" {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid status, must be ONLINE or OFFLINE",
		})
		return
	}

	// Sort
	sortBy := q.Get("sortBy")
	validSortFields := map[string]bool{
		"meter_number": true,
		"uptime":       true,
		"consumption":  true,
		"":             true,
	}
	if !validSortFields[sortBy] {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid sortBy, must be one of: meter_number, uptime, consumption",
		})
		return
	}

	sortOrder := strings.ToLower(q.Get("sortOrder"))
	if sortOrder == "" {
		sortOrder = "desc"
	}
	if sortOrder != "asc" && sortOrder != "desc" {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid sortOrder, must be asc or desc",
		})
		return
	}

	params := AmrStatusDetailQueryParams{
		AmrReadingFilterParams: buildAmrFilterParams(r, dateFrom, dateTo),
		Page:                   page,
		Limit:                  limit,
		Search:                 q.Get("search"),
		Status:                 status,
		SortBy:                 sortBy,
		SortOrder:              sortOrder,
	}

	details, err := h.service.GetMeterStatusDetails(ctx, params)
	if err != nil {
		h.logr.Error("amr: failed to get meter status details", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve meter status details",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    details,
	})
}

// ===================================================
// HEALTH
// ===================================================

// GET /api/v1/amr/health/summary
func (h *Handler) GetMeterHealthSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	dateFrom, dateTo, err := amrParseDates(r)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid date format, expected YYYY-MM-DD",
		})
		return
	}

	if dateTo.Before(dateFrom) {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "dateTo must be after dateFrom",
		})
		return
	}

	params := buildAmrFilterParams(r, dateFrom, dateTo)

	summary, err := h.service.GetMeterHealthSummary(ctx, params)
	if err != nil {
		h.logr.Error("amr: failed to get meter health summary", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve meter health summary",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    summary,
	})
}

// GET /api/v1/amr/health/details
func (h *Handler) GetMeterHealthDetails(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	dateFrom, dateTo, err := amrParseDates(r)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid date format, expected YYYY-MM-DD",
		})
		return
	}

	if dateTo.Before(dateFrom) {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "dateTo must be after dateFrom",
		})
		return
	}

	// Pagination
	page := 1
	if p := q.Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	limit := 50
	if l := q.Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 200 {
			limit = v
		}
	}

	// Health category
	healthCategory := strings.ToLower(strings.TrimSpace(q.Get("healthCategory")))
	validCategories := map[string]bool{
		"excellent": true,
		"good":      true,
		"poor":      true,
		"critical":  true,
		"offline":   true,
		"":          true,
	}
	if !validCategories[healthCategory] {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid healthCategory, must be one of: excellent, good, poor, critical, offline",
		})
		return
	}

	// Sort
	sortBy := q.Get("sortBy")
	validSortFields := map[string]bool{
		"meter_number": true,
		"uptime":       true,
		"consumption":  true,
		"last_seen":    true,
		"":             true,
	}
	if !validSortFields[sortBy] {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid sortBy, must be one of: meter_number, uptime, consumption, last_seen",
		})
		return
	}

	sortOrder := strings.ToLower(q.Get("sortOrder"))
	if sortOrder == "" {
		sortOrder = "desc"
	}
	if sortOrder != "asc" && sortOrder != "desc" {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid sortOrder, must be asc or desc",
		})
		return
	}

	params := AmrHealthDetailParams{
		AmrReadingFilterParams: buildAmrFilterParams(r, dateFrom, dateTo),
		Page:                   page,
		Limit:                  limit,
		Search:                 q.Get("search"),
		HealthCategory:         healthCategory,
		SortBy:                 sortBy,
		SortOrder:              sortOrder,
	}

	details, err := h.service.GetMeterHealthDetails(ctx, params)
	if err != nil {
		h.logr.Error("amr: failed to get meter health details", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve meter health details",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    details,
	})
}

// ===================================================
// FILTER DROPDOWN HELPERS
// ===================================================

// GET /api/v1/amr/filters/regions
func (h *Handler) GetRegions(w http.ResponseWriter, r *http.Request) {
	regions, err := h.service.GetUniqueRegions(r.Context())
	if err != nil {
		h.logr.Error("amr: failed to get regions", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve regions"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"data":  regions,
		"count": len(regions),
	})
}

// GET /api/v1/amr/filters/districts
func (h *Handler) GetDistricts(w http.ResponseWriter, r *http.Request) {
	region := r.URL.Query().Get("region")
	districts, err := h.service.GetUniqueDistricts(r.Context(), region)
	if err != nil {
		h.logr.Error("amr: failed to get districts", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve districts"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"data":  districts,
		"count": len(districts),
	})
}

// GET /api/v1/amr/filters/communities
func (h *Handler) GetCommunities(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	communities, err := h.service.GetUniqueCommunities(r.Context(), q.Get("region"), q.Get("district"))
	if err != nil {
		h.logr.Error("amr: failed to get communities", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve communities"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"data":  communities,
		"count": len(communities),
	})
}

// GET /api/v1/amr/filters/tariff-classes
func (h *Handler) GetTariffClasses(w http.ResponseWriter, r *http.Request) {
	tariffs, err := h.service.GetUniqueTariffClasses(r.Context())
	if err != nil {
		h.logr.Error("amr: failed to get tariff classes", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve tariff classes"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"data":  tariffs,
		"count": len(tariffs),
	})
}

// GET /api/v1/amr/filters/contract-statuses
func (h *Handler) GetContractStatuses(w http.ResponseWriter, r *http.Request) {
	statuses, err := h.service.GetUniqueContractStatuses(r.Context())
	if err != nil {
		h.logr.Error("amr: failed to get contract statuses", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve contract statuses"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"data":  statuses,
		"count": len(statuses),
	})
}

// GET /api/v1/amr/filters/customer-types
func (h *Handler) GetCustomerTypes(w http.ResponseWriter, r *http.Request) {
	types, err := h.service.GetUniqueCustomerTypes(r.Context())
	if err != nil {
		h.logr.Error("amr: failed to get customer types", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve customer types"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"data":  types,
		"count": len(types),
	})
}

// GET /api/v1/amr/filters/service-types
func (h *Handler) GetServiceTypes(w http.ResponseWriter, r *http.Request) {
	types, err := h.service.GetUniqueServiceTypes(r.Context())
	if err != nil {
		h.logr.Error("amr: failed to get service types", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve service types"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"data":  types,
		"count": len(types),
	})
}

// ===================================================
// CUSTOMER RECORD LOOKUP (single meter)
// ===================================================

// GET /api/v1/amr/meters/{meterNumber}
func (h *Handler) GetMeterByNumber(w http.ResponseWriter, r *http.Request) {
	meterNumber := chi.URLParam(r, "meterNumber")
	if meterNumber == "" {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "meterNumber is required",
		})
		return
	}

	// Return all customer records for this meter number
	// (there may be multiple accounts on one meter)
	var records []AmrCustomerRecord
	err := h.service.DB().NewSelect().
		TableExpr("app.amr_customer_records").
		Where("meter_number = ?", meterNumber).
		OrderExpr("id").
		Scan(r.Context(), &records)

	if err != nil {
		h.logr.Error("amr: failed to get meter by number", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve meter record",
		})
		return
	}

	if len(records) == 0 {
		httpx.JSON(w, http.StatusNotFound, map[string]string{
			"error": "meter not found",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    records,
		"count":   len(records),
	})
}
