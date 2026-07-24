package zeussales

import (
	"context"
	"strings"
	"sync"

	"bknd-3/internal/dbx"
	"bknd-3/internal/httpx"

	"github.com/uptrace/bun"
)

const table = "app.customer_sales_zeus"

type Service struct {
	db *bun.DB
}

func NewService(db *bun.DB) *Service { return &Service{db: db} }

// base returns a select on the sales table with all filters applied.
// Both Detail and Aggregate use this — there is no pre-aggregated summary
// table for this domain (unlike mmssales), so Aggregate always scans raw
// rows grouped by the requested dimensions.
func (s *Service) base(p FilterParams) *bun.SelectQuery {
	q := s.db.NewSelect().TableExpr(table)
	q = dbx.InLower(q, "regionname", p.RegionName)
	q = dbx.InLower(q, "districtname", p.DistrictName)
	q = dbx.InLower(q, "servicetype", p.ServiceType)
	q = dbx.InLower(q, "serviceclass", p.ServiceClass)
	q = dbx.InLower(q, "tariffclasscode", p.TariffClassCode)
	q = dbx.InLower(q, "customertype", p.CustomerType)
	q = dbx.InLower(q, "accounttype", p.AccountType)
	q = dbx.InLower(q, "contractstatus", p.ContractStatus)
	q = dbx.InLower(q, "billmonth", p.BillMonth)
	q = dbx.In(q, "accountnumber", p.AccountNumber)
	q = dbx.In(q, "servicepointnumber", p.ServicePointNumber)
	q = dbx.DateRange(q, "lastbilldate", p.LastBillDateFrom, p.LastBillDateTo)
	q = dbx.DateRange(q, "lastreadingdate", p.LastReadingDateFrom, p.LastReadingDateTo)

	if p.IsAMR != "" {
		val := p.IsAMR == "true" || p.IsAMR == "t" || p.IsAMR == "1"
		q = q.Where("isamr = ?", val)
	}
	if p.Search != "" {
		search := "%" + strings.ToLower(strings.TrimSpace(p.Search)) + "%"
		q = q.Where(
			"(lower(fullname) LIKE ? OR lower(servicepointnumber::text) LIKE ? OR lower(accountnumber::text) LIKE ?)",
			search, search, search,
		)
	}
	return q
}

// Whitelisted sort columns for Detail. Keys are query-param values.
var detailSortCols = map[string]string{
	"lastbilldate":        "lastbilldate",
	"lastbillconsumption": "lastbillconsumption",
	"lastbillamount":      "lastbillamount",
	"currentbalance":      "currentbalance",
	"lastpaymentdate":     "lastpaymentdate",
	"fullname":            "fullname",
}

func detailOrderExpr(sortBy, sortDir string) string {
	col, ok := detailSortCols[strings.ToLower(strings.TrimSpace(sortBy))]
	if !ok {
		col = "lastbillconsumption"
	}
	dir := "DESC"
	if strings.EqualFold(strings.TrimSpace(sortDir), "asc") {
		dir = "ASC"
	}
	// Tie-breakers keep pages stable when values collide.
	return col + " " + dir + " NULLS LAST, accountnumber ASC, servicepointnumber ASC"
}

// Detail returns a page of matching rows. The select and its count run
// concurrently inside dbx.Paginate.
func (s *Service) Detail(ctx context.Context, p FilterParams, pg httpx.Pagination, sortBy, sortDir string) (*dbx.Page[Sale], error) {
	q := s.base(p).
		ColumnExpr("*").
		ColumnExpr("'Zeus' AS data_src").
		OrderExpr(detailOrderExpr(sortBy, sortDir))
	return dbx.Paginate[Sale](ctx, q, pg)
}

// validGroupBy whitelists groupable columns.
var validGroupBy = map[string]bool{
	"regionname":      true,
	"districtname":    true,
	"contractstatus":  true,
	"servicetype":     true,
	"serviceclass":    true,
	"tariffclasscode": true,
	"customertype":    true,
	"accounttype":     true,
	"mda":             true,
}

// Aggregate returns grouped sums/counts over the raw table.
func (s *Service) Aggregate(ctx context.Context, p FilterParams, groupBy []string) (*AggregateResult, error) {
	var groups []string
	for _, g := range groupBy {
		g = strings.ToLower(strings.TrimSpace(g))
		if validGroupBy[g] {
			groups = append(groups, g)
		}
	}

	q := s.base(p).
		ColumnExpr("'Zeus' AS data_src").
		ColumnExpr("COALESCE(ROUND(SUM(lastbillamount)::numeric, 2), 0) AS sum_lastbillamount").
		ColumnExpr("COALESCE(ROUND(SUM(lastbillconsumption)::numeric, 2), 0) AS sum_lastbillconsumption").
		ColumnExpr("COALESCE(ROUND(SUM(currentbalance)::numeric, 2), 0) AS sum_currentbalance")
	for _, g := range groups {
		q = q.ColumnExpr(g).GroupExpr(g)
	}
	if len(groups) > 0 {
		q = q.OrderExpr(strings.Join(groups, ", "))
	}

	// The sums query and the distinct-customer-count query are independent —
	// run them concurrently on the pool rather than back to back. Each takes
	// several seconds on this table, so this roughly halves wall time versus
	// running them sequentially.
	var data []AggregateRow
	var counts []AggregateRow
	var scanErr, countErr error
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		scanErr = q.Scan(ctx, &data)
	}()
	go func() {
		defer wg.Done()
		counts, countErr = s.distinctCustomerCounts(ctx, p, groups)
	}()
	wg.Wait()

	if scanErr != nil {
		return nil, scanErr
	}
	if countErr != nil {
		return nil, countErr
	}
	if data == nil {
		data = []AggregateRow{}
	}

	byKey := make(map[string]int64, len(counts))
	for _, r := range counts {
		byKey[aggregateGroupKey(r, groups)] = r.CustomerCount
	}
	for i := range data {
		data[i].CustomerCount = byKey[aggregateGroupKey(data[i], groups)]
	}

	return &AggregateResult{Data: data, Total: len(data)}, nil
}

// distinctCustomerCounts computes customer_count per group via a two-level
// GROUP BY. One account can have many service points and many bill months, so
// we collapse to distinct (accountnumber, servicepointnumber) before counting.
// A separate query (vs inline COUNT DISTINCT) lets Postgres hash-aggregate
// and runs concurrently with the sums scan.
func (s *Service) distinctCustomerCounts(ctx context.Context, p FilterParams, groups []string) ([]AggregateRow, error) {
	inner := s.base(p).
		ColumnExpr("accountnumber").
		ColumnExpr("servicepointnumber").
		GroupExpr("accountnumber").
		GroupExpr("servicepointnumber")
	for _, g := range groups {
		inner = inner.ColumnExpr(g).GroupExpr(g)
	}

	q := s.db.NewSelect().
		TableExpr("(?) AS distinct_customers", inner).
		ColumnExpr("COUNT(*) AS customer_count")
	for _, g := range groups {
		q = q.ColumnExpr(g).GroupExpr(g)
	}

	var counts []AggregateRow
	if err := q.Scan(ctx, &counts); err != nil {
		return nil, err
	}
	return counts, nil
}

// aggregateGroupKey builds a composite key from whichever dimensions were
// actually grouped, so results from two separately-executed queries with the
// same GROUP BY can be matched back up row-for-row.
func aggregateGroupKey(r AggregateRow, groups []string) string {
	vals := make([]string, len(groups))
	for i, g := range groups {
		switch g {
		case "regionname":
			vals[i] = r.RegionName
		case "districtname":
			vals[i] = r.DistrictName
		case "contractstatus":
			vals[i] = r.ContractStatus
		case "servicetype":
			vals[i] = r.ServiceType
		case "serviceclass":
			vals[i] = r.ServiceClass
		case "tariffclasscode":
			vals[i] = r.TariffClassCode
		case "customertype":
			vals[i] = r.CustomerType
		case "accounttype":
			vals[i] = r.AccountType
		case "mda":
			vals[i] = r.MDA
		}
	}
	return strings.Join(vals, "\x00")
}
