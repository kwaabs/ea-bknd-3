package amrcustomer

import (
	"context"
	"github.com/uptrace/bun"
	"net/http"
	"strconv"
	"strings"

)

type Service struct {
	db *bun.DB
}

func NewService(db *bun.DB) *Service {
	return &Service{db: db}
}

// Deduped meter rows must expose every column used by buildAmrFilters
// (region, district, community, tariff, customer/account/service type, slt_type,
// account_no_, spn) or status/health queries fail when those filters are set.
const amrMeterDedupSQL = `
			SELECT DISTINCT ON (meter_number)
				id, meter_number, region, district, community,
				customer_name, account_no_, spn,
				tariffclassname, customertype, accounttype,
				contractstatus, servicetype, slt_type
			FROM app.amr_customer_records
			WHERE meter_number IS NOT NULL
			ORDER BY meter_number, id
`

// ===================================================
// QUERY PARAMS PARSER
// ===================================================

type AmrQueryParams struct {
	Page           int
	Limit          int
	Regions        []string
	Districts      []string
	Communities    []string
	TariffClass    []string
	CustomerType   []string
	AccountType    []string
	ContractStatus []string
	ServiceType    []string
	AccountNo      []string
	SPN            []string
	Search         string
	SortBy         string
	SortOrder      string
}

func parseAmrQuery(r *http.Request) AmrQueryParams {
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

	return AmrQueryParams{
		Page:           page,
		Limit:          limit,
		Regions:        trimSplit(q.Get("region")),
		Districts:      trimSplit(q.Get("district")),
		Communities:    trimSplit(q.Get("community")),
		TariffClass:    trimSplit(q.Get("tariffClass")),
		CustomerType:   trimSplit(q.Get("customerType")),
		AccountType:    trimSplit(q.Get("accountType")),
		ContractStatus: trimSplit(q.Get("contractStatus")),
		ServiceType:    trimSplit(q.Get("serviceType")),
		AccountNo:      trimSplit(q.Get("accountNo")),
		SPN:            trimSplit(q.Get("spn")),
		Search:         q.Get("search"),
		SortBy:         q.Get("sortBy"),
		SortOrder:      q.Get("sortOrder"),
	}
}

// ===================================================
// FILTER BUILDER
// ===================================================

type AmrFilter struct {
	Query string
	Args  []interface{}
}

func buildAmrFilters(params AmrReadingFilterParams) []AmrFilter {
	var filters []AmrFilter

	// Date range — always required
	filters = append(filters, AmrFilter{
		Query: "mcd.consumption_date BETWEEN ? AND ?",
		Args:  []interface{}{params.DateFrom, params.DateTo},
	})

	if len(params.MeterNumber) > 0 {
		filters = append(filters, AmrFilter{
			Query: "mcd.meter_number IN (?)",
			Args:  []interface{}{bun.In(params.MeterNumber)},
		})
	}
	if len(params.Regions) > 0 {
		filters = append(filters, AmrFilter{
			Query: "LOWER(acr.region) IN (?)",
			Args:  []interface{}{bun.In(amrStringsToLower(params.Regions))},
		})
	}
	if len(params.Districts) > 0 {
		filters = append(filters, AmrFilter{
			Query: "LOWER(acr.district) IN (?)",
			Args:  []interface{}{bun.In(amrStringsToLower(params.Districts))},
		})
	}
	if len(params.Communities) > 0 {
		filters = append(filters, AmrFilter{
			Query: "LOWER(acr.community) IN (?)",
			Args:  []interface{}{bun.In(amrStringsToLower(params.Communities))},
		})
	}
	if len(params.TariffClass) > 0 {
		filters = append(filters, AmrFilter{
			Query: "LOWER(acr.tariffclassname) IN (?)",
			Args:  []interface{}{bun.In(amrStringsToLower(params.TariffClass))},
		})
	}
	if len(params.CustomerType) > 0 {
		filters = append(filters, AmrFilter{
			Query: "LOWER(acr.customertype) IN (?)",
			Args:  []interface{}{bun.In(amrStringsToLower(params.CustomerType))},
		})
	}
	if len(params.AccountType) > 0 {
		filters = append(filters, AmrFilter{
			Query: "LOWER(acr.accounttype) IN (?)",
			Args:  []interface{}{bun.In(amrStringsToLower(params.AccountType))},
		})
	}
	if len(params.ContractStatus) > 0 {
		filters = append(filters, AmrFilter{
			Query: "LOWER(acr.contractstatus) IN (?)",
			Args:  []interface{}{bun.In(amrStringsToLower(params.ContractStatus))},
		})
	}
	if len(params.ServiceType) > 0 {
		filters = append(filters, AmrFilter{
			Query: "LOWER(acr.servicetype) IN (?)",
			Args:  []interface{}{bun.In(amrStringsToLower(params.ServiceType))},
		})
	}
	if len(params.SLTType) > 0 {
		filters = append(filters, AmrFilter{
			Query: "LOWER(acr.slt_type) IN (?)",
			Args:  []interface{}{bun.In(amrStringsToLower(params.SLTType))},
		})
	}
	if len(params.AccountNo) > 0 {
		filters = append(filters, AmrFilter{
			Query: "acr.account_no_ IN (?)",
			Args:  []interface{}{bun.In(params.AccountNo)},
		})
	}
	if len(params.SPN) > 0 {
		filters = append(filters, AmrFilter{
			Query: "acr.spn IN (?)",
			Args:  []interface{}{bun.In(params.SPN)},
		})
	}

	return filters
}

// ===================================================
// DAILY CONSUMPTION
// ===================================================

// dailyConsumptionBaseQuery builds the grouped join query shared by the
// count and the paginated data fetch, with all filters applied but no
// LIMIT/OFFSET.
func (s *Service) dailyConsumptionBaseQuery(params AmrDailyConsumptionQueryParams) *bun.SelectQuery {
	filters := buildAmrFilters(params.AmrReadingFilterParams)

	q := s.db.NewSelect().
		// canonical deduplicated meter join
		TableExpr(`(
			SELECT DISTINCT ON (meter_number)
				id,
				meter_number,
				region,
				district,
				community,
				customer_name,
				account_no_,
				spn,
				tariffclassname,
				customertype,
				accounttype,
				contractstatus,
				meterphase,
				servicetype,
				slt_type,
				multiply_factor
			FROM app.amr_customer_records
			WHERE meter_number IS NOT NULL
			ORDER BY meter_number, id
		) AS acr`).
		Join("INNER JOIN app.amr_meter_consumption_daily AS mcd ON mcd.meter_number = acr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		ColumnExpr("mcd.consumption_date AT TIME ZONE 'UTC' AS consumption_date").
		ColumnExpr("mcd.meter_number").
		ColumnExpr("acr.region").
		ColumnExpr("acr.district").
		ColumnExpr("acr.community").
		ColumnExpr("acr.customer_name").
		ColumnExpr("acr.account_no_").
		ColumnExpr("acr.spn").
		ColumnExpr("acr.tariffclassname").
		ColumnExpr("acr.customertype").
		ColumnExpr("acr.accounttype").
		ColumnExpr("acr.contractstatus").
		ColumnExpr("acr.meterphase").
		ColumnExpr("acr.servicetype").
		ColumnExpr("acr.slt_type").
		ColumnExpr("acr.multiply_factor").
		ColumnExpr("mcd.day_start_reading").
		ColumnExpr("mcd.day_end_reading").
		ColumnExpr("ROUND((SUM(mcd.consumption))::numeric, 4) AS consumed_energy").
		ColumnExpr("dim.system_name")

	for _, f := range filters {
		q = q.Where(f.Query, f.Args...)
	}
	if params.SystemName != "" {
		q = q.Where("dim.system_name = ?", params.SystemName)
	}

	return q.
		GroupExpr("mcd.consumption_date").
		GroupExpr("mcd.meter_number").
		GroupExpr("acr.region").
		GroupExpr("acr.district").
		GroupExpr("acr.community").
		GroupExpr("acr.customer_name").
		GroupExpr("acr.account_no_").
		GroupExpr("acr.spn").
		GroupExpr("acr.tariffclassname").
		GroupExpr("acr.customertype").
		GroupExpr("acr.accounttype").
		GroupExpr("acr.contractstatus").
		GroupExpr("acr.meterphase").
		GroupExpr("acr.servicetype").
		GroupExpr("acr.slt_type").
		GroupExpr("acr.multiply_factor").
		GroupExpr("mcd.day_start_reading").
		GroupExpr("mcd.day_end_reading").
		GroupExpr("dim.system_name")
}

func (s *Service) GetDailyConsumption(
	ctx context.Context,
	params AmrDailyConsumptionQueryParams,
) (*AmrDailyConsumptionResponse, error) {
	if params.Page < 1 {
		params.Page = 1
	}
	if params.Limit <= 0 || params.Limit > 500 {
		params.Limit = 100
	}

	total, err := s.db.NewSelect().
		TableExpr("(?) AS daily_consumption", s.dailyConsumptionBaseQuery(params)).
		Count(ctx)
	if err != nil {
		return nil, err
	}

	// ORDER BY + LIMIT applied directly alongside the GROUP BY (18 columns,
	// a join, and a subquery) sends Postgres down a pathological plan here —
	// measured at 35+s for a LIMIT 3. Wrapping the grouped result as its own
	// subquery and sorting/limiting that in a separate outer step forces
	// Postgres to materialize the (fast, ~600ms) grouped result first, then
	// do a cheap sort+limit over it.
	offset := (params.Page - 1) * params.Limit
	var results []AmrDailyConsumptionResult
	q := s.db.NewSelect().
		TableExpr("(?) AS daily_consumption", s.dailyConsumptionBaseQuery(params)).
		ColumnExpr("*").
		OrderExpr("consumption_date, meter_number").
		Limit(params.Limit).
		Offset(offset)
	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}
	if results == nil {
		results = []AmrDailyConsumptionResult{}
	}

	return &AmrDailyConsumptionResponse{
		Data:       results,
		Total:      total,
		Page:       params.Page,
		Limit:      params.Limit,
		TotalPages: (total + params.Limit - 1) / params.Limit,
	}, nil
}

// ===================================================
// AGGREGATED CONSUMPTION
// ===================================================

func (s *Service) GetAggregatedConsumption(
	ctx context.Context,
	params AmrReadingFilterParams,
	groupBy string,
	additionalGroups []string,
) ([]AmrAggregatedConsumptionResult, error) {
	var results []AmrAggregatedConsumptionResult

	filters := buildAmrFilters(params)

	q := s.db.NewSelect().
		TableExpr(`(
			SELECT DISTINCT ON (meter_number)
				id,
				meter_number,
				region,
				district,
				community,
				tariffclassname,
				customertype,
				accounttype,
				contractstatus,
				servicetype,
				slt_type,
				multiply_factor
			FROM app.amr_customer_records
			WHERE meter_number IS NOT NULL
			ORDER BY meter_number, id
		) AS acr`).
		Join("INNER JOIN app.amr_meter_consumption_daily AS mcd ON mcd.meter_number = acr.meter_number").
		Join("JOIN app.data_item_mapping AS dim ON mcd.data_item_id = dim.data_item_id").
		ColumnExpr("dim.system_name AS system_name").
		ColumnExpr("acr.region AS region").
		ColumnExpr("ROUND(SUM(mcd.consumption)::numeric, 4) AS total_consumption").
		ColumnExpr("COUNT(DISTINCT mcd.meter_number) AS active_meters")

	// --- Subquery: total meter count ---
	subQ := s.db.NewSelect().
		TableExpr(`(
			SELECT DISTINCT ON (meter_number) meter_number, region, district, community,
				tariffclassname, customertype, contractstatus, servicetype, slt_type
			FROM app.amr_customer_records
			WHERE meter_number IS NOT NULL
			ORDER BY meter_number, id
		) AS acr2`).
		ColumnExpr("COUNT(DISTINCT acr2.meter_number)")

	if len(params.Regions) > 0 {
		subQ = subQ.Where("LOWER(acr2.region) IN (?)", bun.In(amrStringsToLower(params.Regions)))
	}
	if len(params.Districts) > 0 {
		subQ = subQ.Where("LOWER(acr2.district) IN (?)", bun.In(amrStringsToLower(params.Districts)))
	}
	if len(params.Communities) > 0 {
		subQ = subQ.Where("LOWER(acr2.community) IN (?)", bun.In(amrStringsToLower(params.Communities)))
	}
	if len(params.TariffClass) > 0 {
		subQ = subQ.Where("LOWER(acr2.tariffclassname) IN (?)", bun.In(amrStringsToLower(params.TariffClass)))
	}
	if len(params.CustomerType) > 0 {
		subQ = subQ.Where("LOWER(acr2.customertype) IN (?)", bun.In(amrStringsToLower(params.CustomerType)))
	}
	if len(params.ContractStatus) > 0 {
		subQ = subQ.Where("LOWER(acr2.contractstatus) IN (?)", bun.In(amrStringsToLower(params.ContractStatus)))
	}
	if len(params.ServiceType) > 0 {
		subQ = subQ.Where("LOWER(acr2.servicetype) IN (?)", bun.In(amrStringsToLower(params.ServiceType)))
	}
	if len(params.SLTType) > 0 {
		subQ = subQ.Where("LOWER(acr2.slt_type) IN (?)", bun.In(amrStringsToLower(params.SLTType)))
	}

	q = q.ColumnExpr("(?) AS total_meter_count", subQ)

	// --- Time grouping ---
	groupCols := []bun.Safe{
		bun.Safe("dim.system_name"),
		bun.Safe("acr.region"),
	}

	switch groupBy {
	case "month":
		q = q.ColumnExpr("DATE_TRUNC('month', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('month', mcd.consumption_date)"))
	case "year":
		q = q.ColumnExpr("DATE_TRUNC('year', mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE_TRUNC('year', mcd.consumption_date)"))
	default: // day
		q = q.ColumnExpr("DATE(mcd.consumption_date) AS group_period")
		groupCols = append(groupCols, bun.Safe("DATE(mcd.consumption_date)"))
	}

	// --- Additional grouping (district, community, tariffclassname, etc.) ---
	validAdditional := map[string]string{
		"district":       "acr.district",
		"community":      "acr.community",
		"tariffclass":    "acr.tariffclassname",
		"customertype":   "acr.customertype",
		"accounttype":    "acr.accounttype",
		"contractstatus": "acr.contractstatus",
		"servicetype":    "acr.servicetype",
		"slt_type":       "acr.slt_type",
		"slttype":        "acr.slt_type",
	}

	for _, g := range additionalGroups {
		key := strings.ToLower(g)
		col, ok := validAdditional[key]
		if !ok {
			continue
		}
		alias := key
		if key == "slttype" {
			alias = "slt_type"
		}
		q = q.ColumnExpr(col + " AS " + alias)
		groupCols = append(groupCols, bun.Safe(col))
	}

	// --- Apply filters ---
	for _, f := range filters {
		q = q.Where(f.Query, f.Args...)
	}

	// --- Group by ---
	for _, g := range groupCols {
		q = q.GroupExpr(string(g))
	}

	q = q.OrderExpr("group_period, acr.region")

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

// ===================================================
// METER STATUS
// ===================================================

func (s *Service) GetMeterStatus(
	ctx context.Context,
	params AmrReadingFilterParams,
) ([]AmrMeterStatusResult, error) {
	var results []AmrMeterStatusResult

	filters := buildAmrFilters(params)

	q := s.db.NewSelect().
		TableExpr("("+amrMeterDedupSQL+") AS acr").
		Join(`LEFT JOIN app.amr_meter_consumption_daily AS mcd
			ON acr.meter_number = mcd.meter_number
			AND mcd.consumption_date BETWEEN ? AND ?`,
			params.DateFrom, params.DateTo,
		).
		ColumnExpr("acr.meter_number").
		ColumnExpr("acr.region").
		ColumnExpr("acr.district").
		ColumnExpr("acr.community").
		ColumnExpr("acr.customer_name").
		ColumnExpr("acr.account_no_").
		ColumnExpr("acr.tariffclassname").
		ColumnExpr("acr.contractstatus").
		ColumnExpr("acr.servicetype").
		ColumnExpr("mcd.consumption_date AT TIME ZONE 'UTC' AS consumption_date").
		ColumnExpr("mcd.consumption").
		ColumnExpr("mcd.reading_count").
		ColumnExpr("mcd.day_start_time").
		ColumnExpr("mcd.day_end_time").
		ColumnExpr(`
			CASE
				WHEN mcd.meter_number IS NULL THEN 'OFFLINE - No Record'
				WHEN mcd.data_item_id = 'NO_DATA' THEN 'OFFLINE - No Data'
				ELSE 'ONLINE'
			END AS status
		`)

	// Apply filters except date (already in JOIN)
	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	q = q.OrderExpr(`
		CASE
			WHEN mcd.meter_number IS NULL THEN 1
			WHEN mcd.data_item_id = 'NO_DATA' THEN 2
			ELSE 3
		END ASC,
		acr.meter_number ASC
	`)

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Service) GetMeterStatusSummary(
	ctx context.Context,
	params AmrReadingFilterParams,
) (*AmrMeterStatusSummary, error) {

	filters := buildAmrFilters(params)

	q := s.db.NewSelect().
		TableExpr("("+amrMeterDedupSQL+") AS acr").
		Join(`LEFT JOIN (
			SELECT
				meter_number,
				MAX(consumption_date) AS last_consumption_date,
				BOOL_OR(data_item_id != 'NO_DATA') AS has_actual_data,
				SUM(consumption) AS total_consumption,
				(COUNT(DISTINCT CASE WHEN data_item_id != 'NO_DATA' THEN DATE(consumption_date) END) * 100.0 /
				 NULLIF(COUNT(DISTINCT DATE(consumption_date)), 0)) AS uptime_percentage
			FROM app.amr_meter_consumption_daily
			WHERE consumption_date BETWEEN ? AND ?
			GROUP BY meter_number
		) AS mcd ON acr.meter_number = mcd.meter_number`,
			params.DateFrom, params.DateTo,
		).
		ColumnExpr("COUNT(DISTINCT CASE WHEN mcd.meter_number IS NULL THEN acr.id END) AS offline_no_record").
		ColumnExpr("COUNT(DISTINCT CASE WHEN mcd.has_actual_data = false THEN acr.id END) AS offline_no_data").
		ColumnExpr("COUNT(DISTINCT CASE WHEN mcd.has_actual_data = true THEN acr.id END) AS online").
		ColumnExpr("COALESCE(AVG(mcd.uptime_percentage), 0) AS avg_uptime").
		ColumnExpr("COALESCE(SUM(mcd.total_consumption), 0) AS total_consumption")

	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	var row struct {
		Online          int     `bun:"online"`
		OfflineNoRecord int     `bun:"offline_no_record"`
		OfflineNoData   int     `bun:"offline_no_data"`
		AvgUptime       float64 `bun:"avg_uptime"`
		TotalConsumption float64 `bun:"total_consumption"`
	}

	if err := q.Scan(ctx, &row); err != nil {
		return nil, err
	}

	totalOffline := row.OfflineNoRecord + row.OfflineNoData
	total := row.Online + totalOffline

	onlinePct, offlinePct := 0.0, 0.0
	if total > 0 {
		onlinePct = float64(row.Online) * 100.0 / float64(total)
		offlinePct = float64(totalOffline) * 100.0 / float64(total)
	}

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
	if len(params.TariffClass) > 0 {
		filtersApplied["tariffClass"] = params.TariffClass
	}
	if len(params.ContractStatus) > 0 {
		filtersApplied["contractStatus"] = params.ContractStatus
	}

	return &AmrMeterStatusSummary{
		Total:               total,
		Online:              row.Online,
		OfflineNoData:       row.OfflineNoData,
		OfflineNoRecord:     row.OfflineNoRecord,
		TotalOffline:        totalOffline,
		OnlinePercentage:    onlinePct,
		OfflinePercentage:   offlinePct,
		AvgUptimePercentage: row.AvgUptime,
		TotalConsumptionKWh: row.TotalConsumption,
		FiltersApplied:      filtersApplied,
	}, nil
}

func (s *Service) GetMeterStatusTimeline(
	ctx context.Context,
	params AmrReadingFilterParams,
) (*AmrMeterStatusTimeline, error) {

	filters := buildAmrFilters(params)

	// Build filtered meter subquery
	meterSubQ := s.db.NewSelect().
		TableExpr("("+amrMeterDedupSQL+") AS acr").
		ColumnExpr("acr.meter_number")

	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		q := strings.ReplaceAll(f.Query, "acr.", "acr.")
		meterSubQ = meterSubQ.Where(q, f.Args...)
	}

	var entries []AmrMeterStatusTimelineEntry

	err := s.db.NewSelect().
		ColumnExpr("date").
		ColumnExpr("COUNT(*) FILTER (WHERE is_online) AS online").
		ColumnExpr("COUNT(*) FILTER (WHERE NOT is_online OR is_online IS NULL) AS offline").
		ColumnExpr("COUNT(*) AS total").
		TableExpr(`(
			SELECT
				d.date,
				acr.meter_number,
				BOOL_OR(mcd.data_item_id != 'NO_DATA') AS is_online
			FROM (
				SELECT generate_series(DATE(?), DATE(?), interval '1 day')::date AS date
			) d
			CROSS JOIN (?) acr
			LEFT JOIN app.amr_meter_consumption_daily mcd
				ON DATE(mcd.consumption_date) = d.date
				AND mcd.meter_number = acr.meter_number
			GROUP BY d.date, acr.meter_number
		) AS daily_status`,
			params.DateFrom,
			params.DateTo,
			meterSubQ,
		).
		GroupExpr("date").
		OrderExpr("date ASC").
		Scan(ctx, &entries)

	if err != nil {
		return nil, err
	}

	timeline := &AmrMeterStatusTimeline{Data: entries}
	timeline.DateRange.From = params.DateFrom.Format("2006-01-02")
	timeline.DateRange.To = params.DateTo.Format("2006-01-02")

	return timeline, nil
}

func (s *Service) GetMeterStatusDetails(
	ctx context.Context,
	params AmrStatusDetailQueryParams,
) (*AmrMeterStatusDetailResponse, error) {

	if params.Page < 1 {
		params.Page = 1
	}
	if params.Limit <= 0 || params.Limit > 200 {
		params.Limit = 50
	}

	daysInRange := int(params.DateTo.Sub(params.DateFrom).Hours()/24) + 1
	filters := buildAmrFilters(params.AmrReadingFilterParams)

	// Fast count
	countQ := s.db.NewSelect().
		TableExpr("("+amrMeterDedupSQL+") AS acr")

	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		countQ = countQ.Where(f.Query, f.Args...)
	}
	if params.Search != "" {
		countQ = countQ.Where(
			"acr.meter_number ILIKE ? OR acr.customer_name ILIKE ? OR acr.account_no_ ILIKE ?",
			"%"+params.Search+"%", "%"+params.Search+"%", "%"+params.Search+"%",
		)
	}

	totalCount, err := countQ.Count(ctx)
	if err != nil {
		return nil, err
	}

	// Main query
	q := s.db.NewSelect().
		TableExpr("("+amrMeterDedupSQL+") AS acr").
		Join(`LEFT JOIN app.amr_meter_consumption_daily AS mcd
			ON acr.meter_number = mcd.meter_number
			AND mcd.consumption_date BETWEEN ? AND ?`,
			params.DateFrom, params.DateTo,
		).
		ColumnExpr("acr.meter_number").
		ColumnExpr("acr.region").
		ColumnExpr("acr.district").
		ColumnExpr("acr.community").
		ColumnExpr("acr.customer_name").
		ColumnExpr("acr.account_no_").
		ColumnExpr("acr.tariffclassname").
		ColumnExpr("acr.contractstatus").
		ColumnExpr("acr.servicetype").
		ColumnExpr(`
			CASE
				WHEN COUNT(CASE WHEN mcd.data_item_id != 'NO_DATA' THEN 1 END) > 0 THEN 'ONLINE'
				WHEN COUNT(CASE WHEN mcd.data_item_id = 'NO_DATA' THEN 1 END) > 0 THEN 'OFFLINE - No Data'
				ELSE 'OFFLINE - No Record'
			END AS status
		`).
		ColumnExpr("MAX(mcd.consumption_date) AS last_consumption_date").
		ColumnExpr("COALESCE(SUM(mcd.consumption), 0) AS total_consumption_kwh").
		ColumnExpr(`
			(COUNT(DISTINCT CASE WHEN mcd.data_item_id != 'NO_DATA' THEN DATE(mcd.consumption_date) END) * 100.0 /
			 ?) AS uptime_percentage
		`, daysInRange).
		ColumnExpr(`
			(? - COUNT(DISTINCT CASE WHEN mcd.data_item_id != 'NO_DATA' THEN DATE(mcd.consumption_date) END)) AS days_offline
		`, daysInRange).
		ColumnExpr("MAX(mcd.day_end_time) AS last_reading_time")

	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	if params.Search != "" {
		q = q.Where(
			"acr.meter_number ILIKE ? OR acr.customer_name ILIKE ? OR acr.account_no_ ILIKE ?",
			"%"+params.Search+"%", "%"+params.Search+"%", "%"+params.Search+"%",
		)
	}

	q = q.
		GroupExpr("acr.meter_number").
		GroupExpr("acr.region").
		GroupExpr("acr.district").
		GroupExpr("acr.community").
		GroupExpr("acr.customer_name").
		GroupExpr("acr.account_no_").
		GroupExpr("acr.tariffclassname").
		GroupExpr("acr.contractstatus").
		GroupExpr("acr.servicetype")

	if params.Status != "" {
		if params.Status == "ONLINE" {
			q = q.Having("COUNT(CASE WHEN mcd.data_item_id != 'NO_DATA' THEN 1 END) > 0")
		} else if params.Status == "OFFLINE" {
			q = q.Having("COUNT(CASE WHEN mcd.data_item_id != 'NO_DATA' THEN 1 END) = 0")
		}
	}

	// Sorting
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
		q = q.OrderExpr("acr.meter_number " + sortOrder)
	default:
		q = q.OrderExpr("acr.meter_number ASC")
	}

	offset := (params.Page - 1) * params.Limit
	q = q.Limit(params.Limit).Offset(offset)

	var records []AmrMeterStatusDetailRecord
	if err := q.Scan(ctx, &records); err != nil {
		return nil, err
	}

	totalPages := (totalCount + params.Limit - 1) / params.Limit

	filtersApplied := map[string]interface{}{
		"dateFrom": params.DateFrom.Format("2006-01-02"),
		"dateTo":   params.DateTo.Format("2006-01-02"),
	}
	if len(params.Regions) > 0 {
		filtersApplied["region"] = params.Regions
	}
	if len(params.TariffClass) > 0 {
		filtersApplied["tariffClass"] = params.TariffClass
	}
	if params.Search != "" {
		filtersApplied["search"] = params.Search
	}
	if params.Status != "" {
		filtersApplied["status"] = params.Status
	}

	resp := &AmrMeterStatusDetailResponse{
		Data:           records,
		FiltersApplied: filtersApplied,
	}
	resp.Pagination.Page = params.Page
	resp.Pagination.Limit = params.Limit
	resp.Pagination.TotalRecords = totalCount
	resp.Pagination.TotalPages = totalPages
	resp.Pagination.HasMore = params.Page < totalPages

	return resp, nil
}

// ===================================================
// HEALTH
// ===================================================

func (s *Service) GetMeterHealthSummary(
	ctx context.Context,
	params AmrReadingFilterParams,
) (*AmrMeterHealthSummary, error) {

	filters := buildAmrFilters(params)

	q := s.db.NewSelect().
		TableExpr("("+amrMeterDedupSQL+") AS acr").
		Join(`LEFT JOIN (
			SELECT
				meter_number,
				BOOL_OR(data_item_id != 'NO_DATA') AS has_actual_data,
				(
					COUNT(DISTINCT CASE WHEN data_item_id != 'NO_DATA' THEN DATE(consumption_date) END) * 100.0
					/ NULLIF(COUNT(DISTINCT DATE(consumption_date)), 0)
				) AS uptime_percentage
			FROM app.amr_meter_consumption_daily
			WHERE consumption_date BETWEEN ? AND ?
			GROUP BY meter_number
		) AS mcd ON acr.meter_number = mcd.meter_number`,
			params.DateFrom, params.DateTo,
		).
		ColumnExpr("COUNT(DISTINCT acr.meter_number) AS total_meters").
		ColumnExpr(`COUNT(DISTINCT CASE WHEN mcd.has_actual_data = true THEN acr.meter_number END) AS online_meters`).
		ColumnExpr(`COUNT(DISTINCT CASE WHEN mcd.has_actual_data = false OR mcd.meter_number IS NULL THEN acr.meter_number END) AS offline_meters`).
		ColumnExpr("COALESCE(AVG(mcd.uptime_percentage), 0) AS avg_uptime").
		ColumnExpr(`COUNT(DISTINCT CASE WHEN mcd.uptime_percentage > 95 THEN acr.meter_number END) AS excellent`).
		ColumnExpr(`COUNT(DISTINCT CASE WHEN mcd.uptime_percentage >= 80 AND mcd.uptime_percentage <= 95 THEN acr.meter_number END) AS good`).
		ColumnExpr(`COUNT(DISTINCT CASE WHEN mcd.uptime_percentage >= 60 AND mcd.uptime_percentage < 80 THEN acr.meter_number END) AS poor`).
		ColumnExpr(`COUNT(DISTINCT CASE WHEN mcd.uptime_percentage < 60 THEN acr.meter_number END) AS critical`)

	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		q = q.Where(f.Query, f.Args...)
	}

	var overall struct {
		TotalMeters   int     `bun:"total_meters"`
		OnlineMeters  int     `bun:"online_meters"`
		OfflineMeters int     `bun:"offline_meters"`
		AvgUptime     float64 `bun:"avg_uptime"`
		Excellent     int     `bun:"excellent"`
		Good          int     `bun:"good"`
		Poor          int     `bun:"poor"`
		Critical      int     `bun:"critical"`
	}

	if err := q.Scan(ctx, &overall); err != nil {
		return nil, err
	}

	// --- By Tariff Class ---
	var byTariff []AmrHealthByTariffClass

	qTariff := s.db.NewSelect().
		TableExpr("("+amrMeterDedupSQL+") AS acr").
		Join(`LEFT JOIN (
			SELECT
				meter_number,
				BOOL_OR(data_item_id != 'NO_DATA') AS has_actual_data,
				(COUNT(DISTINCT CASE WHEN data_item_id != 'NO_DATA' THEN DATE(consumption_date) END) * 100.0 /
				 NULLIF(COUNT(DISTINCT DATE(consumption_date)), 0)) AS uptime_percentage
			FROM app.amr_meter_consumption_daily
			WHERE consumption_date BETWEEN ? AND ?
			GROUP BY meter_number
		) AS mcd ON acr.meter_number = mcd.meter_number`,
			params.DateFrom, params.DateTo,
		).
		ColumnExpr("COALESCE(acr.tariffclassname, 'Unknown') AS tariff_class").
		ColumnExpr("COUNT(DISTINCT acr.meter_number) AS total").
		ColumnExpr(`COUNT(DISTINCT CASE WHEN mcd.has_actual_data = true THEN acr.meter_number END) AS online`).
		ColumnExpr(`COUNT(DISTINCT CASE WHEN mcd.has_actual_data = false OR mcd.meter_number IS NULL THEN acr.meter_number END) AS offline`).
		ColumnExpr("COALESCE(AVG(mcd.uptime_percentage), 0) AS avg_uptime")

	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		qTariff = qTariff.Where(f.Query, f.Args...)
	}

	qTariff = qTariff.GroupExpr("acr.tariffclassname").OrderExpr("acr.tariffclassname")

	if err := qTariff.Scan(ctx, &byTariff); err != nil {
		return nil, err
	}

	// --- By Region ---
	var byRegion []AmrHealthByRegion

	qRegion := s.db.NewSelect().
		TableExpr("("+amrMeterDedupSQL+") AS acr").
		Join(`LEFT JOIN (
			SELECT
				meter_number,
				BOOL_OR(data_item_id != 'NO_DATA') AS has_actual_data,
				(COUNT(DISTINCT CASE WHEN data_item_id != 'NO_DATA' THEN DATE(consumption_date) END) * 100.0 /
				 NULLIF(COUNT(DISTINCT DATE(consumption_date)), 0)) AS uptime_percentage
			FROM app.amr_meter_consumption_daily
			WHERE consumption_date BETWEEN ? AND ?
			GROUP BY meter_number
		) AS mcd ON acr.meter_number = mcd.meter_number`,
			params.DateFrom, params.DateTo,
		).
		ColumnExpr("COALESCE(acr.region, 'Unknown') AS region").
		ColumnExpr("COUNT(DISTINCT acr.meter_number) AS total").
		ColumnExpr(`COUNT(DISTINCT CASE WHEN mcd.has_actual_data = true THEN acr.meter_number END) AS online`).
		ColumnExpr(`COUNT(DISTINCT CASE WHEN mcd.has_actual_data = false OR mcd.meter_number IS NULL THEN acr.meter_number END) AS offline`).
		ColumnExpr("COALESCE(AVG(mcd.uptime_percentage), 0) AS avg_uptime")

	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		qRegion = qRegion.Where(f.Query, f.Args...)
	}

	qRegion = qRegion.GroupExpr("acr.region").OrderExpr("acr.region")

	if err := qRegion.Scan(ctx, &byRegion); err != nil {
		return nil, err
	}

	healthPct := 0.0
	if overall.TotalMeters > 0 {
		healthPct = float64(overall.OnlineMeters) * 100.0 / float64(overall.TotalMeters)
	}

	return &AmrMeterHealthSummary{
		TotalMeters:             overall.TotalMeters,
		OnlineMeters:            overall.OnlineMeters,
		OfflineMeters:           overall.OfflineMeters,
		HealthPercentage:        healthPct,
		AverageUptimePercentage: overall.AvgUptime,
		UptimeDistribution: AmrMeterUptimeDistribution{
			Excellent: overall.Excellent,
			Good:      overall.Good,
			Poor:      overall.Poor,
			Critical:  overall.Critical,
		},
		ByTariffClass: byTariff,
		ByRegion:      byRegion,
	}, nil
}

func (s *Service) GetMeterHealthDetails(
	ctx context.Context,
	params AmrHealthDetailParams,
) (*AmrMeterHealthDetailResponse, error) {

	if params.Page < 1 {
		params.Page = 1
	}
	if params.Limit <= 0 || params.Limit > 200 {
		params.Limit = 50
	}

	daysInRange := int(params.DateTo.Sub(params.DateFrom).Hours()/24) + 1
	filters := buildAmrFilters(params.AmrReadingFilterParams)

	// Base CTE
	baseCTE := s.db.NewSelect().
		TableExpr("("+amrMeterDedupSQL+") AS acr").
		Join(`LEFT JOIN (
			SELECT
				meter_number,
				MAX(consumption_date) AS last_seen_date,
				BOOL_OR(data_item_id != 'NO_DATA') AS has_actual_data,
				COUNT(DISTINCT CASE WHEN data_item_id != 'NO_DATA' THEN DATE(consumption_date) END) AS days_online,
				COUNT(DISTINCT DATE(consumption_date))
					- COUNT(DISTINCT CASE WHEN data_item_id != 'NO_DATA' THEN DATE(consumption_date) END) AS days_offline,
				COUNT(DISTINCT DATE(consumption_date)) AS total_days,
				(
					COUNT(DISTINCT CASE WHEN data_item_id != 'NO_DATA' THEN DATE(consumption_date) END) * 100.0
					/ NULLIF(COUNT(DISTINCT DATE(consumption_date)), 0)
				) AS uptime_percentage,
				SUM(consumption) AS total_consumption
			FROM app.amr_meter_consumption_daily
			WHERE consumption_date BETWEEN ? AND ?
			GROUP BY meter_number
		) AS mcd ON acr.meter_number = mcd.meter_number`,
			params.DateFrom, params.DateTo,
		).
		ColumnExpr("acr.meter_number").
		ColumnExpr("acr.region").
		ColumnExpr("acr.district").
		ColumnExpr("acr.community").
		ColumnExpr("acr.customer_name").
		ColumnExpr("acr.account_no_").
		ColumnExpr("acr.tariffclassname").
		ColumnExpr("acr.contractstatus").
		ColumnExpr("acr.servicetype").
		ColumnExpr(`
			CASE
				WHEN mcd.has_actual_data = true THEN 'ONLINE'
				ELSE 'OFFLINE'
			END AS status
		`).
		ColumnExpr(`
			CASE
				WHEN mcd.uptime_percentage > 95 THEN 'excellent'
				WHEN mcd.uptime_percentage >= 80 THEN 'good'
				WHEN mcd.uptime_percentage >= 60 THEN 'poor'
				WHEN mcd.uptime_percentage < 60 THEN 'critical'
				ELSE 'offline'
			END AS health_category
		`).
		ColumnExpr("COALESCE(mcd.uptime_percentage, 0) AS uptime_percentage").
		ColumnExpr("COALESCE(mcd.days_online, 0) AS days_online").
		ColumnExpr("COALESCE(mcd.days_offline, ?) AS days_offline", daysInRange).
		ColumnExpr("? AS total_days", daysInRange).
		ColumnExpr("mcd.last_seen_date").
		ColumnExpr("COALESCE(mcd.total_consumption, 0) AS total_consumption_kwh").
		ColumnExpr(`
			CASE
				WHEN mcd.days_online > 0 THEN ROUND((mcd.total_consumption / mcd.days_online)::numeric, 2)
				ELSE 0
			END AS avg_daily_consumption
		`)

	for _, f := range filters {
		if strings.Contains(strings.ToLower(f.Query), "consumption_date") {
			continue
		}
		baseCTE = baseCTE.Where(f.Query, f.Args...)
	}

	if params.Search != "" {
		baseCTE = baseCTE.Where(
			"acr.meter_number ILIKE ? OR acr.customer_name ILIKE ? OR acr.account_no_ ILIKE ?",
			"%"+params.Search+"%", "%"+params.Search+"%", "%"+params.Search+"%",
		)
	}

	// Count
	countQ := s.db.NewSelect().
		TableExpr("(?) AS health_data", baseCTE).
		ColumnExpr("COUNT(*)")

	if params.HealthCategory != "" {
		countQ = countQ.Where("health_category = ?", strings.ToLower(params.HealthCategory))
	}

	totalCount, err := countQ.Count(ctx)
	if err != nil {
		return nil, err
	}

	// Main
	q := s.db.NewSelect().
		TableExpr("(?) AS health_data", baseCTE)

	if params.HealthCategory != "" {
		q = q.Where("health_category = ?", strings.ToLower(params.HealthCategory))
	}

	sortOrder := "DESC"
	if strings.ToLower(params.SortOrder) == "asc" {
		sortOrder = "ASC"
	}
	switch params.SortBy {
	case "uptime":
		q = q.OrderExpr("uptime_percentage " + sortOrder)
	case "consumption":
		q = q.OrderExpr("total_consumption_kwh " + sortOrder)
	case "last_seen":
		q = q.OrderExpr("last_seen_date " + sortOrder + " NULLS LAST")
	case "meter_number":
		q = q.OrderExpr("meter_number " + sortOrder)
	default:
		q = q.OrderExpr("uptime_percentage " + sortOrder)
	}

	offset := (params.Page - 1) * params.Limit
	q = q.Limit(params.Limit).Offset(offset)

	var records []AmrMeterHealthDetailRecord
	if err := q.Scan(ctx, &records); err != nil {
		return nil, err
	}

	// Summary from page results
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

	totalPages := (totalCount + params.Limit - 1) / params.Limit

	filtersApplied := map[string]interface{}{
		"dateFrom": params.DateFrom.Format("2006-01-02"),
		"dateTo":   params.DateTo.Format("2006-01-02"),
	}
	if len(params.Regions) > 0 {
		filtersApplied["region"] = params.Regions
	}
	if len(params.TariffClass) > 0 {
		filtersApplied["tariffClass"] = params.TariffClass
	}
	if params.Search != "" {
		filtersApplied["search"] = params.Search
	}
	if params.HealthCategory != "" {
		filtersApplied["healthCategory"] = params.HealthCategory
	}

	resp := &AmrMeterHealthDetailResponse{
		Data:           records,
		FiltersApplied: filtersApplied,
	}
	resp.Pagination.Page = params.Page
	resp.Pagination.Limit = params.Limit
	resp.Pagination.TotalRecords = totalCount
	resp.Pagination.TotalPages = totalPages
	resp.Pagination.HasMore = params.Page < totalPages
	resp.Summary.HealthCategory = params.HealthCategory
	resp.Summary.AverageUptime = avgUptime
	resp.Summary.TotalOnline = totalOnline
	resp.Summary.TotalOffline = totalOffline

	return resp, nil
}

// ===================================================
// FILTER HELPERS — unique values for dropdowns
// ===================================================

func (s *Service) GetUniqueRegions(ctx context.Context) ([]string, error) {
	var regions []string
	err := s.db.NewSelect().
		TableExpr("app.amr_customer_records").
		ColumnExpr("DISTINCT LOWER(region) AS region").
		Where("region IS NOT NULL AND region != ''").
		OrderExpr("region ASC").
		Scan(ctx, &regions)
	return regions, err
}

func (s *Service) GetUniqueDistricts(ctx context.Context, region string) ([]string, error) {
	var districts []string
	q := s.db.NewSelect().
		TableExpr("app.amr_customer_records").
		ColumnExpr("DISTINCT LOWER(district) AS district").
		Where("district IS NOT NULL AND district != ''")
	if region != "" {
		q = q.Where("LOWER(region) = ?", strings.ToLower(region))
	}
	err := q.OrderExpr("district ASC").Scan(ctx, &districts)
	return districts, err
}

func (s *Service) GetUniqueCommunities(ctx context.Context, region, district string) ([]string, error) {
	var communities []string
	q := s.db.NewSelect().
		TableExpr("app.amr_customer_records").
		ColumnExpr("DISTINCT LOWER(community) AS community").
		Where("community IS NOT NULL AND community != ''")
	if region != "" {
		q = q.Where("LOWER(region) = ?", strings.ToLower(region))
	}
	if district != "" {
		q = q.Where("LOWER(district) = ?", strings.ToLower(district))
	}
	err := q.OrderExpr("community ASC").Scan(ctx, &communities)
	return communities, err
}

func (s *Service) GetUniqueTariffClasses(ctx context.Context) ([]string, error) {
	var tariffs []string
	err := s.db.NewSelect().
		TableExpr("app.amr_customer_records").
		ColumnExpr("DISTINCT tariffclassname").
		Where("tariffclassname IS NOT NULL AND tariffclassname != ''").
		OrderExpr("tariffclassname ASC").
		Scan(ctx, &tariffs)
	return tariffs, err
}

func (s *Service) GetUniqueContractStatuses(ctx context.Context) ([]string, error) {
	var statuses []string
	err := s.db.NewSelect().
		TableExpr("app.amr_customer_records").
		ColumnExpr("DISTINCT contractstatus").
		Where("contractstatus IS NOT NULL AND contractstatus != ''").
		OrderExpr("contractstatus ASC").
		Scan(ctx, &statuses)
	return statuses, err
}

func (s *Service) GetUniqueCustomerTypes(ctx context.Context) ([]string, error) {
	var types []string
	err := s.db.NewSelect().
		TableExpr("app.amr_customer_records").
		ColumnExpr("DISTINCT customertype").
		Where("customertype IS NOT NULL AND customertype != ''").
		OrderExpr("customertype ASC").
		Scan(ctx, &types)
	return types, err
}

func (s *Service) GetUniqueServiceTypes(ctx context.Context) ([]string, error) {
	var types []string
	err := s.db.NewSelect().
		TableExpr("app.amr_customer_records").
		ColumnExpr("DISTINCT servicetype").
		Where("servicetype IS NOT NULL AND servicetype != ''").
		OrderExpr("servicetype ASC").
		Scan(ctx, &types)
	return types, err
}

// ===================================================
// HELPERS
// ===================================================

func amrStringsToLower(arr []string) []string {
	out := make([]string, len(arr))
	for i, v := range arr {
		out[i] = strings.ToLower(v)
	}
	return out
}


func (s *Service) DB() *bun.DB {
	return s.db
}
