package pnsconsumption

import (
	"context"
	"strings"

	"bknd-3/internal/dbx"
	"bknd-3/internal/httpx"

	"github.com/uptrace/bun"
)

const table = "app.pns_consumption"

type Service struct {
	db *bun.DB
}

func NewService(db *bun.DB) *Service { return &Service{db: db} }

// base returns a select on app.pns_consumption with all filters applied.
// regionid/districtid are opaque codes (see the package doc comment) —
// matched case-insensitively like every other filtered column here, even
// though case is not expected to vary in practice.
func (s *Service) base(p FilterParams) *bun.SelectQuery {
	q := s.db.NewSelect().TableExpr(table)
	q = dbx.InLower(q, "regionid", p.RegionID)
	q = dbx.InLower(q, "districtid", p.DistrictID)
	q = dbx.InLower(q, "tariffcategory", p.TariffCategory)
	q = dbx.InLower(q, "billmonth", p.BillMonth)
	q = dbx.In(q, "serviceid", p.ServiceID)
	q = dbx.DateRange(q, "billdate", p.DateFrom, p.DateTo)

	if p.Search != "" {
		search := "%" + strings.ToLower(strings.TrimSpace(p.Search)) + "%"
		q = q.Where(
			"(lower(customerid) LIKE ? OR lower(serviceid) LIKE ? OR lower(servicepoint) LIKE ?)",
			search, search, search,
		)
	}
	return q
}

// Detail returns a page of matching rows. The select and its count run
// concurrently inside dbx.Paginate.
func (s *Service) Detail(ctx context.Context, p FilterParams, pg httpx.Pagination) (*dbx.Page[Reading], error) {
	q := s.base(p).
		ColumnExpr("serviceid, customerid, servicepoint, regionid, districtid, tariffcategory, billmonth, billdate, energy, stationid").
		OrderExpr("regionid, districtid, customerid") // stable sort
	return dbx.Paginate[Reading](ctx, q, pg)
}

// groupExpr maps a whitelisted groupBy key to its (select, group-by) SQL
// pair. "region"/"district"/"tariff" are accepted as aliases for
// regionid/districtid/tariffcategory so callers can use the same groupBy
// values as every other source, even though the underlying values here are
// codes rather than names.
func groupExpr(g string) (selectExpr, groupByExpr string, ok bool) {
	switch g {
	case "regionid", "region":
		return "regionid", "regionid", true
	case "districtid", "district":
		return "districtid", "districtid", true
	case "tariffcategory", "tariff":
		return "tariffcategory", "tariffcategory", true
	case "billmonth":
		return "billmonth", "billmonth", true
	default:
		return "", "", false
	}
}

// Aggregate returns grouped sums/counts in a single query. No summary/
// fast-path table yet (this source starts small) — add one only in
// response to a confirmed slow query, matching how the other legacy
// sources were handled.
func (s *Service) Aggregate(ctx context.Context, p FilterParams, groupBy []string) (*AggregateResult, error) {
	q := s.base(p).
		ColumnExpr("COUNT(DISTINCT customerid) AS customer_count").
		ColumnExpr("COALESCE(ROUND(SUM(energy)::numeric, 2), 0) AS sum_energy_kwh")

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
