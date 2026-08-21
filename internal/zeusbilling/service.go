package zeusbilling

import (
	"context"
	"strings"
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
// date derived from billingyear/billingmonth and, critically, the column
// zeus_sales is hypertable-partitioned on. Filtering on this column (rather
// than an expression over billingyear/billingmonth) is what lets Postgres
// exclude whole months' chunks instead of opening every chunk to check an
// index. ok is false when both bounds are zero (no date filter requested).
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

// Aggregate returns grouped sums/counts over the raw table in a single pass.
// customer_count is COUNT(DISTINCT (accountcode, servicepointcode)) computed
// inline — one account can have many service points and many bills across
// billing periods, so a plain COUNT(*) would overcount. This used to run as
// a second query (sums here, counts via a nested GROUP BY subquery there)
// executed concurrently; folding it into one query halves the DB round
// trips this endpoint makes, which matters since callers commonly fire
// several Aggregate() calls (one per breakdown dimension) on page load.
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
		ColumnExpr("COUNT(DISTINCT (accountcode, servicepointcode)) AS customer_count").
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
	if err := q.Scan(ctx, &data); err != nil {
		return nil, err
	}
	if data == nil {
		data = []AggregateRow{}
	}

	return &AggregateResult{Data: data, Total: len(data)}, nil
}
