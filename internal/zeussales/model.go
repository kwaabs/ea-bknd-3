// Package zeussales is a self-contained domain package: models, service,
// handler, and routes for app.customer_sales_zeus. Same shape as mmssales
// — see internal/mmssales for the annotated template.
package zeussales

import "time"

// Sale mirrors a row from app.customer_sales_zeus.
// JSON field names are unchanged from the previous models.CustomerSalesZeus,
// so API responses are byte-compatible.
type Sale struct {
	RegionName          string   `bun:"regionname" json:"regionname"`
	DistrictName        string   `bun:"districtname" json:"districtname"`
	ServiceType         string   `bun:"servicetype" json:"servicetype"`
	ServiceClass        string   `bun:"serviceclass" json:"serviceclass"`
	TariffClassCode     string   `bun:"tariffclasscode" json:"tariffclasscode"`
	TariffClassName     string   `bun:"tariffclassname" json:"tariffclassname"`
	FullName            string   `bun:"fullname" json:"fullname"`
	ServicePointNumber  string   `bun:"servicepointnumber" json:"servicepointnumber"`
	AccountNumber       string   `bun:"accountnumber" json:"accountnumber"`
	ContractStatus      string   `bun:"contractstatus" json:"contractstatus"`
	Activity            string   `bun:"activity" json:"activity"`
	SubActivity         string   `bun:"subactivity" json:"subactivity"`
	CustomerType        string   `bun:"customertype" json:"customertype"`
	LastReadingValue    *float64 `bun:"lastreadingvalue" json:"lastreadingvalue"`
	GeoCode             string   `bun:"geocode" json:"geocode"`
	PlotCode            string   `bun:"plotcode" json:"plotcode"`
	Ministry            *string  `bun:"ministry" json:"ministry"`
	MDA                 string   `bun:"mda" json:"mda"`
	LastReadingDate     *string  `bun:"lastreadingdate" json:"lastreadingdate"`
	LastBillAmount      *float64 `bun:"lastbillamount" json:"lastbillamount"`
	LastBillConsumption *float64 `bun:"lastbillconsumption" json:"lastbillconsumption"`
	LastPaymentDate     *string  `bun:"lastpaymentdate" json:"lastpaymentdate"`
	LastPaymentAmount   *float64 `bun:"lastpaymentamount" json:"lastpaymentamount"`
	CurrentBalance      *float64 `bun:"currentbalance" json:"currentbalance"`
	AccountType         string   `bun:"accounttype" json:"accounttype"`
	IsAMR               bool     `bun:"isamr" json:"isamr"`
	MinistryCode        *string  `bun:"ministrycode" json:"ministrycode"`
	MinistryName        *string  `bun:"ministryname" json:"ministryname"`
	MDACode             *string  `bun:"mdacode" json:"mdacode"`
	MDAName             *string  `bun:"mdaname" json:"mdaname"`
	LastBillDate        *string  `bun:"lastbilldate" json:"lastbilldate"`
	BillMonth           string   `bun:"billmonth" json:"billmonth"`
	CreatedAt           string   `bun:"createdat" json:"createdat"`
	DataSrc             string   `bun:"data_src" json:"data_src"`
}

// FilterParams holds row-level filters shared by detail and aggregate.
// Pagination is not here — it travels as httpx.Pagination, parsed and
// clamped once in the handler.
type FilterParams struct {
	RegionName          []string
	DistrictName        []string
	ServiceType         []string
	ServiceClass        []string
	TariffClassCode     []string
	CustomerType        []string
	AccountType         []string
	IsAMR               string
	BillMonth           []string
	ContractStatus      []string
	Search              string
	AccountNumber       []string
	ServicePointNumber  []string
	LastBillDateFrom    time.Time
	LastBillDateTo      time.Time
	LastReadingDateFrom time.Time
	LastReadingDateTo   time.Time
}

// AggregateRow is a single grouped aggregate row.
type AggregateRow struct {
	DataSrc                string  `bun:"data_src" json:"data_src"`
	RegionName              string  `bun:"regionname" json:"regionname,omitempty"`
	DistrictName            string  `bun:"districtname" json:"districtname,omitempty"`
	ContractStatus          string  `bun:"contractstatus" json:"contractstatus,omitempty"`
	ServiceType             string  `bun:"servicetype" json:"servicetype,omitempty"`
	ServiceClass            string  `bun:"serviceclass" json:"serviceclass,omitempty"`
	TariffClassCode         string  `bun:"tariffclasscode" json:"tariffclasscode,omitempty"`
	CustomerType            string  `bun:"customertype" json:"customertype,omitempty"`
	AccountType             string  `bun:"accounttype" json:"accounttype,omitempty"`
	MDA                     string  `bun:"mda" json:"mda,omitempty"`
	CustomerCount           int64   `bun:"customer_count" json:"customer_count"`
	SumLastBillAmount       float64 `bun:"sum_lastbillamount" json:"sum_lastbillamount"`
	SumLastBillConsumption  float64 `bun:"sum_lastbillconsumption" json:"sum_lastbillconsumption"`
	SumCurrentBalance       float64 `bun:"sum_currentbalance" json:"sum_currentbalance"`
}

// AggregateResult is the aggregate response envelope.
type AggregateResult struct {
	Data  []AggregateRow `json:"data"`
	Total int            `json:"total"`
}
