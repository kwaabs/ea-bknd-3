// Package pnsconsumption is a self-contained domain package: models,
// service, handler, and routes for app.pns_consumption — a PNS-ingested
// legacy customer consumption source, independent of Zeus/MMS/AMR/BOT/BXC
// (no shared keys, just a flat billing record per customer per bill
// month).
//
// Two things set this source apart from botconsumption/bxcconsumption:
//
//  1. It has a real billdate timestamp column, not just a free-text
//     billmonth label — so date-range filtering here is a plain SQL
//     range on billdate (see dbx.DateRange in service.go), not the
//     billmonth-label-parsing dance bot/bxc need. billmonth is still
//     stored and returned, just not used for filtering.
//  2. Region/district are only available as regionid/districtid — opaque
//     codes (e.g. "10001001"), not human-readable names. Unlike every
//     other source in this codebase, there is currently no lookup table
//     anywhere that maps these codes to names, so Reading/AggregateRow
//     expose the raw codes as-is. A name-resolving lookup is expected
//     from the DBA later — swapping it in only needs a JOIN added to
//     base() plus new Name fields here, not a reshape of this package.
package pnsconsumption

import "time"

// Reading mirrors a row from app.pns_consumption. EnergyKwh binds to the
// "energy" column specifically, not "totalenergy" — energy is the actual
// consumption in kWh; totalenergy folds in service/demand charges and is
// not a consumption figure.
type Reading struct {
	ServiceID      string    `bun:"serviceid" json:"service_id"`
	CustomerID     string    `bun:"customerid" json:"customer_id"`
	ServicePoint   string    `bun:"servicepoint" json:"service_point"`
	RegionID       string    `bun:"regionid" json:"region_id"`
	DistrictID     string    `bun:"districtid" json:"district_id"`
	TariffCategory string    `bun:"tariffcategory" json:"tariff_category"`
	BillMonth      string    `bun:"billmonth" json:"bill_month"`
	BillDate       time.Time `bun:"billdate" json:"bill_date"`
	EnergyKwh      float64   `bun:"energy" json:"energy_kwh"`
	StationID      string    `bun:"stationid" json:"station_id"`
}

// FilterParams holds row-level filters shared by detail and aggregate.
// Pagination is NOT here — it travels as httpx.Pagination, parsed and
// clamped once in the handler.
type FilterParams struct {
	RegionID       []string
	DistrictID     []string
	TariffCategory []string
	BillMonth      []string
	ServiceID      []string
	Search         string

	// DateFrom/DateTo filter directly against the real billdate column
	// (see dbx.DateRange) — unlike botconsumption/bxcconsumption, this
	// table has an actual timestamp, so no billmonth-label resolution is
	// needed here.
	DateFrom time.Time
	DateTo   time.Time
}

// AggregateRow is a single grouped aggregate row.
type AggregateRow struct {
	RegionID       string  `bun:"regionid" json:"region_id,omitempty"`
	DistrictID     string  `bun:"districtid" json:"district_id,omitempty"`
	TariffCategory string  `bun:"tariffcategory" json:"tariff_category,omitempty"`
	BillMonth      string  `bun:"billmonth" json:"bill_month,omitempty"`
	CustomerCount  int64   `bun:"customer_count" json:"customer_count"`
	SumEnergyKwh   float64 `bun:"sum_energy_kwh" json:"sum_energy_kwh"`
}

// AggregateResult is the aggregate response envelope.
type AggregateResult struct {
	Data  []AggregateRow `json:"data"`
	Total int            `json:"total"` // number of groups
}
