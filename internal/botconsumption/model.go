// Package botconsumption is a self-contained domain package: models,
// service, handler, and routes for app.bot_consumption — a bot-ingested
// customer consumption source, structurally independent of Zeus/MMS/AMR
// (no shared keys, just a flat reading per customer per bill month). There
// is no real date/timestamp column, only billmonth, a free-text
// "monthname-year" label with month precision only — see
// FilterParams.DateFrom/DateTo and Service.resolveDateRangeToBillMonths.
package botconsumption

import "time"

// Reading mirrors a row from app.bot_consumption. Field names fix the
// source table's typos/abbreviations (tarrif -> Tariff, meternumber ->
// MeterNumber) without changing the underlying bun column binding.
type Reading struct {
	CustomerName string  `bun:"customer_name" json:"customer_name"`
	MeterNumber  string  `bun:"meternumber" json:"meter_number"`
	GeoCode      string  `bun:"geo_code" json:"geo_code"`
	Kwh          float64 `bun:"kwh" json:"kwh"`
	Tariff       string  `bun:"tarrif" json:"tariff"`
	BillMonth    string  `bun:"billmonth" json:"bill_month"`
	District     string  `bun:"district" json:"district"`
	Region       string  `bun:"region" json:"region"`
}

// FilterParams holds row-level filters shared by detail and aggregate.
// Pagination is NOT here — it travels as httpx.Pagination, parsed and
// clamped once in the handler.
type FilterParams struct {
	Region      []string
	District    []string
	Tariff      []string
	BillMonth   []string
	MeterNumber []string
	Search      string

	// DateFrom/DateTo are the app-wide dateFrom/dateTo filter, accepted for
	// consistency with every other page's date picker — but this table has
	// no real date column, only billmonth ("june-2026", month precision
	// only). Service.resolveDateRangeToBillMonths translates whatever range
	// is given into the set of billmonth labels it overlaps, so any
	// day-of-month here is ignored entirely: dateFrom=2026-06-15 and
	// dateFrom=2026-06-01 behave identically, both meaning "from June 2026
	// onward". Ignored if BillMonth is already set explicitly.
	DateFrom time.Time
	DateTo   time.Time
}

// AggregateRow is a single grouped aggregate row.
type AggregateRow struct {
	Region        string  `bun:"region" json:"region,omitempty"`
	District      string  `bun:"district" json:"district,omitempty"`
	Tariff        string  `bun:"tariff" json:"tariff,omitempty"`
	BillMonth     string  `bun:"billmonth" json:"bill_month,omitempty"`
	CustomerCount int64   `bun:"customer_count" json:"customer_count"`
	SumKwh        float64 `bun:"sum_kwh" json:"sum_kwh"`
}

// AggregateResult is the aggregate response envelope.
type AggregateResult struct {
	Data  []AggregateRow `json:"data"`
	Total int            `json:"total"` // number of groups
}
