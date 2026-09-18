package holleyconsumption

import (
	"context"
	"strings"

	"bknd-3/internal/dbx"
	"bknd-3/internal/httpx"

	"github.com/uptrace/bun"
)

const table = "app.holley_consumption"

// regionLookupTable holds the ECG operational region/district boundary
// reference data. holley_consumption's own region column stores a region
// CODE (e.g. "09"), not a name — this table is the only place that maps
// those codes to real region names, keyed by region_code (e.g. "04" ->
// "Volta"). district_code/district also exist here but holley_consumption's
// district column already stores real district names directly, so only
// region needs this join.
const regionLookupTable = "app.dbo_ecg_operational_regions_and_district_boundaries_10_7_25"

// regionExpr resolves hc.region (a code) to its real name via regionLookupTable,
// falling back to the raw stored value when the code has no known mapping
// (garbage/out-of-range codes, or the column is empty) rather than hiding
// it. Every query below selects/filters/groups on this expression — never
// the raw hc.region column — once region resolution is needed.
const regionExpr = "COALESCE(rl.region_name, hc.region)"

type Service struct {
	db *bun.DB
}

func NewService(db *bun.DB) *Service { return &Service{db: db} }

// base returns a select on app.holley_consumption (aliased hc) left-joined
// against a distinct region_code -> region_name lookup (aliased rl), with
// all filters applied. hc is joined rather than queried bare so every
// column reference here and in Detail/Aggregate needs an hc./rl. prefix.
func (s *Service) base(p FilterParams) *bun.SelectQuery {
	q := s.db.NewSelect().
		TableExpr(table + " AS hc").
		// DISTINCT ON (not plain DISTINCT) guarantees at most one rl row per
		// region_code even if the boundary table ever has multiple district
		// rows disagreeing on a region's name spelling — a LEFT JOIN that
		// could match more than one row per hc row would silently fan out
		// (duplicate) every holley_consumption row it touches.
		Join("LEFT JOIN (SELECT DISTINCT ON (region_code) region_code, region AS region_name FROM " + regionLookupTable +
			" ORDER BY region_code, region) AS rl" +
			" ON rl.region_code = LPAD(NULLIF(TRIM(hc.region), ''), 2, '0')")
	q = dbx.InLower(q, regionExpr, p.Region)
	q = dbx.InLower(q, "hc.district", p.District)
	q = dbx.InLower(q, "hc.tariff_class", p.TariffClass)
	q = dbx.In(q, "hc.meter_no", p.MeterNo)
	q = dbx.DateRange(q, "hc.date_time", p.DateFrom, p.DateTo)

	if p.Search != "" {
		search := "%" + strings.ToLower(strings.TrimSpace(p.Search)) + "%"
		q = q.Where(
			"(lower(hc.customer_name) LIKE ? OR lower(hc.meter_no) LIKE ? OR lower(hc.customer_no) LIKE ?)",
			search, search, search,
		)
	}
	return q
}

// detailSortColumn maps a whitelisted sortBy key (matching the frontend
// table's own sort fields, named after the JSON field they sort) to the
// column Detail's ORDER BY uses. Anything not in the whitelist returns
// ok=false so the caller falls back to the stable default order.
func detailSortColumn(sortBy string) (column string, ok bool) {
	switch sortBy {
	case "customer_name":
		return "hc.customer_name", true
	case "consumption_kwh":
		return "hc.consumptionkwh", true
	case "date_time":
		return "hc.date_time", true
	default:
		return "", false
	}
}

// Detail returns a page of matching rows. The select and its count run
// concurrently inside dbx.Paginate. region is selected as the resolved
// name (see regionExpr), aliased back to "region" so Reading.Region binds
// exactly as before — callers never see the raw code.
func (s *Service) Detail(ctx context.Context, p FilterParams, pg httpx.Pagination, sortBy, sortOrder string) (*dbx.Page[Reading], error) {
	q := s.base(p).
		ColumnExpr("hc.meter_id, hc.date_time, hc.consumptionkwh, hc.meter_no, hc.customer_no, hc.customer_id, hc.customer_name, hc.geocode, " +
			regionExpr + " AS region, hc.district, hc.tariff_class")

	if col, ok := detailSortColumn(sortBy); ok {
		dir := "ASC"
		if strings.ToLower(sortOrder) == "desc" {
			dir = "DESC"
		}
		q = q.OrderExpr(col + " " + dir + ", hc.customer_name, hc.meter_no")
	} else {
		q = q.OrderExpr(regionExpr + ", hc.district, hc.customer_name, hc.meter_no") // stable default sort
	}
	return dbx.Paginate[Reading](ctx, q, pg)
}

// groupExpr maps a whitelisted groupBy key to its (select, group-by) SQL
// pair. "tariff" is accepted as an alias for tariff_class so callers can
// use the same groupBy values as every other source.
func groupExpr(g string) (selectExpr, groupByExpr string, ok bool) {
	switch g {
	case "region":
		return regionExpr + " AS region", regionExpr, true
	case "district":
		return "hc.district", "hc.district", true
	case "tariff_class", "tariff":
		return "hc.tariff_class", "hc.tariff_class", true
	default:
		return "", "", false
	}
}

// Aggregate returns grouped sums/counts in a single query.
func (s *Service) Aggregate(ctx context.Context, p FilterParams, groupBy []string) (*AggregateResult, error) {
	q := s.base(p).
		ColumnExpr("COUNT(DISTINCT hc.meter_no) AS customer_count").
		ColumnExpr("COALESCE(ROUND(SUM(hc.consumptionkwh)::numeric, 2), 0) AS sum_kwh")

	var orderExprs []string
	for _, g := range groupBy {
		g = strings.ToLower(strings.TrimSpace(g))
		selectExpr, groupByExpr, ok := groupExpr(g)
		if !ok {
			continue
		}
		q = q.ColumnExpr(selectExpr).GroupExpr(groupByExpr)
		orderExprs = append(orderExprs, groupByExpr)
	}
	if len(orderExprs) > 0 {
		q = q.OrderExpr(strings.Join(orderExprs, ", "))
	}

	var data []AggregateRow
	if err := q.Scan(ctx, &data); err != nil {
		return nil, err
	}
	if data == nil {
		data = []AggregateRow{}
	}
	return &AggregateResult{Data: data, Total: len(data)}, nil
}
