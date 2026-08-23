package zeusbilling

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"bknd-3/internal/dbx"
	"bknd-3/internal/httpx"

	"github.com/uptrace/bun"
)

const table = "app.zeus_sales"

type Service struct {
	db *bun.DB
}

func NewService(db *bun.DB) *Service { return &Service{db: db} }

// billingPeriodDateBounds normalizes [from, to] into a half-open
// [start, endExclusive) range over billingperiod_date — the first-of-month
// date derived from billingyear/billingmonth and the column zeus_sales is
// meant to be hypertable-partitioned on (see
// sql/convert_zeus_sales_hypertable.sql; run it against your environment
// to actually enable partitioning, and check
// `SELECT * FROM timescaledb_information.hypertables WHERE
// hypertable_name = 'zeus_sales'` if unsure whether it's applied — this
// table shipped for a long time as a plain, non-partitioned table with the
// same column, so don't assume chunk exclusion is happening without
// checking). Filtering on this column (rather than an expression over
// billingyear/billingmonth) is what lets Postgres exclude whole months'
// chunks once hypertable partitioning is actually enabled — and even
// without it, this is still the column the covering index in
// sql/indexes_zeus_sales_map_aggregate.sql is built on, so filtering here
// stays worthwhile either way. ok is false when both bounds are zero (no
// date filter requested).
func billingPeriodDateBounds(from, to time.Time) (start, endExclusive time.Time, ok bool) {
	if from.IsZero() && to.IsZero() {
		return time.Time{}, time.Time{}, false
	}
	if from.IsZero() {
		from = to
	}
	if to.IsZero() {
		to = from
	}
	if to.Before(from) {
		from, to = to, from
	}
	start = time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	endExclusive = time.Date(to.Year(), to.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	return start, endExclusive, true
}

// base returns a select on the zeus_sales table with all filters applied.
// Aggregate always scans this directly (grouped) — no pre-aggregated
// summary table exists for this domain yet. If Aggregate turns out slow at
// scale, follow the app.mms_sales_daily_summary pattern documented in
// MIGRATION.md rather than adding more indexes here.
func (s *Service) base(p FilterParams) *bun.SelectQuery {
	q := s.db.NewSelect().TableExpr(table)
	q = dbx.InLower(q, "regionname", p.RegionName)
	q = dbx.InLower(q, "districtname", p.DistrictName)
	q = dbx.InLower(q, "tariffclasscode", p.TariffClassCode)
	q = dbx.InLower(q, "serviceclass", p.ServiceClass)
	q = dbx.InLower(q, "accounttype", p.AccountType)
	q = dbx.InLower(q, "billstatus", p.BillStatus)
	q = dbx.InLower(q, "billconsumptiontype", p.BillConsumptionType)
	q = dbx.InLower(q, "metermodeltype", p.MeterModelType)
	q = dbx.InLower(q, "servicepointstatus", p.ServicePointStatus)
	q = dbx.In(q, "accountcode", p.AccountCode)
	q = dbx.In(q, "servicepointcode", p.ServicePointCode)
	q = dbx.In(q, "metercode", p.MeterCode)
	q = dbx.DateRange(q, "lastpaymentdate", p.LastPaymentDateFrom, p.LastPaymentDateTo)
	q = dbx.DateRange(q, "createdat", p.CreatedAtFrom, p.CreatedAtTo)

	// billingyear/billingmonth are integer columns — dbx.In/InLower only
	// take []string, so these two go straight through bun.In.
	if len(p.BillingYear) > 0 {
		q = q.Where("billingyear IN (?)", bun.In(p.BillingYear))
	}
	if len(p.BillingMonth) > 0 {
		q = q.Where("billingmonth IN (?)", bun.In(p.BillingMonth))
	}
	if start, endExclusive, ok := billingPeriodDateBounds(p.BillDateFrom, p.BillDateTo); ok {
		q = q.Where("billingperiod_date >= ?", start).Where("billingperiod_date < ?", endExclusive)
	}

	if p.IsSensitive != "" {
		q = q.Where("lower(issensitive) = lower(?)", p.IsSensitive)
	}
	if p.Search != "" {
		search := "%" + strings.ToLower(strings.TrimSpace(p.Search)) + "%"
		q = q.Where(
			"(lower(customername) LIKE ? OR lower(accountcode::text) LIKE ? OR lower(servicepointcode::text) LIKE ?)",
			search, search, search,
		)
	}
	return q
}

// Whitelisted sort columns for Detail. Keys are query-param values.
var detailSortCols = map[string]string{
	"createdat":            "createdat",
	"billamount":           "billamount",
	"billconsumptionvalue": "billconsumptionvalue",
	"amountdue":            "amountdue",
	"debtamount":           "debtamount",
	"customername":         "customername",
}

func detailOrderExpr(sortBy, sortDir string) string {
	col, ok := detailSortCols[strings.ToLower(strings.TrimSpace(sortBy))]
	if !ok {
		col = "createdat"
	}
	dir := "DESC"
	if strings.EqualFold(strings.TrimSpace(sortDir), "asc") {
		dir = "ASC"
	}
	// Tie-breakers keep pages stable when values collide.
	return col + " " + dir + " NULLS LAST, accountcode ASC, servicepointcode ASC"
}

// Detail returns a page of matching rows. The select and its count run
// concurrently inside dbx.Paginate.
func (s *Service) Detail(ctx context.Context, p FilterParams, pg httpx.Pagination, sortBy, sortDir string) (*dbx.Page[Bill], error) {
	q := s.base(p).
		ColumnExpr("*").
		OrderExpr(detailOrderExpr(sortBy, sortDir))
	return dbx.Paginate[Bill](ctx, q, pg)
}

// validGroupBy whitelists groupable columns.
var validGroupBy = map[string]bool{
	"regionname":         true,
	"districtname":       true,
	"tariffclasscode":    true,
	"tariffclassname":    true,
	"serviceclass":       true,
	"accounttype":        true,
	"billstatus":         true,
	"metermodeltype":     true,
	"servicepointstatus": true,
	"billingyear":        true,
	"billingmonth":       true,
}

// Aggregate returns grouped sums/counts over the raw table. Sums and
// customer_count run as two concurrent queries rather than one — measured
// against an 18M-row table, folding customer_count into the sums query via
// COUNT(DISTINCT (accountcode, servicepointcode)) forces Postgres into a
// disk-sorted unique (it can't hash a ROW() composite the way it can plain
// grouping columns), which came out ~33% slower than keeping the count as
// its own GROUP BY-based subquery below, even accounting for the extra
// round trip. Two hash-aggregate-friendly queries running concurrently
// beats one sort-aggregate query.
func (s *Service) Aggregate(ctx context.Context, p FilterParams, groupBy []string) (*AggregateResult, error) {
	var groups []string
	for _, g := range groupBy {
		g = strings.ToLower(strings.TrimSpace(g))
		if validGroupBy[g] {
			groups = append(groups, g)
		}
	}

	q := s.base(p).
		ColumnExpr("'ZeusBilling' AS data_src").
		ColumnExpr("COALESCE(ROUND(SUM(billamount)::numeric, 2), 0) AS sum_billamount").
		ColumnExpr("COALESCE(ROUND(SUM(amountdue)::numeric, 2), 0) AS sum_amountdue").
		ColumnExpr("COALESCE(ROUND(SUM(debtamount)::numeric, 2), 0) AS sum_debtamount").
		ColumnExpr("COALESCE(ROUND(SUM(outstandingamount)::numeric, 2), 0) AS sum_outstandingamount").
		ColumnExpr("COALESCE(ROUND(SUM(billconsumptionvalue)::numeric, 2), 0) AS sum_billconsumptionvalue").
		ColumnExpr("COALESCE(ROUND(SUM(paymentsamount)::numeric, 2), 0) AS sum_paymentsamount")
	for _, g := range groups {
		q = q.ColumnExpr(g).GroupExpr(g)
	}
	if len(groups) > 0 {
		q = q.OrderExpr(strings.Join(groups, ", "))
	}

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
// GROUP BY (hash-aggregate friendly) rather than COUNT(DISTINCT (row)),
// which forces a sort — see the comment on Aggregate. One account can have
// many service points and many bills across billing periods, so we collapse
// to distinct (accountcode, servicepointcode) before counting.
func (s *Service) distinctCustomerCounts(ctx context.Context, p FilterParams, groups []string) ([]AggregateRow, error) {
	inner := s.base(p).
		ColumnExpr("accountcode").
		ColumnExpr("servicepointcode").
		GroupExpr("accountcode").
		GroupExpr("servicepointcode")
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
// actually grouped, so results from the two separately-executed queries
// with the same GROUP BY can be matched back up row-for-row.
func aggregateGroupKey(r AggregateRow, groups []string) string {
	vals := make([]string, len(groups))
	for i, g := range groups {
		switch g {
		case "regionname":
			vals[i] = r.RegionName
		case "districtname":
			vals[i] = r.DistrictName
		case "tariffclasscode":
			vals[i] = r.TariffClassCode
		case "tariffclassname":
			vals[i] = r.TariffClassName
		case "serviceclass":
			vals[i] = r.ServiceClass
		case "accounttype":
			vals[i] = r.AccountType
		case "billstatus":
			vals[i] = r.BillStatus
		case "metermodeltype":
			vals[i] = r.MeterModelType
		case "servicepointstatus":
			vals[i] = r.ServicePointStatus
		case "billingyear":
			vals[i] = strconv.Itoa(r.BillingYear)
		case "billingmonth":
			vals[i] = strconv.Itoa(r.BillingMonth)
		}
	}
	return strings.Join(vals, "\x00")
}
