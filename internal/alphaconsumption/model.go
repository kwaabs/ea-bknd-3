// Package alphaconsumption is a self-contained domain package: models,
// service, handler, and routes for app.alpha_tnd_consumption_raw — a
// legacy Oracle metering source (TBL_ENERGY_CONSUMPTION JOIN
// V_COMMONVIEWFORALL), loaded daily by the ETL job
// "alpha-tnd-consumption-fetch" (internal/etl) rather than owned by this
// package. Modeled on holleyconsumption: this table has real
// fromdatetime/todatetime TIMESTAMP columns (not a free-text billmonth
// label), so date-range filtering is a plain SQL range (see dbx.DateRange
// in service.go) — no billmonth-parsing dance needed.
//
// Unlike every other legacy consumption source in this app, there's no
// region/district dimension here at all — the source has no such concept,
// only a substation name and consumer/meter/location identifiers — so no
// boundary-table join is needed the way holleyconsumption needs one for
// its region code.
//
// One row per (readingmasterid, energycode): a single meter reading can
// carry several energy register values (import/export/demand/etc) as
// separate rows sharing the same readingmasterid. energycode is kept as a
// raw numeric code with no human-readable mapping — none exists for this
// source yet.
package alphaconsumption

import "time"

// Reading mirrors a row from app.alpha_tnd_consumption_raw.
type Reading struct {
	ReadingMasterID int64     `bun:"readingmasterid" json:"reading_master_id"`
	ConnectionID    int64     `bun:"connectionpkid" json:"connection_id"`
	EnergyCode      int       `bun:"energycode" json:"energy_code"`
	Value           float64   `bun:"value" json:"value"`
	FromDate        time.Time `bun:"fromdatetime" json:"from_date"`
	ToDate          time.Time `bun:"todatetime" json:"to_date"`
	Substation      string    `bun:"substationname" json:"substation"`
	ConsumerID      int64     `bun:"consumerid" json:"consumer_id"`
	ConsumerName    string    `bun:"consumername" json:"consumer_name"`
	MeterSerialNo   string    `bun:"mtrsrno" json:"meter_serial_no"`
	LocationName    string    `bun:"locationname" json:"location_name,omitempty"`
	LocationCode    string    `bun:"locationcode" json:"location_code,omitempty"`
}

// FilterParams holds row-level filters shared by detail and aggregate.
// Pagination is NOT here — it travels as httpx.Pagination, parsed and
// clamped once in the handler.
type FilterParams struct {
	Substation []string
	EnergyCode []int
	Search     string

	// DateFrom/DateTo filter directly against the real fromdatetime column
	// (see dbx.DateRange) — same as holleyconsumption/pnsconsumption, no
	// billmonth-label resolution needed.
	DateFrom time.Time
	DateTo   time.Time
}

// AggregateRow is a single grouped aggregate row.
type AggregateRow struct {
	Substation    string  `bun:"substation" json:"substation,omitempty"`
	EnergyCode    int     `bun:"energy_code" json:"energy_code,omitempty"`
	ConsumerCount int64   `bun:"consumer_count" json:"consumer_count"`
	SumValue      float64 `bun:"sum_value" json:"sum_value"`
}

// AggregateResult is the aggregate response envelope.
type AggregateResult struct {
	Data  []AggregateRow `json:"data"`
	Total int            `json:"total"` // number of groups
}
