package alphaconsumption

import (
	"context"
	"strings"

	"bknd-3/internal/dbx"
	"bknd-3/internal/httpx"

	"github.com/uptrace/bun"
)

const table = "app.alpha_tnd_consumption_raw"

type Service struct {
	db *bun.DB
}

func NewService(db *bun.DB) *Service { return &Service{db: db} }

// base returns a select on app.alpha_tnd_consumption_raw with all filters
// applied. Unlike holleyconsumption/pnsconsumption, there's no
// region/district dimension on this source at all — only a substation
// name and consumer/meter identifiers — so no boundary-table join is
// needed here.
func (s *Service) base(p FilterParams) *bun.SelectQuery {
	q := s.db.NewSelect().TableExpr(table)
	q = dbx.InLower(q, "substationname", p.Substation)
	q = dbx.DateRange(q, "fromdatetime", p.DateFrom, p.DateTo)

	if len(p.EnergyCode) > 0 {
		q = q.Where("energycode IN (?)", bun.In(p.EnergyCode))
	}

	if p.Search != "" {
		search := "%" + strings.ToLower(strings.TrimSpace(p.Search)) + "%"
		q = q.Where(
			"(lower(consumername) LIKE ? OR lower(mtrsrno) LIKE ? OR lower(substationname) LIKE ?)",
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
	case "consumer_name":
		return "consumername", true
	case "value":
		return "value", true
	case "from_date":
		return "fromdatetime", true
	default:
		return "", false
	}
}

// Detail returns a page of matching rows. The select and its count run
// concurrently inside dbx.Paginate.
func (s *Service) Detail(ctx context.Context, p FilterParams, pg httpx.Pagination, sortBy, sortOrder string) (*dbx.Page[Reading], error) {
	q := s.base(p).
		ColumnExpr("readingmasterid, connectionpkid, energycode, value, fromdatetime, todatetime, substationname, consumerid, consumername, mtrsrno, locationname, locationcode")

	if col, ok := detailSortColumn(sortBy); ok {
		dir := "ASC"
		if strings.ToLower(sortOrder) == "desc" {
			dir = "DESC"
		}
		q = q.OrderExpr(col + " " + dir + ", consumername, mtrsrno")
	} else {
		q = q.OrderExpr("substationname, consumername, mtrsrno, fromdatetime") // stable default sort
	}
	return dbx.Paginate[Reading](ctx, q, pg)
}

// groupExpr maps a whitelisted groupBy key to its (select, group-by) SQL
// pair.
func groupExpr(g string) (selectExpr, groupByExpr string, ok bool) {
	switch g {
	case "substation":
		return "substationname AS substation", "substationname", true
	case "energy_code", "energycode":
		return "energycode AS energy_code", "energycode", true
	default:
		return "", "", false
	}
}

// Aggregate returns grouped sums/counts in a single query — there's no
// summary/fast-path table (this source is new; add one the same way
// Zeus/MMS did, only once real scale makes it necessary).
func (s *Service) Aggregate(ctx context.Context, p FilterParams, groupBy []string) (*AggregateResult, error) {
	q := s.base(p).
		ColumnExpr("COUNT(DISTINCT consumerid) AS consumer_count").
		ColumnExpr("COALESCE(ROUND(SUM(value)::numeric, 2), 0) AS sum_value")

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
