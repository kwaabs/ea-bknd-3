package handlers

import (
	"bknd-3/internal/models"
	"bknd-3/internal/services"
	"go.uber.org/zap"
	"net/http"
	"strings"
)

type MeterMetricsHandler struct {
	service *services.MeterMetricsService
	logr    *zap.Logger
}

func NewMeterMetricsHandler(svc *services.MeterMetricsService, logr *zap.Logger) *MeterMetricsHandler {
	return &MeterMetricsHandler{service: svc, logr: logr}
}

func (h *MeterMetricsHandler) GetMeterMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := &models.AggregatedQueryParams{}
	if err := parseQueryParams(r, params); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	metrics, err := h.service.GetMetrics(ctx, params)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, metrics)
}

func parseQueryParams(r *http.Request, params *models.AggregatedQueryParams) error {
	q := r.URL.Query()

	params.DateFrom = q.Get("date_from")
	params.DateTo = q.Get("date_to")
	params.Regions = strings.Split(q.Get("regions"), ",")
	params.Districts = strings.Split(q.Get("districts"), ",")
	params.Stations = strings.Split(q.Get("stations"), ",")
	params.Locations = strings.Split(q.Get("locations"), ",")
	params.BoundaryPoints = strings.Split(q.Get("boundaryPoints"), ",")
	params.MeterTypes = strings.Split(q.Get("meterTypes"), ",")

	if q.Get("stackByMeterType") == "true" {
		params.StackByMeterType = true
	}

	return nil
}
