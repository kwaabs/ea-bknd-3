package botconsumption

import (
	"context"
	"strings"

	"bknd-3/internal/dbx"
	"bknd-3/internal/httpx"

	"github.com/uptrace/bun"
)

const table = "app.bot_consumption"

type Service struct {
	db *bun.DB
}

func NewService(db *bun.DB) *Service { return &Service{db: db} }

// base returns a select on app.bot_consumption with all filters applied.
//
// region is declared bpchar(15), so Postgres stores and returns it blank-
// padded to the full column width (e.g. "ACCRA WEST     "). trim(region) is
// used everywhere the column is filtered or returned so callers never have
// to know about or match that padding — same class of naming/formatting
// mismatch as the Zeus/MMS region-name issues elsewhere in this codebase,
// just caused by the column type instead of the source data.
func (s *Service) base(p FilterParams) *bun.SelectQuery {
	q := s.db.NewSelect().TableExpr(table)
	q = dbx.InLower(q, "trim(region)", p.Region)
	q = dbx.InLower(q, "district", p.District)
	q = dbx.InLower(q, "tarrif", p.Tariff)
	q = dbx.InLower(q, "billmonth", p.BillMonth)
	q = dbx.In(q, "meternumber", p.MeterNumber)

	if p.Search != "" {
		search := "%" + strings.ToLower(strings.TrimSpace(p.Search)) + "%"
		q = q.Where(
			"(lower(customer_name) LIKE ? OR lower(meternumber) LIKE ? OR lower(geo_code) LIKE ?)",
			search, search, search,
		)
	}
	return q
}

// Detail returns a page of matching rows. The select and its count run
// concurrently inside dbx.Paginate.
func (s *Service) Detail(ctx context.Context, p FilterParams, pg httpx.Pagination) (*dbx.Page[Reading], error) {
	q := s.base(p).
		ColumnExpr("customer_name, meternumber, geo_code, kwh, tarrif, billmonth, district").
		ColumnExpr("trim(region) AS region").
		OrderExpr("region, district, customer_name, meternumber") // stable sort
	return dbx.Paginate[Reading](ctx, q, pg)
}

// groupExpr maps a whitelisted groupBy key to its (select, group-by) SQL
// pair — region needs trim(), tariff needs to rename off the source
// table's "tarrif" typo, the rest are plain columns.
func groupExpr(g string) (selectExpr, groupByExpr string, ok bool) {
	switch g {
	case "region":
		return "trim(region) AS region", "trim(region)", true
	case "district":
		return "district", "district", true
	case "tariff":
		return "tarrif AS tariff", "tarrif", true
	case "billmonth":
		return "billmonth", "billmonth", true
	default:
		return "", "", false
	}
}

// Aggregate returns grouped sums/counts in a single query — there's no
// summary/fast-path table (this source is small; add one the same way
// Zeus/MMS did, only once real scale makes it necessary) and
// COUNT(DISTINCT meternumber) is a single-column distinct, which Postgres
// hashes fine alongside the other aggregates without the composite-key cost
// noted on zeusbilling's equivalent.
func (s *Service) Aggregate(ctx context.Context, p FilterParams, groupBy []string) (*AggregateResult, error) {
	q := s.base(p).
		ColumnExpr("COUNT(DISTINCT meternumber) AS customer_count").
		ColumnExpr("COALESCE(ROUND(SUM(kwh)::numeric, 2), 0) AS sum_kwh")

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
