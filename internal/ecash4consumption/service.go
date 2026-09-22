package ecash4consumption

import (
	"context"
	"strings"

	"bknd-3/internal/dbx"
	"bknd-3/internal/httpx"

	"github.com/uptrace/bun"
)

const table = "app.ecash4_consumption"

type Service struct {
	db *bun.DB
}

func NewService(db *bun.DB) *Service { return &Service{db: db} }

// base returns a select on app.ecash4_consumption with all filters applied.
func (s *Service) base(p FilterParams) *bun.SelectQuery {
	q := s.db.NewSelect().TableExpr(table)
	q = dbx.InLower(q, "region", p.Region)
	q = dbx.InLower(q, "district", p.District)
	q = dbx.InLower(q, "tariff_class", p.TariffClass)
	q = dbx.In(q, "meter_serial", p.MeterSerial)
	q = dbx.DateRange(q, "period_date", p.DateFrom, p.DateTo)

	if p.Search != "" {
		search := "%" + strings.ToLower(strings.TrimSpace(p.Search)) + "%"
		q = q.Where(
			"(lower(customer_name) LIKE ? OR lower(meter_serial) LIKE ? OR lower(spn) LIKE ?)",
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
		return "customer_name", true
	case "energy_kwh":
		return "energy_kwh", true
	case "period_date":
		return "period_date", true
	default:
		return "", false
	}
}

// Detail returns a page of matching rows. The select and its count run
// concurrently inside dbx.Paginate.
func (s *Service) Detail(ctx context.Context, p FilterParams, pg httpx.Pagination, sortBy, sortOrder string) (*dbx.Page[Reading], error) {
	q := s.base(p).
		ColumnExpr("meter_serial, spn, district, region, customer_name, year_month, period_date, energy_kwh, tariff_class")

	if col, ok := detailSortColumn(sortBy); ok {
		dir := "ASC"
		if strings.ToLower(sortOrder) == "desc" {
			dir = "DESC"
		}
		q = q.OrderExpr(col + " " + dir + ", customer_name, meter_serial")
	} else {
		q = q.OrderExpr("region, district, customer_name, meter_serial") // stable default sort
	}
	return dbx.Paginate[Reading](ctx, q, pg)
}

// groupExpr maps a whitelisted groupBy key to its (select, group-by) SQL
// pair. "tariff" is accepted as an alias for tariff_class so callers can
// use the same groupBy values as every other source.
func groupExpr(g string) (selectExpr, groupByExpr string, ok bool) {
	switch g {
	case "region":
		return "region", "region", true
	case "district":
		return "district", "district", true
	case "tariff_class", "tariff":
		return "tariff_class", "tariff_class", true
	default:
		return "", "", false
	}
}

// Aggregate returns grouped sums/counts in a single query.
func (s *Service) Aggregate(ctx context.Context, p FilterParams, groupBy []string) (*AggregateResult, error) {
	q := s.base(p).
		ColumnExpr("COUNT(DISTINCT spn) AS customer_count").
		ColumnExpr("COALESCE(ROUND(SUM(energy_kwh)::numeric, 2), 0) AS sum_kwh")

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
