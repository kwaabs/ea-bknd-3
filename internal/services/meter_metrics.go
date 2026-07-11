package services

import (
	"bknd-3/internal/models"
	"context"
	"github.com/uptrace/bun"

	"sort"
	"strings"
	"time"
)

type MeterMetricsService struct {
	db *bun.DB
}

func NewMeterMetricsService(db *bun.DB) *MeterMetricsService {
	return &MeterMetricsService{db: db}
}

// GetMetrics fetches readings and calculates consumption metrics
func (s *MeterMetricsService) GetMetrics(ctx context.Context, params *models.AggregatedQueryParams) (*models.MeterMetricsResult, error) {
	// 1️⃣ Build filters (same as your aggregation service)
	filters := []string{"1=1"}
	args := []interface{}{}

	for i := range params.Regions {
		params.Regions[i] = strings.ToLower(params.Regions[i])
	}
	for i := range params.Districts {
		params.Districts[i] = strings.ToLower(params.Districts[i])
	}
	for i := range params.Stations {
		params.Stations[i] = strings.ToLower(params.Stations[i])
	}
	for i := range params.Locations {
		params.Locations[i] = strings.ToLower(params.Locations[i])
	}
	for i := range params.BoundaryPoints {
		params.BoundaryPoints[i] = strings.ToLower(params.BoundaryPoints[i])
	}

	if params.DateFrom != "" {
		filters = append(filters, "r.reading_date >= ?")
		args = append(args, params.DateFrom)
	}
	if params.DateTo != "" {
		filters = append(filters, "r.reading_date <= ?")
		args = append(args, params.DateTo)
	}
	if len(params.Regions) > 0 {
		filters = append(filters, "lower(m.region) IN (?)")
		args = append(args, bun.In(params.Regions))
	}
	if len(params.MeterTypes) > 0 {
		filters = append(filters, "m.meter_type IN (?)")
		args = append(args, bun.In(params.MeterTypes))
	}

	whereClause := strings.Join(filters, " AND ")

	// 2️⃣ Fetch readings
	type row struct {
		MeterNumber string    `bun:"meter_number"`
		Date        time.Time `bun:"reading_date"`
		SystemName  string    `bun:"system_name"`
		TotalVal    float64   `bun:"total_val"`
	}

	var rows []row
	err := s.db.NewRaw(`
        SELECT r.meter_number, r.reading_date, d.system_name, r.total_val
        FROM app.meter_readings_daily r
        JOIN app.meters m ON r.meter_number = m.meter_number
        LEFT JOIN app.data_item_mapping d ON r.data_item_id = d.data_item_id
        WHERE `+whereClause+`
        ORDER BY r.meter_number, r.reading_date
    `, args...).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	// 3️⃣ Calculate metrics
	type meterData struct {
		LastRead map[string]float64 // system_name -> last reading
	}

	meterMap := map[string]*meterData{}
	timeSeriesMap := map[string]map[string]float64{} // date -> system_name -> value

	for _, r := range rows {
		if _, ok := meterMap[r.MeterNumber]; !ok {
			meterMap[r.MeterNumber] = &meterData{LastRead: map[string]float64{}}
		}
		md := meterMap[r.MeterNumber]

		delta := r.TotalVal
		if last, ok := md.LastRead[r.SystemName]; ok {
			delta = r.TotalVal - last
		}
		md.LastRead[r.SystemName] = r.TotalVal

		key := r.Date.Format("2006-01-02")
		if _, ok := timeSeriesMap[key]; !ok {
			timeSeriesMap[key] = map[string]float64{}
		}
		timeSeriesMap[key][r.SystemName] += delta
	}

	// 4️⃣ Build time series list
	var tsList []models.TimeSeriesReading
	for dateStr, valMap := range timeSeriesMap {
		t, _ := time.Parse("2006-01-02", dateStr)
		ts := models.TimeSeriesReading{
			Date:  t,
			Extra: make(map[string]float64),
		}
		var totalImportKWh, totalExportKWh float64
		for k, v := range valMap {
			ts.Extra[k] = v
			switch k {
			case "import_kwh":
				totalImportKWh += v
			case "export_kwh":
				totalExportKWh += v
			}
		}
		ts.TotalImportKWh = totalImportKWh
		ts.TotalExportKWh = totalExportKWh
		tsList = append(tsList, ts)
	}

	sort.Slice(tsList, func(i, j int) bool { return tsList[i].Date.Before(tsList[j].Date) })

	// 5️⃣ Compute totals & peak
	var totalImport, totalExport, peakImport, peakExport float64
	for _, t := range tsList {
		totalImport += t.TotalImportKWh
		totalExport += t.TotalExportKWh
		if t.TotalImportKWh > peakImport {
			peakImport = t.TotalImportKWh
		}
		if t.TotalExportKWh > peakExport {
			peakExport = t.TotalExportKWh
		}
	}

	return &models.MeterMetricsResult{
		TimeSeries:     tsList,
		TotalImport:    totalImport,
		TotalExport:    totalExport,
		PeakImport:     peakImport,
		PeakExport:     peakExport,
		MeterCount:     len(meterMap),
		ActiveMeterCnt: len(meterMap), // all meters that returned readings
	}, nil
}
