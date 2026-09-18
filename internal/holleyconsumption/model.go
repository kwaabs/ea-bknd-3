// Package holleyconsumption is a self-contained domain package: models,
// service, handler, and routes for app.holley_consumption — the Holley
// legacy meter source, loaded by the ETL job "holley-consumption-pull"
// (internal/etl) rather than owned by this package. Modeled directly on
// pnsconsumption: this table has a real date_time TIMESTAMP column (not a
// free-text billmonth label), so date-range filtering is a plain SQL range
// on date_time (see dbx.DateRange in service.go) — no billmonth-parsing
// dance needed. region/district here are already human-readable names
// (not opaque codes like PNS's regionid/districtid), stored as plain
// varchar (not bpchar), so no trim() is needed either, unlike
// botconsumption's blank-padded region column.
package holleyconsumption

import "time"

// Reading mirrors a row from app.holley_consumption.
type Reading struct {
	MeterID        int64     `bun:"meter_id" json:"meter_id"`
	DateTime       time.Time `bun:"date_time" json:"date_time"`
	ConsumptionKwh float64   `bun:"consumptionkwh" json:"consumption_kwh"`
	MeterNo        string    `bun:"meter_no" json:"meter_no"`
	CustomerNo     string    `bun:"customer_no" json:"customer_no"`
	CustomerID     string    `bun:"customer_id" json:"customer_id"`
	CustomerName   string    `bun:"customer_name" json:"customer_name"`
	Geocode        string    `bun:"geocode" json:"geocode"`
	Region         string    `bun:"region" json:"region"`
	District       string    `bun:"district" json:"district"`
	TariffClass    string    `bun:"tariff_class" json:"tariff_class"`
}

// FilterParams holds row-level filters shared by detail and aggregate.
// Pagination is NOT here — it travels as httpx.Pagination, parsed and
// clamped once in the handler.
type FilterParams struct {
	Region      []string
	District    []string
	TariffClass []string
	MeterNo     []string
	Search      string

	// DateFrom/DateTo filter directly against the real date_time column
	// (see dbx.DateRange) — same as pnsconsumption, unlike
	// botconsumption/bxcconsumption's billmonth-label resolution.
	DateFrom time.Time
	DateTo   time.Time
}

// AggregateRow is a single grouped aggregate row.
type AggregateRow struct {
	Region        string  `bun:"region" json:"region,omitempty"`
	District      string  `bun:"district" json:"district,omitempty"`
	TariffClass   string  `bun:"tariff_class" json:"tariff_class,omitempty"`
	CustomerCount int64   `bun:"customer_count" json:"customer_count"`
	SumKwh        float64 `bun:"sum_kwh" json:"sum_kwh"`
}

// AggregateResult is the aggregate response envelope.
type AggregateResult struct {
	Data  []AggregateRow `json:"data"`
	Total int            `json:"total"` // number of groups
}
