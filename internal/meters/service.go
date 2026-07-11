package meters

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/uptrace/bun"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Service struct {
	db *bun.DB
}

func NewService(db *bun.DB) *Service {
	return &Service{db: db}
}

type MeterQueryParams struct {
	Page       int
	Limit      int
	Regions    []string
	Districts  []string
	MeterTypes []string
	Locations  []string

	BoundaryMeteringPoints []string // 👈 STRING slice

	Search    string
	SortBy    string
	SortOrder string
	Columns   []string
}

func parseMeterQuery(r *http.Request) MeterQueryParams {
	q := r.URL.Query()

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	}

	trimSplit := func(val string) []string {
		if val == "" {
			return nil
		}
		parts := strings.Split(val, ",")
		out := []string{}
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}

	// Helper to get parameter supporting both singular and plural forms
	getParam := func(singular, plural string) string {
		if val := q.Get(singular); val != "" {
			return val
		}
		return q.Get(plural)
	}

	return MeterQueryParams{
		Page:                   page,
		Limit:                  limit,
		Regions:                trimSplit(getParam("region", "regions")),                                   // ✅ Support both
		Districts:              trimSplit(getParam("district", "districts")),                               // ✅ Support both
		MeterTypes:             trimSplit(getParam("meterType", "meterTypes")),                             // ✅ Support both
		Locations:              trimSplit(getParam("location", "locations")),                               // ✅ Support both
		BoundaryMeteringPoints: trimSplit(getParam("boundary_metering_point", "boundary_metering_points")), // ✅ Support both
		Search:                 q.Get("search"),
		SortBy:                 q.Get("sortBy"),
		SortOrder:              q.Get("sortOrder"),
		Columns:                trimSplit(q.Get("columns")),
	}
}

type MeterQueryResult struct {
	Data []Meter `json:"data"`
	Meta any            `json:"meta"`
}

type DailyConsumptionResult struct {
	ConsumptionDate time.Time `bun:"consumption_date" json:"consumption_date"`
	MeterNumber     string    `bun:"meter_number" json:"meter_number"`
	DayStartReading float64   `bun:"day_start_reading" json:"day_start_reading"`
	DayEndReading   float64   `bun:"day_end_reading" json:"day_end_reading"`
	ConsumedEnergy  float64   `bun:"consumed_energy" json:"consumed_energy"`
	SystemName      string    `bun:"system_name" json:"system_name"`
}

// Updated QueryMeters with case-insensitive region filter
func (s *Service) QueryMeters(ctx context.Context, r *http.Request) (*MeterQueryResult, error) {
	params := parseMeterQuery(r)

	q := s.db.NewSelect().Model((*Meter)(nil))

	// Case-insensitive region filter
	if len(params.Regions) > 0 {
		lowerRegions := stringsToLower(params.Regions)
		q = q.Where("LOWER(region) IN (?)", bun.In(lowerRegions))
	}
	if len(params.Districts) > 0 {
		lowerDistricts := stringsToLower(params.Districts)
		q = q.Where("LOWER(district) IN (?)", bun.In(lowerDistricts))
	}
	if len(params.MeterTypes) > 0 {
		q = q.Where("meter_type IN (?)", bun.In(params.MeterTypes))
	}
	if len(params.Locations) > 0 {
		lowerLocations := stringsToLower(params.Locations)
		q = q.Where("lower(location) IN (?)", bun.In(lowerLocations))
	}
	// Boundary metering point filter
	if len(params.BoundaryMeteringPoints) > 0 {
		lowerboundaryMeteringPoints := stringsToLower(params.BoundaryMeteringPoints)
		q = q.Where(
			"lower(boundary_metering_point) IN (?)",
			bun.In(lowerboundaryMeteringPoints),
		)
	}
	if params.Search != "" {
		search := "%" + params.Search + "%"
		q = q.Where("meter_number ILIKE ? OR station ILIKE ? OR feeder_panel_name ILIKE ?", search, search, search)
	}

	// Sorting
	if params.SortBy != "" {
		order := "ASC"
		if strings.ToLower(params.SortOrder) == "desc" {
			order = "DESC"
		}
		q = q.Order(params.SortBy + " " + order)
	}

	// Count total before pagination
	total, err := q.Count(ctx)
	if err != nil {
		return nil, err
	}

	// Apply pagination
	q = q.Offset((params.Page - 1) * params.Limit).Limit(params.Limit)

	var meters []Meter
	if err := q.Scan(ctx, &meters); err != nil {
		return nil, err
	}

	meta := map[string]any{
		"page":  params.Page,
		"limit": params.Limit,
		"total": total,
		"pages": (total + params.Limit - 1) / params.Limit, // ceil
	}

	// Add applied filters dynamically
	filters := map[string]any{}
	if len(params.Regions) > 0 {
		filters["regions"] = params.Regions
	}
	if len(params.Districts) > 0 {
		filters["districts"] = params.Districts
	}
	if len(params.MeterTypes) > 0 {
		filters["meterTypes"] = params.MeterTypes
	}
	if len(params.Locations) > 0 {
		filters["locations"] = params.Locations
	}
	if len(params.BoundaryMeteringPoints) > 0 {
		filters["boundaryMeteringPoints"] = params.BoundaryMeteringPoints
	}
	if params.Search != "" {
		filters["search"] = params.Search
	}
	if params.SortBy != "" {
		filters["sortBy"] = params.SortBy
		filters["sortOrder"] = params.SortOrder
	}
	if len(filters) > 0 {
		meta["filters"] = filters
	}

	return &MeterQueryResult{
		Data: meters,
		Meta: meta,
	}, nil
}

// GetByID returns a single meter by ID
func (s *Service) GetMeterByID(ctx context.Context, id string) (*Meter, error) {
	meter := new(Meter)
	err := s.db.NewSelect().Model(meter).Where("id = ?", id).Scan(ctx)
	return meter, err
}

func (s *Service) GetAggregated(ctx context.Context, params *AggregatedQueryParams) (*AggregatedResult, error) {
	// 1️⃣ Build filters
	filters := []string{"1=1"}
	args := []interface{}{}

	// convert all filter lists to lower-case
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
	for i := range params.MeterTypes {
		params.MeterTypes[i] = strings.ToLower(params.MeterTypes[i])
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
	if len(params.Districts) > 0 {
		filters = append(filters, "lower(m.district) IN (?)")
		args = append(args, bun.In(params.Districts))
	}
	if len(params.Stations) > 0 {
		filters = append(filters, "lower(m.station) IN (?)")
		args = append(args, bun.In(params.Stations))
	}
	if len(params.Locations) > 0 {
		filters = append(filters, "lower(m.location) IN (?)")
		args = append(args, bun.In(params.Locations))
	}
	if len(params.BoundaryPoints) > 0 {
		filters = append(filters, "lower(m.boundary_metering_point) IN (?)")
		args = append(args, bun.In(params.BoundaryPoints))
	}
	if len(params.MeterTypes) > 0 {
		filters = append(filters, "lower(m.meter_type) IN (?)")
		args = append(args, bun.In(params.MeterTypes))
	}
	if params.StackByMeterType == false {
		params.StackByMeterType = false // optional, same as zero value
	}

	whereClause := strings.Join(filters, " AND ")

	// 2️⃣ Query aggregated totals
	var agg AggregatedReading
	err := s.db.NewRaw(`
        WITH filtered_meters AS (SELECT * FROM app.meters m),
        meter_readings AS (
            SELECT r.*, d.system_name, m.meter_type
            FROM app.meter_readings_daily r
            JOIN filtered_meters m ON r.meter_number = m.meter_number
            LEFT JOIN app.data_item_mapping d ON r.data_item_id = d.data_item_id
            WHERE `+whereClause+`
        )
        SELECT
            COUNT(DISTINCT meter_number) AS meter_count,
            SUM(record_count) AS reading_count,
            SUM(CASE WHEN system_name='import_kwh' THEN total_val ELSE 0 END) AS total_import_kwh,
            SUM(CASE WHEN system_name='export_kwh' THEN total_val ELSE 0 END) AS total_export_kwh,
            SUM(CASE WHEN system_name='import_kvah' THEN total_val ELSE 0 END) AS total_import_kvah,
            SUM(CASE WHEN system_name='export_kvah' THEN total_val ELSE 0 END) AS total_export_kvah,
            SUM(CASE WHEN system_name='import_kvar' THEN total_val ELSE 0 END) AS total_import_kvar,
            SUM(CASE WHEN system_name='export_kvar' THEN total_val ELSE 0 END) AS total_export_kvar
        FROM meter_readings
    `, args...).Scan(ctx, &agg)
	if err != nil {
		return nil, err
	}

	// 3️⃣ Query time series grouped by day
	type row struct {
		Date       time.Time `bun:"reading_date"`
		MeterType  string    `bun:"meter_type"`
		SystemName string    `bun:"system_name"`
		TotalVal   float64   `bun:"total_val"`
	}

	var rows []row
	err = s.db.NewRaw(`
        SELECT r.reading_date, m.meter_type, d.system_name, SUM(r.total_val) AS total_val
        FROM app.meter_readings_daily r
        JOIN app.meters m ON r.meter_number = m.meter_number
        LEFT JOIN app.data_item_mapping d ON r.data_item_id = d.data_item_id
        WHERE `+whereClause+`
        GROUP BY r.reading_date, m.meter_type, d.system_name
        ORDER BY r.reading_date
    `, args...).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	// 4️⃣ Build time series response
	timeSeriesMap := map[string]*TimeSeriesReading{}
	for _, r := range rows {
		key := r.Date.Format("2006-01-02")
		ts, ok := timeSeriesMap[key]
		if !ok {
			ts = &TimeSeriesReading{
				Date:  r.Date,
				Extra: make(map[string]float64),
			}
			timeSeriesMap[key] = ts
		}

		if params.StackByMeterType && r.MeterType != "" && r.SystemName != "" {
			ts.Extra[r.MeterType+"_"+r.SystemName] = r.TotalVal
		} else {
			switch r.SystemName {
			case "import_kwh":
				ts.TotalImportKWh += r.TotalVal
			case "export_kwh":
				ts.TotalExportKWh += r.TotalVal
			case "import_kvah":
				ts.TotalImportKVah += r.TotalVal
			case "export_kvah":
				ts.TotalExportKVah += r.TotalVal
			case "import_kvar":
				ts.TotalImportKVar += r.TotalVal
			case "export_kvar":
				ts.TotalExportKVar += r.TotalVal
			}
		}
	}

	var tsList []TimeSeriesReading
	for _, v := range timeSeriesMap {
		tsList = append(tsList, *v)
	}
	sort.Slice(tsList, func(i, j int) bool {
		return tsList[i].Date.Before(tsList[j].Date)
	})

	// 5️⃣ Query byMeterType totals
	var byType []ByMeterTypeReading
	err = s.db.NewRaw(`
        SELECT m.meter_type,
               SUM(CASE WHEN d.system_name='import_kwh' THEN r.total_val ELSE 0 END) AS total_import_kwh,
               SUM(CASE WHEN d.system_name='export_kwh' THEN r.total_val ELSE 0 END) AS total_export_kwh,
               SUM(CASE WHEN d.system_name='import_kvah' THEN r.total_val ELSE 0 END) AS total_import_kvah,
               SUM(CASE WHEN d.system_name='export_kvah' THEN r.total_val ELSE 0 END) AS total_export_kvah,
               SUM(CASE WHEN d.system_name='import_kvar' THEN r.total_val ELSE 0 END) AS total_import_kvar,
               SUM(CASE WHEN d.system_name='export_kvar' THEN r.total_val ELSE 0 END) AS total_export_kvar,
               SUM(r.record_count) AS reading_count
        FROM app.meter_readings_daily r
        JOIN app.meters m ON r.meter_number = m.meter_number
        LEFT JOIN app.data_item_mapping d ON r.data_item_id = d.data_item_id
        WHERE `+whereClause+`
        GROUP BY m.meter_type
    `, args...).Scan(ctx, &byType)
	if err != nil {
		return nil, err
	}

	// 6️⃣ Collect meter types
	var meterTypes []string
	for _, t := range byType {
		meterTypes = append(meterTypes, t.MeterType)
	}

	return &AggregatedResult{
		Aggregated:  agg,
		TimeSeries:  tsList,
		ByMeterType: byType,
		MeterTypes:  meterTypes,
	}, nil
}

type Filter struct {
	Query string
	Args  []interface{}
}

func (s *Service) GetDailyConsumption(
	ctx context.Context,
	params ReadingFilterParams,
) ([]DailyConsumptionResults, error) {
	var results []DailyConsumptionResults

	filters := buildReadingFilters(params)

	q := s.db.NewSelect().
		ColumnExpr("mcd.consumption_date AT TIME ZONE 'UTC' AS consumption_date").
		Column("mcd.meter_number").
		Column("mtr.region").
		Column("mtr.district").
		Column("mtr.station").
		Column("mtr.meter_type").
		Column("mtr.location").
		Column("mtr.multiply_factor").
		Column("mtr.voltage_kv").
		Column("mcd.day_start_reading").
		Column("mcd.day_end_reading").
		ColumnExpr("round((sum(mcd.consumption))::numeric, 4) as consumed_energy").
		Column("dim.system_name").
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id")

	for _, f := range filters {
		q = q.Where(f.Query, f.Args...)
	}

	q = q.
		Group("mcd.consumption_date").
		Group("mcd.meter_number").
		Group("mtr.region").
		Group("mtr.district").
		Group("mtr.meter_type").
		Group("mtr.multiply_factor").
		Group("mtr.station").
		Group("mtr.voltage_kv").
		Group("mtr.location").
		Group("dim.system_name").
		Group("mcd.day_start_reading").
		Group("mcd.day_end_reading")

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Service) GetRegionalBoundaryDailyConsumption(
	ctx context.Context,
	params ReadingFilterParams,
) ([]DailyConsumptionResults, error) {
	var results []DailyConsumptionResults

	filters := buildReadingFilters(params)

	q := s.db.NewSelect().
		ColumnExpr("mcd.consumption_date AT TIME ZONE 'UTC' AS consumption_date").
		Column("mcd.meter_number").
		Column("mtr.region").
		Column("mtr.district").
		Column("mtr.station").
		Column("mtr.meter_type").
		Column("mtr.voltage_kv").
		Column("mtr.multiply_factor").
		Column("mtr.boundary_metering_point").
		Column("mtr.location").
		Column("mcd.day_start_reading").
		Column("mcd.day_end_reading").
		ColumnExpr("round((sum(mcd.consumption))::numeric, 4) AS consumed_energy").
		Column("dim.system_name").
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		Where("mtr.meter_type = ?", "REGIONAL_BOUNDARY") // ✅ strict base filter

	// ✅ Apply dynamic filters (except meter_type)
	for _, f := range filters {
		// prevent user from overriding meter_type
		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	q = q.
		Group("mcd.consumption_date").
		Group("mcd.meter_number").
		Group("mtr.region").
		Group("mtr.district").
		Group("mtr.meter_type").
		Group("mtr.station").
		Group("mtr.voltage_kv").
		Group("mtr.multiply_factor").
		Group("mtr.boundary_metering_point").
		Group("mtr.location").
		Group("dim.system_name").
		Group("mcd.day_start_reading").
		Group("mcd.day_end_reading")

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Service) GetDistrictBoundaryDailyConsumption(
	ctx context.Context,
	params ReadingFilterParams,
) ([]DailyConsumptionResults, error) {
	var results []DailyConsumptionResults

	filters := buildReadingFilters(params)

	q := s.db.NewSelect().
		ColumnExpr("mcd.consumption_date AT TIME ZONE 'UTC' AS consumption_date").
		Column("mcd.meter_number").
		Column("mtr.region").
		Column("mtr.district").
		Column("mtr.boundary_metering_point").
		Column("mtr.location").
		Column("mtr.station").
		Column("mtr.meter_type").
		Column("mtr.voltage_kv").
		Column("mtr.multiply_factor").
		Column("mcd.day_start_reading").
		Column("mcd.day_end_reading").
		ColumnExpr("round((sum(mcd.consumption))::numeric, 4) AS consumed_energy").
		Column("dim.system_name").
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		Where("mtr.meter_type = ?", "DISTRICT_BOUNDARY") // ✅ strict base filter

	// ✅ Apply dynamic filters (except meter_type)
	for _, f := range filters {
		// prevent user from overriding meter_type
		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	q = q.
		Group("mcd.consumption_date").
		Group("mcd.meter_number").
		Group("mtr.region").
		Group("mtr.district").
		Group("mtr.meter_type").
		Group("mtr.station").
		Group("mtr.voltage_kv").
		Group("mtr.multiply_factor").
		Group("mtr.boundary_metering_point").
		Group("mtr.location").
		Group("dim.system_name").
		Group("mcd.day_start_reading").
		Group("mcd.day_end_reading")

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Service) GetBSPDailyConsumption(
	ctx context.Context,
	params ReadingFilterParams,
) ([]DailyConsumptionResults, error) {
	var results []DailyConsumptionResults

	filters := buildReadingFilters(params)

	q := s.db.NewSelect().
		ColumnExpr("mcd.consumption_date AT TIME ZONE 'UTC' AS consumption_date").
		Column("mcd.meter_number").
		Column("mtr.region").
		Column("mtr.district").
		Column("mtr.station").
		Column("mtr.meter_type").
		Column("mtr.feeder_panel_name").
		ColumnExpr("mtr.ic_og AS ic_og").
		Column("mtr.voltage_kv").
		Column("mtr.multiply_factor").
		Column("mcd.day_start_reading").
		Column("mcd.day_end_reading").
		ColumnExpr("round((sum(mcd.consumption))::numeric, 4) AS consumed_energy").
		Column("dim.system_name").
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		Where("mtr.meter_type = ?", "BSP") // ✅ strict base filter

	// ✅ Apply dynamic filters (except meter_type)
	for _, f := range filters {
		// prevent user from overriding meter_type
		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	q = q.
		Group("mcd.consumption_date").
		Group("mcd.meter_number").
		Group("mtr.region").
		Group("mtr.district").
		Group("mtr.meter_type").
		Group("mtr.multiply_factor").
		Group("mtr.station").
		Group("mtr.feeder_panel_name").
		Group("mtr.ic_og").
		Group("mtr.voltage_kv").
		Group("dim.system_name").
		Group("mcd.day_start_reading").
		Group("mcd.day_end_reading")

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Service) GetDTXDailyConsumption(
	ctx context.Context,
	params ReadingFilterParams,
) ([]DailyConsumptionResults, error) {
	var results []DailyConsumptionResults

	filters := buildReadingFilters(params)

	q := s.db.NewSelect().
		ColumnExpr("mcd.consumption_date AT TIME ZONE 'UTC' AS consumption_date").
		Column("mcd.meter_number").
		Column("mtr.region").
		Column("mtr.district").
		Column("mtr.station").
		Column("mtr.meter_type").
		Column("mtr.feeder_panel_name").
		Column("mtr.voltage_kv").
		Column("mtr.multiply_factor").
		Column("mcd.day_start_reading").
		Column("mcd.day_end_reading").
		ColumnExpr("round((sum(mcd.consumption))::numeric, 4) AS consumed_energy").
		Column("dim.system_name").
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		Where("mtr.meter_type = ?", "DTX") // ✅ strict base filter

	// ✅ Apply dynamic filters (except meter_type)
	for _, f := range filters {
		// prevent user from overriding meter_type
		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	q = q.
		Group("mcd.consumption_date").
		Group("mcd.meter_number").
		Group("mtr.region").
		Group("mtr.district").
		Group("mtr.meter_type").
		Group("mtr.station").
		Group("mtr.multiply_factor").
		Group("mtr.feeder_panel_name").
		Group("mtr.voltage_kv").
		Group("dim.system_name").
		Group("mcd.day_start_reading").
		Group("mcd.day_end_reading")

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Service) GetDTXAggregatedConsumption(
	ctx context.Context,
	params ReadingFilterParams,
	groupBy string,
	additionalGroups []string,
) ([]AggregatedConsumptionResult, error) {

	var results []AggregatedConsumptionResult
	filters := buildReadingFilters(params)

	q := s.db.NewSelect().
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		ColumnExpr("dim.system_name AS system_name").
		ColumnExpr("mtr.station AS station").
		ColumnExpr("mtr.region AS region").
		ColumnExpr("mtr.district AS district").
		ColumnExpr("mtr.feeder_panel_name AS feeder_panel_name").
		ColumnExpr("ROUND(SUM(mcd.consumption)::numeric, 4) AS total_consumption").
		ColumnExpr("COUNT(DISTINCT mcd.meter_number) AS active_meters").
		Where("mtr.meter_type = ?", "DTX")

	// --- Subquery: total count of all DTX meters ---
	subQTotal := s.db.NewSelect().
		TableExpr("app.meters AS mtr2").
		ColumnExpr("COUNT(DISTINCT mtr2.meter_number)").
		Where("mtr2.meter_type = ?", "DTX")

	// Apply filters for total meters (global level)
	if len(params.Regions) > 0 {
		subQTotal = subQTotal.Where("mtr2.region IN (?)", bun.In(params.Regions))
	}
	if len(params.Districts) > 0 {
		subQTotal = subQTotal.Where("mtr2.district IN (?)", bun.In(params.Districts))
	}
	if len(params.Stations) > 0 {
		subQTotal = subQTotal.Where("mtr2.station IN (?)", bun.In(params.Stations))
	}
	if len(params.Locations) > 0 {
		subQTotal = subQTotal.Where("mtr2.location IN (?)", bun.In(params.Locations))
	}

	q = q.ColumnExpr("(?) AS total_meter_count", subQTotal)

	// --- Subquery: total meters by region ---
	subQRegion := s.db.NewSelect().
		TableExpr("app.meters AS mtr3").
		ColumnExpr("COUNT(DISTINCT mtr3.meter_number)").
		Where("mtr3.meter_type = ?", "DTX").
		Where("mtr3.region = mtr.region") // correlate by region

	q = q.ColumnExpr("(?) AS total_meters_by_region", subQRegion)

	// --- Subquery: total meters by district ---
	subQDistrict := s.db.NewSelect().
		TableExpr("app.meters AS mtr4").
		ColumnExpr("COUNT(DISTINCT mtr4.meter_number)").
		Where("mtr4.meter_type = ?", "DTX").
		Where("mtr4.district = mtr.district") // correlate by district

	q = q.ColumnExpr("(?) AS total_meters_by_district", subQDistrict)

	// --- Time grouping ---
	groupCols := []bun.Safe{
		bun.Safe("dim.system_name"),
		bun.Safe("mtr.station"),
		bun.Safe("mtr.region"),
		bun.Safe("mtr.district"),
		bun.Safe("mtr.feeder_panel_name"),
	}

	switch groupBy {
	case "day":
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	case "month":
		q = q.ColumnExpr("DATE_TRUNC('month', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('month', mcd.consumption_date)"))
	case "year":
		q = q.ColumnExpr("DATE_TRUNC('year', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('year', mcd.consumption_date)"))
	default:
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	}

	// --- Additional grouping ---
	for _, g := range additionalGroups {
		col := fmt.Sprintf("mtr.%s", g)
		if g != "meter_type" {
			col = fmt.Sprintf("LOWER(mtr.%s)", g)
		}
		q = q.ColumnExpr(fmt.Sprintf("%s AS %s", col, g))
		groupCols = append(groupCols, bun.Safe(col))
	}

	// --- Apply filters (except meter_type override) ---
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	// --- Group by all relevant columns ---
	for _, g := range groupCols {
		q = q.GroupExpr(string(g))
	}

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Service) GetAggregatedConsumption(
	ctx context.Context,
	params ReadingFilterParams,
	groupBy string,
	additionalGroups []string,
) ([]AggregatedConsumptionResult, error) {

	var results []AggregatedConsumptionResult
	filters := buildReadingFilters(params)

	q := s.db.NewSelect().
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		ColumnExpr("dim.system_name AS system_name").
		ColumnExpr("mtr.station AS station").
		ColumnExpr("mtr.meter_type AS meter_type").
		ColumnExpr("mtr.region AS region").
		ColumnExpr("mtr.boundary_metering_point AS boundary_metering_point").
		ColumnExpr("mtr.feeder_panel_name AS feeder_panel_name").
		ColumnExpr("ROUND(SUM(mcd.consumption)::numeric, 4) AS total_consumption").
		ColumnExpr("COUNT(DISTINCT mcd.meter_number) AS active_meters")

	// --- Dynamic subquery for total meters ---
	subQuery := strings.Builder{}
	subQuery.WriteString(`
		(
			SELECT COUNT(DISTINCT mtr2.meter_number)
			FROM app.meters AS mtr2
			WHERE TRUE
	`)

	// Apply same filters to subquery
	if len(params.Regions) > 0 {
		subQuery.WriteString(" AND mtr2.region IN (?)")
	}
	if len(params.Districts) > 0 {
		subQuery.WriteString(" AND mtr2.district IN (?)")
	}
	if len(params.Stations) > 0 {
		subQuery.WriteString(" AND mtr2.station IN (?)")
	}
	if len(params.Locations) > 0 {
		subQuery.WriteString(" AND mtr2.location IN (?)")
	}
	if len(params.MeterTypes) > 0 {
		subQuery.WriteString(" AND mtr2.meter_type IN (?)")
	}

	subQuery.WriteString(") AS total_meter_count")

	// Collect bind parameters
	var subArgs []interface{}
	if len(params.Regions) > 0 {
		subArgs = append(subArgs, bun.In(params.Regions))
	}
	if len(params.Districts) > 0 {
		subArgs = append(subArgs, bun.In(params.Districts))
	}
	if len(params.Stations) > 0 {
		subArgs = append(subArgs, bun.In(params.Stations))
	}
	if len(params.Locations) > 0 {
		subArgs = append(subArgs, bun.In(params.Locations))
	}
	if len(params.MeterTypes) > 0 {
		subArgs = append(subArgs, bun.In(params.MeterTypes))
	}

	q = q.ColumnExpr(subQuery.String(), subArgs...)

	// --- Time grouping ---

	groupCols := []bun.Safe{
		bun.Safe("dim.system_name"),
		bun.Safe("mtr.region"),
		bun.Safe("mtr.boundary_metering_point"),
		bun.Safe("mtr.meter_type"),
		bun.Safe("mtr.station"),
		bun.Safe("mtr.feeder_panel_name"),
	}
	switch groupBy {
	case "day":
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	case "month":
		q = q.ColumnExpr("DATE_TRUNC('month', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('month', mcd.consumption_date)"))
	case "year":
		q = q.ColumnExpr("DATE_TRUNC('year', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('year', mcd.consumption_date)"))
	default:
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	}

	// --- Additional grouping (region, station, etc.) ---
	for _, g := range additionalGroups {
		col := fmt.Sprintf("mtr.%s", g)
		if g != "meter_type" {
			col = fmt.Sprintf("LOWER(mtr.%s)", g)
		}
		q = q.ColumnExpr(fmt.Sprintf("%s AS %s", col, g))
		groupCols = append(groupCols, bun.Safe(col))
	}

	// Apply filters to main query
	for _, f := range filters {
		q = q.Where(f.Query, f.Args...)
	}

	// Group by all relevant columns
	for _, g := range groupCols {
		q = q.GroupExpr(string(g))
	}

	// Run query
	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Service) GetBSPAggregatedConsumption(
	ctx context.Context,
	params ReadingFilterParams,
	groupBy string,
	additionalGroups []string,
) ([]AggregatedConsumptionResult, error) {

	var results []AggregatedConsumptionResult
	filters := buildReadingFilters(params)

	q := s.db.NewSelect().
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		ColumnExpr("dim.system_name AS system_name").
		ColumnExpr("mtr.station AS station").
		ColumnExpr("mtr.feeder_panel_name AS feeder_panel_name").
		ColumnExpr("mtr.ic_og AS ic_og").
		ColumnExpr("ROUND(SUM(mcd.consumption)::numeric, 4) AS total_consumption").
		ColumnExpr("COUNT(DISTINCT mcd.meter_number) AS active_meters").
		Where("mtr.meter_type = ?", "BSP")

	// --- Subquery 1: total_meter_count (filtered) ---
	subQFiltered := s.db.NewSelect().
		TableExpr("app.meters AS mtr2").
		Join("JOIN app.meter_consumption_daily AS mcd2 ON mcd2.meter_number = mtr2.meter_number").
		ColumnExpr("COUNT(DISTINCT mtr2.meter_number)").
		Where("mtr2.meter_type = ?", "BSP")

	for _, f := range filters {
		qry := strings.ReplaceAll(f.Query, "mtr.", "mtr2.")
		qry = strings.ReplaceAll(qry, "mcd.", "mcd2.")
		if strings.Contains(strings.ToLower(qry), "meter_type") {
			continue
		}
		subQFiltered = subQFiltered.Where(qry, f.Args...)
	}

	q = q.ColumnExpr("(?) AS total_meter_count", subQFiltered)

	// --- Subquery 2: all_meters_count (unfiltered BSP) ---
	subQAll := s.db.NewSelect().
		TableExpr("app.meters AS mtr3").
		ColumnExpr("COUNT(DISTINCT mtr3.meter_number)").
		Where("mtr3.meter_type = ?", "BSP")

	q = q.ColumnExpr("(?) AS all_meters_count", subQAll)

	// --- Time grouping ---
	groupCols := []bun.Safe{
		bun.Safe("dim.system_name"),
		bun.Safe("mtr.station"),
		bun.Safe("mtr.feeder_panel_name"),
		bun.Safe("mtr.ic_og"),
	}

	switch groupBy {
	case "day":
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	case "month":
		q = q.ColumnExpr("DATE_TRUNC('month', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('month', mcd.consumption_date)"))
	case "year":
		q = q.ColumnExpr("DATE_TRUNC('year', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('year', mcd.consumption_date)"))
	default:
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	}

	// --- Additional grouping ---
	for _, g := range additionalGroups {
		col := fmt.Sprintf("mtr.%s", g)
		if g != "meter_type" {
			col = fmt.Sprintf("LOWER(mtr.%s)", g)
		}
		q = q.ColumnExpr(fmt.Sprintf("%s AS %s", col, g))
		groupCols = append(groupCols, bun.Safe(col))
	}

	// --- Apply filters (skip meter_type override) ---
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	// --- Group by all relevant columns ---
	for _, g := range groupCols {
		q = q.GroupExpr(string(g))
	}

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

//func (s *Service) GetFeederAggregatedConsumption(
//	ctx context.Context,
//	params ReadingFilterParams,
//	groupBy string,
//	additionalGroups []string,
//	meterTypes []string, // New parameter to specify meter types
//) ([]AggregatedConsumptionResult, error) {
//
//	var results []AggregatedConsumptionResult
//	filters := buildReadingFilters(params)
//
//	// Validate and set default meter types if none provided
//	if len(meterTypes) == 0 {
//		meterTypes = []string{"BSP", "PSS", "SS"} // Default to all types
//	}
//
//	q := s.db.NewSelect().
//		TableExpr("app.meter_consumption_daily AS mcd").
//		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
//		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
//		ColumnExpr("dim.system_name AS system_name").
//		ColumnExpr("mtr.station AS station").
//		ColumnExpr("mtr.feeder_panel_name AS feeder_panel_name").
//		ColumnExpr("mtr.ic_og AS ic_og").
//		ColumnExpr("mtr.meter_type AS meter_type"). // Include meter_type in results
//		ColumnExpr("ROUND(SUM(mcd.consumption)::numeric, 4) AS total_consumption").
//		ColumnExpr("COUNT(DISTINCT mcd.meter_number) AS active_meters").
//		Where("mtr.meter_type IN (?)", bun.In(meterTypes)) // Use IN clause for multiple types
//
//	// --- Subquery 1: total_meter_count (filtered) ---
//	subQFiltered := s.db.NewSelect().
//		TableExpr("app.meters AS mtr2").
//		Join("JOIN app.meter_consumption_daily AS mcd2 ON mcd2.meter_number = mtr2.meter_number").
//		ColumnExpr("COUNT(DISTINCT mtr2.meter_number)").
//		Where("mtr2.meter_type IN (?)", bun.In(meterTypes))
//
//	for _, f := range filters {
//		qry := strings.ReplaceAll(f.Query, "mtr.", "mtr2.")
//		qry = strings.ReplaceAll(qry, "mcd.", "mcd2.")
//		if strings.Contains(strings.ToLower(qry), "meter_type") {
//			continue
//		}
//		subQFiltered = subQFiltered.Where(qry, f.Args...)
//	}
//
//	q = q.ColumnExpr("(?) AS total_meter_count", subQFiltered)
//
//	// --- Subquery 2: all_meters_count (unfiltered by specified types) ---
//	subQAll := s.db.NewSelect().
//		TableExpr("app.meters AS mtr3").
//		ColumnExpr("COUNT(DISTINCT mtr3.meter_number)").
//		Where("mtr3.meter_type IN (?)", bun.In(meterTypes))
//
//	q = q.ColumnExpr("(?) AS all_meters_count", subQAll)
//
//	// --- Time grouping ---
//	groupCols := []bun.Safe{
//		bun.Safe("dim.system_name"),
//		bun.Safe("mtr.station"),
//		bun.Safe("mtr.feeder_panel_name"),
//		bun.Safe("mtr.ic_og"),
//		bun.Safe("mtr.meter_type"), // Include meter_type in grouping
//	}
//
//	switch groupBy {
//	case "day":
//		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
//		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
//	case "month":
//		q = q.ColumnExpr("DATE_TRUNC('month', mcd.consumption_date) AS group_period")
//		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('month', mcd.consumption_date)"))
//	case "year":
//		q = q.ColumnExpr("DATE_TRUNC('year', mcd.consumption_date) AS group_period")
//		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('year', mcd.consumption_date)"))
//	default:
//		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
//		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
//	}
//
//	// --- Additional grouping ---
//	for _, g := range additionalGroups {
//		col := fmt.Sprintf("mtr.%s", g)
//		if g != "meter_type" {
//			col = fmt.Sprintf("LOWER(mtr.%s)", g)
//		}
//		q = q.ColumnExpr(fmt.Sprintf("%s AS %s", col, g))
//		groupCols = append(groupCols, bun.Safe(col))
//	}
//
//	// --- Apply filters (skip meter_type override) ---
//	for _, f := range filters {
//		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
//			continue
//		}
//		q = q.Where(f.Query, f.Args...)
//	}
//
//	// --- Group by all relevant columns ---
//	for _, g := range groupCols {
//		q = q.GroupExpr(string(g))
//	}
//
//	// Optional: Order by meter_type for consistent results
//	q = q.OrderExpr("mtr.meter_type", "group_period")
//
//	if err := q.Scan(ctx, &results); err != nil {
//		return nil, err
//	}
//
//	return results, nil
//}

func (s *Service) GetFeederAggregatedConsumption(
	ctx context.Context,
	params ReadingFilterParams,
	groupBy string,
	additionalGroups []string,
	meterTypes []string,
) ([]AggregatedConsumptionResult, error) {

	var results []AggregatedConsumptionResult
	filters := buildReadingFilters(params)

	// Validate and set default meter types if none provided
	if len(meterTypes) == 0 {
		meterTypes = []string{"BSP", "PSS", "SS"}
	}

	// --- Calculate counts ONCE ---
	var totalMeterCount, allMetersCount int64

	// total_meter_count: meters with consumption in date range
	subQFiltered := s.db.NewSelect().
		TableExpr("app.meters AS mtr2").
		Join("JOIN app.meter_consumption_daily AS mcd2 ON mcd2.meter_number = mtr2.meter_number").
		ColumnExpr("COUNT(DISTINCT mtr2.meter_number)").
		Where("mtr2.meter_type IN (?)", bun.In(meterTypes))

	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
			continue
		}
		qry := strings.ReplaceAll(f.Query, "mtr.", "mtr2.")
		qry = strings.ReplaceAll(qry, "mcd.", "mcd2.")
		subQFiltered = subQFiltered.Where(qry, f.Args...)
	}

	if err := subQFiltered.Scan(ctx, &totalMeterCount); err != nil {
		return nil, err
	}

	// all_meters_count: all meters of specified types (no date filter, no join needed)
	if err := s.db.NewSelect().
		TableExpr("app.meters").
		ColumnExpr("COUNT(DISTINCT meter_number)").
		Where("meter_type IN (?)", bun.In(meterTypes)).
		Scan(ctx, &allMetersCount); err != nil {
		return nil, err
	}

	// --- Main query ---
	q := s.db.NewSelect().
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		ColumnExpr("dim.system_name AS system_name").
		ColumnExpr("mtr.feeder_panel_name AS feeder_panel_name").
		ColumnExpr("mtr.ic_og AS ic_og").
		ColumnExpr("mtr.meter_type AS meter_type").
		ColumnExpr("ROUND(SUM(mcd.consumption)::numeric, 4) AS total_consumption").
		ColumnExpr("COUNT(DISTINCT mcd.meter_number) AS active_meters").
		ColumnExpr("? AS total_meter_count", totalMeterCount).
		ColumnExpr("? AS all_meters_count", allMetersCount).
		Where("mtr.meter_type IN (?)", bun.In(meterTypes))

	// --- Time grouping ---
	groupCols := []bun.Safe{
		bun.Safe("dim.system_name"),

		bun.Safe("mtr.feeder_panel_name"),
		bun.Safe("mtr.ic_og"),
		bun.Safe("mtr.meter_type"),
	}

	switch groupBy {
	case "day":
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	case "month":
		q = q.ColumnExpr("DATE_TRUNC('month', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('month', mcd.consumption_date)"))
	case "year":
		q = q.ColumnExpr("DATE_TRUNC('year', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('year', mcd.consumption_date)"))
	default:
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	}

	// --- Additional grouping (skip meter_type if already included) ---
	for _, g := range additionalGroups {
		if g == "meter_type" {
			continue // Already in groupCols
		}
		col := fmt.Sprintf("LOWER(mtr.%s)", g)
		q = q.ColumnExpr(fmt.Sprintf("%s AS %s", col, g))
		groupCols = append(groupCols, bun.Safe(col))
	}

	// --- Apply filters ---
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	// --- Group by ---
	for _, g := range groupCols {
		q = q.GroupExpr(string(g))
	}

	q = q.OrderExpr("mtr.meter_type, group_period")

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Service) GetFeederDailyConsumption(
	ctx context.Context,
	params ReadingFilterParams,
	meterTypes []string, // New parameter to specify meter types
) ([]DailyConsumptionResults, error) {
	var results []DailyConsumptionResults

	filters := buildReadingFilters(params)

	// Validate and set default meter types if none provided
	if len(meterTypes) == 0 {
		meterTypes = []string{"BSP", "PSS", "SS"} // Default to all types
	}

	q := s.db.NewSelect().
		ColumnExpr("mcd.consumption_date AT TIME ZONE 'UTC' AS consumption_date").
		Column("mcd.meter_number").
		Column("mtr.region").
		Column("mtr.district").
		Column("mtr.station").
		Column("mtr.meter_type").
		Column("mtr.feeder_panel_name").
		Column("mtr.ic_og").
		Column("mtr.multiply_factor").
		Column("mtr.voltage_kv").
		Column("mcd.day_start_reading").
		Column("mcd.day_end_reading").
		ColumnExpr("round((sum(mcd.consumption))::numeric, 4) AS consumed_energy").
		Column("dim.system_name").
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		Where("mtr.meter_type IN (?)", bun.In(meterTypes)) // ✅ Use IN clause for multiple types

	// ✅ Apply dynamic filters (except meter_type)
	for _, f := range filters {
		// prevent user from overriding meter_type
		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	q = q.
		Group("mcd.consumption_date").
		Group("mcd.meter_number").
		Group("mtr.region").
		Group("mtr.district").
		Group("mtr.meter_type"). // Keep meter_type in grouping
		Group("mtr.station").
		Group("mtr.feeder_panel_name").
		Group("mtr.ic_og").
		Group("mtr.multiply_factor").
		Group("mtr.voltage_kv").
		Group("dim.system_name").
		Group("mcd.day_start_reading").
		Group("mcd.day_end_reading").
		Order("mtr.meter_type", "mcd.consumption_date") // Optional: Add ordering for consistency

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

// func (s *Service) GetExpressFeederDailyConsumption(
// 	ctx context.Context,
// 	params ReadingFilterParams,
// ) ([]ExpressFeederDailyConsumptionResult, error) {
// 	var results []ExpressFeederDailyConsumptionResult

// 	filters := buildReadingFilters(params)

// 	q := s.db.NewSelect().
// 		ColumnExpr("mcd.consumption_date AT TIME ZONE 'UTC' AS consumption_date").
// 		Column("mcd.meter_number").
// 		Column("mtr.meter_type").
// 		Column("mtr.multiply_factor").
// 		Column("mtr.voltage_kv").
// 		Column("mcd.day_start_reading").
// 		Column("mcd.day_end_reading").
// 		ColumnExpr("round((sum(mcd.consumption))::numeric, 4) AS consumed_energy").
// 		Column("dim.system_name").
// 		// Feeder fields
// 		Column("f.feeder_name").
// 		Column("f.sap_version").
// 		Column("f.sending_station").
// 		Column("f.sending_type_of_station").
// 		Column("f.sending_code").
// 		Column("f.sending_region").
// 		Column("f.sending_district").
// 		Column("f.receiving_station").
// 		Column("f.receiving_type_of_station").
// 		Column("f.receiving_code").
// 		Column("f.receiving_region").
// 		Column("f.receiving_district").
// 		Column("f.comments").
// 		TableExpr("app.meter_consumption_daily AS mcd").
// 		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
// 		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
// 		Join("LEFT JOIN app.express_feeders AS f ON mtr.id = f.sending_meter_id OR mtr.id = f.receiving_meter_id").
// 		Where("mtr.meter_type = ?", "EXPRESS_FEEDER")

// 	// Apply dynamic filters (except meter_type)
// 	for _, f := range filters {
// 		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
// 			continue
// 		}
// 		q = q.Where(f.Query, f.Args...)
// 	}

// 	q = q.
// 		Group("mcd.consumption_date").
// 		Group("mcd.meter_number").
// 		Group("mtr.meter_type").
// 		Group("mtr.multiply_factor").
// 		Group("mtr.voltage_kv").
// 		Group("dim.system_name").
// 		Group("mcd.day_start_reading").
// 		Group("mcd.day_end_reading").
// 		Group("f.feeder_name").
// 		Group("f.sap_version").
// 		Group("f.sending_station").
// 		Group("f.sending_type_of_station").
// 		Group("f.sending_code").
// 		Group("f.sending_region").
// 		Group("f.sending_district").
// 		Group("f.receiving_station").
// 		Group("f.receiving_type_of_station").
// 		Group("f.receiving_code").
// 		Group("f.receiving_region").
// 		Group("f.receiving_district").
// 		Group("f.comments").
// 		Order("mcd.consumption_date")

// 	if err := q.Scan(ctx, &results); err != nil {
// 		return nil, err
// 	}

// 	return results, nil
// }

type expressFeederRawRow struct {
	ConsumptionDate        time.Time `bun:"consumption_date"`
	MeterNumber            string    `bun:"meter_number"`
	MeterType              string    `bun:"meter_type"`
	MultiplyFactor         string    `bun:"multiply_factor"`
	VoltageKv              string    `bun:"voltage_kv"`
	SystemName             string    `bun:"system_name"`
	ConsumedEnergy         float64   `bun:"consumed_energy"`
	Station                string    `bun:"station"`
	Region                 string    `bun:"region"`
	District               string    `bun:"district"`
	FeederName             string    `bun:"feeder_name"`
	SapVersion             string    `bun:"sap_version"`
	Comments               string    `bun:"comments"`
	SendingMeterID         string    `bun:"sending_meter_id"`
	ReceivingMeterID       string    `bun:"receiving_meter_id"`
	MeterID                string    `bun:"meter_id"`
	SendingStation         string    `bun:"sending_station"`
	SendingTypeOfStation   string    `bun:"sending_type_of_station"`
	SendingCode            string    `bun:"sending_code"`
	SendingRegion          string    `bun:"sending_region"`
	SendingDistrict        string    `bun:"sending_district"`
	ReceivingStation       string    `bun:"receiving_station"`
	ReceivingTypeOfStation string    `bun:"receiving_type_of_station"`
	ReceivingCode          string    `bun:"receiving_code"`
	ReceivingRegion        string    `bun:"receiving_region"`
	ReceivingDistrict      string    `bun:"receiving_district"`
}

func (s *Service) GetExpressFeederDailyConsumption(
	ctx context.Context,
	params ReadingFilterParams,
) ([]ExpressFeederDailyResult, error) {

	filters := buildReadingFilters(params)

	type rawRow struct {
		ConsumptionDate         time.Time `bun:"consumption_date"`
		FeederName              string    `bun:"feeder_name"`
		SapVersion              string    `bun:"sap_version"`
		Comments                string    `bun:"comments"`
		SendingMeterNumber      string    `bun:"sending_meter_number"`
		SendingMeterType        string    `bun:"sending_meter_type"`
		SendingMultiplyFactor   string    `bun:"sending_multiply_factor"`
		SendingVoltageKv        string    `bun:"sending_voltage_kv"`
		SendingStation          string    `bun:"sending_station"`
		SendingTypeOfStation    string    `bun:"sending_type_of_station"`
		SendingCode             string    `bun:"sending_code"`
		SendingRegion           string    `bun:"sending_region"`
		SendingDistrict         string    `bun:"sending_district"`
		ReceivingMeterNumber    string    `bun:"receiving_meter_number"`
		ReceivingMeterType      string    `bun:"receiving_meter_type"`
		ReceivingMultiplyFactor string    `bun:"receiving_multiply_factor"`
		ReceivingVoltageKv      string    `bun:"receiving_voltage_kv"`
		ReceivingStation        string    `bun:"receiving_station"`
		ReceivingTypeOfStation  string    `bun:"receiving_type_of_station"`
		ReceivingCode           string    `bun:"receiving_code"`
		ReceivingRegion         string    `bun:"receiving_region"`
		ReceivingDistrict       string    `bun:"receiving_district"`
		SendingSystemName       string    `bun:"sending_system_name"`
		ReceivingSystemName     string    `bun:"receiving_system_name"`
		SendingConsumption      float64   `bun:"sending_consumption"`
		ReceivingConsumption    float64   `bun:"receiving_consumption"`
	}

	q := s.db.NewSelect().
		TableExpr("app.express_feeders AS f").
		Join("LEFT JOIN app.meters AS sm ON f.sending_meter_id = sm.id").
		Join("LEFT JOIN app.meters AS rm ON f.receiving_meter_id = rm.id").
		Join("LEFT JOIN app.meter_consumption_daily AS smcd ON smcd.meter_number = sm.meter_number AND smcd.consumption_date BETWEEN ? AND ?", params.DateFrom, params.DateTo).
		Join("LEFT JOIN app.meter_consumption_daily AS rmcd ON rmcd.meter_number = rm.meter_number AND rmcd.consumption_date BETWEEN ? AND ?", params.DateFrom, params.DateTo).
		Join("LEFT JOIN app.data_item_mapping AS sdim ON smcd.data_item_id = sdim.data_item_id").
		Join("LEFT JOIN app.data_item_mapping AS rdim ON rmcd.data_item_id = rdim.data_item_id").
		ColumnExpr("DATE(COALESCE(smcd.consumption_date, rmcd.consumption_date)) AS consumption_date").
		ColumnExpr("f.feeder_name AS feeder_name").
		ColumnExpr("f.sap_version AS sap_version").
		ColumnExpr("f.comments AS comments").
		ColumnExpr("sm.meter_number AS sending_meter_number").
		ColumnExpr("sm.meter_type AS sending_meter_type").
		ColumnExpr("sm.multiply_factor::text AS sending_multiply_factor").
		ColumnExpr("sm.voltage_kv::text AS sending_voltage_kv").
		ColumnExpr("f.sending_station AS sending_station").
		ColumnExpr("f.sending_type_of_station AS sending_type_of_station").
		ColumnExpr("f.sending_code AS sending_code").
		ColumnExpr("f.sending_region AS sending_region").
		ColumnExpr("f.sending_district AS sending_district").
		ColumnExpr("rm.meter_number AS receiving_meter_number").
		ColumnExpr("rm.meter_type AS receiving_meter_type").
		ColumnExpr("rm.multiply_factor::text AS receiving_multiply_factor").
		ColumnExpr("rm.voltage_kv::text AS receiving_voltage_kv").
		ColumnExpr("f.receiving_station AS receiving_station").
		ColumnExpr("f.receiving_type_of_station AS receiving_type_of_station").
		ColumnExpr("f.receiving_code AS receiving_code").
		ColumnExpr("f.receiving_region AS receiving_region").
		ColumnExpr("f.receiving_district AS receiving_district").
		ColumnExpr("sdim.system_name AS sending_system_name").
		ColumnExpr("rdim.system_name AS receiving_system_name").
		ColumnExpr("ROUND(SUM(smcd.consumption)::numeric, 4) AS sending_consumption").
		ColumnExpr("ROUND(SUM(rmcd.consumption)::numeric, 4) AS receiving_consumption").
		GroupExpr("DATE(COALESCE(smcd.consumption_date, rmcd.consumption_date))").
		GroupExpr("f.feeder_name, f.sap_version, f.comments").
		GroupExpr("sm.meter_number, sm.meter_type, sm.multiply_factor, sm.voltage_kv").
		GroupExpr("rm.meter_number, rm.meter_type, rm.multiply_factor, rm.voltage_kv").
		GroupExpr("f.sending_station, f.sending_type_of_station, f.sending_code, f.sending_region, f.sending_district").
		GroupExpr("f.receiving_station, f.receiving_type_of_station, f.receiving_code, f.receiving_region, f.receiving_district").
		GroupExpr("sdim.system_name, rdim.system_name").
		OrderExpr("consumption_date, f.feeder_name")

	// ✅ NEW: Process filters – rewrite meter filters to apply to both sending and receiving meters
	for _, filter := range filters {
		lq := strings.ToLower(filter.Query)

		// Date filters are already enforced in JOIN conditions – skip them
		if strings.Contains(lq, "consumption_date") {
			continue
		}

		// Rewrite meter filters (mtr.*) to apply to both sending and receiving meters
		if strings.Contains(lq, "mtr.") {
			// Build left side (sending meter) and right side (receiving meter)
			left := strings.ReplaceAll(filter.Query, "mtr.", "sm.")
			right := strings.ReplaceAll(filter.Query, "mtr.", "rm.")
			combined := fmt.Sprintf("(%s OR %s)", left, right)
			// Duplicate arguments because both sides need them
			bothArgs := append(filter.Args, filter.Args...)
			q = q.Where(combined, bothArgs...)
			continue
		}

		// Filters that target the express_feeders table (f.) are kept as‑is
		q = q.Where(filter.Query, filter.Args...)
	}

	var rows []rawRow
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, err
	}

	type dailyKey struct {
		ConsumptionDate string
		FeederName      string
		SapVersion      string
	}

	pivoted := map[dailyKey]*ExpressFeederDailyResult{}
	var order []dailyKey

	for _, row := range rows {
		key := dailyKey{
			ConsumptionDate: row.ConsumptionDate.Format("2006-01-02"),
			FeederName:      row.FeederName,
			SapVersion:      row.SapVersion,
		}

		if _, exists := pivoted[key]; !exists {
			entry := &ExpressFeederDailyResult{
				ConsumptionDate:        row.ConsumptionDate,
				FeederName:             row.FeederName,
				SapVersion:             row.SapVersion,
				Comments:               row.Comments,
				SendingMeterNumber:     row.SendingMeterNumber,
				SendingStation:         row.SendingStation,
				SendingTypeOfStation:   row.SendingTypeOfStation,
				SendingCode:            row.SendingCode,
				SendingRegion:          row.SendingRegion,
				SendingDistrict:        row.SendingDistrict,
				ReceivingMeterNumber:   row.ReceivingMeterNumber,
				ReceivingStation:       row.ReceivingStation,
				ReceivingTypeOfStation: row.ReceivingTypeOfStation,
				ReceivingCode:          row.ReceivingCode,
				ReceivingRegion:        row.ReceivingRegion,
				ReceivingDistrict:      row.ReceivingDistrict,
			}
			if row.SendingMeterNumber != "" {
				entry.SendingMeter = &ExpressFeederMeterDetail{
					MeterType:      row.SendingMeterType,
					MultiplyFactor: row.SendingMultiplyFactor,
					VoltageKv:      row.SendingVoltageKv,
				}
			}
			if row.ReceivingMeterNumber != "" {
				entry.ReceivingMeter = &ExpressFeederMeterDetail{
					MeterType:      row.ReceivingMeterType,
					MultiplyFactor: row.ReceivingMultiplyFactor,
					VoltageKv:      row.ReceivingVoltageKv,
				}
			}
			pivoted[key] = entry
			order = append(order, key)
		}

		entry := pivoted[key]

		if entry.SendingMeter != nil {
			switch row.SendingSystemName {
			case "import_kwh":
				entry.SendingMeter.ImportKwh += row.SendingConsumption
			case "export_kwh":
				entry.SendingMeter.ExportKwh += row.SendingConsumption
			}
			entry.SendingMeter.NetKwh = entry.SendingMeter.ImportKwh - entry.SendingMeter.ExportKwh
		}

		if entry.ReceivingMeter != nil {
			switch row.ReceivingSystemName {
			case "import_kwh":
				entry.ReceivingMeter.ImportKwh += row.ReceivingConsumption
			case "export_kwh":
				entry.ReceivingMeter.ExportKwh += row.ReceivingConsumption
			}
			entry.ReceivingMeter.NetKwh = entry.ReceivingMeter.ImportKwh - entry.ReceivingMeter.ExportKwh
		}
	}

	var results []ExpressFeederDailyResult
	for _, key := range order {
		results = append(results, *pivoted[key])
	}

	return results, nil
}

func (s *Service) GetExpressFeederAggregatedConsumption(
	ctx context.Context,
	params ReadingFilterParams,
	groupBy string,
	additionalGroups []string,
) ([]ExpressFeederAggregatedConsumptionResult, error) {

	filters := buildReadingFilters(params)

	type rawRow struct {
		GroupPeriod            time.Time `bun:"group_period"`
		FeederName             string    `bun:"feeder_name"`
		SapVersion             string    `bun:"sap_version"`
		MeterType              string    `bun:"meter_type"`
		SendingMeterNumber     string    `bun:"sending_meter_number"`
		ReceivingMeterNumber   string    `bun:"receiving_meter_number"`
		SendingStation         string    `bun:"sending_station"`
		SendingTypeOfStation   string    `bun:"sending_type_of_station"`
		SendingCode            string    `bun:"sending_code"`
		SendingRegion          string    `bun:"sending_region"`
		SendingDistrict        string    `bun:"sending_district"`
		ReceivingStation       string    `bun:"receiving_station"`
		ReceivingTypeOfStation string    `bun:"receiving_type_of_station"`
		ReceivingCode          string    `bun:"receiving_code"`
		ReceivingRegion        string    `bun:"receiving_region"`
		ReceivingDistrict      string    `bun:"receiving_district"`
		SendingSystemName      string    `bun:"sending_system_name"`
		ReceivingSystemName    string    `bun:"receiving_system_name"`
		SendingConsumption     float64   `bun:"sending_consumption"`
		ReceivingConsumption   float64   `bun:"receiving_consumption"`
	}

	var periodExpr string
	switch groupBy {
	case "month":
		periodExpr = "DATE_TRUNC('month', COALESCE(smcd.consumption_date, rmcd.consumption_date))"
	case "year":
		periodExpr = "DATE_TRUNC('year', COALESCE(smcd.consumption_date, rmcd.consumption_date))"
	default:
		periodExpr = "DATE(COALESCE(smcd.consumption_date, rmcd.consumption_date))"
	}

	q := s.db.NewSelect().
		TableExpr("app.express_feeders AS f").
		Join("LEFT JOIN app.meters AS sm ON f.sending_meter_id = sm.id").
		Join("LEFT JOIN app.meters AS rm ON f.receiving_meter_id = rm.id").
		Join("LEFT JOIN app.meter_consumption_daily AS smcd ON smcd.meter_number = sm.meter_number AND smcd.consumption_date BETWEEN ? AND ?", params.DateFrom, params.DateTo).
		Join("LEFT JOIN app.meter_consumption_daily AS rmcd ON rmcd.meter_number = rm.meter_number AND rmcd.consumption_date BETWEEN ? AND ?", params.DateFrom, params.DateTo).
		Join("LEFT JOIN app.data_item_mapping AS sdim ON smcd.data_item_id = sdim.data_item_id").
		Join("LEFT JOIN app.data_item_mapping AS rdim ON rmcd.data_item_id = rdim.data_item_id").
		ColumnExpr(periodExpr + " AS group_period").
		ColumnExpr("f.feeder_name AS feeder_name").
		ColumnExpr("f.sap_version AS sap_version").
		ColumnExpr("COALESCE(sm.meter_type, rm.meter_type) AS meter_type").
		ColumnExpr("sm.meter_number AS sending_meter_number").
		ColumnExpr("rm.meter_number AS receiving_meter_number").
		ColumnExpr("f.sending_station AS sending_station").
		ColumnExpr("f.sending_type_of_station AS sending_type_of_station").
		ColumnExpr("f.sending_code AS sending_code").
		ColumnExpr("f.sending_region AS sending_region").
		ColumnExpr("f.sending_district AS sending_district").
		ColumnExpr("f.receiving_station AS receiving_station").
		ColumnExpr("f.receiving_type_of_station AS receiving_type_of_station").
		ColumnExpr("f.receiving_code AS receiving_code").
		ColumnExpr("f.receiving_region AS receiving_region").
		ColumnExpr("f.receiving_district AS receiving_district").
		ColumnExpr("sdim.system_name AS sending_system_name").
		ColumnExpr("rdim.system_name AS receiving_system_name").
		ColumnExpr("ROUND(SUM(smcd.consumption)::numeric, 4) AS sending_consumption").
		ColumnExpr("ROUND(SUM(rmcd.consumption)::numeric, 4) AS receiving_consumption").
		GroupExpr(periodExpr).
		GroupExpr("f.feeder_name, f.sap_version").
		GroupExpr("sm.meter_number, rm.meter_number").
		GroupExpr("sm.meter_type, rm.meter_type").
		GroupExpr("f.sending_station, f.sending_type_of_station, f.sending_code, f.sending_region, f.sending_district").
		GroupExpr("f.receiving_station, f.receiving_type_of_station, f.receiving_code, f.receiving_region, f.receiving_district").
		GroupExpr("sdim.system_name, rdim.system_name").
		OrderExpr("group_period, f.feeder_name")

	// ✅ NEW: Process filters – rewrite meter filters to apply to both sending and receiving meters
	for _, filter := range filters {
		lq := strings.ToLower(filter.Query)

		// Date filters are already enforced in JOIN conditions – skip them
		if strings.Contains(lq, "consumption_date") {
			continue
		}

		// Rewrite meter filters (mtr.*) to apply to both sending and receiving meters
		if strings.Contains(lq, "mtr.") {
			left := strings.ReplaceAll(filter.Query, "mtr.", "sm.")
			right := strings.ReplaceAll(filter.Query, "mtr.", "rm.")
			combined := fmt.Sprintf("(%s OR %s)", left, right)
			bothArgs := append(filter.Args, filter.Args...)
			q = q.Where(combined, bothArgs...)
			continue
		}

		// Filters that target the express_feeders table (f.) are kept as‑is
		q = q.Where(filter.Query, filter.Args...)
	}

	var rows []rawRow
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, err
	}

	type feederAggKey struct {
		GroupPeriod string
		FeederName  string
		SapVersion  string
	}

	pivoted := map[feederAggKey]*ExpressFeederAggregatedConsumptionResult{}
	var order []feederAggKey

	for _, row := range rows {
		key := feederAggKey{
			GroupPeriod: row.GroupPeriod.Format("2006-01-02"),
			FeederName:  row.FeederName,
			SapVersion:  row.SapVersion,
		}

		if _, exists := pivoted[key]; !exists {
			pivoted[key] = &ExpressFeederAggregatedConsumptionResult{
				GroupPeriod:            row.GroupPeriod,
				FeederName:             row.FeederName,
				SapVersion:             row.SapVersion,
				MeterType:              row.MeterType,
				SendingMeterNumber:     row.SendingMeterNumber,
				SendingStation:         row.SendingStation,
				SendingTypeOfStation:   row.SendingTypeOfStation,
				SendingCode:            row.SendingCode,
				SendingRegion:          row.SendingRegion,
				SendingDistrict:        row.SendingDistrict,
				ReceivingMeterNumber:   row.ReceivingMeterNumber,
				ReceivingStation:       row.ReceivingStation,
				ReceivingTypeOfStation: row.ReceivingTypeOfStation,
				ReceivingCode:          row.ReceivingCode,
				ReceivingRegion:        row.ReceivingRegion,
				ReceivingDistrict:      row.ReceivingDistrict,
				SendingMeter:           &ExpressFeederMeterAgg{},
				ReceivingMeter:         &ExpressFeederMeterAgg{},
			}
			order = append(order, key)
		}

		entry := pivoted[key]

		switch row.SendingSystemName {
		case "import_kwh":
			entry.SendingMeter.ImportKwh += row.SendingConsumption
		case "export_kwh":
			entry.SendingMeter.ExportKwh += row.SendingConsumption
		}
		entry.SendingMeter.NetKwh = entry.SendingMeter.ImportKwh - entry.SendingMeter.ExportKwh

		switch row.ReceivingSystemName {
		case "import_kwh":
			entry.ReceivingMeter.ImportKwh += row.ReceivingConsumption
		case "export_kwh":
			entry.ReceivingMeter.ExportKwh += row.ReceivingConsumption
		}
		entry.ReceivingMeter.NetKwh = entry.ReceivingMeter.ImportKwh - entry.ReceivingMeter.ExportKwh
	}

	var finalResults []ExpressFeederAggregatedConsumptionResult
	for _, key := range order {
		finalResults = append(finalResults, *pivoted[key])
	}

	return finalResults, nil
}

func (s *Service) GetPSSDailyConsumption(
	ctx context.Context,
	params ReadingFilterParams,
) ([]DailyConsumptionResults, error) {
	var results []DailyConsumptionResults

	filters := buildReadingFilters(params)

	q := s.db.NewSelect().
		ColumnExpr("mcd.consumption_date AT TIME ZONE 'UTC' AS consumption_date").
		Column("mcd.meter_number").
		Column("mtr.region").
		Column("mtr.district").
		Column("mtr.station").
		Column("mtr.meter_type").
		Column("mtr.feeder_panel_name").
		Column("mtr.ic_og").
		Column("mtr.voltage_kv").
		Column("mtr.multiply_factor").
		Column("mcd.day_start_reading").
		Column("mcd.day_end_reading").
		ColumnExpr("round((sum(mcd.consumption))::numeric, 4) AS consumed_energy").
		Column("dim.system_name").
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		Where("mtr.meter_type = ?", "PSS") // ✅ strict base filter

	// ✅ Apply dynamic filters (except meter_type)
	for _, f := range filters {
		// prevent user from overriding meter_type
		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	q = q.
		Group("mcd.consumption_date").
		Group("mcd.meter_number").
		Group("mtr.region").
		Group("mtr.district").
		Group("mtr.meter_type").
		Group("mtr.station").
		Group("mtr.multiply_factor").
		Group("mtr.feeder_panel_name").
		Group("mtr.ic_og").
		Group("mtr.voltage_kv").
		Group("dim.system_name").
		Group("mcd.day_start_reading").
		Group("mcd.day_end_reading")

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Service) GetPSSAggregatedConsumption(
	ctx context.Context,
	params ReadingFilterParams,
	groupBy string,
	additionalGroups []string,
) ([]AggregatedConsumptionResult, error) {

	var results []AggregatedConsumptionResult
	filters := buildReadingFilters(params)

	q := s.db.NewSelect().
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		ColumnExpr("dim.system_name AS system_name").
		ColumnExpr("mtr.station AS station").
		ColumnExpr("mtr.feeder_panel_name AS feeder_panel_name").
		ColumnExpr("mtr.ic_og AS ic_og").
		ColumnExpr("ROUND(SUM(mcd.consumption)::numeric, 4) AS total_consumption").
		ColumnExpr("COUNT(DISTINCT mcd.meter_number) AS active_meters").
		Where("mtr.meter_type = ?", "PSS")

	// --- Subquery 1: total_meter_count (filtered) ---
	subQFiltered := s.db.NewSelect().
		TableExpr("app.meters AS mtr2").
		Join("JOIN app.meter_consumption_daily AS mcd2 ON mcd2.meter_number = mtr2.meter_number").
		ColumnExpr("COUNT(DISTINCT mtr2.meter_number)").
		Where("mtr2.meter_type = ?", "PSS")

	for _, f := range filters {
		qry := strings.ReplaceAll(f.Query, "mtr.", "mtr2.")
		qry = strings.ReplaceAll(qry, "mcd.", "mcd2.")
		if strings.Contains(strings.ToLower(qry), "meter_type") {
			continue
		}
		subQFiltered = subQFiltered.Where(qry, f.Args...)
	}

	q = q.ColumnExpr("(?) AS total_meter_count", subQFiltered)

	// --- Subquery 2: all_meters_count (unfiltered PSS) ---
	subQAll := s.db.NewSelect().
		TableExpr("app.meters AS mtr3").
		ColumnExpr("COUNT(DISTINCT mtr3.meter_number)").
		Where("mtr3.meter_type = ?", "PSS")

	q = q.ColumnExpr("(?) AS all_meters_count", subQAll)

	// --- Time grouping ---
	groupCols := []bun.Safe{
		bun.Safe("dim.system_name"),
		bun.Safe("mtr.station"),
		bun.Safe("mtr.feeder_panel_name"),
		bun.Safe("mtr.ic_og"),
	}

	switch groupBy {
	case "day":
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	case "month":
		q = q.ColumnExpr("DATE_TRUNC('month', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('month', mcd.consumption_date)"))
	case "year":
		q = q.ColumnExpr("DATE_TRUNC('year', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('year', mcd.consumption_date)"))
	default:
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	}

	// --- Additional grouping ---
	for _, g := range additionalGroups {
		col := fmt.Sprintf("mtr.%s", g)
		if g != "meter_type" {
			col = fmt.Sprintf("LOWER(mtr.%s)", g)
		}
		q = q.ColumnExpr(fmt.Sprintf("%s AS %s", col, g))
		groupCols = append(groupCols, bun.Safe(col))
	}

	// --- Apply filters (skip meter_type override) ---
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	// --- Group by all relevant columns ---
	for _, g := range groupCols {
		q = q.GroupExpr(string(g))
	}

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Service) GetSSDailyConsumption(
	ctx context.Context,
	params ReadingFilterParams,
) ([]DailyConsumptionResults, error) {
	var results []DailyConsumptionResults

	filters := buildReadingFilters(params)

	q := s.db.NewSelect().
		ColumnExpr("mcd.consumption_date AT TIME ZONE 'UTC' AS consumption_date").
		Column("mcd.meter_number").
		Column("mtr.region").
		Column("mtr.district").
		Column("mtr.station").
		Column("mtr.meter_type").
		Column("mtr.feeder_panel_name").
		Column("mtr.voltage_kv").
		Column("mtr.multiply_factor").
		Column("mcd.day_start_reading").
		Column("mcd.day_end_reading").
		ColumnExpr("round((sum(mcd.consumption))::numeric, 4) AS consumed_energy").
		Column("dim.system_name").
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		Where("mtr.meter_type = ?", "SS") // ✅ strict base filter

	// ✅ Apply dynamic filters (except meter_type)
	for _, f := range filters {
		// prevent user from overriding meter_type
		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	q = q.
		Group("mcd.consumption_date").
		Group("mcd.meter_number").
		Group("mtr.region").
		Group("mtr.district").
		Group("mtr.meter_type").
		Group("mtr.station").
		Group("mtr.multiply_factor").
		Group("mtr.feeder_panel_name").
		Group("mtr.voltage_kv").
		Group("dim.system_name").
		Group("mcd.day_start_reading").
		Group("mcd.day_end_reading")

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Service) GetSSAggregatedConsumption(
	ctx context.Context,
	params ReadingFilterParams,
	groupBy string,
	additionalGroups []string,
) ([]AggregatedConsumptionResult, error) {

	var results []AggregatedConsumptionResult
	filters := buildReadingFilters(params)

	q := s.db.NewSelect().
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		ColumnExpr("dim.system_name AS system_name").
		ColumnExpr("mtr.station AS station").
		ColumnExpr("mtr.feeder_panel_name AS feeder_panel_name").
		ColumnExpr("mtr.ic_og AS ic_og").
		ColumnExpr("ROUND(SUM(mcd.consumption)::numeric, 4) AS total_consumption").
		ColumnExpr("COUNT(DISTINCT mcd.meter_number) AS active_meters").
		Where("mtr.meter_type = ?", "SS")

	// --- Subquery 1: total_meter_count (filtered) ---
	subQFiltered := s.db.NewSelect().
		TableExpr("app.meters AS mtr2").
		Join("JOIN app.meter_consumption_daily AS mcd2 ON mcd2.meter_number = mtr2.meter_number").
		ColumnExpr("COUNT(DISTINCT mtr2.meter_number)").
		Where("mtr2.meter_type = ?", "SS")

	for _, f := range filters {
		qry := strings.ReplaceAll(f.Query, "mtr.", "mtr2.")
		qry = strings.ReplaceAll(qry, "mcd.", "mcd2.")
		if strings.Contains(strings.ToLower(qry), "meter_type") {
			continue
		}
		subQFiltered = subQFiltered.Where(qry, f.Args...)
	}

	q = q.ColumnExpr("(?) AS total_meter_count", subQFiltered)

	// --- Subquery 2: all_meters_count (unfiltered SS) ---
	subQAll := s.db.NewSelect().
		TableExpr("app.meters AS mtr3").
		ColumnExpr("COUNT(DISTINCT mtr3.meter_number)").
		Where("mtr3.meter_type = ?", "SS")

	q = q.ColumnExpr("(?) AS all_meters_count", subQAll)

	// --- Time grouping ---
	groupCols := []bun.Safe{
		bun.Safe("dim.system_name"),
		bun.Safe("mtr.station"),
		bun.Safe("mtr.feeder_panel_name"),
		bun.Safe("mtr.ic_og"),
	}

	switch groupBy {
	case "day":
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	case "month":
		q = q.ColumnExpr("DATE_TRUNC('month', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('month', mcd.consumption_date)"))
	case "year":
		q = q.ColumnExpr("DATE_TRUNC('year', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('year', mcd.consumption_date)"))
	default:
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	}

	// --- Additional grouping ---
	for _, g := range additionalGroups {
		col := fmt.Sprintf("mtr.%s", g)
		if g != "meter_type" {
			col = fmt.Sprintf("LOWER(mtr.%s)", g)
		}
		q = q.ColumnExpr(fmt.Sprintf("%s AS %s", col, g))
		groupCols = append(groupCols, bun.Safe(col))
	}

	// --- Apply filters (skip meter_type override) ---
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	// --- Group by all relevant columns ---
	for _, g := range groupCols {
		q = q.GroupExpr(string(g))
	}

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Service) GetRegionalBoundaryAggregatedConsumption(
	ctx context.Context,
	params ReadingFilterParams,
	groupBy string,
	additionalGroups []string,
) ([]AggregatedConsumptionResult, error) {

	var results []AggregatedConsumptionResult
	filters := buildReadingFilters(params)

	q := s.db.NewSelect().
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		ColumnExpr("dim.system_name AS system_name").
		ColumnExpr("mtr.boundary_metering_point AS boundary_metering_point").
		ColumnExpr("mtr.location AS location").
		ColumnExpr("ROUND(SUM(mcd.consumption)::numeric, 4) AS total_consumption").
		ColumnExpr("COUNT(DISTINCT mcd.meter_number) AS active_meters").
		Where("mtr.meter_type = ?", "REGIONAL_BOUNDARY")

	// --- Subquery for total meter count ---
	subQ := s.db.NewSelect().
		TableExpr("app.meters AS mtr2").
		ColumnExpr("COUNT(DISTINCT mtr2.meter_number)").
		Where("mtr2.meter_type = ?", "REGIONAL_BOUNDARY")

	if len(params.Regions) > 0 {
		subQ = subQ.Where("mtr2.region IN (?)", bun.In(params.Regions))
	}
	if len(params.Districts) > 0 {
		subQ = subQ.Where("mtr2.district IN (?)", bun.In(params.Districts))
	}
	if len(params.Stations) > 0 {
		subQ = subQ.Where("mtr2.station IN (?)", bun.In(params.Stations))
	}
	if len(params.Locations) > 0 {
		subQ = subQ.Where("mtr2.location IN (?)", bun.In(params.Locations))
	}

	q = q.ColumnExpr("(?) AS total_meter_count", subQ)

	// --- Time grouping ---
	groupCols := []bun.Safe{
		bun.Safe("dim.system_name"),
		bun.Safe("mtr.boundary_metering_point"), // ✅ Add here!
		bun.Safe("mtr.location"),                // ✅ Add here!
	}

	switch groupBy {
	case "day":
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	case "month":
		q = q.ColumnExpr("DATE_TRUNC('month', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('month', mcd.consumption_date)"))
	case "year":
		q = q.ColumnExpr("DATE_TRUNC('year', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('year', mcd.consumption_date)"))
	default:
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	}

	// --- Additional grouping ---
	for _, g := range additionalGroups {
		col := fmt.Sprintf("mtr.%s", g)
		if g != "meter_type" {
			col = fmt.Sprintf("LOWER(mtr.%s)", g)
		}
		q = q.ColumnExpr(fmt.Sprintf("%s AS %s", col, g))
		groupCols = append(groupCols, bun.Safe(col))
	}

	// --- Apply filters (skip meter_type override) ---
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	// --- Group by all relevant columns ---
	for _, g := range groupCols {
		q = q.GroupExpr(string(g))
	}

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Service) GetDistrictBoundaryAggregatedConsumption(
	ctx context.Context,
	params ReadingFilterParams,
	groupBy string,
	additionalGroups []string,
) ([]AggregatedConsumptionResult, error) {

	var results []AggregatedConsumptionResult
	filters := buildReadingFilters(params)

	q := s.db.NewSelect().
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		ColumnExpr("dim.system_name AS system_name").
		ColumnExpr("mtr.boundary_metering_point AS boundary_metering_point").
		ColumnExpr("mtr.location AS location").
		ColumnExpr("ROUND(SUM(mcd.consumption)::numeric, 4) AS total_consumption").
		ColumnExpr("COUNT(DISTINCT mcd.meter_number) AS active_meters").
		Where("mtr.meter_type = ?", "DISTRICT_BOUNDARY")

	// --- Subquery for total meter count ---
	subQ := s.db.NewSelect().
		TableExpr("app.meters AS mtr2").
		ColumnExpr("COUNT(DISTINCT mtr2.meter_number)").
		Where("mtr2.meter_type = ?", "DISTRICT_BOUNDARY")

	if len(params.Regions) > 0 {
		subQ = subQ.Where("mtr2.region IN (?)", bun.In(params.Regions))
	}
	if len(params.Districts) > 0 {
		subQ = subQ.Where("mtr2.district IN (?)", bun.In(params.Districts))
	}
	if len(params.Stations) > 0 {
		subQ = subQ.Where("mtr2.station IN (?)", bun.In(params.Stations))
	}
	if len(params.Locations) > 0 {
		subQ = subQ.Where("mtr2.location IN (?)", bun.In(params.Locations))
	}

	q = q.ColumnExpr("(?) AS total_meter_count", subQ)

	// --- Time grouping ---
	groupCols := []bun.Safe{
		bun.Safe("dim.system_name"),
		bun.Safe("mtr.boundary_metering_point"), // ✅ Add here!
		bun.Safe("mtr.location"),                // ✅ Add here!
	}

	switch groupBy {
	case "day":
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	case "month":
		q = q.ColumnExpr("DATE_TRUNC('month', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('month', mcd.consumption_date)"))
	case "year":
		q = q.ColumnExpr("DATE_TRUNC('year', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('year', mcd.consumption_date)"))
	default:
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	}

	// --- Additional grouping ---
	for _, g := range additionalGroups {
		col := fmt.Sprintf("mtr.%s", g)
		if g != "meter_type" {
			col = fmt.Sprintf("LOWER(mtr.%s)", g)
		}
		q = q.ColumnExpr(fmt.Sprintf("%s AS %s", col, g))
		groupCols = append(groupCols, bun.Safe(col))
	}

	// --- Apply filters (skip meter_type override) ---
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "meter_type") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	// --- Group by all relevant columns ---
	for _, g := range groupCols {
		q = q.GroupExpr(string(g))
	}

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Service) GetMeterStatus(ctx context.Context, params ReadingFilterParams) ([]MeterStatusResult, error) {
	var results []MeterStatusResult

	q := s.db.NewSelect().
		TableExpr("app.meters AS mtr").
		Column("mtr.meter_number").
		Column("mtr.meter_type").
		Column("mtr.region").
		Column("mtr.district").
		Column("mtr.boundary_metering_point").
		Column("mtr.station").
		Column("mtr.feeder_panel_name").
		Column("mtr.location").
		Column("mcd.consumption_date").
		Column("mcd.consumption").
		Column("mcd.reading_count").
		Column("mcd.day_start_time").
		Column("mcd.day_end_time").
		ColumnExpr(`
	CASE
	WHEN mcd.meter_number IS NULL THEN 'OFFLINE - No Record'
	WHEN mcd.data_item_id = 'NO_DATA' THEN 'OFFLINE - No Data'
	ELSE 'ONLINE'
	END AS status`).
		Join(`
	LEFT JOIN app.meter_consumption_daily AS mcd
	ON mtr.meter_number = mcd.meter_number
	AND mcd.consumption_date BETWEEN ? AND ?
	`, params.DateFrom, params.DateTo)

	// ----------------------------------------------------
	// Apply universal filters (same as other functions)
	// ----------------------------------------------------
	filters := buildReadingFilters(params)

	for _, f := range filters {
		// Skip date filter: JOIN already applied it
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	// Sorting
	q = q.OrderExpr(`
	CASE
	WHEN mcd.meter_number IS NULL THEN 1
	WHEN mcd.data_item_id = 'NO_DATA' THEN 2
	ELSE 3
	END ASC,
		mtr.meter_number ASC
	`)

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil

}

func (s *Service) GetMeterStatusCounts(
	ctx context.Context,
	params ReadingFilterParams,
) (map[string]int, error) {

	counts := map[string]int{
		"total":             0,
		"online":            0,
		"offline_no_record": 0,
		"offline_no_data":   0,
	}

	filters := buildReadingFilters(params)

	// 🔥 Fixed query using BOOL_OR approach
	q := s.db.NewSelect().
		TableExpr("app.meters AS mtr").
		ColumnExpr(`
          COUNT(DISTINCT CASE WHEN mcd.meter_number IS NULL THEN mtr.id END) AS offline_no_record
       `).
		ColumnExpr(`
          COUNT(DISTINCT CASE WHEN mcd.has_actual_data = false THEN mtr.id END) AS offline_no_data
       `).
		ColumnExpr(`
          COUNT(DISTINCT CASE WHEN mcd.has_actual_data = true THEN mtr.id END) AS online
       `).
		Join(`
          LEFT JOIN (
             SELECT
                meter_number,
                MAX(consumption_date) AS last_consumption_date,
                BOOL_OR(data_item_id != 'NO_DATA' AND data_item_id = '00100000') AS has_actual_data
             FROM app.meter_consumption_daily
             WHERE consumption_date BETWEEN ? AND ?
             GROUP BY meter_number
          ) AS mcd
          ON mtr.meter_number = mcd.meter_number
       `, params.DateFrom, params.DateTo)

	// Apply filters on meters table
	for _, f := range filters {
		// Skip date filters since they're already in the subquery
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	var row struct {
		Online          int `bun:"online"`
		OfflineNoRecord int `bun:"offline_no_record"`
		OfflineNoData   int `bun:"offline_no_data"`
	}

	if err := q.Scan(ctx, &row); err != nil {
		return nil, err
	}

	counts["online"] = row.Online
	counts["offline_no_record"] = row.OfflineNoRecord
	counts["offline_no_data"] = row.OfflineNoData
	counts["total"] = row.Online + row.OfflineNoRecord + row.OfflineNoData

	return counts, nil
}

// GetMeterStatusSummary returns aggregated status counts and metrics
func (s *Service) GetMeterStatusSummary(
	ctx context.Context,
	params ReadingFilterParams,
) (*MeterStatusSummary, error) {

	// Calculate days in range for accurate uptime percentage
	daysInRange := int(params.DateTo.Sub(params.DateFrom).Hours()/24) + 1

	filters := buildReadingFilters(params)

	// Build the query
	q := s.db.NewSelect().
		TableExpr("app.meters AS mtr").
		ColumnExpr("COUNT(DISTINCT mtr.meter_number) as total_meters").
		ColumnExpr(`
			COUNT(DISTINCT CASE
				WHEN mcd.has_actual_data = true THEN mtr.meter_number
			END) as online_meters
		`).
		ColumnExpr(`
			COUNT(DISTINCT CASE
				WHEN mcd.has_actual_data = false THEN mtr.meter_number
			END) as offline_no_data_meters
		`).
		ColumnExpr(`
			COUNT(DISTINCT CASE
				WHEN mcd.meter_number IS NULL THEN mtr.meter_number
			END) as offline_no_record_meters
		`).
		ColumnExpr(`
			COALESCE(SUM(mcd.total_consumption), 0) as total_consumption
		`).
		ColumnExpr(`
			COALESCE(AVG(mcd.uptime_percentage), 0) as avg_uptime
		`).
		Join(`
			LEFT JOIN (
				SELECT
					meter_number,
					MAX(consumption_date) as last_consumption_date,
					BOOL_OR(data_item_id != 'NO_DATA') as has_actual_data,
					SUM(consumption) as total_consumption,
					(COUNT(DISTINCT CASE WHEN data_item_id != 'NO_DATA' THEN DATE(consumption_date) END) * 100.0 /
					 ?) as uptime_percentage
				FROM app.meter_consumption_daily
				WHERE consumption_date BETWEEN ? AND ?
				GROUP BY meter_number
			) AS mcd ON mtr.meter_number = mcd.meter_number
		`, daysInRange, params.DateFrom, params.DateTo)

	// Apply filters (skip date filters since they're in the subquery)
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	// Result struct for raw query
	var result struct {
		TotalMeters           int     `bun:"total_meters"`
		OnlineMeters          int     `bun:"online_meters"`
		OfflineNoDataMeters   int     `bun:"offline_no_data_meters"`
		OfflineNoRecordMeters int     `bun:"offline_no_record_meters"`
		TotalConsumption      float64 `bun:"total_consumption"`
		AvgUptime             float64 `bun:"avg_uptime"`
	}

	if err := q.Scan(ctx, &result); err != nil {
		return nil, err
	}

	// Calculate derived values
	totalOffline := result.OfflineNoDataMeters + result.OfflineNoRecordMeters
	onlinePercentage := 0.0
	offlinePercentage := 0.0

	if result.TotalMeters > 0 {
		onlinePercentage = float64(result.OnlineMeters) * 100.0 / float64(result.TotalMeters)
		offlinePercentage = float64(totalOffline) * 100.0 / float64(result.TotalMeters)
	}

	// Build filters applied map
	filtersApplied := map[string]interface{}{
		"dateFrom": params.DateFrom.Format("2006-01-02"),
		"dateTo":   params.DateTo.Format("2006-01-02"),
	}
	if len(params.Regions) > 0 {
		filtersApplied["region"] = params.Regions
	}
	if len(params.Districts) > 0 {
		filtersApplied["district"] = params.Districts
	}
	if len(params.Stations) > 0 {
		filtersApplied["station"] = params.Stations
	}
	if len(params.MeterTypes) > 0 {
		filtersApplied["meterType"] = params.MeterTypes
	}

	return &MeterStatusSummary{
		Total:               result.TotalMeters,
		Online:              result.OnlineMeters,
		OfflineNoData:       result.OfflineNoDataMeters,
		OfflineNoRecord:     result.OfflineNoRecordMeters,
		TotalOffline:        totalOffline,
		OnlinePercentage:    onlinePercentage,
		OfflinePercentage:   offlinePercentage,
		AvgUptimePercentage: result.AvgUptime,
		TotalConsumptionKWh: result.TotalConsumption,
		FiltersApplied:      filtersApplied,
	}, nil
}

// GetMeterStatusTimeline returns daily online/offline counts for charts

//func (s *Service) GetMeterStatusTimeline(
//	ctx context.Context,
//	params ReadingFilterParams,
//) (*MeterStatusTimeline, error) {
//
//	filters := buildReadingFilters(params)
//
//	// Build subquery for meter list with filters
//	meterSubquery := s.db.NewSelect().
//		TableExpr("app.meters AS mtr").
//		Column("mtr.meter_number")
//
//	// Apply meter filters (skip date filters)
//	for _, f := range filters {
//		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
//			continue
//		}
//		// Replace mcd. with mtr. for meter filters
//		qry := strings.ReplaceAll(f.Query, "mcd.", "mtr.")
//		meterSubquery = meterSubquery.Where(qry, f.Args...)
//	}
//
//	// ✅ FIX: Use a subquery that determines status per meter per day first
//	var entries []MeterStatusTimelineEntry
//
//	err := s.db.NewSelect().
//		ColumnExpr("date").
//		ColumnExpr("COUNT(DISTINCT CASE WHEN is_online THEN meter_number END) as online").
//		ColumnExpr("COUNT(DISTINCT CASE WHEN NOT is_online THEN meter_number END) as offline").
//		ColumnExpr("COUNT(DISTINCT meter_number) as total").
//		TableExpr(`(
//			SELECT
//				DATE(mcd.consumption_date) as date,
//				mcd.meter_number,
//				BOOL_OR(mcd.data_item_id = '00100000') as is_online
//			FROM app.meter_consumption_daily AS mcd
//			WHERE mcd.consumption_date BETWEEN ? AND ?
//			  AND mcd.meter_number IN (?)
//			GROUP BY DATE(mcd.consumption_date), mcd.meter_number
//		) AS daily_status`, params.DateFrom, params.DateTo, meterSubquery).
//		GroupExpr("date").
//		OrderExpr("date ASC").
//		Scan(ctx, &entries)
//
//	if err != nil {
//		return nil, err
//	}
//
//	timeline := &MeterStatusTimeline{
//		Data: entries,
//	}
//	timeline.DateRange.From = params.DateFrom.Format("2006-01-02")
//	timeline.DateRange.To = params.DateTo.Format("2006-01-02")
//
//	return timeline, nil
//}

// GetMeterStatusTimeline returns daily online/offline counts for charts
func (s *Service) GetMeterStatusTimeline(
	ctx context.Context,
	params ReadingFilterParams,
) (*MeterStatusTimeline, error) {

	filters := buildReadingFilters(params)

	// Build subquery for meter list with filters
	meterSubquery := s.db.NewSelect().
		TableExpr("app.meters AS mtr").
		Column("mtr.meter_number")

	// Apply meter filters (skip date filters)
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		// Replace mcd. with mtr. for meter filters
		qry := strings.ReplaceAll(f.Query, "mcd.", "mtr.")
		meterSubquery = meterSubquery.Where(qry, f.Args...)
	}

	// Main query for timeline with corrected status logic
	var entries []MeterStatusTimelineEntry

	err := s.db.NewSelect().
		ColumnExpr("date").
		ColumnExpr("COUNT(*) FILTER (WHERE is_online) AS online").
		ColumnExpr("COUNT(*) FILTER (WHERE NOT is_online OR is_online IS NULL) AS offline").
		ColumnExpr("COUNT(*) AS total").
		TableExpr(`(
        SELECT
            d.date,
            mtr.meter_number,
            BOOL_OR(mcd.data_item_id != 'NO_DATA') AS is_online
        FROM (
            SELECT generate_series(
                DATE(?),
                DATE(?),
                interval '1 day'
            )::date AS date
        ) d
        CROSS JOIN (
            SELECT mtr.meter_number
            FROM app.meters mtr
            WHERE mtr.meter_number IN (?)
        ) mtr
        LEFT JOIN app.meter_consumption_daily mcd
          ON DATE(mcd.consumption_date) = d.date
         AND mcd.meter_number = mtr.meter_number
        GROUP BY d.date, mtr.meter_number
    ) AS daily_status`,
			params.DateFrom,
			params.DateTo,
			meterSubquery,
		).
		Group("date").
		Order("date ASC").
		Scan(ctx, &entries)

	if err != nil {
		return nil, err
	}

	timeline := &MeterStatusTimeline{
		Data: entries,
	}
	timeline.DateRange.From = params.DateFrom.Format("2006-01-02")
	timeline.DateRange.To = params.DateTo.Format("2006-01-02")

	return timeline, nil
}

// GetMeterStatusDetails returns paginated meter status details
func (s *Service) GetMeterStatusDetails(
	ctx context.Context,
	params StatusDetailQueryParams,
) (*MeterStatusDetailResponse, error) {

	// Validate and set defaults
	if params.Page < 1 {
		params.Page = 1
	}
	if params.Limit <= 0 || params.Limit > 200 {
		params.Limit = 50
	}
	if params.SortOrder == "" {
		params.SortOrder = "desc"
	}

	// Calculate days in range for accurate uptime percentage
	daysInRange := int(params.DateTo.Sub(params.DateFrom).Hours()/24) + 1

	filters := buildReadingFilters(params.ReadingFilterParams)

	// Fast count query - just count meters, not consumption data
	countQuery := s.db.NewSelect().
		TableExpr("app.meters AS mtr").
		Where("1=1")

	// Apply meter filters only (no consumption join needed for count)
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		countQuery = countQuery.Where(f.Query, f.Args...)
	}

	if params.Search != "" {
		countQuery = countQuery.Where("mtr.meter_number ILIKE ?", "%"+params.Search+"%")
	}

	// Fast count without aggregating consumption
	totalCount, err := countQuery.Count(ctx)
	if err != nil {
		return nil, err
	}

	// Build the aggregated meter summary query
	q := s.db.NewSelect().
		TableExpr("app.meters AS mtr").
		Column("mtr.meter_number").
		Column("mtr.meter_type").
		Column("mtr.region").
		Column("mtr.district").
		Column("mtr.station").
		Column("mtr.voltage_kv").
		Column("mtr.feeder_panel_name").
		Column("mtr.location").
		Column("mtr.ic_og").
		Column("mtr.boundary_metering_point").
		ColumnExpr(`
			CASE
				WHEN COUNT(CASE WHEN mcd.data_item_id != 'NO_DATA' THEN 1 END) > 0 THEN 'ONLINE'
				WHEN COUNT(CASE WHEN mcd.data_item_id = 'NO_DATA' THEN 1 END) > 0 THEN 'OFFLINE - No Data'
				ELSE 'OFFLINE - No Record'
			END as status
		`).
		ColumnExpr("MAX(mcd.consumption_date) as last_consumption_date").
		ColumnExpr("COALESCE(SUM(mcd.consumption), 0) as total_consumption_kwh").
		ColumnExpr(`
			(COUNT(DISTINCT CASE WHEN mcd.data_item_id != 'NO_DATA' THEN DATE(mcd.consumption_date) END) * 100.0 /
			 ?) as uptime_percentage
		`, daysInRange).
		ColumnExpr(`
			(? - COUNT(DISTINCT CASE WHEN mcd.data_item_id != 'NO_DATA' THEN DATE(mcd.consumption_date) END)) as days_offline
		`, daysInRange).
		ColumnExpr("MAX(mcd.day_end_time) as last_reading_time").
		Join(`
			LEFT JOIN app.meter_consumption_daily AS mcd
			ON mtr.meter_number = mcd.meter_number
			AND mcd.consumption_date BETWEEN ? AND ?
		`, params.DateFrom, params.DateTo)

	// Apply meter filters (skip date filters)
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	// Apply search filter
	if params.Search != "" {
		q = q.Where("mtr.meter_number ILIKE ?", "%"+params.Search+"%")
	}

	// Group by meter
	q = q.Group("mtr.meter_number").
		Group("mtr.meter_type").
		Group("mtr.region").
		Group("mtr.district").
		Group("mtr.station").
		Group("mtr.feeder_panel_name").
		Group("mtr.location").
		Group("mtr.voltage_kv").
		Group("mtr.ic_og").
		Group("mtr.boundary_metering_point")

	// Apply status filter after grouping (via HAVING)
	if params.Status != "" {
		if params.Status == "ONLINE" {
			q = q.Having("COUNT(CASE WHEN mcd.data_item_id != 'NO_DATA' THEN 1 END) > 0")
		} else if params.Status == "OFFLINE" {
			q = q.Having("COUNT(CASE WHEN mcd.data_item_id != 'NO_DATA' THEN 1 END) = 0")
		}
	}

	// Apply sorting
	sortOrder := "DESC"
	if strings.ToLower(params.SortOrder) == "asc" {
		sortOrder = "ASC"
	}

	switch params.SortBy {
	case "uptime":
		q = q.OrderExpr("uptime_percentage " + sortOrder)
	case "consumption":
		q = q.OrderExpr("total_consumption_kwh " + sortOrder)
	case "meter_number":
		q = q.OrderExpr("mtr.meter_number " + sortOrder)
	default:
		q = q.OrderExpr("mtr.meter_number ASC")
	}

	// Apply pagination
	offset := (params.Page - 1) * params.Limit
	q = q.Limit(params.Limit).Offset(offset)

	// Execute query
	var records []MeterStatusDetailRecord
	if err := q.Scan(ctx, &records); err != nil {
		return nil, err
	}

	// Build response
	totalPages := (totalCount + params.Limit - 1) / params.Limit
	hasMore := params.Page < totalPages

	// Build filters applied map
	filtersApplied := map[string]interface{}{
		"dateFrom": params.DateFrom.Format("2006-01-02"),
		"dateTo":   params.DateTo.Format("2006-01-02"),
	}
	if len(params.Regions) > 0 {
		filtersApplied["region"] = params.Regions
	}
	if len(params.MeterTypes) > 0 {
		filtersApplied["meterType"] = params.MeterTypes
	}
	if params.Search != "" {
		filtersApplied["search"] = params.Search
	}
	if params.Status != "" {
		filtersApplied["status"] = params.Status
	}
	if params.SortBy != "" {
		filtersApplied["sortBy"] = params.SortBy
		filtersApplied["sortOrder"] = params.SortOrder
	}

	response := &MeterStatusDetailResponse{
		Data:           records,
		FiltersApplied: filtersApplied,
	}
	response.Pagination.Page = params.Page
	response.Pagination.Limit = params.Limit
	response.Pagination.TotalRecords = totalCount
	response.Pagination.TotalPages = totalPages
	response.Pagination.HasMore = hasMore

	return response, nil
}

// GetConsumptionByRegion returns consumption aggregated by region over time
// (No changes needed - this one was already correct)
func (s *Service) GetConsumptionByRegion(
	ctx context.Context,
	params ReadingFilterParams,
	groupBy string,
) (*ConsumptionByRegionResponse, error) {

	// Validate groupBy parameter
	if groupBy == "" {
		groupBy = "day"
	}

	filters := buildReadingFilters(params)

	// Determine date grouping expression
	var dateGroupExpr string
	switch groupBy {
	case "week":
		dateGroupExpr = "DATE_TRUNC('week', mcd.consumption_date)"
	case "month":
		dateGroupExpr = "DATE_TRUNC('month', mcd.consumption_date)"
	case "year":
		dateGroupExpr = "DATE_TRUNC('year', mcd.consumption_date)"
	default: // day
		dateGroupExpr = "DATE(mcd.consumption_date)"
	}

	// Build the main query
	q := s.db.NewSelect().
		ColumnExpr(dateGroupExpr + " as date").
		Column("mtr.region").
		ColumnExpr("COALESCE(SUM(mcd.consumption), 0) as total_consumption_kwh").
		ColumnExpr("COUNT(DISTINCT mcd.meter_number) as meter_count").
		ColumnExpr("COALESCE(AVG(mcd.consumption), 0) as avg_consumption_per_meter").
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Where("mcd.consumption IS NOT NULL")

	// Apply filters
	for _, f := range filters {
		q = q.Where(f.Query, f.Args...)
	}

	// Group by date and region
	q = q.GroupExpr(dateGroupExpr).
		Group("mtr.region").
		OrderExpr("date ASC, mtr.region ASC")

	// Execute query
	var entries []ConsumptionByRegionEntry
	if err := q.Scan(ctx, &entries); err != nil {
		return nil, err
	}

	// Calculate summary statistics
	var totalConsumption float64
	regionsMap := make(map[string]bool)

	for _, entry := range entries {
		totalConsumption += entry.TotalConsumptionKWh
		if entry.Region != "" {
			regionsMap[entry.Region] = true
		}
	}

	// Build response
	response := &ConsumptionByRegionResponse{
		Data: entries,
	}
	response.Summary.TotalConsumptionKWh = totalConsumption
	response.Summary.UniqueRegions = len(regionsMap)
	response.Summary.DateRange.From = params.DateFrom.Format("2006-01-02")
	response.Summary.DateRange.To = params.DateTo.Format("2006-01-02")

	return response, nil
}

// GetMeterHealthMetrics returns health breakdown and metrics

func (s *Service) GetMeterHealthMetrics(
	ctx context.Context,
	params ReadingFilterParams,
) (*MeterHealthMetrics, error) {

	// ✅ Calculate days in range for accurate uptime percentage
	daysInRange := int(params.DateTo.Sub(params.DateFrom).Hours()/24) + 1

	filters := buildReadingFilters(params)

	// Define health thresholds
	const (
		healthyThreshold = 85.0 // >= 85% uptime = healthy
		warningThreshold = 60.0 // 60-85% uptime = warning
		// < 60% uptime = critical
	)

	// Calculate dates for "no data" checks
	sevenDaysAgo := params.DateTo.AddDate(0, 0, -7)
	thirtyDaysAgo := params.DateTo.AddDate(0, 0, -30)

	// Main query for overall health metrics
	q := s.db.NewSelect().
		TableExpr("app.meters AS mtr").
		ColumnExpr("COUNT(DISTINCT mtr.meter_number) as total_meters").
		ColumnExpr(`
			COUNT(DISTINCT CASE
				WHEN mcd.uptime_percentage >= ? THEN mtr.meter_number
			END) as healthy_meters
		`, healthyThreshold).
		ColumnExpr(`
			COUNT(DISTINCT CASE
				WHEN mcd.uptime_percentage >= ? AND mcd.uptime_percentage < ? THEN mtr.meter_number
			END) as warning_meters
		`, warningThreshold, healthyThreshold).
		ColumnExpr(`
			COUNT(DISTINCT CASE
				WHEN mcd.uptime_percentage < ? OR mcd.uptime_percentage IS NULL THEN mtr.meter_number
			END) as critical_meters
		`, warningThreshold).
		ColumnExpr("COALESCE(AVG(mcd.uptime_percentage), 0) as avg_uptime").
		ColumnExpr(`
			COUNT(DISTINCT CASE
				WHEN mcd.last_consumption_date < ? THEN mtr.meter_number
			END) as no_data_7days
		`, sevenDaysAgo).
		ColumnExpr(`
			COUNT(DISTINCT CASE
				WHEN mcd.last_consumption_date < ? THEN mtr.meter_number
			END) as no_data_30days
		`, thirtyDaysAgo).
		Join(`
			LEFT JOIN (
				SELECT
					meter_number,
					MAX(consumption_date) as last_consumption_date,
					(COUNT(DISTINCT CASE WHEN data_item_id = '00100000' THEN DATE(consumption_date) END) * 100.0 /
					 ?) as uptime_percentage
				FROM app.meter_consumption_daily
				WHERE consumption_date BETWEEN ? AND ?
				GROUP BY meter_number
			) AS mcd ON mtr.meter_number = mcd.meter_number
		`, daysInRange, params.DateFrom, params.DateTo) // ✅ FIX: Pass daysInRange

	// Apply meter filters (skip date filters)
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	// Result struct for overall metrics
	var overallResult struct {
		TotalMeters    int     `bun:"total_meters"`
		HealthyMeters  int     `bun:"healthy_meters"`
		WarningMeters  int     `bun:"warning_meters"`
		CriticalMeters int     `bun:"critical_meters"`
		AvgUptime      float64 `bun:"avg_uptime"`
		NoData7Days    int     `bun:"no_data_7days"`
		NoData30Days   int     `bun:"no_data_30days"`
	}

	if err := q.Scan(ctx, &overallResult); err != nil {
		return nil, err
	}

	// Query for breakdown by meter type
	var breakdownByType []MeterHealthByType

	qByType := s.db.NewSelect().
		TableExpr("app.meters AS mtr").
		Column("mtr.meter_type").
		ColumnExpr("COUNT(DISTINCT mtr.meter_number) as total").
		ColumnExpr(`
			COUNT(DISTINCT CASE
				WHEN mcd.uptime_percentage >= ? THEN mtr.meter_number
			END) as healthy
		`, healthyThreshold).
		ColumnExpr(`
			COUNT(DISTINCT CASE
				WHEN mcd.uptime_percentage >= ? AND mcd.uptime_percentage < ? THEN mtr.meter_number
			END) as warning
		`, warningThreshold, healthyThreshold).
		ColumnExpr(`
			COUNT(DISTINCT CASE
				WHEN mcd.uptime_percentage < ? OR mcd.uptime_percentage IS NULL THEN mtr.meter_number
			END) as critical
		`, warningThreshold).
		Join(`
			LEFT JOIN (
				SELECT
					meter_number,
					(COUNT(DISTINCT CASE WHEN data_item_id = '00100000' THEN DATE(consumption_date) END) * 100.0 /
					 ?) as uptime_percentage
				FROM app.meter_consumption_daily
				WHERE consumption_date BETWEEN ? AND ?
				GROUP BY meter_number
			) AS mcd ON mtr.meter_number = mcd.meter_number
		`, daysInRange, params.DateFrom, params.DateTo) // ✅ FIX: Pass daysInRange

	// Apply same filters as overall query
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		qByType = qByType.Where(f.Query, f.Args...)
	}

	qByType = qByType.Group("mtr.meter_type").
		Order("mtr.meter_type")

	if err := qByType.Scan(ctx, &breakdownByType); err != nil {
		return nil, err
	}

	// Calculate health percentage
	healthPercentage := 0.0
	if overallResult.TotalMeters > 0 {
		healthPercentage = float64(overallResult.HealthyMeters) * 100.0 / float64(overallResult.TotalMeters)
	}

	return &MeterHealthMetrics{
		TotalMeters:            overallResult.TotalMeters,
		HealthyMeters:          overallResult.HealthyMeters,
		WarningMeters:          overallResult.WarningMeters,
		CriticalMeters:         overallResult.CriticalMeters,
		HealthPercentage:       healthPercentage,
		AvgUptime:              overallResult.AvgUptime,
		MetersWithNoData7Days:  overallResult.NoData7Days,
		MetersWithNoData30Days: overallResult.NoData30Days,
		BreakdownByType:        breakdownByType,
	}, nil
}

// GetMetersWithServiceArea returns meters with their spatial service area assignment
func (s *Service) GetMetersWithServiceArea(
	ctx context.Context,
	params MeterSpatialJoinParams,
) (*MeterWithServiceAreaResult, error) { // ✅ Changed return type

	// Validate and set defaults
	if params.Page < 1 {
		params.Page = 1
	}
	if params.Limit <= 0 {
		params.Limit = 50
	}
	if params.Limit > 200 {
		params.Limit = 200
	}

	// Build query with spatial join
	q := s.db.NewSelect().
		ColumnExpr("m.*").
		ColumnExpr("e.district AS service_area_district").
		ColumnExpr("e.region AS service_area_region").
		TableExpr("app.meters AS m").
		Join(`LEFT JOIN app.dbo_ecg AS e ON ST_Intersects(
			ST_SetSRID(e.the_geom, 4326),
			ST_SetSRID(ST_MakePoint(m.longitude, m.latitude), 4326)
		)`)

	// Apply filters
	if len(params.MeterTypes) > 0 {
		q = q.Where("m.meter_type IN (?)", bun.In(params.MeterTypes))
	}

	if len(params.Regions) > 0 {
		// Filter by meter's own region column
		lowerRegions := stringsToLower(params.Regions)
		q = q.Where("LOWER(m.region) IN (?)", bun.In(lowerRegions))
	}

	if len(params.Districts) > 0 {
		// Filter by meter's own district column
		lowerDistricts := stringsToLower(params.Districts)
		q = q.Where("LOWER(m.district) IN (?)", bun.In(lowerDistricts))
	}

	if len(params.ServiceAreaRegion) > 0 {
		// Filter by spatial service area region
		lowerServiceRegions := stringsToLower(params.ServiceAreaRegion)
		q = q.Where("LOWER(e.region) IN (?)", bun.In(lowerServiceRegions))
	}

	if params.HasCoordinates != nil {
		if *params.HasCoordinates {
			// Only meters with valid coordinates
			q = q.Where("m.latitude IS NOT NULL AND m.longitude IS NOT NULL")
		} else {
			// Only meters without coordinates
			q = q.Where("m.latitude IS NULL OR m.longitude IS NULL")
		}
	}

	if params.Search != "" {
		search := "%" + params.Search + "%"
		q = q.Where("m.meter_number ILIKE ? OR m.station ILIKE ? OR m.feeder_panel_name ILIKE ?",
			search, search, search)
	}

	// Count total before pagination
	total, err := q.Count(ctx)
	if err != nil {
		return nil, err
	}

	// Apply sorting
	sortBy := params.SortBy
	if sortBy == "" {
		sortBy = "meter_number"
	}

	sortOrder := "ASC"
	if strings.ToLower(params.SortOrder) == "desc" {
		sortOrder = "DESC"
	}

	// Validate sort field to prevent SQL injection
	validSortFields := map[string]string{
		"meter_number":     "m.meter_number",
		"meter_type":       "m.meter_type",
		"region":           "m.region",
		"district":         "m.district",
		"station":          "m.station",
		"service_region":   "e.region",
		"service_district": "e.district",
	}

	if sortColumn, ok := validSortFields[sortBy]; ok {
		q = q.OrderExpr(sortColumn + " " + sortOrder)
	} else {
		q = q.Order("m.meter_number ASC")
	}

	// Apply pagination
	offset := (params.Page - 1) * params.Limit
	q = q.Limit(params.Limit).Offset(offset)

	// Execute query
	var meters []MeterWithServiceArea
	if err := q.Scan(ctx, &meters); err != nil {
		return nil, err
	}

	// Build metadata
	totalPages := (total + params.Limit - 1) / params.Limit

	meta := map[string]any{
		"page":  params.Page,
		"limit": params.Limit,
		"total": total,
		"pages": totalPages,
	}

	// Add applied filters dynamically
	filters := map[string]any{}
	if len(params.MeterTypes) > 0 {
		filters["meterTypes"] = params.MeterTypes
	}
	if len(params.Regions) > 0 {
		filters["regions"] = params.Regions
	}
	if len(params.Districts) > 0 {
		filters["districts"] = params.Districts
	}
	if len(params.ServiceAreaRegion) > 0 {
		filters["serviceAreaRegion"] = params.ServiceAreaRegion
	}
	if params.HasCoordinates != nil {
		filters["hasCoordinates"] = *params.HasCoordinates
	}
	if params.Search != "" {
		filters["search"] = params.Search
	}
	if params.SortBy != "" {
		filters["sortBy"] = params.SortBy
		filters["sortOrder"] = params.SortOrder
	}
	if len(filters) > 0 {
		meta["filters"] = filters
	}

	return &MeterWithServiceAreaResult{ // ✅ Changed struct type
		Data: meters,
		Meta: meta,
	}, nil
}

// GetMeterSpatialMismatch returns meters where their assigned region/district
// differs from their spatial service area
func (s *Service) GetMeterSpatialMismatch(
	ctx context.Context,
	params MeterSpatialJoinParams,
) (*MeterWithServiceAreaResult, error) { // ✅ Changed return type

	// Similar to above but add WHERE clause for mismatches
	if params.Page < 1 {
		params.Page = 1
	}
	if params.Limit <= 0 {
		params.Limit = 50
	}

	q := s.db.NewSelect().
		ColumnExpr("m.*").
		ColumnExpr("e.district AS service_area_district").
		ColumnExpr("e.region AS service_area_region").
		TableExpr("app.meters AS m").
		Join(`LEFT JOIN app.dbo_ecg AS e ON ST_Intersects(
			ST_SetSRID(e.the_geom, 4326),
			ST_SetSRID(ST_MakePoint(m.longitude, m.latitude), 4326)
		)`).
		Where("m.latitude IS NOT NULL AND m.longitude IS NOT NULL"). // Must have coordinates
		Where(`(
			LOWER(m.region) != LOWER(e.region) OR
			LOWER(m.district) != LOWER(e.district) OR
			e.region IS NULL OR
			e.district IS NULL
		)`) // Find mismatches

	// Apply other filters
	if len(params.MeterTypes) > 0 {
		q = q.Where("m.meter_type IN (?)", bun.In(params.MeterTypes))
	}

	if params.Search != "" {
		search := "%" + params.Search + "%"
		q = q.Where("m.meter_number ILIKE ?", search)
	}

	// Count total
	total, err := q.Count(ctx)
	if err != nil {
		return nil, err
	}

	// Apply pagination
	offset := (params.Page - 1) * params.Limit
	q = q.Limit(params.Limit).Offset(offset)

	// Execute
	var meters []MeterWithServiceArea
	if err := q.Scan(ctx, &meters); err != nil {
		return nil, err
	}

	meta := map[string]any{
		"page":  params.Page,
		"limit": params.Limit,
		"total": total,
		"pages": (total + params.Limit - 1) / params.Limit,
	}

	return &MeterWithServiceAreaResult{ // ✅ Changed struct type
		Data: meters,
		Meta: meta,
	}, nil
}

// GetMeterSpatialStats returns statistics about spatial assignments
func (s *Service) GetMeterSpatialStats(ctx context.Context) (map[string]interface{}, error) {
	type stats struct {
		TotalMeters              int `bun:"total_meters"`
		MetersWithCoordinates    int `bun:"meters_with_coords"`
		MetersWithoutCoordinates int `bun:"meters_without_coords"`
		MetersInServiceArea      int `bun:"meters_in_service_area"`
		MetersOutsideServiceArea int `bun:"meters_outside_service_area"`
		MetersMismatchRegion     int `bun:"meters_mismatch_region"`
		MetersMismatchDistrict   int `bun:"meters_mismatch_district"`
	}

	var result stats

	err := s.db.NewRaw(`
		WITH spatial_join AS (
			SELECT
				m.*,
				e.region AS service_area_region,
				e.district AS service_area_district
			FROM app.meters m
			LEFT JOIN app.dbo_ecg e ON ST_Intersects(
				ST_SetSRID(e.the_geom, 4326),
				ST_SetSRID(ST_MakePoint(m.longitude, m.latitude), 4326)
			)
		)
		SELECT
			COUNT(*) AS total_meters,
			COUNT(CASE WHEN latitude IS NOT NULL AND longitude IS NOT NULL THEN 1 END) AS meters_with_coords,
			COUNT(CASE WHEN latitude IS NULL OR longitude IS NULL THEN 1 END) AS meters_without_coords,
			COUNT(CASE WHEN service_area_region IS NOT NULL THEN 1 END) AS meters_in_service_area,
			COUNT(CASE WHEN latitude IS NOT NULL AND longitude IS NOT NULL AND service_area_region IS NULL THEN 1 END) AS meters_outside_service_area,
			COUNT(CASE WHEN LOWER(region) != LOWER(service_area_region) THEN 1 END) AS meters_mismatch_region,
			COUNT(CASE WHEN LOWER(district) != LOWER(service_area_district) THEN 1 END) AS meters_mismatch_district
		FROM spatial_join
	`).Scan(ctx, &result)

	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"total_meters":                result.TotalMeters,
		"meters_with_coordinates":     result.MetersWithCoordinates,
		"meters_without_coordinates":  result.MetersWithoutCoordinates,
		"meters_in_service_area":      result.MetersInServiceArea,
		"meters_outside_service_area": result.MetersOutsideServiceArea,
		"meters_mismatch_region":      result.MetersMismatchRegion,
		"meters_mismatch_district":    result.MetersMismatchDistrict,
		"coordinate_coverage_pct":     float64(result.MetersWithCoordinates) * 100.0 / float64(result.TotalMeters),
		"service_area_coverage_pct":   float64(result.MetersInServiceArea) * 100.0 / float64(result.MetersWithCoordinates),
	}, nil
}

// GetMeterSpatialCounts returns aggregated meter counts by service area
func (s *Service) GetMeterSpatialCounts(
	ctx context.Context,
	params MeterSpatialCountParams,
) (*MeterSpatialCountResponse, error) {

	// Determine what to group by
	if params.GroupBy == "" {
		params.GroupBy = "region"
	}

	// Build the base CTE with spatial join
	var groupColumns []string
	var selectColumns []string

	switch params.GroupBy {
	case "region":
		groupColumns = []string{"service_area_region"} // ✅ Changed from "e.region"
		selectColumns = []string{"service_area_region"}
	case "district":
		groupColumns = []string{"service_area_region", "service_area_district"} // ✅ Changed
		selectColumns = []string{
			"service_area_region",
			"service_area_district",
		}
	case "meter_type":
		groupColumns = []string{"meter_type"} // ✅ Changed from "m.meter_type"
		selectColumns = []string{"meter_type"}
	case "region_meter_type":
		groupColumns = []string{"service_area_region", "meter_type"} // ✅ Changed
		selectColumns = []string{
			"service_area_region",
			"meter_type",
		}
	case "district_meter_type":
		groupColumns = []string{"service_area_region", "service_area_district", "meter_type"} // ✅ Changed
		selectColumns = []string{
			"service_area_region",
			"service_area_district",
			"meter_type",
		}
	default:
		groupColumns = []string{"service_area_region"}
		selectColumns = []string{"service_area_region"}
	}

	// Build query
	q := s.db.NewSelect().
		TableExpr(`(
			SELECT
				m.*,
				e.region AS service_area_region,
				e.district AS service_area_district,
				CASE
					WHEN LOWER(m.region) != LOWER(e.region) OR LOWER(m.district) != LOWER(e.district)
					THEN 1 ELSE 0
				END AS is_mismatched
			FROM app.meters m
			LEFT JOIN app.dbo_ecg e ON ST_Intersects(
				ST_SetSRID(e.the_geom, 4326),
				ST_SetSRID(ST_MakePoint(m.longitude, m.latitude), 4326)
			)
		) AS spatial_join`)

	// Add select columns
	for _, col := range selectColumns {
		q = q.ColumnExpr(col) // ✅ No need for alias, already correct name
	}

	// Add aggregation columns
	q = q.ColumnExpr("COUNT(*) AS total_meters").
		ColumnExpr("COUNT(CASE WHEN latitude IS NOT NULL AND longitude IS NOT NULL THEN 1 END) AS meters_with_coords").
		ColumnExpr("COUNT(CASE WHEN service_area_region IS NOT NULL THEN 1 END) AS meters_in_service_area").
		ColumnExpr("SUM(is_mismatched) AS meters_mismatched")

	// Apply filters
	if len(params.MeterTypes) > 0 {
		q = q.Where("meter_type IN (?)", bun.In(params.MeterTypes))
	}

	if len(params.Regions) > 0 {
		lowerRegions := stringsToLower(params.Regions)
		q = q.Where("LOWER(region) IN (?)", bun.In(lowerRegions))
	}

	if len(params.Districts) > 0 {
		lowerDistricts := stringsToLower(params.Districts)
		q = q.Where("LOWER(district) IN (?)", bun.In(lowerDistricts))
	}

	// Group by - ✅ Use column names from the subquery, not table aliases
	for _, col := range groupColumns {
		q = q.Group(col)
	}

	// Order by total meters descending
	q = q.Order("total_meters DESC")

	// Execute query
	var results []MeterSpatialCount
	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	// Calculate summary statistics
	var (
		totalMeters     int
		totalMismatched int
		regionsMap      = make(map[string]bool)
		districtsMap    = make(map[string]bool)
	)

	for _, r := range results {
		totalMeters += r.TotalMeters
		totalMismatched += r.MetersMismatched

		if r.ServiceAreaRegion != nil && *r.ServiceAreaRegion != "" {
			regionsMap[*r.ServiceAreaRegion] = true
		}
		if r.ServiceAreaDistrict != nil && *r.ServiceAreaDistrict != "" {
			districtsMap[*r.ServiceAreaDistrict] = true
		}
	}

	avgMetersPerRegion := 0.0
	if len(regionsMap) > 0 {
		avgMetersPerRegion = float64(totalMeters) / float64(len(regionsMap))
	}

	mismatchPct := 0.0
	if totalMeters > 0 {
		mismatchPct = float64(totalMismatched) * 100.0 / float64(totalMeters)
	}

	// Build response
	response := &MeterSpatialCountResponse{
		Data: results,
	}
	response.Summary.TotalMeters = totalMeters
	response.Summary.TotalRegions = len(regionsMap)
	response.Summary.TotalDistricts = len(districtsMap)
	response.Summary.AvgMetersPerRegion = avgMetersPerRegion
	response.Summary.MismatchPercentage = mismatchPct

	return response, nil
}

// GetMeterSpatialCountsByRegion is a convenience wrapper
func (s *Service) GetMeterSpatialCountsByRegion(
	ctx context.Context,
	meterTypes []string,
) (*MeterSpatialCountResponse, error) {
	return s.GetMeterSpatialCounts(ctx, MeterSpatialCountParams{
		GroupBy:    "region",
		MeterTypes: meterTypes,
	})
}

// GetMeterSpatialCountsByDistrict is a convenience wrapper
func (s *Service) GetMeterSpatialCountsByDistrict(
	ctx context.Context,
	region string,
	meterTypes []string,
) (*MeterSpatialCountResponse, error) {
	regions := []string{}
	if region != "" {
		regions = append(regions, region)
	}
	return s.GetMeterSpatialCounts(ctx, MeterSpatialCountParams{
		GroupBy:    "district",
		MeterTypes: meterTypes,
		Regions:    regions,
	})
}

// GetMeterSpatialCountsByType is a convenience wrapper
func (s *Service) GetMeterSpatialCountsByType(
	ctx context.Context,
) (*MeterSpatialCountResponse, error) {
	return s.GetMeterSpatialCounts(ctx, MeterSpatialCountParams{
		GroupBy: "meter_type",
	})
}

// GetTopBottomConsumers retrieves top and bottom consumers per meter type
func (s *Service) GetTopBottomConsumers(
	ctx context.Context,
	params ReadingFilterParams,
) ([]MeterTypeConsumers, error) {

	filters := buildReadingFilters(params)

	// Get aggregated consumption per meter with all filters applied
	q := s.db.NewSelect().
		ColumnExpr("mcd.meter_number").
		ColumnExpr("mtr.meter_type").
		ColumnExpr("mtr.location").
		ColumnExpr("mtr.region").
		ColumnExpr("mtr.district").
		ColumnExpr("mtr.station").
		ColumnExpr("mtr.metering_point").
		ColumnExpr("mtr.boundary_metering_point").
		ColumnExpr("mtr.feeder_panel_name").
		ColumnExpr("mtr.voltage_kv").
		ColumnExpr(`ROUND(
			SUM(mcd.consumption)
			FILTER (WHERE dim.name = 'Active Energy -Import [Register] [Total](Unit:kWh)')::numeric,
			4
		) as total_import_kwh`).
		ColumnExpr(`ROUND(
			SUM(mcd.consumption)
			FILTER (WHERE dim.name = 'Active Energy -Export [Register] [Total](Unit:kWh)')::numeric,
			4
		) as total_export_kwh`).
		ColumnExpr("COUNT(*) as reading_count").
		TableExpr("app.meter_consumption_daily AS mcd").
		Join("LEFT JOIN app.meters AS mtr ON mcd.meter_number = mtr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		Where("mtr.meter_type IS NOT NULL")

	// Apply all filters
	for _, f := range filters {
		q = q.Where(f.Query, f.Args...)
	}

	// Group by meter and its attributes
	q = q.
		Group("mcd.meter_number").
		Group("mtr.meter_type").
		Group("mtr.location").
		Group("mtr.region").
		Group("mtr.district").
		Group("mtr.station").
		Group("mtr.metering_point").
		Group("mtr.boundary_metering_point").
		Group("mtr.feeder_panel_name").
		Group("mtr.voltage_kv")

	// Execute query to get all meter consumptions
	var meterConsumptions []MeterConsumption
	if err := q.Scan(ctx, &meterConsumptions); err != nil {
		return nil, err
	}

	// Group by meter_type and find top/bottom for each
	meterTypeMap := make(map[string][]MeterConsumption)
	for _, mc := range meterConsumptions {
		meterTypeMap[mc.MeterType] = append(meterTypeMap[mc.MeterType], mc)
	}

	var result []MeterTypeConsumers
	for meterType, meters := range meterTypeMap {
		if len(meters) == 0 {
			continue
		}

		mtc := MeterTypeConsumers{
			MeterType:  meterType,
			MeterCount: len(meters),
		}

		// Find top and bottom import consumers
		var topImport, bottomImport *MeterConsumption
		for i := range meters {
			m := &meters[i]
			if topImport == nil || m.TotalImportKwh > topImport.TotalImportKwh {
				topImport = m
			}
			if bottomImport == nil || m.TotalImportKwh < bottomImport.TotalImportKwh {
				bottomImport = m
			}
		}
		mtc.TopImportConsumer = topImport
		mtc.BottomImportConsumer = bottomImport

		// Find top and bottom export consumers
		var topExport, bottomExport *MeterConsumption
		for i := range meters {
			m := &meters[i]
			if topExport == nil || m.TotalExportKwh > topExport.TotalExportKwh {
				topExport = m
			}
			if bottomExport == nil || m.TotalExportKwh < bottomExport.TotalExportKwh {
				bottomExport = m
			}
		}
		mtc.TopExportConsumer = topExport
		mtc.BottomExportConsumer = bottomExport

		result = append(result, mtc)
	}

	return result, nil
}

// GetMeterHealthSummary returns overall meter health statistics with uptime distribution
func (s *Service) GetMeterHealthSummary(
	ctx context.Context,
	params ReadingFilterParams,
) (*MeterHealthSummary, error) {

	// Calculate days in range for accurate uptime percentage
	daysInRange := int(params.DateTo.Sub(params.DateFrom).Hours()/24) + 1

	filters := buildReadingFilters(params)

	// Main query for overall health metrics
	q := s.db.NewSelect().
		TableExpr("app.meters AS mtr").
		ColumnExpr("COUNT(DISTINCT mtr.meter_number) as total_meters").
		ColumnExpr(`
        COUNT(DISTINCT CASE
            WHEN mcd.has_actual_data = true THEN mtr.meter_number
        END) as online_meters
    `).
		ColumnExpr(`
        COUNT(DISTINCT CASE
            WHEN mcd.has_actual_data = false OR mcd.meter_number IS NULL THEN mtr.meter_number
        END) as offline_meters
    `).
		ColumnExpr("COALESCE(AVG(mcd.uptime_percentage), 0) as avg_uptime").
		ColumnExpr(`
        COUNT(DISTINCT CASE
            WHEN mcd.uptime_percentage > 95 THEN mtr.meter_number
        END) as excellent
    `).
		ColumnExpr(`
        COUNT(DISTINCT CASE
            WHEN mcd.uptime_percentage >= 80 AND mcd.uptime_percentage <= 95 THEN mtr.meter_number
        END) as good
    `).
		ColumnExpr(`
        COUNT(DISTINCT CASE
            WHEN mcd.uptime_percentage >= 60 AND mcd.uptime_percentage < 80 THEN mtr.meter_number
        END) as poor
    `).
		ColumnExpr(`
        COUNT(DISTINCT CASE
            WHEN mcd.uptime_percentage < 60 THEN mtr.meter_number
        END) as critical
    `).
		Join(`
        LEFT JOIN (
            SELECT
                meter_number,
                BOOL_OR(data_item_id != 'NO_DATA') AS has_actual_data,
                (
                    COUNT(DISTINCT CASE
                        WHEN data_item_id != 'NO_DATA'
                        THEN DATE(consumption_date)
                    END) * 100.0
                    /
                    NULLIF(
                        COUNT(DISTINCT DATE(consumption_date)),
                        0
                    )
                ) AS uptime_percentage
            FROM app.meter_consumption_daily
            WHERE consumption_date BETWEEN ? AND ?
            GROUP BY meter_number
        ) AS mcd ON mtr.meter_number = mcd.meter_number
    `, params.DateFrom, params.DateTo)

	// Apply meter filters (skip date filters since they're in the subquery)
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	// Result struct for overall metrics
	var overallResult struct {
		TotalMeters   int     `bun:"total_meters"`
		OnlineMeters  int     `bun:"online_meters"`
		OfflineMeters int     `bun:"offline_meters"`
		AvgUptime     float64 `bun:"avg_uptime"`
		Excellent     int     `bun:"excellent"`
		Good          int     `bun:"good"`
		Poor          int     `bun:"poor"`
		Critical      int     `bun:"critical"`
	}

	if err := q.Scan(ctx, &overallResult); err != nil {
		return nil, err
	}

	// Query for breakdown by meter type
	qByType := s.db.NewSelect().
		TableExpr("app.meters AS mtr").
		Column("mtr.meter_type").
		ColumnExpr("COUNT(DISTINCT mtr.meter_number) as total").
		ColumnExpr(`
			COUNT(DISTINCT CASE
				WHEN mcd.has_actual_data = true THEN mtr.meter_number
			END) as online
		`).
		ColumnExpr(`
			COUNT(DISTINCT CASE
				WHEN mcd.has_actual_data = false OR mcd.meter_number IS NULL THEN mtr.meter_number
			END) as offline
		`).
		ColumnExpr("COALESCE(AVG(mcd.uptime_percentage), 0) as avg_uptime").
		Join(`
			LEFT JOIN (
				SELECT
					meter_number,
					BOOL_OR(data_item_id != 'NO_DATA') as has_actual_data,
					(COUNT(DISTINCT CASE WHEN data_item_id != 'NO_DATA' THEN DATE(consumption_date) END) * 100.0 /
					 ?) as uptime_percentage
				FROM app.meter_consumption_daily
				WHERE consumption_date BETWEEN ? AND ?
				GROUP BY meter_number
			) AS mcd ON mtr.meter_number = mcd.meter_number
		`, daysInRange, params.DateFrom, params.DateTo)

	// Apply same filters as overall query
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		qByType = qByType.Where(f.Query, f.Args...)
	}

	qByType = qByType.Group("mtr.meter_type").Order("mtr.meter_type")

	var byMeterType []MeterHealthByMeterType
	if err := qByType.Scan(ctx, &byMeterType); err != nil {
		return nil, err
	}

	// Calculate health percentage
	healthPercentage := 0.0
	if overallResult.TotalMeters > 0 {
		healthPercentage = float64(overallResult.OnlineMeters) * 100.0 / float64(overallResult.TotalMeters)
	}

	return &MeterHealthSummary{
		TotalMeters:             overallResult.TotalMeters,
		OnlineMeters:            overallResult.OnlineMeters,
		OfflineMeters:           overallResult.OfflineMeters,
		HealthPercentage:        healthPercentage,
		AverageUptimePercentage: overallResult.AvgUptime,
		ByMeterType:             byMeterType,
		UptimeDistribution: MeterUptimeDistribution{
			Excellent: overallResult.Excellent,
			Good:      overallResult.Good,
			Poor:      overallResult.Poor,
			Critical:  overallResult.Critical,
		},
	}, nil
}

// GetMeterHealthDetails returns paginated meter health details with filtering
func (s *Service) GetMeterHealthDetails(
	ctx context.Context,
	params MeterHealthDetailParams,
) (*MeterHealthDetailResponse, error) {

	// Validate and set defaults
	if params.Page < 1 {
		params.Page = 1
	}
	if params.Limit <= 0 || params.Limit > 200 {
		params.Limit = 50
	}
	if params.SortOrder == "" {
		params.SortOrder = "desc"
	}

	// Calculate days in range for accurate uptime percentage
	daysInRange := int(params.DateTo.Sub(params.DateFrom).Hours()/24) + 1

	filters := buildReadingFilters(params.ReadingFilterParams)

	// Build the main CTE with meter health calculations
	baseCTE := s.db.NewSelect().
		TableExpr("app.meters AS mtr").
		Column("mtr.meter_number").
		Column("mtr.meter_type").
		Column("mtr.region").
		Column("mtr.district").
		Column("mtr.station").
		Column("mtr.feeder_panel_name").
		Column("mtr.location").
		Column("mtr.voltage_kv").
		Column("mtr.boundary_metering_point").
		ColumnExpr(`
			CASE
				WHEN mcd.has_actual_data = true THEN 'ONLINE'
				ELSE 'OFFLINE'
			END as status
		`).
		ColumnExpr(`
			CASE
				WHEN mcd.uptime_percentage > 95 THEN 'excellent'
				WHEN mcd.uptime_percentage >= 80 AND mcd.uptime_percentage <= 95 THEN 'good'
				WHEN mcd.uptime_percentage >= 60 AND mcd.uptime_percentage < 80 THEN 'poor'
				WHEN mcd.uptime_percentage < 60 THEN 'critical'
				ELSE 'offline'
			END as health_category
		`).
		ColumnExpr("COALESCE(mcd.uptime_percentage, 0) as uptime_percentage").
		ColumnExpr("COALESCE(mcd.days_online, 0) as days_online").
		ColumnExpr("COALESCE(mcd.days_offline, ?) as days_offline", daysInRange).
		ColumnExpr("? as total_days", daysInRange).
		ColumnExpr("mcd.last_seen_date").
		ColumnExpr("COALESCE(mcd.total_consumption, 0) as total_consumption_kwh").
		ColumnExpr(`
			CASE
				WHEN mcd.days_online > 0 THEN ROUND((mcd.total_consumption / mcd.days_online)::numeric, 2)
				ELSE 0
			END as avg_daily_consumption
		`).
		Join(`
    LEFT JOIN (
        SELECT
            meter_number,
            MAX(consumption_date) AS last_seen_date,
            BOOL_OR(data_item_id != 'NO_DATA') AS has_actual_data,

            COUNT(DISTINCT CASE
                WHEN data_item_id != 'NO_DATA'
                THEN DATE(consumption_date)
            END) AS days_online,

            COUNT(DISTINCT DATE(consumption_date))
            - COUNT(DISTINCT CASE
                WHEN data_item_id != 'NO_DATA'
                THEN DATE(consumption_date)
            END) AS days_offline,

            COUNT(DISTINCT DATE(consumption_date)) AS total_days,

            (
                COUNT(DISTINCT CASE
                    WHEN data_item_id != 'NO_DATA'
                    THEN DATE(consumption_date)
                END) * 100.0
                /
                NULLIF(COUNT(DISTINCT DATE(consumption_date)), 0)
            ) AS uptime_percentage,

            SUM(consumption) AS total_consumption
        FROM app.meter_consumption_daily
        WHERE consumption_date BETWEEN ? AND ?
        GROUP BY meter_number
    ) AS mcd ON mtr.meter_number = mcd.meter_number
`, params.DateFrom, params.DateTo)

	// Apply meter filters (skip date filters since they're in the subquery)
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		baseCTE = baseCTE.Where(f.Query, f.Args...)
	}

	// Apply search filter
	if params.Search != "" {
		baseCTE = baseCTE.Where("mtr.meter_number ILIKE ?", "%"+params.Search+"%")
	}

	// Fast count query - wrap the CTE
	countQuery := s.db.NewSelect().
		TableExpr("(?) AS health_data", baseCTE).
		ColumnExpr("COUNT(*)")

	// Apply health category filter to count
	if params.HealthCategory != "" {
		countQuery = countQuery.Where("health_category = ?", strings.ToLower(params.HealthCategory))
	}

	totalCount, err := countQuery.Count(ctx)
	if err != nil {
		return nil, err
	}

	// Main query - reuse the CTE
	q := s.db.NewSelect().
		TableExpr("(?) AS health_data", baseCTE)

	// Apply health category filter
	if params.HealthCategory != "" {
		q = q.Where("health_category = ?", strings.ToLower(params.HealthCategory))
	}

	// Apply sorting
	sortOrder := "DESC"
	if strings.ToLower(params.SortOrder) == "asc" {
		sortOrder = "ASC"
	}

	switch params.SortBy {
	case "uptime":
		q = q.OrderExpr("uptime_percentage " + sortOrder)
	case "meter_type":
		q = q.OrderExpr("meter_type " + sortOrder)
	case "last_seen":
		q = q.OrderExpr("last_seen_date " + sortOrder + " NULLS LAST")
	case "consumption":
		q = q.OrderExpr("total_consumption_kwh " + sortOrder)
	case "meter_number":
		q = q.OrderExpr("meter_number " + sortOrder)
	default:
		q = q.OrderExpr("uptime_percentage " + sortOrder)
	}

	// Apply pagination
	offset := (params.Page - 1) * params.Limit
	q = q.Limit(params.Limit).Offset(offset)

	// Execute query
	var records []MeterHealthDetailRecord
	if err := q.Scan(ctx, &records); err != nil {
		return nil, err
	}

	// Calculate summary stats from results
	var totalOnline, totalOffline int
	var sumUptime float64
	for _, r := range records {
		if r.Status == "ONLINE" {
			totalOnline++
		} else {
			totalOffline++
		}
		sumUptime += r.UptimePercentage
	}

	avgUptime := 0.0
	if len(records) > 0 {
		avgUptime = sumUptime / float64(len(records))
	}

	// Build response
	totalPages := (totalCount + params.Limit - 1) / params.Limit
	hasMore := params.Page < totalPages

	// Build filters applied map
	filtersApplied := map[string]interface{}{
		"dateFrom": params.DateFrom.Format("2006-01-02"),
		"dateTo":   params.DateTo.Format("2006-01-02"),
	}
	if len(params.Regions) > 0 {
		filtersApplied["region"] = params.Regions
	}
	if len(params.MeterTypes) > 0 {
		filtersApplied["meterType"] = params.MeterTypes
	}
	if params.Search != "" {
		filtersApplied["search"] = params.Search
	}
	if params.HealthCategory != "" {
		filtersApplied["healthCategory"] = params.HealthCategory
	}
	if params.SortBy != "" {
		filtersApplied["sortBy"] = params.SortBy
		filtersApplied["sortOrder"] = params.SortOrder
	}

	response := &MeterHealthDetailResponse{
		Data:           records,
		FiltersApplied: filtersApplied,
	}
	response.Pagination.Page = params.Page
	response.Pagination.Limit = params.Limit
	response.Pagination.TotalRecords = totalCount
	response.Pagination.TotalPages = totalPages
	response.Pagination.HasMore = hasMore

	response.Summary.HealthCategory = params.HealthCategory
	response.Summary.AverageUptime = avgUptime
	response.Summary.TotalOnline = totalOnline
	response.Summary.TotalOffline = totalOffline

	return response, nil
}

// GetUniqueDistricts returns all unique districts from dbo_ecg table (whether meters exist or not)
func (s *Service) GetUniqueDistricts(ctx context.Context, region string, meterTypes []string) ([]string, error) {
	var districts []string

	q := s.db.NewSelect().
		TableExpr("app.dbo_ecg AS e").
		ColumnExpr("DISTINCT LOWER(e.district) as district").
		Join("LEFT JOIN app.meters AS m ON LOWER(e.district) = LOWER(m.district)").
		Where("e.district IS NOT NULL AND e.district != ''")

	if region != "" {
		q = q.Where("LOWER(e.region) = ?", strings.ToLower(region))
	}

	if len(meterTypes) > 0 {
		// Only show districts that have meters of these types
		q = q.Where("m.meter_type IN (?)", bun.In(stringsToUpper(meterTypes)))
	}

	q = q.Order("district ASC")

	if err := q.Scan(ctx, &districts); err != nil {
		return nil, err
	}

	return districts, nil
}

// GetUniqueRegions returns all unique regions from dbo_ecg table (whether meters exist or not)
func (s *Service) GetUniqueRegions(ctx context.Context, meterTypes []string) ([]string, error) {
	var regions []string

	q := s.db.NewSelect().
		TableExpr("app.dbo_ecg AS e").
		ColumnExpr("DISTINCT LOWER(e.region) as region").
		Join("LEFT JOIN app.meters AS m ON LOWER(e.region) = LOWER(m.region)").
		Where("e.region IS NOT NULL AND e.region != ''")

	if len(meterTypes) > 0 {
		// Only show regions that have meters of these types
		q = q.Where("m.meter_type IN (?)", bun.In(stringsToUpper(meterTypes)))
	}

	q = q.Order("region ASC")

	if err := q.Scan(ctx, &regions); err != nil {
		return nil, err
	}

	return regions, nil
}

// GetUniqueStations returns all unique stations from meters table with left join on dbo_ecg
func (s *Service) GetUniqueStations(ctx context.Context, region, district string, meterTypes []string) ([]string, error) {
	var stations []string

	q := s.db.NewSelect().
		TableExpr("app.meters AS m").
		ColumnExpr("DISTINCT LOWER(m.station) as station").
		Join("LEFT JOIN app.dbo_ecg AS e ON LOWER(m.district) = LOWER(e.district)").
		Where("m.station IS NOT NULL AND m.station != ''")

	if region != "" {
		q = q.Where("LOWER(m.region) = ?", strings.ToLower(region))
	}

	if district != "" {
		q = q.Where("LOWER(m.district) = ?", strings.ToLower(district))
	}

	if len(meterTypes) > 0 {
		q = q.Where("m.meter_type IN (?)", bun.In(stringsToUpper(meterTypes)))
	}

	q = q.Order("station ASC")

	if err := q.Scan(ctx, &stations); err != nil {
		return nil, err
	}

	return stations, nil
}

// GetUniqueLocations returns all unique locations from meters table with left join on dbo_ecg
func (s *Service) GetUniqueLocations(ctx context.Context, region, district string, meterTypes []string) ([]string, error) {
	var locations []string

	q := s.db.NewSelect().
		TableExpr("app.meters AS m").
		ColumnExpr("DISTINCT LOWER(m.location) as location").
		Join("LEFT JOIN app.dbo_ecg AS e ON LOWER(m.district) = LOWER(e.district)").
		Where("m.location IS NOT NULL AND m.location != ''")

	if region != "" {
		q = q.Where("LOWER(m.region) = ?", strings.ToLower(region))
	}

	if district != "" {
		q = q.Where("LOWER(m.district) = ?", strings.ToLower(district))
	}

	if len(meterTypes) > 0 {
		q = q.Where("m.meter_type IN (?)", bun.In(stringsToUpper(meterTypes)))
	}

	q = q.Order("location ASC")

	if err := q.Scan(ctx, &locations); err != nil {
		return nil, err
	}

	return locations, nil
}

// GetUniqueBoundaryPoints returns all unique boundary metering points with left join on dbo_ecg
func (s *Service) GetUniqueBoundaryPoints(ctx context.Context, region, district string, meterTypes []string) ([]string, error) {
	var boundaryPoints []string

	q := s.db.NewSelect().
		TableExpr("app.meters AS m").
		ColumnExpr("DISTINCT LOWER(m.boundary_metering_point) as boundary_metering_point").
		Join("LEFT JOIN app.dbo_ecg AS e ON LOWER(m.district) = LOWER(e.district)").
		Where("m.boundary_metering_point IS NOT NULL AND m.boundary_metering_point != ''")

	if region != "" {
		q = q.Where("LOWER(m.region) = ?", strings.ToLower(region))
	}

	if district != "" {
		q = q.Where("LOWER(m.district) = ?", strings.ToLower(district))
	}

	if len(meterTypes) > 0 {
		q = q.Where("m.meter_type IN (?)", bun.In(stringsToUpper(meterTypes)))
	}

	q = q.Order("boundary_metering_point ASC")

	if err := q.Scan(ctx, &boundaryPoints); err != nil {
		return nil, err
	}

	return boundaryPoints, nil
}

// GetUniqueVoltages returns all unique voltage levels with left join on dbo_ecg
func (s *Service) GetUniqueVoltages(ctx context.Context, region, district string, meterTypes []string) ([]float64, error) {
	var voltages []float64

	q := s.db.NewSelect().
		TableExpr("app.meters AS m").
		ColumnExpr("DISTINCT m.voltage_kv").
		Join("LEFT JOIN app.dbo_ecg AS e ON LOWER(m.district) = LOWER(e.district)").
		Where("m.voltage_kv IS NOT NULL")

	if region != "" {
		q = q.Where("LOWER(m.region) = ?", strings.ToLower(region))
	}

	if district != "" {
		q = q.Where("LOWER(m.district) = ?", strings.ToLower(district))
	}

	if len(meterTypes) > 0 {
		q = q.Where("m.meter_type IN (?)", bun.In(stringsToUpper(meterTypes)))
	}

	q = q.Order("m.voltage_kv ASC")

	if err := q.Scan(ctx, &voltages); err != nil {
		return nil, err
	}

	return voltages, nil
}

// GetUniqueMeterTypes returns all unique meter types (no join needed)
func (s *Service) GetUniqueMeterTypes(ctx context.Context) ([]string, error) {
	var meterTypes []string

	q := s.db.NewSelect().
		TableExpr("app.meters").
		ColumnExpr("DISTINCT meter_type").
		Where("meter_type IS NOT NULL AND meter_type != ''").
		Order("meter_type ASC")

	if err := q.Scan(ctx, &meterTypes); err != nil {
		return nil, err
	}

	return meterTypes, nil
}

// GetRegionalMapConsumption returns consumption data by district with GeoJSON for mapping

// GetRegionalMapConsumption returns consumption data by district with GeoJSON for mapping

func (s *Service) GetRegionalMapConsumption(
	ctx context.Context,
	params RegionalMapParams,
) (*RegionalMapResponse, error) {

	// First, get ALL districts with their GeoJSON
	var allDistricts []struct {
		District string          `bun:"district"`
		Region   string          `bun:"region"`
		GeoJSON  json.RawMessage `bun:"geojson"`
	}

	// Query to get all districts from dbo_ecg
	districtQuery := s.db.NewSelect().
		Column("district").
		Column("region").
		ColumnExpr("ST_AsGeoJSON(the_geom)::jsonb as geojson").
		TableExpr("app.dbo_ecg").
		Where("district IS NOT NULL").
		Where("region IS NOT NULL")

	if params.Region != "" {
		districtQuery = districtQuery.Where("LOWER(region) = ?", strings.ToLower(params.Region))
	}

	if params.District != "" {
		districtQuery = districtQuery.Where("LOWER(district) = ?", strings.ToLower(params.District))
	}

	if err := districtQuery.Scan(ctx, &allDistricts); err != nil {
		return nil, fmt.Errorf("failed to get districts: %w", err)
	}

	// Build the consumption query
	var queryBuilder strings.Builder
	var args []interface{}

	// Start building the CTE
	queryBuilder.WriteString(`
        WITH district_consumption AS (
            -- Try spatial join first (coordinates available)
            SELECT
                d.district,
                d.region,
                m.meter_type,
                m.meter_number,
                m.location,
                m.station,
                m.feeder_panel_name,
                m.boundary_metering_point,
                m.voltage_kv,
                m.ic_og,
                DATE(mcd.consumption_date) as consumption_date,
                COALESCE(SUM(CASE WHEN dim.system_name = 'import_kwh' THEN mcd.consumption ELSE 0 END), 0) as total_import_kwh,
                COALESCE(SUM(CASE WHEN dim.system_name = 'export_kwh' THEN mcd.consumption ELSE 0 END), 0) as total_export_kwh,
                'spatial' as match_type
            FROM app.dbo_ecg d
            INNER JOIN app.meters m
                ON ST_Intersects(ST_SetSRID(d.the_geom, 4326), ST_SetSRID(ST_MakePoint(m.longitude, m.latitude), 4326))
                AND m.latitude IS NOT NULL
                AND m.longitude IS NOT NULL
            LEFT JOIN app.meter_consumption_daily mcd
                ON m.meter_number = mcd.meter_number
                AND mcd.consumption_date BETWEEN ? AND ?
            LEFT JOIN app.data_item_mapping dim
                ON mcd.data_item_id = dim.data_item_id
            WHERE d.district IS NOT NULL
                AND d.region IS NOT NULL
                AND m.meter_type IS NOT NULL
    `)

	// Add date parameters for first UNION
	args = append(args, params.DateFrom, params.DateTo)

	// Add filters for first UNION
	if len(params.MeterType) > 0 {
		queryBuilder.WriteString(` AND m.meter_type IN (?)`)
		args = append(args, bun.In(stringsToUpper(params.MeterType)))
	}

	if params.Region != "" {
		queryBuilder.WriteString(` AND LOWER(d.region) = ?`)
		args = append(args, strings.ToLower(params.Region))
	}

	if params.District != "" {
		queryBuilder.WriteString(` AND LOWER(d.district) = ?`)
		args = append(args, strings.ToLower(params.District))
	}

	// Close first UNION and start second
	queryBuilder.WriteString(`
            GROUP BY d.district, d.region, d.the_geom, m.meter_type, m.meter_number, m.station,
                     m.location, m.boundary_metering_point, m.voltage_kv, m.feeder_panel_name,
                     m.ic_og, DATE(mcd.consumption_date)

            UNION ALL

            -- Fallback: District name matching (for meters without coordinates or not intersecting)
            SELECT
                d.district,
                d.region,
                m.meter_type,
                m.meter_number,
                m.location,
                m.station,
                m.feeder_panel_name,
                m.boundary_metering_point,
                m.voltage_kv,
                m.ic_og,
                DATE(mcd.consumption_date) as consumption_date,
                COALESCE(SUM(CASE WHEN dim.system_name = 'import_kwh' THEN mcd.consumption ELSE 0 END), 0) as total_import_kwh,
                COALESCE(SUM(CASE WHEN dim.system_name = 'export_kwh' THEN mcd.consumption ELSE 0 END), 0) as total_export_kwh,
                'district_name' as match_type
            FROM app.dbo_ecg d
            INNER JOIN app.meters m
                ON LOWER(TRIM(d.district)) = LOWER(TRIM(m.district))
                AND (m.latitude IS NULL OR m.longitude IS NULL)  -- Only meters without coordinates
            LEFT JOIN app.meter_consumption_daily mcd
                ON m.meter_number = mcd.meter_number
                AND mcd.consumption_date BETWEEN ? AND ?
            LEFT JOIN app.data_item_mapping dim
                ON mcd.data_item_id = dim.data_item_id
            WHERE d.district IS NOT NULL
                AND d.region IS NOT NULL
                AND m.meter_type IS NOT NULL
                AND m.district IS NOT NULL
    `)

	// Add date parameters for second UNION
	args = append(args, params.DateFrom, params.DateTo)

	// Add filters for second UNION
	if len(params.MeterType) > 0 {
		queryBuilder.WriteString(` AND m.meter_type IN (?)`)
		args = append(args, bun.In(stringsToUpper(params.MeterType)))
	}

	if params.Region != "" {
		queryBuilder.WriteString(` AND LOWER(d.region) = ?`)
		args = append(args, strings.ToLower(params.Region))
	}

	if params.District != "" {
		queryBuilder.WriteString(` AND LOWER(d.district) = ?`)
		args = append(args, strings.ToLower(params.District))
	}

	// Close second UNION and start third
	queryBuilder.WriteString(`
            GROUP BY d.district, d.region, d.the_geom, m.meter_type, m.meter_number, m.station,
                     m.location, m.boundary_metering_point, m.voltage_kv, m.feeder_panel_name,
                     m.ic_og, DATE(mcd.consumption_date)

            UNION ALL

            -- Fallback 2: Region name matching (if district doesn't match but region does)
            SELECT
                d.district,
                d.region,
                m.meter_type,
                m.meter_number,
                m.location,
                m.station,
                m.feeder_panel_name,
                m.boundary_metering_point,
                m.voltage_kv,
                m.ic_og,
                DATE(mcd.consumption_date) as consumption_date,
                COALESCE(SUM(CASE WHEN dim.system_name = 'import_kwh' THEN mcd.consumption ELSE 0 END), 0) as total_import_kwh,
                COALESCE(SUM(CASE WHEN dim.system_name = 'export_kwh' THEN mcd.consumption ELSE 0 END), 0) as total_export_kwh,
                'region_name' as match_type
            FROM app.dbo_ecg d
            INNER JOIN app.meters m
                ON LOWER(TRIM(d.region)) = LOWER(TRIM(m.region))
                AND m.district IS NULL  -- Only meters with NULL district
            LEFT JOIN app.meter_consumption_daily mcd
                ON m.meter_number = mcd.meter_number
                AND mcd.consumption_date BETWEEN ? AND ?
            LEFT JOIN app.data_item_mapping dim
                ON mcd.data_item_id = dim.data_item_id
            WHERE d.district IS NOT NULL
                AND d.region IS NOT NULL
                AND m.meter_type IS NOT NULL
                AND m.region IS NOT NULL
    `)

	// Add date parameters for third UNION
	args = append(args, params.DateFrom, params.DateTo)

	// Add filters for third UNION
	if len(params.MeterType) > 0 {
		queryBuilder.WriteString(` AND m.meter_type IN (?)`)
		args = append(args, bun.In(stringsToUpper(params.MeterType)))
	}

	if params.Region != "" {
		queryBuilder.WriteString(` AND LOWER(d.region) = ?`)
		args = append(args, strings.ToLower(params.Region))
	}

	if params.District != "" {
		queryBuilder.WriteString(` AND LOWER(d.district) = ?`)
		args = append(args, strings.ToLower(params.District))
	}

	// Close the CTE and add final SELECT
	queryBuilder.WriteString(`
            GROUP BY d.district, d.region, d.the_geom, m.meter_type, m.meter_number, m.station,
                     m.location, m.boundary_metering_point, m.voltage_kv, m.feeder_panel_name,
                     m.ic_og, DATE(mcd.consumption_date)
        )
        SELECT
            district,
            region,
            meter_type,
            meter_number,
            location,
            station,
            feeder_panel_name,
            boundary_metering_point,
            voltage_kv,
            ic_og,
            consumption_date,
            total_import_kwh,
            total_export_kwh
        FROM district_consumption
        ORDER BY district, meter_type, consumption_date
    `)

	// Execute query - use the variable name you declared
	q := s.db.NewRaw(queryBuilder.String(), args...)

	var consumptionRows []struct {
		District              string    `bun:"district"`
		Region                string    `bun:"region"`
		MeterType             string    `bun:"meter_type"`
		MeterNumber           string    `bun:"meter_number"`
		Station               string    `bun:"station"`
		Location              string    `bun:"location"`
		FeederPanelName       string    `bun:"feeder_panel_name"`
		BoundaryMeteringPoint string    `bun:"boundary_metering_point"`
		VoltageKV             string    `bun:"voltage_kv"`
		IC_OG                 string    `bun:"ic_og"`
		ConsumptionDate       time.Time `bun:"consumption_date"`
		TotalImportKWh        float64   `bun:"total_import_kwh"`
		TotalExportKWh        float64   `bun:"total_export_kwh"`
	}

	// Use 'q' not 'q1'
	if err := q.Scan(ctx, &consumptionRows); err != nil {
		return nil, fmt.Errorf("failed to query consumption data: %w", err)
	}

	// Organize consumption data by district and meter type
	consumptionMap := make(map[string]map[string][]DistrictMapConsumptionByType)

	for _, row := range consumptionRows {
		districtKey := fmt.Sprintf("%s|%s", row.District, row.Region)

		if _, exists := consumptionMap[districtKey]; !exists {
			consumptionMap[districtKey] = make(map[string][]DistrictMapConsumptionByType)
		}

		meterTypeKey := row.MeterType
		if _, exists := consumptionMap[districtKey][meterTypeKey]; !exists {
			consumptionMap[districtKey][meterTypeKey] = []DistrictMapConsumptionByType{}
		}

		consumption := DistrictMapConsumptionByType{
			Date:                  row.ConsumptionDate,
			MeterType:             row.MeterType,
			MeterNumber:           row.MeterNumber,
			Station:               row.Station,
			VoltageKV:             row.VoltageKV,
			Location:              row.Location,
			BoundaryMeteringPoint: row.BoundaryMeteringPoint,
			FeederPanelName:       row.FeederPanelName,
			IC_OG:                 row.IC_OG,
			TotalImportKWh:        row.TotalImportKWh,
			TotalExportKWh:        row.TotalExportKWh,
			NetConsumptionKWh:     row.TotalImportKWh - row.TotalExportKWh,
		}

		consumptionMap[districtKey][meterTypeKey] = append(
			consumptionMap[districtKey][meterTypeKey],
			consumption,
		)
	}

	// Build final response
	districts := make([]DistrictMapData, 0, len(allDistricts))

	for _, districtInfo := range allDistricts {
		districtKey := fmt.Sprintf("%s|%s", districtInfo.District, districtInfo.Region)

		// Transform GeoJSON to Feature format
		featureGeoJSON := transformToFeatureGeoJSON(districtInfo.GeoJSON, districtInfo.District, districtInfo.Region)

		// Get consumption for this district
		districtConsumption := consumptionMap[districtKey]

		// Flatten consumption by meter type into single timeseries array
		var allTimeseries []DistrictMapConsumptionByType

		for _, timeseries := range districtConsumption {
			allTimeseries = append(allTimeseries, timeseries...)
		}

		// Sort timeseries by date and meter type
		sort.Slice(allTimeseries, func(i, j int) bool {
			if allTimeseries[i].Date.Equal(allTimeseries[j].Date) {
				return allTimeseries[i].MeterType < allTimeseries[j].MeterType
			}
			return allTimeseries[i].Date.Before(allTimeseries[j].Date)
		})

		// Create district data
		districtData := DistrictMapData{
			District:   districtInfo.District,
			Region:     districtInfo.Region,
			GeoJSON:    featureGeoJSON,
			Timeseries: allTimeseries,
		}

		districts = append(districts, districtData)
	}

	// Sort districts by name
	sort.Slice(districts, func(i, j int) bool {
		return districts[i].District < districts[j].District
	})

	return &RegionalMapResponse{
		Districts: districts,
	}, nil
}

// GetRegionalEnergyBalance calculates energy balance with ALL filters properly applied
func (s *Service) GetRegionalEnergyBalance(
	ctx context.Context,
	params EnergyBalanceParams,
) (*EnergyBalanceResponse, error) {

	// Validate date range
	if params.DateFrom.IsZero() || params.DateTo.IsZero() {
		return nil, fmt.Errorf("date_from and date_to are required")
	}

	// Build filters for internal consumption (BSP/DTX)
	var internalConditions []string
	var internalArgs []interface{}

	// Date range (always required)
	internalConditions = append(internalConditions, "DATE(mcd.consumption_date) BETWEEN ? AND ?")
	internalArgs = append(internalArgs, params.DateFrom, params.DateTo)

	// Region filter
	if len(params.Regions) > 0 {
		placeholders := make([]string, len(params.Regions))
		for i := range params.Regions {
			placeholders[i] = "?"
		}
		internalConditions = append(internalConditions, "LOWER(m.region) IN ("+strings.Join(placeholders, ",")+")")
		for _, r := range stringsToLower(params.Regions) {
			internalArgs = append(internalArgs, r)
		}
	}

	// District filter
	if len(params.Districts) > 0 {
		placeholders := make([]string, len(params.Districts))
		for i := range params.Districts {
			placeholders[i] = "?"
		}
		internalConditions = append(internalConditions, "LOWER(m.district) IN ("+strings.Join(placeholders, ",")+")")
		for _, d := range stringsToLower(params.Districts) {
			internalArgs = append(internalArgs, d)
		}
	}

	// Station filter
	if len(params.Stations) > 0 {
		placeholders := make([]string, len(params.Stations))
		for i := range params.Stations {
			placeholders[i] = "?"
		}
		internalConditions = append(internalConditions, "LOWER(m.station) IN ("+strings.Join(placeholders, ",")+")")
		for _, st := range stringsToLower(params.Stations) {
			internalArgs = append(internalArgs, st)
		}
	}

	// Location filter
	if len(params.Locations) > 0 {
		placeholders := make([]string, len(params.Locations))
		for i := range params.Locations {
			placeholders[i] = "?"
		}
		internalConditions = append(internalConditions, "LOWER(m.location) IN ("+strings.Join(placeholders, ",")+")")
		for _, l := range stringsToLower(params.Locations) {
			internalArgs = append(internalArgs, l)
		}
	}

	// Meter number filter
	if len(params.MeterNumber) > 0 {
		placeholders := make([]string, len(params.MeterNumber))
		for i := range params.MeterNumber {
			placeholders[i] = "?"
		}
		internalConditions = append(internalConditions, "m.meter_number IN ("+strings.Join(placeholders, ",")+")")
		for _, mn := range params.MeterNumber {
			internalArgs = append(internalArgs, mn)
		}
	}

	// Meter type filter (for BSP/DTX only)
	meterTypeFilter := "m.meter_type IN ('BSP', 'DTX')"
	if len(params.MeterTypes) > 0 {
		// Only allow BSP/DTX for internal consumption
		validTypes := []string{}
		for _, mt := range params.MeterTypes {
			upperType := strings.ToUpper(mt)
			if upperType == "BSP" || upperType == "DTX" {
				validTypes = append(validTypes, upperType)
			}
		}
		if len(validTypes) > 0 {
			placeholders := make([]string, len(validTypes))
			for i := range validTypes {
				placeholders[i] = "?"
			}
			meterTypeFilter = "m.meter_type IN (" + strings.Join(placeholders, ",") + ")"
			for _, vt := range validTypes {
				internalArgs = append(internalArgs, vt)
			}
		} else {
			// If they filtered for only REGIONAL_BOUNDARY, no internal consumption
			meterTypeFilter = "1 = 0" // Return empty
		}
	}

	// Voltage filter
	if len(params.Voltages) > 0 {
		placeholders := make([]string, len(params.Voltages))
		for i := range params.Voltages {
			placeholders[i] = "?"
		}
		internalConditions = append(internalConditions, "m.voltage_kv IN ("+strings.Join(placeholders, ",")+")")
		for _, v := range params.Voltages {
			internalArgs = append(internalArgs, v)
		}
	}

	// Build WHERE clause for internal consumption
	internalWhereClause := meterTypeFilter + " AND mcd.data_item_id != 'NO_DATA' AND di.system_name = 'import_kwh'"
	if len(internalConditions) > 0 {
		internalWhereClause += " AND " + strings.Join(internalConditions, " AND ")
	}

	// Build filters for boundary flows
	var boundaryConditions []string
	var boundaryArgs []interface{}

	// Date range (always required)
	boundaryConditions = append(boundaryConditions, "DATE(mcd.consumption_date) BETWEEN ? AND ?")
	boundaryArgs = append(boundaryArgs, params.DateFrom, params.DateTo)

	// Location filter (for boundary meters)
	if len(params.Locations) > 0 {
		placeholders := make([]string, len(params.Locations))
		for i := range params.Locations {
			placeholders[i] = "?"
		}
		boundaryConditions = append(boundaryConditions, "LOWER(m.location) IN ("+strings.Join(placeholders, ",")+")")
		for _, l := range stringsToLower(params.Locations) {
			boundaryArgs = append(boundaryArgs, l)
		}
	}

	// Boundary metering point filter
	if len(params.BoundaryMeteringPoint) > 0 {
		placeholders := make([]string, len(params.BoundaryMeteringPoint))
		for i := range params.BoundaryMeteringPoint {
			placeholders[i] = "?"
		}
		boundaryConditions = append(boundaryConditions, "LOWER(m.boundary_metering_point) IN ("+strings.Join(placeholders, ",")+")")
		for _, bmp := range stringsToLower(params.BoundaryMeteringPoint) {
			boundaryArgs = append(boundaryArgs, bmp)
		}
	}

	// Meter number filter (for boundary meters)
	if len(params.MeterNumber) > 0 {
		placeholders := make([]string, len(params.MeterNumber))
		for i := range params.MeterNumber {
			placeholders[i] = "?"
		}
		boundaryConditions = append(boundaryConditions, "m.meter_number IN ("+strings.Join(placeholders, ",")+")")
		for _, mn := range params.MeterNumber {
			boundaryArgs = append(boundaryArgs, mn)
		}
	}

	// Voltage filter (for boundary meters)
	if len(params.Voltages) > 0 {
		placeholders := make([]string, len(params.Voltages))
		for i := range params.Voltages {
			placeholders[i] = "?"
		}
		boundaryConditions = append(boundaryConditions, "m.voltage_kv IN ("+strings.Join(placeholders, ",")+")")
		for _, v := range params.Voltages {
			boundaryArgs = append(boundaryArgs, v)
		}
	}

	// Build WHERE clause for boundary flows
	boundaryWhereClause := "m.meter_type = 'REGIONAL_BOUNDARY' AND mcd.data_item_id != 'NO_DATA' AND m.boundary_metering_point IS NOT NULL AND m.boundary_metering_point LIKE '%/%' AND di.system_name IN ('import_kwh', 'export_kwh')"
	if len(boundaryConditions) > 0 {
		boundaryWhereClause += " AND " + strings.Join(boundaryConditions, " AND ")
	}

	// Build final region filter for the outer query (CRITICAL!)
	var finalRegionFilter string
	var finalRegionArgs []interface{}
	if len(params.Regions) > 0 {
		placeholders := make([]string, len(params.Regions))
		for i := range params.Regions {
			placeholders[i] = "?"
		}
		finalRegionFilter = "AND LOWER(COALESCE(i.region, b.region)) IN (" + strings.Join(placeholders, ",") + ")"
		for _, r := range stringsToLower(params.Regions) {
			finalRegionArgs = append(finalRegionArgs, r)
		}
	}

	// Build the complete energy balance query
	query := `
		WITH
		-- 0. Get valid regions from m.region (BSP/DTX meters only - our source of truth)
		valid_regions AS (
			SELECT DISTINCT LOWER(TRIM(region)) as region_name
			FROM app.meters
			WHERE region IS NOT NULL
			  AND TRIM(region) != ''
			  AND meter_type IN ('BSP', 'DTX')
		),
		-- 1. Internal consumption (BSP - DTX) for each region
		internal_consumption AS (
			SELECT
				COALESCE(LOWER(m.region), 'unknown') as region,
				DATE(mcd.consumption_date) as date,
				SUM(CASE WHEN m.meter_type = 'BSP' AND di.system_name = 'import_kwh'
						 THEN mcd.consumption ELSE 0 END) as bsp_import,
				SUM(CASE WHEN m.meter_type = 'DTX' AND di.system_name = 'import_kwh'
						 THEN mcd.consumption ELSE 0 END) as dtx_import,
				SUM(CASE WHEN m.meter_type = 'BSP' AND di.system_name = 'import_kwh'
						 THEN mcd.consumption
						 WHEN m.meter_type = 'DTX' AND di.system_name = 'import_kwh'
						 THEN -mcd.consumption
						 ELSE 0 END) as internal_net,
				COUNT(DISTINCT CASE WHEN m.meter_type = 'BSP' THEN m.meter_number END) as bsp_meter_count,
				COUNT(DISTINCT CASE WHEN m.meter_type = 'DTX' THEN m.meter_number END) as dtx_meter_count
			FROM app.meter_consumption_daily mcd
			JOIN app.meters m ON m.meter_number = mcd.meter_number
			JOIN app.data_item_mapping di ON di.data_item_id = mcd.data_item_id
			WHERE ` + internalWhereClause + `
			GROUP BY COALESCE(LOWER(m.region), 'unknown'), DATE(mcd.consumption_date)
		),
		-- 2. Parse boundary metering points
		boundary_flows_parsed AS (
			SELECT
				DATE(mcd.consumption_date) as date,
				m.meter_number,
				m.boundary_metering_point,
				m.location,
				m.voltage_kv,
				LOWER(TRIM(SPLIT_PART(m.boundary_metering_point, '/', 1))) as region_a,
				LOWER(TRIM(SPLIT_PART(m.boundary_metering_point, '/', 2))) as region_b,
				SUM(CASE WHEN di.system_name = 'import_kwh' THEN mcd.consumption ELSE 0 END) as import_kwh,
				SUM(CASE WHEN di.system_name = 'export_kwh' THEN mcd.consumption ELSE 0 END) as export_kwh,
				COUNT(*) as reading_count,
				CASE
					WHEN COUNT(*) >= 48 THEN 'complete'
					WHEN COUNT(*) >= 24 THEN 'partial'
					ELSE 'incomplete'
				END as data_quality
			FROM app.meter_consumption_daily mcd
			JOIN app.meters m ON m.meter_number = mcd.meter_number
			JOIN app.data_item_mapping di ON di.data_item_id = mcd.data_item_id
			WHERE ` + boundaryWhereClause + `
			GROUP BY
				DATE(mcd.consumption_date),
				m.meter_number,
				m.boundary_metering_point,
				m.location,
				m.voltage_kv
		),
		-- 3. Validate that BOTH regions exist in valid_regions
		boundary_flows_validated AS (
			SELECT
				bfp.*,
				EXISTS (SELECT 1 FROM valid_regions WHERE region_name = bfp.region_a) as region_a_valid,
				EXISTS (SELECT 1 FROM valid_regions WHERE region_name = bfp.region_b) as region_b_valid
			FROM boundary_flows_parsed bfp
		),
		-- 4. Create boundary flow entries for BOTH regions (if both valid)
		boundary_flows_per_region AS (
			-- Entries for region_a
			SELECT
				date,
				region_a as region,
				meter_number,
				boundary_metering_point,
				location,
				voltage_kv,
				region_b as connected_region,
				import_kwh,
				export_kwh,
				import_kwh - export_kwh as net_flow,
				reading_count,
				data_quality,
				region_a_valid,
				region_b_valid
			FROM boundary_flows_validated
			WHERE region_a_valid = true

			UNION ALL

			-- Entries for region_b (swapped perspective)
			SELECT
				date,
				region_b as region,
				meter_number,
				boundary_metering_point,
				location,
				voltage_kv,
				region_a as connected_region,
				export_kwh as import_kwh,
				import_kwh as export_kwh,
				export_kwh - import_kwh as net_flow,
				reading_count,
				data_quality,
				region_a_valid,
				region_b_valid
			FROM boundary_flows_validated
			WHERE region_b_valid = true
		),
		-- 5. Build boundary meter details for each region
		boundary_meter_details AS (
			SELECT
				date,
				region,
				jsonb_agg(
					jsonb_build_object(
						'meter_number', meter_number,
						'boundary_metering_point', boundary_metering_point,
						'connected_region', connected_region,
						'location', location,
						'voltage_kv', voltage_kv,
						'import_kwh', import_kwh,
						'export_kwh', export_kwh,
						'net_flow', net_flow,
						'reading_count', reading_count,
						'data_quality', data_quality
					) ORDER BY meter_number
				) as boundary_meters_json
			FROM boundary_flows_per_region
			GROUP BY date, region
		),
		-- 6. Aggregate by connected region AND location
		boundary_by_connected_region_and_location AS (
			SELECT
				date,
				region,
				connected_region,
				location,
				SUM(import_kwh) as location_import,
				SUM(export_kwh) as location_export,
				SUM(net_flow) as location_net_flow
			FROM boundary_flows_per_region
			GROUP BY date, region, connected_region, location
		),
		-- 6b. Aggregate totals by connected region (without location)
		boundary_by_connected_region AS (
			SELECT
				date,
				region,
				connected_region,
				SUM(import_kwh) as total_import_from_region,
				SUM(export_kwh) as total_export_to_region,
				SUM(net_flow) as net_flow
			FROM boundary_flows_per_region
			GROUP BY date, region, connected_region
		),
		-- 7. Build by_location map for each connected region
		boundary_location_map AS (
			SELECT
				date,
				region,
				connected_region,
				jsonb_object_agg(
					location,
					jsonb_build_object(
						'location', location,
						'import_kwh', location_import,
						'export_kwh', location_export,
						'net_flow', location_net_flow
					)
				) as by_location_json
			FROM boundary_by_connected_region_and_location
			GROUP BY date, region, connected_region
		),
		-- 8. Build by_connected_region JSON map with location breakdown
		boundary_connected_region_map AS (
			SELECT
				bcr.date,
				bcr.region,
				jsonb_object_agg(
					bcr.connected_region,
					jsonb_build_object(
						'connected_region', bcr.connected_region,
						'total_import_from_them', bcr.total_import_from_region,
						'total_export_to_them', bcr.total_export_to_region,
						'net_flow', bcr.net_flow,
						'flow_balance', CASE
							WHEN bcr.net_flow > 10 THEN 'importing'
							WHEN bcr.net_flow < -10 THEN 'exporting'
							ELSE 'balanced'
						END,
						'by_location', COALESCE(blm.by_location_json, '{}'::jsonb)
					)
				) as by_connected_region_json
			FROM boundary_by_connected_region bcr
			LEFT JOIN boundary_location_map blm
				ON bcr.date = blm.date
				AND bcr.region = blm.region
				AND bcr.connected_region = blm.connected_region
			GROUP BY bcr.date, bcr.region
		),
		-- 9. Aggregate total boundary flows by region
		boundary_totals AS (
			SELECT
				date,
				region,
				SUM(import_kwh) as total_import,
				SUM(export_kwh) as total_export,
				SUM(net_flow) as net_boundary_flow,
				COUNT(DISTINCT meter_number) as boundary_meter_count
			FROM boundary_flows_per_region
			GROUP BY date, region
		)
		-- 10. Final result combining internal consumption and boundary flows
		SELECT
			COALESCE(i.region, b.region) as region,
			COALESCE(i.date, b.date) as date,
			-- Internal consumption
			COALESCE(i.bsp_import, 0) as bsp_import,
			COALESCE(i.dtx_import, 0) as dtx_import,
			COALESCE(i.internal_net, 0) as internal_net_consumption,
			COALESCE(i.bsp_meter_count, 0) as bsp_meter_count,
			COALESCE(i.dtx_meter_count, 0) as dtx_meter_count,
			-- Boundary flows
			COALESCE(b.total_import, 0) as boundary_total_import,
			COALESCE(b.total_export, 0) as boundary_total_export,
			COALESCE(b.net_boundary_flow, 0) as net_boundary_flow,
			COALESCE(b.boundary_meter_count, 0) as boundary_meter_count,
			COALESCE(bmd.boundary_meters_json, '[]'::jsonb) as boundary_meters_json,
			COALESCE(bcr.by_connected_region_json, '{}'::jsonb) as by_connected_region_json,
			-- Total
			COALESCE(i.internal_net, 0) + COALESCE(b.net_boundary_flow, 0) as total_net_consumption
		FROM internal_consumption i
		FULL OUTER JOIN boundary_totals b
			ON i.region = b.region AND i.date = b.date
		LEFT JOIN boundary_meter_details bmd
			ON COALESCE(i.region, b.region) = bmd.region
			AND COALESCE(i.date, b.date) = bmd.date
		LEFT JOIN boundary_connected_region_map bcr
			ON COALESCE(i.region, b.region) = bcr.region
			AND COALESCE(i.date, b.date) = bcr.date
		WHERE COALESCE(i.region, b.region) IS NOT NULL
			AND COALESCE(i.date, b.date) IS NOT NULL
			` + finalRegionFilter + `
		ORDER BY COALESCE(i.date, b.date), COALESCE(i.region, b.region)
	`

	// Combine all arguments (order matters!)
	args := append(internalArgs, boundaryArgs...)
	args = append(args, finalRegionArgs...)

	// Define raw result structure from query
	type rawResult struct {
		Region                 string          `bun:"region"`
		Date                   time.Time       `bun:"date"`
		BSPImport              float64         `bun:"bsp_import"`
		DTXImport              float64         `bun:"dtx_import"`
		InternalNetConsumption float64         `bun:"internal_net_consumption"`
		BSPMeterCount          int             `bun:"bsp_meter_count"`
		DTXMeterCount          int             `bun:"dtx_meter_count"`
		BoundaryTotalImport    float64         `bun:"boundary_total_import"`
		BoundaryTotalExport    float64         `bun:"boundary_total_export"`
		NetBoundaryFlow        float64         `bun:"net_boundary_flow"`
		BoundaryMeterCount     int             `bun:"boundary_meter_count"`
		BoundaryMetersJSON     json.RawMessage `bun:"boundary_meters_json"`
		ByConnectedRegionJSON  json.RawMessage `bun:"by_connected_region_json"`
		TotalNetConsumption    float64         `bun:"total_net_consumption"`
	}

	// Execute query
	var rawResults []rawResult
	err := s.db.NewRaw(query, args...).Scan(ctx, &rawResults)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate energy balance: %w", err)
	}

	// Transform raw results into enhanced structure
	results := make([]RegionalEnergyBalance, len(rawResults))

	for i, raw := range rawResults {
		// Parse boundary meters JSON (handle null/empty cases)
		var boundaryMeters []BoundaryMeterDetail
		if len(raw.BoundaryMetersJSON) > 0 &&
			string(raw.BoundaryMetersJSON) != "[]" &&
			string(raw.BoundaryMetersJSON) != "null" {
			if err := json.Unmarshal(raw.BoundaryMetersJSON, &boundaryMeters); err != nil {
				boundaryMeters = []BoundaryMeterDetail{}
			}
		} else {
			boundaryMeters = []BoundaryMeterDetail{}
		}

		// Parse by_connected_region JSON map
		var byConnectedRegion map[string]RegionFlowDetail
		if len(raw.ByConnectedRegionJSON) > 0 &&
			string(raw.ByConnectedRegionJSON) != "{}" &&
			string(raw.ByConnectedRegionJSON) != "null" {
			if err := json.Unmarshal(raw.ByConnectedRegionJSON, &byConnectedRegion); err != nil {
				byConnectedRegion = make(map[string]RegionFlowDetail)
			}
		} else {
			byConnectedRegion = make(map[string]RegionFlowDetail)
		}

		// Calculate metrics
		var crossBoundaryDependency, internalSufficiency float64
		if raw.TotalNetConsumption > 0 {
			crossBoundaryDependency = (raw.NetBoundaryFlow / raw.TotalNetConsumption) * 100
		}
		if raw.TotalNetConsumption != 0 {
			internalSufficiency = (raw.InternalNetConsumption / raw.TotalNetConsumption) * 100
		}

		// Determine dominant flow direction
		dominantFlow := "balanced"
		if raw.BoundaryTotalImport > raw.BoundaryTotalExport*1.1 {
			dominantFlow = "net_importer"
		} else if raw.BoundaryTotalExport > raw.BoundaryTotalImport*1.1 {
			dominantFlow = "net_exporter"
		}

		// Determine balance type
		var balanceType string
		if internalSufficiency >= 95 {
			balanceType = "self_sufficient"
		} else if raw.NetBoundaryFlow > 0 {
			balanceType = "net_importer"
		} else if raw.NetBoundaryFlow < 0 {
			balanceType = "net_exporter"
		} else {
			balanceType = "balanced"
		}

		// Generate flags
		var flags []string
		if crossBoundaryDependency > 50 {
			flags = append(flags, "critical_import_dependency")
		} else if crossBoundaryDependency > 30 {
			flags = append(flags, "heavy_cross_boundary_importer")
		}
		if math.Abs(crossBoundaryDependency) > 30 && raw.NetBoundaryFlow < 0 {
			flags = append(flags, "heavy_cross_boundary_exporter")
		}
		if raw.BoundaryMeterCount == 0 {
			flags = append(flags, "isolated_region")
		} else if raw.BoundaryMeterCount >= 5 {
			flags = append(flags, "highly_interconnected")
		}
		if raw.BSPMeterCount < 2 {
			flags = append(flags, "limited_bsp_coverage")
		}
		if raw.DTXMeterCount < 5 {
			flags = append(flags, "limited_dtx_coverage")
		}

		// Build final result
		results[i] = RegionalEnergyBalance{
			Region: raw.Region,
			Date:   raw.Date,
			InternalConsumption: InternalConsumptionDetail{
				BSPImport:     raw.BSPImport,
				DTXImport:     raw.DTXImport,
				NetInternal:   raw.InternalNetConsumption,
				BSPMeterCount: raw.BSPMeterCount,
				DTXMeterCount: raw.DTXMeterCount,
			},
			CrossBoundaryFlows: CrossBoundaryFlowDetail{
				BoundaryMeters:          boundaryMeters,
				ByConnectedRegion:       byConnectedRegion,
				TotalImportKWh:          raw.BoundaryTotalImport,
				TotalExportKWh:          raw.BoundaryTotalExport,
				NetCrossBoundaryFlow:    raw.NetBoundaryFlow,
				BoundaryMeterCount:      raw.BoundaryMeterCount,
				IsNetImporter:           raw.NetBoundaryFlow > 0,
				CrossBoundaryDependency: math.Round(crossBoundaryDependency*100) / 100,
				DominantFlowDirection:   dominantFlow,
			},
			TotalNetConsumption: raw.TotalNetConsumption,
			BalanceAnalysis: BalanceAnalysis{
				InternalSufficiency:   math.Round(internalSufficiency*100) / 100,
				CrossBoundaryReliance: math.Round(math.Abs(crossBoundaryDependency)*100) / 100,
				BalanceType:           balanceType,
				Flags:                 flags,
			},
		}
	}

	// Build response with summary
	response := &EnergyBalanceResponse{
		Data: results,
	}

	response.Summary.DateRange.From = params.DateFrom.Format("2006-01-02")
	response.Summary.DateRange.To = params.DateTo.Format("2006-01-02")

	// Calculate aggregate metrics
	regionsMap := make(map[string]bool)
	var totalBSP, totalDTX, totalInternal, totalBoundary, totalNet float64

	for _, r := range results {
		regionsMap[r.Region] = true
		totalBSP += r.InternalConsumption.BSPImport
		totalDTX += r.InternalConsumption.DTXImport
		totalInternal += r.InternalConsumption.NetInternal
		totalBoundary += r.CrossBoundaryFlows.NetCrossBoundaryFlow
		totalNet += r.TotalNetConsumption
	}

	response.Summary.TotalRegions = len(regionsMap)
	response.Summary.Metrics.TotalBSPImport = math.Round(totalBSP*100) / 100
	response.Summary.Metrics.TotalDTXImport = math.Round(totalDTX*100) / 100
	response.Summary.Metrics.TotalInternalNet = math.Round(totalInternal*100) / 100
	response.Summary.Metrics.TotalCrossBoundaryNet = math.Round(totalBoundary*100) / 100
	response.Summary.Metrics.TotalNetConsumption = math.Round(totalNet*100) / 100

	if len(results) > 0 {
		response.Summary.Metrics.AverageDailyConsumption = math.Round((totalNet/float64(len(results)))*100) / 100
	}

	return response, nil
}

// GetRegionalEnergyBalanceSummary returns aggregated balance by region over entire date range
func (s *Service) GetRegionalEnergyBalanceSummary(
	ctx context.Context,
	params EnergyBalanceParams,
) ([]RegionalEnergyBalanceSummary, error) {

	detailedResponse, err := s.GetRegionalEnergyBalance(ctx, params)
	if err != nil {
		return nil, err
	}

	regionMap := make(map[string]*RegionalEnergyBalanceSummary)

	for _, r := range detailedResponse.Data {
		if _, exists := regionMap[r.Region]; !exists {
			regionMap[r.Region] = &RegionalEnergyBalanceSummary{
				Region: r.Region,
			}
		}

		summary := regionMap[r.Region]
		summary.TotalBSPImport += r.InternalConsumption.BSPImport
		summary.TotalDTXImport += r.InternalConsumption.DTXImport
		summary.TotalInternalNet += r.InternalConsumption.NetInternal
		summary.TotalCrossBoundaryNet += r.CrossBoundaryFlows.NetCrossBoundaryFlow
		summary.TotalNetConsumption += r.TotalNetConsumption
		summary.DayCount++
	}

	results := make([]RegionalEnergyBalanceSummary, 0, len(regionMap))
	for _, summary := range regionMap {
		if summary.DayCount > 0 {
			summary.AverageDailyConsumption = math.Round((summary.TotalNetConsumption/float64(summary.DayCount))*100) / 100
		}
		summary.TotalBSPImport = math.Round(summary.TotalBSPImport*100) / 100
		summary.TotalDTXImport = math.Round(summary.TotalDTXImport*100) / 100
		summary.TotalInternalNet = math.Round(summary.TotalInternalNet*100) / 100
		summary.TotalCrossBoundaryNet = math.Round(summary.TotalCrossBoundaryNet*100) / 100
		summary.TotalNetConsumption = math.Round(summary.TotalNetConsumption*100) / 100

		results = append(results, *summary)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Region < results[j].Region
	})

	return results, nil
}

// Helper function to transform raw GeoJSON to Feature format
func transformToFeatureGeoJSON(rawGeoJSON json.RawMessage, district, region string) json.RawMessage {
	type Geometry struct {
		Type        string        `json:"type"`
		Coordinates [][][]float64 `json:"coordinates"`
	}

	type FeatureProperties struct {
		Name   string `json:"name"`
		Region string `json:"region"`
	}

	type Feature struct {
		Type       string            `json:"type"`
		Properties FeatureProperties `json:"properties"`
		Geometry   Geometry          `json:"geometry"`
	}

	// Parse the raw geometry
	var geometry Geometry
	if err := json.Unmarshal(rawGeoJSON, &geometry); err != nil {
		// If parsing fails, return empty feature
		geometry = Geometry{
			Type:        "Polygon",
			Coordinates: [][][]float64{},
		}
	}

	feature := Feature{
		Type: "Feature",
		Properties: FeatureProperties{
			Name:   district,
			Region: region,
		},
		Geometry: geometry,
	}

	featureJSON, _ := json.Marshal(feature)
	return featureJSON
}

// Helper to generate time points
func generateTimePoints(from, to time.Time, interval string) []string {
	var timePoints []string

	current := from
	for !current.After(to) {
		timePoints = append(timePoints, current.Format("2006-01-02"))

		switch interval {
		case "daily":
			current = current.AddDate(0, 0, 1)
		case "weekly":
			current = current.AddDate(0, 0, 7)
		case "monthly":
			current = current.AddDate(0, 1, 0)
		default:
			current = current.AddDate(0, 0, 1)
		}
	}

	return timePoints
}

// GetDistrictGeometries returns simplified district boundaries for mapping
func (s *Service) GetDistrictGeometries(
	ctx context.Context,
	regions []string,
	districts []string,
) (*DistrictGeometryResponse, error) {

	// Get the latest boundary update date for versioning
	var versionDate time.Time
	err := s.db.NewSelect().
		ColumnExpr("MAX(updated_at) as max_date").
		TableExpr("app.dbo_ecg_operational_regions_and_district_boundaries_10_7_25").
		Scan(ctx, &versionDate)
	if err != nil {
		versionDate = time.Now() // Fallback to current date
	}

	version := versionDate.Format("2006-01-02")

	// Build query for simplified geometries
	q := s.db.NewSelect().
		ColumnExpr("d.district_code").
		ColumnExpr("d.district").
		ColumnExpr("d.region").
		ColumnExpr("ST_Y(ST_Centroid(d.the_geom)) as center_lat").
		ColumnExpr("ST_X(ST_Centroid(d.the_geom)) as center_lng").
		ColumnExpr(`
            jsonb_build_object(
                'type', 'Feature',
                'properties', jsonb_build_object(
                    'district_code', d.district_code,
                    'district', d.district,
                    'region', d.region
                ),
                'geometry', ST_AsGeoJSON(
                    ST_SimplifyPreserveTopology(d.the_geom, 0.001) -- Simplify to ~111m tolerance
                )::jsonb
            ) as geojson
        `).
		TableExpr("app.dbo_ecg_operational_regions_and_district_boundaries_10_7_25 d").
		Where("d.district IS NOT NULL").
		Where("d.region IS NOT NULL").
		OrderExpr("d.region, d.district")

	// Apply region filter
	if len(regions) > 0 {
		lowerRegions := stringsToLower(regions)
		q = q.Where("LOWER(d.region) IN (?)", bun.In(lowerRegions))
	}

	// Apply district filter
	if len(districts) > 0 {

		exact := make([]string, len(districts))
		likes := make([]string, len(districts))

		for i, d := range districts {
			exact[i] = strings.ToLower(d)
			likes[i] = "%" + strings.ToLower(d) + "%"
		}

		q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {

			q = q.Where("LOWER(d.district) IN (?)", bun.In(exact))

			q = q.WhereGroup(" OR ", func(q *bun.SelectQuery) *bun.SelectQuery {
				for _, l := range likes {
					q = q.Where("LOWER(d.district) ILIKE ?", l)
				}
				return q
			})

			return q
		})
	}

	var geometries []DistrictGeometry
	if err := q.Scan(ctx, &geometries); err != nil {
		return nil, fmt.Errorf("failed to get district geometries: %w", err)
	}

	// Round coordinates for optimization
	for i := range geometries {
		geometries[i].CenterLat = roundCoordinate(geometries[i].CenterLat, 6)
		geometries[i].CenterLng = roundCoordinate(geometries[i].CenterLng, 6)
		geometries[i].GeoJSON = simplifyGeoJSONCoordinates(geometries[i].GeoJSON, 6)
	}

	return &DistrictGeometryResponse{
		Version:   version,
		Districts: geometries,
	}, nil
}

// GetRegionGeometries returns simplified regional boundaries by dissolving/unioning district geometries
func (s *Service) GetRegionGeometries(
	ctx context.Context,
	regions []string,
) (*RegionGeometryResponse, error) {

	// Get the latest boundary update date for versioning
	var versionDate time.Time
	err := s.db.NewSelect().
		ColumnExpr("MAX(updated_at) as max_date").
		TableExpr("app.regions_dissolved").
		Scan(ctx, &versionDate)
	if err != nil {
		versionDate = time.Now() // Fallback to current date
	}

	version := versionDate.Format("2006-01-02")

	// Build query for regional geometries
	q := s.db.NewSelect().
		ColumnExpr("d.region").
		ColumnExpr("ST_Y(ST_Centroid(d.the_geom)) as center_lat").
		ColumnExpr("ST_X(ST_Centroid(d.the_geom)) as center_lng").
		ColumnExpr(`
            jsonb_build_object(
                'type', 'Feature',
                'properties', jsonb_build_object(
                    'region', d.region
                ),
                'geometry', ST_AsGeoJSON(
                    ST_SimplifyPreserveTopology(
                        d.the_geom,
                        0.001  -- Simplify to ~111m tolerance
                    )
                )::jsonb
            ) as geojson
        `).
		TableExpr("app.regions_dissolved d").
		Where("d.region IS NOT NULL").
		OrderExpr("d.region")

	// Apply region filter if provided
	if len(regions) > 0 {
		lowerRegions := stringsToLower(regions)
		q = q.Where("LOWER(d.region) IN (?)", bun.In(lowerRegions))
	}

	var geometries []RegionGeometry
	if err := q.Scan(ctx, &geometries); err != nil {
		return nil, fmt.Errorf("failed to get region geometries: %w", err)
	}

	// Round coordinates for optimization
	for i := range geometries {
		geometries[i].CenterLat = roundCoordinate(geometries[i].CenterLat, 6)
		geometries[i].CenterLng = roundCoordinate(geometries[i].CenterLng, 6)
		geometries[i].GeoJSON = simplifyGeoJSONCoordinates(geometries[i].GeoJSON, 6)
	}

	return &RegionGeometryResponse{
		Version: version,
		Regions: geometries,
	}, nil
}

// GetDistrictTimeseriesConsumption returns aggregated consumption by district
func (s *Service) GetDistrictTimeseriesConsumption(
	ctx context.Context,
	params DistrictConsumptionParams,
) (*DistrictTimeseriesResponse, error) {

	// Build the query for aggregated district consumption
	var queryBuilder strings.Builder
	var args []interface{}

	queryBuilder.WriteString(`
        WITH district_meters AS (
            -- Match meters to districts using multiple strategies
            SELECT DISTINCT
                COALESCE(
                    d_spatial.district,
                    d_name.district,
                    'Unknown District'
                ) as district,
                COALESCE(
                    d_spatial.region,
                    d_name.region,
                    m.region,
                    'Unknown Region'
                ) as region,
                m.meter_number
            FROM app.meters m
            -- Try spatial join first
            LEFT JOIN app.dbo_ecg d_spatial
                ON ST_Intersects(ST_SetSRID(d_spatial.the_geom, 4326), ST_SetSRID(ST_MakePoint(m.longitude, m.latitude), 4326))
                AND m.latitude IS NOT NULL
                AND m.longitude IS NOT NULL
            -- Fallback to name matching
            LEFT JOIN app.dbo_ecg d_name
                ON LOWER(TRIM(d_name.district)) = LOWER(TRIM(m.district))
                AND LOWER(TRIM(d_name.region)) = LOWER(TRIM(m.region))
            WHERE m.meter_type IS NOT NULL
    `)

	// Apply meter type filter
	if len(params.MeterType) > 0 {
		queryBuilder.WriteString(` AND m.meter_type IN (?)`)
		args = append(args, bun.In(stringsToUpper(params.MeterType)))
	}

	// Apply region filter (on meter table)
	if len(params.Region) > 0 {
		lowerRegions := stringsToLower(params.Region)
		queryBuilder.WriteString(` AND LOWER(m.region) IN (?)`)
		args = append(args, bun.In(lowerRegions))
	}

	// Apply district filter (on meter table)
	if len(params.District) > 0 {
		lowerDistricts := stringsToLower(params.District)
		queryBuilder.WriteString(` AND LOWER(m.district) IN (?)`)
		args = append(args, bun.In(lowerDistricts))
	}

	queryBuilder.WriteString(`
        ),
        consumption_data AS (
            SELECT
                dm.district,
                dm.region,
                DATE(mcd.consumption_date) as timestamp,
                COALESCE(SUM(CASE WHEN dim.system_name = 'import_kwh' THEN mcd.consumption ELSE 0 END), 0) as total_import_kwh,
                COALESCE(SUM(CASE WHEN dim.system_name = 'export_kwh' THEN mcd.consumption ELSE 0 END), 0) as total_export_kwh
            FROM district_meters dm
            INNER JOIN app.meter_consumption_daily mcd
                ON dm.meter_number = mcd.meter_number
                AND mcd.consumption_date BETWEEN ? AND ?
            LEFT JOIN app.data_item_mapping dim
                ON mcd.data_item_id = dim.data_item_id
            GROUP BY dm.district, dm.region, DATE(mcd.consumption_date)
        )
        SELECT
            district,
            region,
            timestamp,
            total_import_kwh,
            total_export_kwh
        FROM consumption_data
        ORDER BY region, district, timestamp
    `)

	// Add date parameters
	args = append(args, params.DateFrom, params.DateTo)

	q := s.db.NewRaw(queryBuilder.String(), args...)

	var rawRows []struct {
		District       string    `bun:"district"`
		Region         string    `bun:"region"`
		Timestamp      time.Time `bun:"timestamp"`
		TotalImportKWh float64   `bun:"total_import_kwh"`
		TotalExportKWh float64   `bun:"total_export_kwh"`
	}

	if err := q.Scan(ctx, &rawRows); err != nil {
		return nil, fmt.Errorf("failed to query district timeseries: %w", err)
	}

	// Group data by district
	districtMap := make(map[string]*DistrictTimeseriesData)

	for _, row := range rawRows {
		districtKey := fmt.Sprintf("%s|%s", row.District, row.Region)

		if _, exists := districtMap[districtKey]; !exists {
			districtMap[districtKey] = &DistrictTimeseriesData{
				District:   row.District,
				Region:     row.Region,
				Timeseries: []DistrictTimeseriesEntry{},
			}
		}

		entry := DistrictTimeseriesEntry{
			Timestamp:         row.Timestamp,
			TotalImportKWh:    row.TotalImportKWh,
			TotalExportKWh:    row.TotalExportKWh,
			NetConsumptionKWh: row.TotalImportKWh - row.TotalExportKWh,
		}

		districtMap[districtKey].Timeseries = append(
			districtMap[districtKey].Timeseries,
			entry,
		)
	}

	// Convert map to slice
	districts := make([]DistrictTimeseriesData, 0, len(districtMap))
	for _, district := range districtMap {
		districts = append(districts, *district)
	}

	// Sort by region, then district
	sort.Slice(districts, func(i, j int) bool {
		if districts[i].Region != districts[j].Region {
			return districts[i].Region < districts[j].Region
		}
		return districts[i].District < districts[j].District
	})

	return &DistrictTimeseriesResponse{
		Districts: districts,
	}, nil
}









// Helper function to round coordinates
func roundCoordinate(value float64, precision int) float64 {
	multiplier := math.Pow(10, float64(precision))
	return math.Round(value*multiplier) / multiplier
}

// Helper function to simplify GeoJSON coordinates
func simplifyGeoJSONCoordinates(geojson json.RawMessage, precision int) json.RawMessage {
	type Geometry struct {
		Type        string        `json:"type"`
		Coordinates [][][]float64 `json:"coordinates"`
	}
	type Feature struct {
		Type       string                 `json:"type"`
		Properties map[string]interface{} `json:"properties"`
		Geometry   Geometry               `json:"geometry"`
	}

	var feature Feature
	if err := json.Unmarshal(geojson, &feature); err != nil {
		return geojson // Return original if parsing fails
	}

	// ONLY round coordinates - let PostGIS handle simplification
	for i := range feature.Geometry.Coordinates {
		for j := range feature.Geometry.Coordinates[i] {
			for k := range feature.Geometry.Coordinates[i][j] {
				feature.Geometry.Coordinates[i][j][k] = roundCoordinate(
					feature.Geometry.Coordinates[i][j][k],
					precision,
				)
			}
		}
	}

	// Remove this entire block that was causing chunky zigzags:
	// ❌ if len(feature.Geometry.Coordinates) > 0 && len(feature.Geometry.Coordinates[0]) > 100 {
	// ❌     n := len(feature.Geometry.Coordinates[0]) / 80
	// ❌     ... all the naive point-skipping logic ...
	// ❌ }

	simplifiedJSON, _ := json.Marshal(feature)
	return simplifiedJSON
}

func pointsEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

///HELPER

func buildReadingFilters(params ReadingFilterParams) []Filter {
	filters := []Filter{}

	// Date range (required)
	filters = append(filters, Filter{
		Query: "mcd.consumption_date BETWEEN ? AND ?",
		Args:  []interface{}{params.DateFrom, params.DateTo},
	})

	// Meter numbers
	if len(params.MeterNumber) > 0 {
		filters = append(filters, Filter{
			Query: "mcd.meter_number IN (?)",
			Args:  []interface{}{bun.In(params.MeterNumber)},
		})
	}

	// Region
	if len(params.Regions) > 0 {
		filters = append(filters, Filter{
			Query: "lower(mtr.region) IN (?)",
			Args:  []interface{}{bun.In(stringsToLower(params.Regions))},
		})
	}

	// District
	if len(params.Districts) > 0 {
		filters = append(filters, Filter{
			Query: "lower(mtr.district) IN (?)",
			Args:  []interface{}{bun.In(stringsToLower(params.Districts))},
		})
	}

	// Station
	if len(params.Stations) > 0 {
		filters = append(filters, Filter{
			Query: "lower(mtr.station) IN (?)",
			Args:  []interface{}{bun.In(stringsToLower(params.Stations))},
		})
	}

	// Location
	if len(params.Locations) > 0 {
		filters = append(filters, Filter{
			Query: "lower(mtr.location) IN (?)",
			Args:  []interface{}{bun.In(stringsToLower(params.Locations))},
		})
	}

	// Boundary metering point (partial match)
	if len(params.BoundaryMeteringPoint) > 0 {
		conditions := make([]string, len(params.BoundaryMeteringPoint))
		args := make([]interface{}, len(params.BoundaryMeteringPoint))
		for i, bmp := range params.BoundaryMeteringPoint {
			conditions[i] = "mtr.boundary_metering_point ILIKE ?"
			args[i] = "%" + strings.TrimSpace(bmp) + "%"
		}
		filters = append(filters, Filter{
			Query: "(" + strings.Join(conditions, " OR ") + ")",
			Args:  args,
		})
	}

	// Meter type
	if len(params.MeterTypes) > 0 {
		filters = append(filters, Filter{
			Query: "mtr.meter_type IN (?)",
			Args:  []interface{}{bun.In(stringsToUpper(params.MeterTypes))},
		})
	}

	// Voltage
	if len(params.Voltages) > 0 {
		filters = append(filters, Filter{
			Query: "mtr.voltage_kv IN (?)",
			Args:  []interface{}{bun.In(params.Voltages)},
		})
	}

	// --- Feeder secondary filters ---
	if len(params.SendingRegions) > 0 {
		filters = append(filters, Filter{
			Query: "lower(f.sending_region) IN (?)",
			Args:  []interface{}{bun.In(stringsToLower(params.SendingRegions))},
		})
	}

	if len(params.SendingDistricts) > 0 {
		filters = append(filters, Filter{
			Query: "lower(f.sending_district) IN (?)",
			Args:  []interface{}{bun.In(stringsToLower(params.SendingDistricts))},
		})
	}

	if len(params.ReceivingRegions) > 0 {
		filters = append(filters, Filter{
			Query: "lower(f.receiving_region) IN (?)",
			Args:  []interface{}{bun.In(stringsToLower(params.ReceivingRegions))},
		})
	}

	if len(params.ReceivingDistricts) > 0 {
		filters = append(filters, Filter{
			Query: "lower(f.receiving_district) IN (?)",
			Args:  []interface{}{bun.In(stringsToLower(params.ReceivingDistricts))},
		})
	}

	return filters
}

func stringsToLower(arr []string) []string {
	out := make([]string, len(arr))
	for i, v := range arr {
		out[i] = strings.ToLower(v)
	}
	return out
}

func stringsToUpper(arr []string) []string {
	out := make([]string, len(arr))
	for i, v := range arr {
		out[i] = strings.ToUpper(v)
	}
	return out
}
