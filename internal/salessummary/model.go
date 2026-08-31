// Package salessummary is the single canonical place that answers "what is
// the total Prepaid (or Postpaid) figure across every source" — the
// question that used to be answered independently, by hand, in five-plus
// separate frontend files (dashboard, customer-sales-overview,
// region-detail-marquee, energy-flow-diagram, choropleth-map), each
// re-deriving its own copy of "Zeus (deduped) + MMS" and silently going
// stale the moment a new source (BOT, then BXC) was added but not every
// one of those files was remembered and updated.
//
// This package fixes that structurally rather than by finding and patching
// each stale copy again: it calls each source's own Service.Aggregate
// directly (in-process Go function calls, same binary, no network hop
// between them — not HTTP calls to their own endpoints) and merges the
// results server-side into one number. Every frontend consumer now reads
// from this one endpoint instead of fetching every raw source itself, so
// there is exactly one place left to update when a source is added:
// sourcesFor() in service.go. No frontend file needs to change at all
// unless it wants a dedicated tab for the new source specifically.
package salessummary

import "time"

// Category is which side of the Customer Consumption split this summary is
// for. Adding a source means adding it to sourcesFor(category) in
// service.go, under whichever category it belongs to.
type Category string

const (
	Prepaid  Category = "prepaid"
	Postpaid Category = "postpaid"
)

// CommonFilters are the filters every source can be scoped by — the ones a
// page's date/region/district picker expresses. This package exists for
// cross-source TOTALS, not per-source deep dives (search, tariff,
// manufacturer, ...) — those stay on each source's own Detail/Aggregate
// endpoint, unchanged.
type CommonFilters struct {
	DateFrom time.Time
	DateTo   time.Time
	Region   []string
	District []string
}

// SourceStat is one source's contribution to a row or to the grand total.
type SourceStat struct {
	Kwh       float64 `json:"kwh"`
	Customers int64   `json:"customers"`
}

// Row is one grouped row (by region or by district, whichever GroupBy the
// request asked for), broken down per source plus its own total.
type Row struct {
	GroupValue     string                `json:"group_value"`
	BySource       map[string]SourceStat `json:"by_source"`
	TotalKwh       float64               `json:"total_kwh"`
	TotalCustomers int64                 `json:"total_customers"`
}

// Summary is the full cross-source response for one category.
type Summary struct {
	TotalKwh       float64               `json:"total_kwh"`
	TotalCustomers int64                 `json:"total_customers"`
	BySource       map[string]SourceStat `json:"by_source"`
	Rows           []Row                 `json:"rows"`
}

// normalizedRow is one source's row after being translated out of that
// source's own AggregateRow shape (different field names, different
// dimension-grouping conventions) into a common shape the merge step in
// service.go can work with uniformly.
type normalizedRow struct {
	GroupValue string
	Kwh        float64
	Customers  int64
}
