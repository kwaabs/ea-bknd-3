// Package ecash4consumption is a self-contained domain package: models,
// service, handler, and routes for app.ecash4_consumption — the ECASH 4
// legacy meter source (dbo.vw_ENERGYAGG on the source side), loaded by the
// ETL job "ecash4-consumption-pull" (internal/etl) rather than owned by
// this package.
//
// The source has no real date/time column — only a "YYYY-MM" year_month
// label, one row per meter+service-point+month. Rather than repeat
// botconsumption/bxcconsumption's free-text billmonth-parsing dance, the
// destination table stores year_month as-is for display plus a generated
// period_date (first of month, real date column) for filtering/sorting —
// so date-range filtering here is a plain SQL range on period_date (see
// dbx.DateRange in service.go), same shape as pnsconsumption/
// holleyconsumption's real timestamp columns.
//
// region/district are already human-readable text on the source (no code
// resolution needed, unlike holleyconsumption's region).
package ecash4consumption

import "time"

// Reading mirrors a row from app.ecash4_consumption.
type Reading struct {
	MeterSerial  string    `bun:"meter_serial" json:"meter_serial"`
	SPN          string    `bun:"spn" json:"spn"`
	District     string    `bun:"district" json:"district"`
	Region       string    `bun:"region" json:"region"`
	CustomerName string    `bun:"customer_name" json:"customer_name"`
	YearMonth    string    `bun:"year_month" json:"year_month"`
	PeriodDate   time.Time `bun:"period_date" json:"period_date"`
	EnergyKwh    float64   `bun:"energy_kwh" json:"energy_kwh"`
	TariffClass  string    `bun:"tariff_class" json:"tariff_class"`
}

// FilterParams holds row-level filters shared by detail and aggregate.
// Pagination is NOT here — it travels as httpx.Pagination, parsed and
// clamped once in the handler.
type FilterParams struct {
	Region      []string
	District    []string
	TariffClass []string
	MeterSerial []string
	Search      string

	// DateFrom/DateTo filter directly against the generated period_date
	// column (see dbx.DateRange) — same as pnsconsumption/
	// holleyconsumption, unlike botconsumption/bxcconsumption's
	// billmonth-label resolution.
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
