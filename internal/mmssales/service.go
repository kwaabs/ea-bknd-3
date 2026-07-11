package mmssales

import (
	"context"
	"strings"

	"bknd-3/internal/dbx"
	"bknd-3/internal/httpx"

	"github.com/uptrace/bun"
)

const (
	table        = "app.mms_customer_sales"
	summaryTable = "app.mms_sales_daily_summary"
)

type Service struct {
	db *bun.DB
}

func NewService(db *bun.DB) *Service { return &Service{db: db} }

// hasRowLevelFilters reports whether the params include filters that only
// exist on raw rows (not dimensions of the daily summary). When true,
// aggregates must fall back to the raw table.
func (p FilterParams) hasRowLevelFilters() bool {
	return p.Search != "" || len(p.AccountNumber) > 0 || len(p.MeterNumber) > 0
}

// dimensionFilters applies the filters that exist on BOTH the raw table and
// the summary table (same column names by design).
func dimensionFilters(q *bun.SelectQuery, p FilterParams) *bun.SelectQuery {
	q = dbx.InLower(q, "region", p.Region)
	q = dbx.InLower(q, "district", p.District)
	q = dbx.InLower(q, "contract_type", p.ContractType)
	q = dbx.InLower(q, "tariff", p.Tariff)
	q = dbx.InLower(q, "manufacturer", p.Manufacturer)
	q = dbx.InLower(q, "model", p.Model)
	return q
}

// base returns a select on the RAW sales table with all filters applied.
// Detail always uses this; Aggregate uses it only as the row-level fallback.
func (s *Service) base(p FilterParams) *bun.SelectQuery {
	q := s.db.NewSelect().TableExpr(table)
	q = dimensionFilters(q, p)
	q = dbx.In(q, "account_number", p.AccountNumber)
	q = dbx.In(q, "meter_number", p.MeterNumber)
	q = dbx.DateRange(q, "date_time", p.DateTimeFrom, p.DateTimeTo)

	if p.Search != "" {
		search := "%" + strings.ToLower(strings.TrimSpace(p.Search)) + "%"
		q = q.Where(
			"(lower(customer_name) LIKE ? OR lower(account_number::text) LIKE ? OR lower(meter_number::text) LIKE ? OR lower(meter_serial_number::text) LIKE ?)",
			search, search, search, search,
		)
	}
	return q
}

// summaryBase returns a select on the pre-aggregated daily summary with the
// dimension and date filters applied. The summary is small (one row per
// day×dimension combo), so queries here are milliseconds regardless of how
// large the raw table grows.
func (s *Service) summaryBase(p FilterParams) *bun.SelectQuery {
	q := s.db.NewSelect().TableExpr(summaryTable)
	q = dimensionFilters(q, p)
	q = dbx.DateRange(q, "day", p.DateTimeFrom, p.DateTimeTo)
	return q
}

// Detail returns a page of matching raw rows. The select and its count run
// concurrently inside dbx.Paginate.
func (s *Service) Detail(ctx context.Context, p FilterParams, pg httpx.Pagination) (*dbx.Page[Sale], error) {
	q := s.base(p).
		ColumnExpr("*").
		ColumnExpr("'MMS Sales' AS data_src").
		OrderExpr("region, district, customer_name, account_number") // stable sort
	return dbx.Paginate[Sale](ctx, q, pg)
}

// validGroupBy whitelists groupable columns. These are dimensions on both
// the raw and summary tables, so grouping works identically on either path.
var validGroupBy = map[string]bool{
	"region":        true,
	"district":      true,
	"contract_type": true,
	"tariff":        true,
	"manufacturer":  true,
	"model":         true,
}

// Aggregate returns grouped sums/counts.
//
// Routing: if only dimension/date filters are present (the common case), it
// rolls up the pre-aggregated summary table — SUM over a few thousand rows.
// If row-level filters (search / account / meter number) are present, it
// falls back to aggregating the raw table, which those filters require.
// Both paths produce identical shapes and identical numbers for the filters
// they share.
func (s *Service) Aggregate(ctx context.Context, p FilterParams, groupBy []string) (*AggregateResult, error) {
	rowLevel := p.hasRowLevelFilters()

	var q *bun.SelectQuery
	if rowLevel {
		// Raw-table fallback: aggregate raw rows directly. Distinct on
		// (account, meter) — the same customer can have a row per day in
		// range, so COUNT(*) would count them once per day.
		q = s.base(p).
			ColumnExpr("'MMS Sales' AS data_src").
			ColumnExpr("COUNT(DISTINCT (account_number, meter_number)) AS customer_count").
			ColumnExpr("COALESCE(ROUND(SUM(sts_credit_balance_remaining)::numeric, 2), 0) AS sum_credit_balance_remaining").
			ColumnExpr("COALESCE(ROUND(SUM(sts_last_month_credit_read)::numeric, 2), 0) AS sum_last_month_credit_read").
			ColumnExpr("COALESCE(ROUND(SUM(sts_last_month_kwh_read)::numeric, 2), 0) AS sum_last_month_kwh_read")
	} else {
		// Fast path: re-aggregate the daily summary for the flow sums
		// (correct and cheap — SUM over a few thousand summary rows). The
		// summary's customer_count is per calendar day though, so SUM-ing it
		// across a multi-day range would count the same customer once per
		// day they appear. Left as a placeholder here and backfilled from a
		// raw-table distinct count below.
		q = s.summaryBase(p).
			ColumnExpr("'MMS Sales' AS data_src").
			ColumnExpr("0 AS customer_count").
			ColumnExpr("COALESCE(ROUND(SUM(sum_credit_balance_remaining)::numeric, 2), 0) AS sum_credit_balance_remaining").
			ColumnExpr("COALESCE(ROUND(SUM(sum_last_month_credit_read)::numeric, 2), 0) AS sum_last_month_credit_read").
			ColumnExpr("COALESCE(ROUND(SUM(sum_last_month_kwh_read)::numeric, 2), 0) AS sum_last_month_kwh_read")
	}

	var groups []string
	for _, g := range groupBy {
		g = strings.ToLower(strings.TrimSpace(g))
		if !validGroupBy[g] {
			continue
		}
		groups = append(groups, g)
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

	if !rowLevel && len(data) > 0 {
		if err := s.backfillDistinctCustomerCounts(ctx, p, groups, data); err != nil {
			return nil, err
		}
	}

	return &AggregateResult{Data: data, Total: len(data)}, nil
}

// backfillDistinctCustomerCounts replaces the placeholder customer_count on
// each row with a true COUNT(DISTINCT account_number, meter_number) from the
// raw table, grouped the same way as the summary query. Only needed on the
// summary fast-path — the pre-aggregated table can't answer this correctly
// once the date range spans more than one day.
func (s *Service) backfillDistinctCustomerCounts(ctx context.Context, p FilterParams, groups []string, data []AggregateRow) error {
	q := s.base(p).ColumnExpr("COUNT(DISTINCT (account_number, meter_number)) AS customer_count")
	for _, g := range groups {
		q = q.ColumnExpr(g).GroupExpr(g)
	}

	var counts []AggregateRow
	if err := q.Scan(ctx, &counts); err != nil {
		return err
	}

	byKey := make(map[string]int64, len(counts))
	for _, r := range counts {
		byKey[aggregateGroupKey(r, groups)] = r.CustomerCount
	}
	for i := range data {
		data[i].CustomerCount = byKey[aggregateGroupKey(data[i], groups)]
	}
	return nil
}

// aggregateGroupKey builds a composite key from whichever dimensions were
// actually grouped, so results from two separately-executed queries with the
// same GROUP BY can be matched back up row-for-row.
func aggregateGroupKey(r AggregateRow, groups []string) string {
	vals := make([]string, len(groups))
	for i, g := range groups {
		switch g {
		case "region":
			vals[i] = r.Region
		case "district":
			vals[i] = r.District
		case "contract_type":
			vals[i] = r.ContractType
		case "tariff":
			vals[i] = r.Tariff
		case "manufacturer":
			vals[i] = r.Manufacturer
		case "model":
			vals[i] = r.Model
		}
	}
	return strings.Join(vals, "\x00")
}
