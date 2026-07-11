package serviceareas

import (
	"bknd-3/internal/httpx"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

type Handler struct {
	service *Service
	logr    *zap.Logger
}

func NewHandler(svc *Service, logr *zap.Logger) *Handler {
	return &Handler{service: svc, logr: logr}
}

// GetServiceAreas returns ECG service areas with geographic boundaries
func (h *Handler) GetServiceAreas(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	// Helper to split CSV parameters
	splitCSV := func(s string) []string {
		if s == "" {
			return nil
		}
		parts := strings.Split(s, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	}

	// Build filter params
	params := ServiceAreaQueryParams{
		Regions:   splitCSV(q.Get("region")),
		Districts: splitCSV(q.Get("district")),
	}

	// Call service
	response, err := h.service.GetServiceAreas(ctx, params)
	if err != nil {
		h.logr.Error("failed to get service areas", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve service areas",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, response)
}

// GetServiceAreaByID returns a single service area by ID
func (h *Handler) GetServiceAreaByID(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")

	id, err := strconv.Atoi(idStr)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid id parameter",
		})
		return
	}

	feature, err := h.service.GetServiceAreaByID(ctx, id)
	if err != nil {
		h.logr.Error("failed to get service area", zap.Error(err), zap.Int("id", id))
		httpx.JSON(w, http.StatusNotFound, map[string]string{
			"error": "service area not found",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, feature)
}

// GetRegions returns a list of unique regions
func (h *Handler) GetRegions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	regions, err := h.service.GetUniqueRegions(ctx)
	if err != nil {
		h.logr.Error("failed to get regions", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve regions",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"regions": regions,
		"count":   len(regions),
	})
}

// GetDistricts returns a list of unique districts, optionally filtered by region
func (h *Handler) GetDistricts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	region := q.Get("region")

	districts, err := h.service.GetUniqueDistricts(ctx, region)
	if err != nil {
		h.logr.Error("failed to get districts", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve districts",
		})
		return
	}

	response := map[string]interface{}{
		"districts": districts,
		"count":     len(districts),
	}

	if region != "" {
		response["region"] = region
	}

	httpx.JSON(w, http.StatusOK, response)
}
