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

const (
	table              = "app.zeus_sales"
	periodSummaryTable = "app.zeus_sales_period_summary"
	rosterTable        = "app.zeus_customer_roster"
)

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

// normalizeZeusRegionNames appends " Region" to each region name that
// doesn't already end with it. zeus_sales.regionname always stores the
// administrative-unit-qualified form ("Tema Region", "Accra East Region"),
// but every other source of a region value in this system (the meter
// table, the region-name query param callers actually send, the frontend's
// region-select options) uses the short form ("Tema", "Accra East") — so an
// unqualified filter value would silently match zero rows. Confirmed
// against live data: region=Tema returns nothing; region=Tema+Region
// returns real rows. Case-insensitive check so an already-correct or
// differently-cased "region" suffix isn't doubled up; the appended suffix
// itself doesn't need to match the stored casing since every filter that
// reads RegionName compares via lower(regionname) anyway.
func normalizeZeusRegionNames(regions []string) []string {
	if len(regions) == 0 {
		return regions
	}
	out := make([]string, len(regions))
	for i, r := range regions {
		trimmed := strings.TrimSpace(r)
		if trimmed != "" && !strings.HasSuffix(strings.ToLower(trimmed), " region") {
			trimmed += " Region"
		}
		out[i] = trimmed
	}
	return out
}

// base returns a select on the raw zeus_sales table with all filters
// applied. Detail always uses this; Aggregate uses it only as the
// row-level fallback (search / account / service point / meter code /
// last-payment or created-at range / exact billingyear-or-month) — the
// common case reads from the pre-aggregated period summary and customer
// roster instead (sql/summary_zeus_sales.sql, MIGRATION.md "Aggregate fast
// path for zeus_sales").
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

// hasRowLevelFilters reports whether p includes filters that only exist on
// raw rows — not dimensions of either the period summary or the customer
// roster (sql/summary_zeus_sales.sql). When true, Aggregate must fall back
// to the raw table for both sums and customer_count, same as mmssales'
// equivalent check.
func (p FilterParams) hasRowLevelFilters() bool {
	return p.Search != "" ||
		p.IsSensitive != "" ||
		len(p.AccountCode) > 0 ||
		len(p.ServicePointCode) > 0 ||
		len(p.MeterCode) > 0 ||
		len(p.BillConsumptionType) > 0 ||
		len(p.BillingYear) > 0 ||
		len(p.BillingMonth) > 0 ||
		!p.LastPaymentDateFrom.IsZero() ||
		!p.LastPaymentDateTo.IsZero() ||
		!p.CreatedAtFrom.IsZero() ||
		!p.CreatedAtTo.IsZero()
}

// dimensionFilters applies the filters that exist as plain columns on the
// raw table, the period summary, AND the customer roster alike (same
// column names by design).
func dimensionFilters(q *bun.SelectQuery, p FilterParams) *bun.SelectQuery {
	q = dbx.InLower(q, "regionname", p.RegionName)
	q = dbx.InLower(q, "districtname", p.DistrictName)
	q = dbx.InLower(q, "tariffclasscode", p.TariffClassCode)
	q = dbx.InLower(q, "serviceclass", p.ServiceClass)
	q = dbx.InLower(q, "accounttype", p.AccountType)
	q = dbx.InLower(q, "billstatus", p.BillStatus)
	q = dbx.InLower(q, "metermodeltype", p.MeterModelType)
	q = dbx.InLower(q, "servicepointstatus", p.ServicePointStatus)
	return q
}

// summaryBase returns a select on the pre-aggregated period summary
// (sql/summary_zeus_sales.sql) with the dimension and date filters applied.
// Only valid when !p.hasRowLevelFilters() — the summary has no columns for
// those.
func (s *Service) summaryBase(p FilterParams) *bun.SelectQuery {
	q := s.db.NewSelect().TableExpr(periodSummaryTable)
	q = dimensionFilters(q, p)
	if start, endExclusive, ok := billingPeriodDateBounds(p.BillDateFrom, p.BillDateTo); ok {
		q = q.Where("billingperiod_date >= ?", start).Where("billingperiod_date < ?", endExclusive)
	}
	return q
}

// rosterBase returns a select on the customer roster (sql/summary_zeus_sales.sql)
// with the dimension filters applied. Unlike summaryBase, the date range
// is an OVERLAP check against each customer's [first, last] active window,
// not a plain column range — a customer counts if they were active at any
// point in the requested range, not only if their single roster row
// happens to fall inside it (there is only one row per customer, not one
// per period).
func (s *Service) rosterBase(p FilterParams) *bun.SelectQuery {
	q := s.db.NewSelect().TableExpr(rosterTable)
	q = dimensionFilters(q, p)
	if start, endExclusive, ok := billingPeriodDateBounds(p.BillDateFrom, p.BillDateTo); ok {
		q = q.Where("last_billingperiod_date >= ?", start).
			Where("first_billingperiod_date < ?", endExclusive)
	}
	return q
}

// summaryGroupExprs returns the (select, group-by) SQL expression pair for
// a validated groupBy dimension against the period summary table, which
// stores billingperiod_date but not billingyear/billingmonth as separate
// columns — those two are derived via EXTRACT() instead. Every other
// dimension is a plain column shared by name with the raw table.
func summaryGroupExprs(g string) (selectExpr, groupExpr string) {
	switch g {
	case "billingyear":
		return "EXTRACT(YEAR FROM billingperiod_date)::int AS billingyear", "EXTRACT(YEAR FROM billingperiod_date)"
	case "billingmonth":
		return "EXTRACT(MONTH FROM billingperiod_date)::int AS billingmonth", "EXTRACT(MONTH FROM billingperiod_date)"
	default:
		return g, g
	}
}

// groupsIncludePeriod reports whether groups contains billingyear or
// billingmonth. The customer roster stores one [first, last] active window
// per customer rather than per-period membership, so it cannot correctly
// answer "distinct customers in period X" — that's a genuinely
// period-scoped question the raw table's per-row grouping has to answer
// instead. Sums have no such restriction (the period summary has a real
// billingperiod_date on every row), only customer_count does.
func groupsIncludePeriod(groups []string) bool {
	for _, g := range groups {
		if g == "billingyear" || g == "billingmonth" {
			return true
		}
	}
	return false
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

// Aggregate returns grouped sums/counts. Sums and customer_count run as two
// concurrent queries rather than one — measured against an 18M-row table,
// folding customer_count into the sums query via
// COUNT(DISTINCT (accountcode, servicepointcode)) forces Postgres into a
// disk-sorted unique (it can't hash a ROW() composite the way it can plain
// grouping columns), which came out ~33% slower than keeping the count as
// its own GROUP BY-based subquery, even accounting for the extra round
// trip. Two hash-aggregate-friendly queries running concurrently beats one
// sort-aggregate query.
//
// Routing (see sql/summary_zeus_sales.sql for the two tables this reads):
//   - Sums: raw table only when a row-level filter (search / account code /
//     service point code / meter code / last-payment or created-at range /
//     exact billingyear-or-month) is present — otherwise the period
//     summary, which is small regardless of how large zeus_sales grows.
//   - customer_count: raw table when a row-level filter is present, OR when
//     grouping by billingyear/billingmonth (the customer roster stores one
//     [first, last] window per customer, not per period, so it can't
//     answer a period-scoped distinct-count correctly) — otherwise the
//     roster, which is sized to the customer count rather than the bill
//     count.
//
// All four combinations of (sums path) × (count path) are possible and
// produce identical numbers to the all-raw path for the filters/groupings
// they share — this is purely a performance routing, not a behavior change.
//
// excludeMmsPrepaidDuplicates applies the confirmed MMS-takes-precedence
// business rule (sql/zeus_prepaid_mms_precedence.sql): a Zeus Prepaid row
// whose metercode matches a known MMS meter_number is excluded from sums
// and customer_count. Only meaningful for meterModelType=Prepaid queries —
// harmless no-op otherwise, since MMS never matches Postpaid/AMR rows.
// SCOPE: only pass true from the two views that BLEND Zeus Prepaid and MMS
// into one combined figure (Prepaid hub page, customer-sales-overview's
// Combined chart/table) — every other caller of this endpoint wants Zeus's
// own complete book of record and must keep passing false.
func (s *Service) Aggregate(ctx context.Context, p FilterParams, groupBy []string, excludeMmsPrepaidDuplicates bool) (*AggregateResult, error) {
	var groups []string
	for _, g := range groupBy {
		g = strings.ToLower(strings.TrimSpace(g))
		if validGroupBy[g] {
			groups = append(groups, g)
		}
	}

	rowLevel := p.hasRowLevelFilters()

	var data []AggregateRow
	var counts []AggregateRow
	var scanErr, countErr error
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		if rowLevel {
			data, scanErr = s.rawSums(ctx, p, groups, excludeMmsPrepaidDuplicates)
		} else {
			data, scanErr = s.summarySums(ctx, p, groups, excludeMmsPrepaidDuplicates)
		}
	}()
	go func() {
		defer wg.Done()
		if rowLevel || groupsIncludePeriod(groups) {
			counts, countErr = s.distinctCustomerCounts(ctx, p, groups, excludeMmsPrepaidDuplicates)
		} else {
			counts, countErr = s.distinctCustomerCountsFromRoster(ctx, p, groups, excludeMmsPrepaidDuplicates)
		}
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

// mmsDuplicateExistsExpr is the raw-row predicate matching
// sql/zeus_prepaid_mms_precedence.sql's resync-time exclusion: a Prepaid
// row whose metercode is a known MMS meter_number. Used only on the raw
// fallback paths (rawSums, distinctCustomerCounts) — the summary/roster
// fast paths read pre-computed exclusion columns instead, since this EXISTS
// check is too expensive to run per request at raw-table scale.
const mmsDuplicateExistsExpr = "lower(metermodeltype) = 'prepaid' AND EXISTS " +
	"(SELECT 1 FROM app.mms_customer_sales m WHERE upper(trim(m.meter_number)) = upper(trim(zeus_sales.metercode)))"

// rawSums aggregates the raw table directly — the fallback path, used
// whenever a row-level filter is present.
func (s *Service) rawSums(ctx context.Context, p FilterParams, groups []string, excludeMmsDup bool) ([]AggregateRow, error) {
	q := s.base(p)
	if excludeMmsDup {
		q = q.Where("NOT (" + mmsDuplicateExistsExpr + ")")
	}
	q = q.
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
	if err := q.Scan(ctx, &data); err != nil {
		return nil, err
	}
	return data, nil
}

// summarySums re-aggregates the pre-aggregated period summary — SUM over
// at most a few hundred thousand summary rows (one per month × dimension
// combo actually observed in the data) instead of the full raw table.
// Numerically identical to rawSums for any filter/groupBy combination that
// doesn't require a row-level filter, since the summary's SUM columns are
// themselves just SUMs of the raw columns per (month, dimensions).
// excludeMmsDup switches to the _excl_mms_dup sibling columns
// (sql/zeus_prepaid_mms_precedence.sql), pre-computed at resync time.
func (s *Service) summarySums(ctx context.Context, p FilterParams, groups []string, excludeMmsDup bool) ([]AggregateRow, error) {
	sumCol := func(base string) string {
		if excludeMmsDup {
			return base + "_excl_mms_dup"
		}
		return base
	}
	q := s.summaryBase(p).
		ColumnExpr("'ZeusBilling' AS data_src").
		ColumnExpr("COALESCE(ROUND(SUM(" + sumCol("sum_billamount") + ")::numeric, 2), 0) AS sum_billamount").
		ColumnExpr("COALESCE(ROUND(SUM(" + sumCol("sum_amountdue") + ")::numeric, 2), 0) AS sum_amountdue").
		ColumnExpr("COALESCE(ROUND(SUM(" + sumCol("sum_debtamount") + ")::numeric, 2), 0) AS sum_debtamount").
		ColumnExpr("COALESCE(ROUND(SUM(" + sumCol("sum_outstandingamount") + ")::numeric, 2), 0) AS sum_outstandingamount").
		ColumnExpr("COALESCE(ROUND(SUM(" + sumCol("sum_billconsumptionvalue") + ")::numeric, 2), 0) AS sum_billconsumptionvalue").
		ColumnExpr("COALESCE(ROUND(SUM(" + sumCol("sum_paymentsamount") + ")::numeric, 2), 0) AS sum_paymentsamount")
	for _, g := range groups {
		selectExpr, groupExpr := summaryGroupExprs(g)
		q = q.ColumnExpr(selectExpr).GroupExpr(groupExpr)
	}
	if len(groups) > 0 {
		q = q.OrderExpr(strings.Join(groups, ", "))
	}
	var data []AggregateRow
	if err := q.Scan(ctx, &data); err != nil {
		return nil, err
	}
	return data, nil
}

// distinctCustomerCounts computes customer_count per group via a two-level
// GROUP BY (hash-aggregate friendly) rather than COUNT(DISTINCT (row)),
// which forces a sort — see the comment on Aggregate. One account can have
// many service points and many bills across billing periods, so we collapse
// to distinct (accountcode, servicepointcode) before counting. The
// raw-table fallback, used for row-level-filtered queries and for any
// query grouping by billingyear/billingmonth.
func (s *Service) distinctCustomerCounts(ctx context.Context, p FilterParams, groups []string, excludeMmsDup bool) ([]AggregateRow, error) {
	inner := s.base(p)
	if excludeMmsDup {
		inner = inner.Where("NOT (" + mmsDuplicateExistsExpr + ")")
	}
	inner = inner.
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

// distinctCustomerCountsFromRoster computes customer_count per group from
// the pre-aggregated customer roster — already one row per distinct
// customer, so this is a plain COUNT(*)/GROUP BY over a table sized to the
// customer count rather than the bill count, instead of the two-level
// GROUP BY distinctCustomerCounts needs to run against raw rows. Only
// called when groups excludes billingyear/billingmonth — see
// groupsIncludePeriod. excludeMmsDup filters out roster rows flagged
// has_mms_duplicate (sql/zeus_prepaid_mms_precedence.sql).
func (s *Service) distinctCustomerCountsFromRoster(ctx context.Context, p FilterParams, groups []string, excludeMmsDup bool) ([]AggregateRow, error) {
	q := s.rosterBase(p)
	if excludeMmsDup {
		q = q.Where("NOT has_mms_duplicate")
	}
	q = q.ColumnExpr("COUNT(*) AS customer_count")
	for _, g := range groups {
		selectExpr, groupExpr := summaryGroupExprs(g)
		q = q.ColumnExpr(selectExpr).GroupExpr(groupExpr)
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
