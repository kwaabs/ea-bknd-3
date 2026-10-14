// Package mmssales is a self-contained domain package: models, service,
// handler, and routes for app.mms_customer_sales (STS/prepaid sales).
// Wire it into the app with a single Mount call — see routes.go.
package mmssales

import "time"

// Sale mirrors a row from app.mms_customer_sales.
// JSON field names are unchanged from the previous models.MMSCustomerSales,
// so API responses are byte-compatible.
type Sale struct {
	MeterNumber             string     `bun:"meter_number" json:"meter_number"`
	Manufacturer            string     `bun:"manufacturer" json:"manufacturer"`
	Model                   string     `bun:"model" json:"model"`
	InstallationDate        *time.Time `bun:"installation_date" json:"installation_date"`
	RemovalDate             *time.Time `bun:"removal_date" json:"removal_date"`
	CustomerName            string     `bun:"customer_name" json:"customer_name"`
	ContractCode            string     `bun:"contract_code" json:"contract_code"`
	ContractType            string     `bun:"contract_type" json:"contract_type"`
	ServiceCommencementDate *time.Time `bun:"service_commencement_date" json:"service_commencement_date"`
	ServiceTerminationDate  *time.Time `bun:"service_termination_date" json:"service_termination_date"`
	AccountNumber           string     `bun:"account_number" json:"account_number"`
	Tariff                  string     `bun:"tariff" json:"tariff"`
	UsagePoint              string     `bun:"usage_point" json:"usage_point"`
	Geocode                 string     `bun:"geocode" json:"geocode"`
	Region                  string     `bun:"region" json:"region"`
	District                string     `bun:"district" json:"district"`
	Address                 string     `bun:"address" json:"address"`
	Latitude                *float64   `bun:"latitude" json:"latitude"`
	Longitude               *float64   `bun:"longitude" json:"longitude"`
	MeterSerialNumber       string     `bun:"meter_serial_number" json:"meter_serial_number"`
	STSLastMonthKwhRead     *float64   `bun:"sts_last_month_kwh_read" json:"sts_last_month_kwh_read"`
	// STSCreditBalanceRemaining / STSLastMonthCreditRead: app.mms_customer_sales
	// still HAS these columns — the mms_sales_accounting migration (sql/
	// mms_sales_accounting_migration.sql) is minimal-footprint, it never
	// dropped them, it just stopped populating them (always NULL for any
	// row inserted after the cutover). Detail's query is a SELECT * (see
	// service.go), so bun needs a struct field for every column the table
	// actually has, scanned or not — omitting these two entirely, as a
	// first pass at this change did, makes every Detail call fail with
	// "bun: Sale does not have column \"sts_credit_balance_remaining\"",
	// a live incident this comment exists to prevent recurring. json:"-"
	// keeps them out of the API response, matching the "removed from the
	// UI" decision without needing the column itself to be gone.
	STSCreditBalanceRemaining *float64 `bun:"sts_credit_balance_remaining" json:"-"`
	STSLastMonthCreditRead    *float64 `bun:"sts_last_month_credit_read" json:"-"`
	// Consumption/RoundedConsumption/ConsumptionBand/Year/Month/MonthYear
	// are the new columns mms_sales_accounting_migration.sql added —
	// empty/zero for older rows still carrying their original MMS_SALES
	// shape. Same SELECT * reasoning as above: all six must have a struct
	// field or Detail breaks the moment a row populated by the new
	// pipeline is scanned.
	Consumption        *float64   `bun:"consumption" json:"consumption,omitempty"`
	RoundedConsumption *float64   `bun:"rounded_consumption" json:"rounded_consumption,omitempty"`
	ConsumptionBand    string     `bun:"consumption_band" json:"consumption_band,omitempty"`
	Year               string     `bun:"year" json:"year,omitempty"`
	Month              string     `bun:"month" json:"month,omitempty"`
	MonthYear          string     `bun:"monthyear" json:"monthyear,omitempty"`
	DateTime           *time.Time `bun:"date_time" json:"date_time"`
	DataSrc            string     `bun:"data_src" json:"data_src"`
	// IsDuplicateReading (sql/mms_customer_sales_dedup.sql) is scanned so
	// Detail's SELECT * doesn't fail on this column, but base() already
	// filters WHERE NOT is_duplicate_reading unconditionally — every row
	// reaching here is always false, so it's excluded from JSON to keep
	// API responses byte-compatible with the pre-dedup shape.
	IsDuplicateReading bool `bun:"is_duplicate_reading" json:"-"`
}

// FilterParams holds row-level filters shared by detail and aggregate.
// Pagination is NOT here anymore — it travels as httpx.Pagination, parsed
// and clamped once in the handler.
type FilterParams struct {
	Region          []string
	District        []string
	ContractType    []string
	Tariff          []string
	Manufacturer    []string
	Model           []string
	ConsumptionBand []string
	AccountNumber   []string
	MeterNumber     []string
	Search          string
	DateTimeFrom    time.Time
	DateTimeTo      time.Time
}

// AggregateRow is a single grouped aggregate row.
type AggregateRow struct {
	DataSrc             string  `bun:"data_src" json:"data_src"`
	Region              string  `bun:"region" json:"region,omitempty"`
	District            string  `bun:"district" json:"district,omitempty"`
	ContractType        string  `bun:"contract_type" json:"contract_type,omitempty"`
	Tariff              string  `bun:"tariff" json:"tariff,omitempty"`
	Manufacturer        string  `bun:"manufacturer" json:"manufacturer,omitempty"`
	Model               string  `bun:"model" json:"model,omitempty"`
	ConsumptionBand     string  `bun:"consumption_band" json:"consumption_band,omitempty"`
	CustomerCount       int64   `bun:"customer_count" json:"customer_count"`
	SumLastMonthKwhRead float64 `bun:"sum_last_month_kwh_read" json:"sum_last_month_kwh_read"`
}

// AggregateResult is the aggregate response envelope.
type AggregateResult struct {
	Data  []AggregateRow `json:"data"`
	Total int            `json:"total"` // number of groups
}
