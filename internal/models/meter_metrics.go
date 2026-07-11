package models

// Metrics result
type MeterMetricsResult struct {
	TimeSeries     []TimeSeriesReading `json:"timeSeries"`
	TotalImport    float64             `json:"total_import"`
	TotalExport    float64             `json:"total_export"`
	PeakImport     float64             `json:"peak_import"`
	PeakExport     float64             `json:"peak_export"`
	MeterCount     int                 `json:"meter_count"`
	ActiveMeterCnt int                 `json:"active_meter_count"`
}
