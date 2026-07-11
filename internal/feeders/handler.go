package feeders

import (
	"bknd-3/internal/httpx"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
	"net/http"
	"strconv"
	"strings"
)

type Handler struct {
	service *Service
	logr    *zap.Logger
}

func NewHandler(svc *Service, logr *zap.Logger) *Handler {
	return &Handler{service: svc, logr: logr}
}

// GetAllFeeders handles GET /api/feeders
// Returns feeders from all voltage levels (11kV and 33kV) and orientations (OH and UG)
func (h *Handler) GetAllFeeders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	// Parse filter parameters
	params := h.parseFeederParams(q)

	// Execute service method
	feeders, err := h.service.GetAllFeeders(ctx, params)
	if err != nil {
		h.logr.Error("failed to fetch feeders", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "failed to retrieve feeders",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    feeders,
		"total":   len(feeders),
	})
}

// GetFeedersByVoltage handles GET /api/feeders/voltage/{voltage}
// Returns feeders for a specific voltage level (11 or 33 kV)
func (h *Handler) GetFeedersByVoltage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	// Get voltage from URL parameter
	voltageStr := chi.URLParam(r, "voltage")
	voltage, err := strconv.Atoi(voltageStr)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "invalid voltage parameter, expected 11 or 33",
		})
		return
	}

	if voltage != 11 && voltage != 33 {
		httpx.JSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "voltage must be 11 or 33 kV",
		})
		return
	}

	// Parse filter parameters
	params := h.parseFeederParams(q)

	// Execute service method
	feeders, err := h.service.GetFeedersByVoltage(ctx, voltage, params)
	if err != nil {
		h.logr.Error("failed to fetch feeders by voltage",
			zap.Int("voltage", voltage),
			zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "failed to retrieve feeders",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    feeders,
		"total":   len(feeders),
		"voltage": voltage,
	})
}

// Get11kVFeeders handles GET /api/feeders/11kv
// Returns feeders from 11kV OH and UG tables
func (h *Handler) Get11kVFeeders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	// Parse filter parameters
	params := h.parseFeederParams(q)

	// Execute service method
	feeders, err := h.service.Get11kVFeeders(ctx, params)
	if err != nil {
		h.logr.Error("failed to fetch 11kV feeders", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "failed to retrieve 11kV feeders",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    feeders,
		"total":   len(feeders),
		"voltage": 11,
	})
}

// Get33kVFeeders handles GET /api/feeders/33kv
// Returns feeders from 33kV OH and UG tables
func (h *Handler) Get33kVFeeders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	// Parse filter parameters
	params := h.parseFeederParams(q)

	// Execute service method
	feeders, err := h.service.Get33kVFeeders(ctx, params)
	if err != nil {
		h.logr.Error("failed to fetch 33kV feeders", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "failed to retrieve 33kV feeders",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    feeders,
		"total":   len(feeders),
		"voltage": 33,
	})
}

// GetFeederByCircuitID handles GET /api/feeders/{circuitId}
// Returns a specific feeder by circuit ID
func (h *Handler) GetFeederByCircuitID(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	circuitID := chi.URLParam(r, "circuitId")

	if circuitID == "" {
		httpx.JSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "circuit ID is required",
		})
		return
	}

	feeder, err := h.service.GetFeederByCircuitID(ctx, circuitID)
	if err != nil {
		h.logr.Error("failed to fetch feeder by circuit ID",
			zap.String("circuit_id", circuitID),
			zap.Error(err))
		httpx.JSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false,
			"error":   "feeder not found",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    feeder,
	})
}

// GetFeederStats handles GET /api/feeders/stats
// Returns summary statistics for feeders
func (h *Handler) GetFeederStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	// Parse filter parameters
	params := h.parseFeederParams(q)

	// Execute service method
	stats, err := h.service.GetFeederStats(ctx, params)
	if err != nil {
		h.logr.Error("failed to fetch feeder stats", zap.Error(err))
		httpx.JSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "failed to retrieve statistics",
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    stats,
	})
}

// parseFeederParams extracts and parses query parameters into FeederFilterParams
func (h *Handler) parseFeederParams(q map[string][]string) FeederFilterParams {
	// Helper to get query parameter
	getParam := func(key string) string {
		if values, ok := q[key]; ok && len(values) > 0 {
			return values[0]
		}
		return ""
	}

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

	// Helper to parse voltage values
	parseVoltages := func(s string) []int {
		if s == "" {
			return nil
		}
		parts := strings.Split(s, ",")
		var voltages []int
		for _, p := range parts {
			if v, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
				voltages = append(voltages, v)
			}
		}
		return voltages
	}

	return FeederFilterParams{
		Orientations:   splitCSV(getParam("orientation")),
		CircuitIDs:     splitCSV(getParam("circuitId")),
		PhaseConfigs:   splitCSV(getParam("phaseConfig")),
		ConductorTypes: splitCSV(getParam("conductorType")),
		Voltages:       parseVoltages(getParam("voltage")),
	}
}
