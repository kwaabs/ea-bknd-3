package models

import (
	"encoding/json"
	"time"
)

// TimeSeriesReading is used by the (still unmigrated) meter_metrics domain.
// It used to live alongside the other meter types in this package; those
// moved to internal/meters, but meter_metrics.go still depends on this one,
// so it stays here as its own file rather than duplicating meters.TimeSeriesReading.
type TimeSeriesReading struct {
	Date            time.Time `bun:"date" json:"date"`
	TotalImportKWh  float64   `bun:"total_import_kwh" json:"total_import_kwh"`
	TotalExportKWh  float64   `bun:"total_export_kwh" json:"total_export_kwh"`
	TotalImportKVah float64   `bun:"total_import_kvah" json:"total_import_kvah"`
	TotalExportKVah float64   `bun:"total_export_kvah" json:"total_export_kvah"`
	TotalImportKVar float64   `bun:"total_import_kvar" json:"total_import_kvar"`
	TotalExportKVar float64   `bun:"total_export_kvar" json:"total_export_kvar"`

	// dynamic stacked values: e.g., PSS_import_kwh, BSP_export_kvah
	Extra map[string]float64 `json:"-"`
}

// MarshalJSON allows Extra fields to be serialized dynamically
func (t TimeSeriesReading) MarshalJSON() ([]byte, error) {
	aux := make(map[string]interface{})

	aux["date"] = t.Date
	aux["total_import_kwh"] = t.TotalImportKWh
	aux["total_export_kwh"] = t.TotalExportKWh
	aux["total_import_kvah"] = t.TotalImportKVah
	aux["total_export_kvah"] = t.TotalExportKVah
	aux["total_import_kvar"] = t.TotalImportKVar
	aux["total_export_kvar"] = t.TotalExportKVar

	for k, v := range t.Extra {
		aux[k] = v
	}

	return json.Marshal(aux)
}

// AggregatedQueryParams is used by the (still unmigrated) meter_metrics
// domain; see the TimeSeriesReading comment above for why it lives here.
type AggregatedQueryParams struct {
	DateFrom         string
	DateTo           string
	Regions          []string
	Districts        []string
	Stations         []string
	Voltages         []float64
	Locations        []string
	BoundaryPoints   []string
	MeterTypes       []string
	GroupBy          string // "day"
	StackByMeterType bool
}
