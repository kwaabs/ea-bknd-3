package botconsumption

import (
	"context"
	"strconv"
	"strings"
	"time"

	"bknd-3/internal/dbx"
	"bknd-3/internal/httpx"

	"github.com/uptrace/bun"
)

const table = "app.bot_consumption"

// monthByName maps a lowercase month name OR its standard 3-letter
// abbreviation to its time.Month, for parsing billmonth labels
// ("june-2026", "JAN-2026"). Deliberately independent of Go's own
// "January"/"Jan" layout parsing, which requires exact capitalization —
// this matches any case the source data happens to use. Both forms are
// listed because different load batches have used different ones for
// different months (confirmed live: "JAN-2026" alongside "june-2026" in
// the same table) — a label using either must resolve the same way, or a
// date-range query silently loses whichever months happened to be
// abbreviated that time.
var monthByName = map[string]time.Month{
	"january": time.January, "jan": time.January,
	"february": time.February, "feb": time.February,
	"march": time.March, "mar": time.March,
	"april": time.April, "apr": time.April,
	"may":  time.May,
	"june": time.June, "jun": time.June,
	"july": time.July, "jul": time.July,
	"august": time.August, "aug": time.August,
	"september": time.September, "sep": time.September, "sept": time.September,
	"october": time.October, "oct": time.October,
	"november": time.November, "nov": time.November,
	"december": time.December, "dec": time.December,
}

// parseBillMonth parses a "monthname-year" label into the first of that
// month (UTC, day is arbitrary — only year/month are ever read back out via
// monthKey). Returns ok=false for anything that doesn't match this shape
// rather than an error: a single malformed label in the table must never
// fail every date-range query, just be silently excluded from the match.
func parseBillMonth(raw string) (t time.Time, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(raw), "-", 2)
	if len(parts) != 2 {
		return time.Time{}, false
	}
	month, found := monthByName[strings.ToLower(strings.TrimSpace(parts[0]))]
	if !found {
		return time.Time{}, false
	}
	year, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(year, month, 1, 0, 0, 0, 0, time.UTC), true
}

// monthKey reduces a time.Time to a single comparable int (year*12+month),
// so two months can be range-compared regardless of day/time-of-day.
func monthKey(t time.Time) int {
	y, m, _ := t.Date()
	return y*12 + int(m)
}

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
//
// billmonth gets the same trim() treatment for the same reason, just
// caused by inconsistent source data instead of a fixed-width column
// type: different load batches have left stray leading/trailing
// whitespace on the label (confirmed live — "JAN-2026 " next to
// "june-2026"). Trimming both the column here and the values
// resolveDateRangeToBillMonths puts in p.BillMonth means neither side has
// to byte-exactly match whatever whitespace a given batch happened to
// leave.
func (s *Service) base(p FilterParams) *bun.SelectQuery {
	q := s.db.NewSelect().TableExpr(table)
	q = dbx.InLower(q, "trim(region)", p.Region)
	q = dbx.InLower(q, "district", p.District)
	q = dbx.InLower(q, "tarrif", p.Tariff)
	q = dbx.InLower(q, "trim(billmonth)", p.BillMonth)
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

// resolveDateRangeToBillMonths translates p.DateFrom/DateTo into a
// BillMonth filter, since billmonth is the only time dimension this table
// has and it's a free-text "monthname-year" label, not a real date column
// — matching it against a date range means parsing every distinct label
// and comparing at month granularity, not building a SQL date-range WHERE.
// No-op (returns p unchanged, noMatch=false) if BillMonth was already given
// explicitly — that takes precedence — or if both DateFrom and DateTo are
// zero.
//
// Fetches the table's distinct billmonth values (expected to stay a small
// set — one label per calendar month the source has ever sent) and matches
// each in Go via parseBillMonth rather than parsing month names in SQL, so
// one malformed label can't fail the whole query.
//
// noMatch=true means a date range WAS given but zero billmonth labels
// overlap it — the caller must treat this as "return nothing" and skip
// querying base() entirely, NOT fall through with p.BillMonth left empty.
// dbx.InLower treats an empty slice as "no filter requested" (correct for
// the normal case), so setting p.BillMonth to []string{} here would make
// the date filter silently vanish and match every row instead of none —
// exactly the "a Jan-Apr 2026 filter returned everything" bug this
// exists to prevent.
func (s *Service) resolveDateRangeToBillMonths(ctx context.Context, p FilterParams) (params FilterParams, noMatch bool, err error) {
	if len(p.BillMonth) > 0 || (p.DateFrom.IsZero() && p.DateTo.IsZero()) {
		return p, false, nil
	}

	var raw []string
	if err := s.db.NewSelect().
		TableExpr(table).
		ColumnExpr("DISTINCT billmonth").
		Scan(ctx, &raw); err != nil {
		return p, false, err
	}

	// Trimmed and deduped: base() now compares against trim(billmonth), so
	// the values put here must be trimmed the same way, or a comparison
	// against the padded raw value would never match the trimmed column
	// expression. Two distinct raw labels that only differ by whitespace
	// (e.g. "JAN-2026" and "JAN-2026 ", both present as separate DISTINCT
	// rows) trim down to the same string — seenTrimmed skips the repeat
	// rather than sending a harmless but redundant duplicate into the IN
	// list.
	matched := make([]string, 0, len(raw))
	seenTrimmed := make(map[string]bool, len(raw))
	for _, r := range raw {
		t, ok := parseBillMonth(r)
		if !ok {
			continue
		}
		k := monthKey(t)
		if !p.DateFrom.IsZero() && k < monthKey(p.DateFrom) {
			continue
		}
		if !p.DateTo.IsZero() && k > monthKey(p.DateTo) {
			continue
		}
		trimmed := strings.TrimSpace(r)
		if seenTrimmed[trimmed] {
			continue
		}
		seenTrimmed[trimmed] = true
		matched = append(matched, trimmed)
	}

	if len(matched) == 0 {
		return p, true, nil
	}

	p.BillMonth = matched
	return p, false, nil
}

// Detail returns a page of matching rows. The select and its count run
// concurrently inside dbx.Paginate.
func (s *Service) Detail(ctx context.Context, p FilterParams, pg httpx.Pagination) (*dbx.Page[Reading], error) {
	p, noMatch, err := s.resolveDateRangeToBillMonths(ctx, p)
	if err != nil {
		return nil, err
	}
	if noMatch {
		return &dbx.Page[Reading]{
			Data: []Reading{}, Total: 0, Page: pg.Page, Limit: pg.Limit, TotalPages: pg.TotalPages(0),
		}, nil
	}
	q := s.base(p).
		ColumnExpr("customer_name, meternumber, geo_code, kwh, tarrif, billmonth, district").
		ColumnExpr("trim(region) AS region").
		OrderExpr("region, district, customer_name, meternumber") // stable sort
	return dbx.Paginate[Reading](ctx, q, pg)
}

// groupExpr maps a whitelisted groupBy key to its (select, group-by) SQL
// pair — region and billmonth need trim() (see base()'s comment on both),
// tariff needs to rename off the source table's "tarrif" typo, district
// is the one plain column.
func groupExpr(g string) (selectExpr, groupByExpr string, ok bool) {
	switch g {
	case "region":
		return "trim(region) AS region", "trim(region)", true
	case "district":
		return "district", "district", true
	case "tariff":
		return "tarrif AS tariff", "tarrif", true
	case "billmonth":
		return "trim(billmonth) AS billmonth", "trim(billmonth)", true
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
	p, noMatch, err := s.resolveDateRangeToBillMonths(ctx, p)
	if err != nil {
		return nil, err
	}
	if noMatch {
		return &AggregateResult{Data: []AggregateRow{}, Total: 0}, nil
	}
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
