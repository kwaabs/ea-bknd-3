package mmssales

import (
	"context"
	"strings"
	"sync"
	"time"

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
	q = dbx.InLowerOrBlank(q, "region", p.Region)
	q = dbx.InLowerOrBlank(q, "district", p.District)
	q = dbx.InLower(q, "contract_type", p.ContractType)
	q = dbx.InLower(q, "tariff", p.Tariff)
	q = dbx.InLower(q, "manufacturer", p.Manufacturer)
	q = dbx.InLower(q, "model", p.Model)
	return q
}

// base returns a select on the RAW sales table with all filters applied.
// Detail always uses this; Aggregate uses it only as the row-level fallback.
//
// Excludes is_duplicate_reading rows (sql/mms_customer_sales_dedup.sql) —
// the ingestion process periodically re-writes the same
// (meter_number, month, reading values) under a new date_time, and every
// query here sums/lists across whatever days fall in the requested range,
// so an unflagged duplicate silently inflates any range covering more than
// one of its re-sync dates. This exclusion is unconditional (not an opt-in
// flag like Zeus's excludeMmsDuplicates) — there is no caller that should
// ever want re-sync duplicates included.
func (s *Service) base(p FilterParams) *bun.SelectQuery {
	q := s.db.NewSelect().TableExpr(table).Where("NOT is_duplicate_reading")
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
// large the raw table grows. No separate is_duplicate_reading exclusion
// needed here (unlike base()) — resync_mms_sales_summary
// (sql/mms_customer_sales_dedup.sql) already excludes flagged rows when
// building the summary, so it never contains their inflated sums.
func (s *Service) summaryBase(p FilterParams) *bun.SelectQuery {
	q := s.db.NewSelect().TableExpr(summaryTable)
	q = dimensionFilters(q, p)
	q = dbx.DateRange(q, "day", p.DateTimeFrom, p.DateTimeTo)
	return q
}

// detailSortColumn maps a whitelisted sortBy key (matching the frontend
// table's own sort fields) to the column Detail's ORDER BY uses. Anything
// not in the whitelist returns ok=false so the caller falls back to the
// stable default order — never interpolate caller input into SQL directly.
func detailSortColumn(sortBy string) (column string, ok bool) {
	switch sortBy {
	case "customer_name":
		return "customer_name", true
	case "sts_last_month_kwh_read":
		return "sts_last_month_kwh_read", true
	case "sts_last_month_credit_read":
		return "sts_last_month_credit_read", true
	case "sts_credit_balance_remaining":
		return "sts_credit_balance_remaining", true
	case "date_time":
		return "date_time", true
	default:
		return "", false
	}
}

// Detail returns a page of matching raw rows. The select and its count run
// concurrently inside dbx.Paginate.
//
// sortBy/sortOrder drive the ORDER BY — without this, a client fetching a
// large limit once and sorting/paginating the fetched slice client-side
// silently loses everything past this endpoint's own per-request cap, the
// same bug already fixed on botconsumption's equivalent Detail (see that
// package's comment for the full story). Real pagination needs a real,
// server-side ORDER BY so "sorted by X, page N" is well-defined across the
// whole table, not just whatever page happened to be fetched.
func (s *Service) Detail(ctx context.Context, p FilterParams, pg httpx.Pagination, sortBy, sortOrder string) (*dbx.Page[Sale], error) {
	q := s.base(p).
		ColumnExpr("*").
		ColumnExpr("'MMS Sales' AS data_src")

	if col, ok := detailSortColumn(sortBy); ok {
		dir := "ASC"
		if strings.ToLower(sortOrder) == "desc" {
			dir = "DESC"
		}
		// Tie-break on customer_name/account_number so rows with an equal
		// sort value still land in a consistent order across pages.
		q = q.OrderExpr(col + " " + dir + ", customer_name, account_number")
	} else {
		q = q.OrderExpr("region, district, customer_name, account_number") // stable default sort
	}
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

	var groups []string
	for _, g := range groupBy {
		g = strings.ToLower(strings.TrimSpace(g))
		if validGroupBy[g] {
			groups = append(groups, g)
		}
	}

	if rowLevel {
		// Raw-table fallback: aggregate raw rows directly, everything in one
		// query. Distinct on (account, meter) — the same customer can have a
		// row per day in range, so COUNT(*) would count them once per day.
		q := s.base(p).
			ColumnExpr("'MMS Sales' AS data_src").
			ColumnExpr("COUNT(DISTINCT (account_number, meter_number)) AS customer_count").
			ColumnExpr("COALESCE(ROUND(SUM(sts_credit_balance_remaining)::numeric, 2), 0) AS sum_credit_balance_remaining").
			ColumnExpr("COALESCE(ROUND(SUM(sts_last_month_credit_read)::numeric, 2), 0) AS sum_last_month_credit_read").
			ColumnExpr("COALESCE(ROUND(SUM(sts_last_month_kwh_read)::numeric, 2), 0) AS sum_last_month_kwh_read")
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

	// Fast path: re-aggregate the daily summary for the flow sums (correct
	// and cheap — SUM over a few thousand summary rows). The summary's
	// customer_count is per calendar day though, so SUM-ing it across a
	// multi-day range would count the same customer once per day they
	// appear — a true distinct count needs distinctCustomerCountsFast's
	// own summary table (app.mms_customer_activity) instead. That query
	// and this one run concurrently rather than back to back, then get
	// merged by group key below.
	q := s.summaryBase(p).
		ColumnExpr("'MMS Sales' AS data_src").
		ColumnExpr("COALESCE(ROUND(SUM(sum_credit_balance_remaining)::numeric, 2), 0) AS sum_credit_balance_remaining").
		ColumnExpr("COALESCE(ROUND(SUM(sum_last_month_credit_read)::numeric, 2), 0) AS sum_last_month_credit_read").
		ColumnExpr("COALESCE(ROUND(SUM(sum_last_month_kwh_read)::numeric, 2), 0) AS sum_last_month_kwh_read")
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
		counts, countErr = s.distinctCustomerCountsFast(ctx, p, groups)
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

// monthsInRange returns every first-of-month date the [from, to] range
// touches (inclusive of both ends' months) — the exact set
// distinctCustomerCountsFast checks app.mms_customer_activity.active_months
// against. Empty (from/to zero) means no date filter should be applied at
// all, matching dbx.DateRange's convention elsewhere in this codebase —
// distinguished from "no months match" by the caller checking len() before
// deciding whether to add a WHERE clause at all.
func monthsInRange(from, to time.Time) []string {
	if from.IsZero() || to.IsZero() {
		return nil
	}
	var months []string
	m := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(to.Year(), to.Month(), 1, 0, 0, 0, 0, time.UTC)
	for !m.After(end) {
		months = append(months, m.Format("2006-01-02"))
		m = m.AddDate(0, 1, 0)
	}
	return months
}

// distinctCustomerCountsFast computes a true COUNT(DISTINCT account_number,
// meter_number) per group from app.mms_customer_activity — a summary table
// mirroring app.mms_sales_daily_summary's role, but keyed one row per
// customer per dimension combination (not per day), carrying an
// active_months array — so a date-range query is a fast array-overlap
// check instead of a live scan+sort over millions of raw rows. See
// sql/summary_mms_customer_activity.sql for the full design and the
// month-granularity trade-off it accepts. Replaces a raw-table
// COUNT(DISTINCT ...) that measured ~65s for an 8-month range (EXPLAIN
// ANALYZE, confirmed even with a covering index the planner correctly
// declined to use).
func (s *Service) distinctCustomerCountsFast(ctx context.Context, p FilterParams, groups []string) ([]AggregateRow, error) {
	q := s.db.NewSelect().
		TableExpr("app.mms_customer_activity").
		ColumnExpr("COUNT(DISTINCT (account_number, meter_number)) AS customer_count")
	q = dimensionFilters(q, p)

	months := monthsInRange(p.DateTimeFrom, p.DateTimeTo)
	if len(months) > 0 {
		placeholders := make([]string, len(months))
		args := make([]interface{}, len(months))
		for i, m := range months {
			placeholders[i] = "?"
			args[i] = m
		}
		q = q.Where("active_months && ARRAY["+strings.Join(placeholders, ",")+"]::date[]", args...)
	}

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
