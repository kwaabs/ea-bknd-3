// Package zeusbilling is a self-contained domain package: models, service,
// handler, and routes for app.zeus_sales — the improved, per-bill Zeus
// source data (replaces the old raw feed that app.customer_sales_zeus /
// internal/zeussales was originally built from). Same shape as
// internal/mmssales / internal/zeussales — see internal/mmssales for the
// annotated template. This package is additive only: it does not read from
// or modify app.customer_sales_zeus, and internal/zeussales is untouched.
package zeusbilling

import "time"

// Bill mirrors a row from app.zeus_sales. Field/JSON names follow the
// source CSV headers (camelCase), lower-cased to match this table's actual
// (unquoted, Postgres-folded) column names — consistent with every other
// table in this codebase (regionname, lastbillamount, ...).
type Bill struct {
	ID       int64  `bun:"id" json:"id"`
	MongoID  string `bun:"_id" json:"_id"`
	Bill     string `bun:"bill" json:"bill"`
	BillType string `bun:"billtype" json:"billType"`

	ServicePoint       string `bun:"servicepoint" json:"servicePoint"`
	ServicePointCode   string `bun:"servicepointcode" json:"servicePointCode"`
	ServicePointStatus string `bun:"servicepointstatus" json:"servicePointStatus"`

	TariffClass     string `bun:"tariffclass" json:"tariffClass"`
	TariffClassCode string `bun:"tariffclasscode" json:"tariffClassCode"`
	TariffClassName string `bun:"tariffclassname" json:"tariffClassName"`
	ServiceClass    string `bun:"serviceclass" json:"serviceClass"`

	GeoCode        string `bun:"geocode" json:"geoCode"`
	AccountCode    string `bun:"accountcode" json:"accountCode"`
	MeterCode      string `bun:"metercode" json:"meterCode"`
	MeterModelType string `bun:"metermodeltype" json:"meterModelType"`

	Region     string `bun:"region" json:"region"`
	RegionCode string `bun:"regioncode" json:"regionCode"`
	RegionName string `bun:"regionname" json:"regionName"`

	District     string `bun:"district" json:"district"`
	DistrictCode string `bun:"districtcode" json:"districtCode"`
	DistrictName string `bun:"districtname" json:"districtName"`

	// SoeName/MdaName/IsSensitive come through as literal "N/A" in the
	// source rather than null, so they stay plain strings (not pointers).
	SoeName      string `bun:"soename" json:"soeName"`
	MdaName      string `bun:"mdaname" json:"mdaName"`
	IsSensitive  string `bun:"issensitive" json:"isSensitive"`
	CustomerName string `bun:"customername" json:"customerName"`

	BillConsumptionValue          float64 `bun:"billconsumptionvalue" json:"billConsumptionValue"`
	BillConsumptionApparentValue  float64 `bun:"billconsumptionapparentvalue" json:"billConsumptionApparentValue"`
	BillConsumptionMaxDemandValue float64 `bun:"billconsumptionmaxdemandvalue" json:"billConsumptionMaxDemandValue"`
	BillConsumptionExportValue    float64 `bun:"billconsumptionexportvalue" json:"billConsumptionExportValue"`
	BillAvgConsumptionValue       float64 `bun:"billavgconsumptionvalue" json:"billAvgConsumptionValue"`
	BillPeriod                    int     `bun:"billperiod" json:"billPeriod"`
	BillConsumptionType           string  `bun:"billconsumptiontype" json:"billConsumptionType"`

	OutstandingAmount     float64  `bun:"outstandingamount" json:"outstandingAmount"`
	LifeLineAmount        float64  `bun:"lifelineamount" json:"lifeLineAmount"`
	FirstThresholdAmount  float64  `bun:"firstthresholdamount" json:"firstThresholdAmount"`
	SecondThresholdAmount float64  `bun:"secondthresholdamount" json:"secondThresholdAmount"`
	ThirdThresholdAmount  *float64 `bun:"thirdthresholdamount" json:"thirdThresholdAmount"`

	EnergyCharge                  float64 `bun:"energycharge" json:"energyCharge"`
	ServiceCharge                 float64 `bun:"servicecharge" json:"serviceCharge"`
	EnergyPlusServiceCharge       float64 `bun:"energyplusservicecharge" json:"energyPlusServiceCharge"`
	PowerFactorSurcharge          float64 `bun:"powerfactorsurcharge" json:"powerFactorSurcharge"`
	VatCharge                     float64 `bun:"vatcharge" json:"vatCharge"`
	NhilCharge                    float64 `bun:"nhilcharge" json:"nhilCharge"`
	GetfundCharge                 float64 `bun:"getfundcharge" json:"getfundCharge"`
	StreetLightCharge             float64 `bun:"streetlightcharge" json:"streetLightCharge"`
	NationalElectrificationCharge float64 `bun:"nationalelectrificationcharge" json:"nationalElectrificationCharge"`

	BillAmount                 float64 `bun:"billamount" json:"billAmount"`
	PaymentsAmount             float64 `bun:"paymentsamount" json:"paymentsAmount"`
	AdjustmentConsumptionValue float64 `bun:"adjustmentconsumptionvalue" json:"adjustmentConsumptionValue"`
	AdjustmentAmount           float64 `bun:"adjustmentamount" json:"adjustmentAmount"`
	AmountDue                  float64 `bun:"amountdue" json:"amountDue"`
	DebtAmount                 float64 `bun:"debtamount" json:"debtAmount"`

	LastPaymentAmount float64    `bun:"lastpaymentamount" json:"lastPaymentAmount"`
	LastPaymentDate   *time.Time `bun:"lastpaymentdate" json:"lastPaymentDate"`

	BillingMonth int    `bun:"billingmonth" json:"billingMonth"`
	BillingYear  int    `bun:"billingyear" json:"billingYear"`
	BillStatus   string `bun:"billstatus" json:"billStatus"`

	CreatedAt time.Time `bun:"createdat" json:"createdAt"`
	UpdatedAt time.Time `bun:"updatedat" json:"updatedAt"`
	V         int       `bun:"__v" json:"__v"`

	AccountType string `bun:"accounttype" json:"accountType"`
}

// FilterParams holds row-level filters shared by Detail and Aggregate.
// Pagination is not here — it travels as httpx.Pagination, parsed and
// clamped once in the handler.
type FilterParams struct {
	RegionName          []string
	DistrictName        []string
	TariffClassCode     []string
	ServiceClass        []string
	AccountType         []string
	BillStatus          []string
	BillConsumptionType []string
	MeterModelType      []string
	ServicePointStatus  []string
	BillingYear         []int
	BillingMonth        []int
	IsSensitive         string
	Search              string
	AccountCode         []string
	ServicePointCode    []string
	MeterCode           []string
	LastPaymentDateFrom time.Time
	LastPaymentDateTo   time.Time
	CreatedAtFrom       time.Time
	CreatedAtTo         time.Time
}

// AggregateRow is a single grouped aggregate row.
type AggregateRow struct {
	DataSrc                 string  `bun:"data_src" json:"data_src"`
	RegionName              string  `bun:"regionname" json:"regionname,omitempty"`
	DistrictName            string  `bun:"districtname" json:"districtname,omitempty"`
	TariffClassCode         string  `bun:"tariffclasscode" json:"tariffclasscode,omitempty"`
	ServiceClass            string  `bun:"serviceclass" json:"serviceclass,omitempty"`
	AccountType             string  `bun:"accounttype" json:"accounttype,omitempty"`
	BillStatus              string  `bun:"billstatus" json:"billstatus,omitempty"`
	MeterModelType          string  `bun:"metermodeltype" json:"metermodeltype,omitempty"`
	BillingYear             int     `bun:"billingyear" json:"billingyear,omitempty"`
	BillingMonth            int     `bun:"billingmonth" json:"billingmonth,omitempty"`
	CustomerCount           int64   `bun:"customer_count" json:"customer_count"`
	SumBillAmount           float64 `bun:"sum_billamount" json:"sum_billamount"`
	SumAmountDue            float64 `bun:"sum_amountdue" json:"sum_amountdue"`
	SumDebtAmount           float64 `bun:"sum_debtamount" json:"sum_debtamount"`
	SumOutstandingAmount    float64 `bun:"sum_outstandingamount" json:"sum_outstandingamount"`
	SumBillConsumptionValue float64 `bun:"sum_billconsumptionvalue" json:"sum_billconsumptionvalue"`
	SumPaymentsAmount       float64 `bun:"sum_paymentsamount" json:"sum_paymentsamount"`
}

// AggregateResult is the aggregate response envelope.
type AggregateResult struct {
	Data  []AggregateRow `json:"data"`
	Total int            `json:"total"`
}
