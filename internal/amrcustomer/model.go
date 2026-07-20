package amrcustomer

import (
	"time"
)

// AmrCustomerRecord mirrors app.amr_customer_records
type AmrCustomerRecord struct {
	ID             string   `bun:"id,pk,type:uuid" json:"id"`
	Region         *string  `bun:"region" json:"region,omitempty"`
	District       *string  `bun:"district" json:"district,omitempty"`
	CustomerName   *string  `bun:"customer_name" json:"customer_name,omitempty"`
	AccountNo      *string  `bun:"account_no_" json:"account_no,omitempty"`
	SPN            *string  `bun:"spn" json:"spn,omitempty"`
	GeoCode        *string  `bun:"geocode" json:"geocode,omitempty"`
	GhanaPostAddr  *string  `bun:"ghanapostaddress" json:"ghanapost_address,omitempty"`
	ActivityType   *string  `bun:"activity_type" json:"activity_type,omitempty"`
	MeterNumber    *string  `bun:"meter_number" json:"meter_number,omitempty"`
	PhoneNumber    *string  `bun:"phone_number" json:"phone_number,omitempty"`
	TariffClass    *string  `bun:"tariffclassname" json:"tariff_class,omitempty"`
	SubActivity    *string  `bun:"subactivity" json:"sub_activity,omitempty"`
	AccountType    *string  `bun:"accounttype" json:"account_type,omitempty"`
	Activity       *string  `bun:"activity" json:"activity,omitempty"`
	ContractStatus *string  `bun:"contractstatus" json:"contract_status,omitempty"`
	MeterPhase     *string  `bun:"meterphase" json:"meter_phase,omitempty"`
	ServiceType    *string  `bun:"servicetype" json:"service_type,omitempty"`
	Community      *string  `bun:"community" json:"community,omitempty"`
	CustomerType   *string  `bun:"customertype" json:"customer_type,omitempty"`
	HouseNumber    *string  `bun:"housenumber" json:"house_number,omitempty"`
	SLTType        *string  `bun:"slt_type" json:"slt_type,omitempty"`
	MultiplyFactor  float64  `bun:"multiply_factor" json:"multiply_factor"`
}

// ===================================================
// FILTER PARAMS
// ===================================================

type AmrReadingFilterParams struct {
	MeterNumber    []string
	Regions        []string
	Districts      []string
	Communities    []string
	TariffClass    []string
	CustomerType   []string
	AccountType    []string
	ContractStatus []string
	ServiceType    []string
	SLTType        []string
	AccountNo      []string
	SPN            []string
	DateFrom       time.Time
	DateTo         time.Time
}

type AmrStatusDetailQueryParams struct {
	AmrReadingFilterParams
	Page      int
	Limit     int
	Search    string
	Status    string
	SortBy    string
	SortOrder string
}

type AmrHealthDetailParams struct {
	AmrReadingFilterParams
	Page           int
	Limit          int
	Search         string
	HealthCategory string
	SortBy         string
	SortOrder      string
}

// ===================================================
// DAILY CONSUMPTION
// ===================================================

type AmrDailyConsumptionResult struct {
	ConsumptionDate time.Time `bun:"consumption_date" json:"consumption_date"`
	MeterNumber     string    `bun:"meter_number" json:"meter_number"`
	DayStartReading float64   `bun:"day_start_reading" json:"day_start_reading"`
	DayEndReading   float64   `bun:"day_end_reading" json:"day_end_reading"`
	ConsumedEnergy  float64   `bun:"consumed_energy" json:"consumed_energy"`
	SystemName      string    `bun:"system_name" json:"system_name"`
	// from amr_customer_records
	Region         string  `bun:"region" json:"region,omitempty"`
	District       string  `bun:"district" json:"district,omitempty"`
	Community      string  `bun:"community" json:"community,omitempty"`
	CustomerName   string  `bun:"customer_name" json:"customer_name,omitempty"`
	AccountNo      string  `bun:"account_no_" json:"account_no,omitempty"`
	SPN            string  `bun:"spn" json:"spn,omitempty"`
	TariffClass    string  `bun:"tariffclassname" json:"tariff_class,omitempty"`
	CustomerType   string  `bun:"customertype" json:"customer_type,omitempty"`
	AccountType    string  `bun:"accounttype" json:"account_type,omitempty"`
	ContractStatus string  `bun:"contractstatus" json:"contract_status,omitempty"`
	MeterPhase     string  `bun:"meterphase" json:"meter_phase,omitempty"`
	ServiceType    string  `bun:"servicetype" json:"service_type,omitempty"`
	SLTType        string  `bun:"slt_type" json:"slt_type,omitempty"`
	MultiplyFactor float64 `bun:"multiply_factor" json:"multiply_factor"`
}

// AmrDailyConsumptionQueryParams adds pagination and a system_name filter
// (import_kwh / export_kwh) on top of the shared reading filters.
type AmrDailyConsumptionQueryParams struct {
	AmrReadingFilterParams
	Page       int
	Limit      int
	SystemName string
}

// AmrDailyConsumptionResponse is the paginated response envelope, matching
// the {data, total, page, limit, total_pages} shape used elsewhere.
type AmrDailyConsumptionResponse struct {
	Data       []AmrDailyConsumptionResult `json:"data"`
	Total      int                         `json:"total"`
	Page       int                         `json:"page"`
	Limit      int                         `json:"limit"`
	TotalPages int                         `json:"total_pages"`
}

// ===================================================
// AGGREGATED CONSUMPTION
// ===================================================

type AmrAggregatedConsumptionResult struct {
	GroupPeriod      time.Time `bun:"group_period" json:"group_period"`
	SystemName       *string   `bun:"system_name" json:"system_name,omitempty"`
	Region           *string   `bun:"region" json:"region,omitempty"`
	District         *string   `bun:"district" json:"district,omitempty"`
	Community        *string   `bun:"community" json:"community,omitempty"`
	TariffClass      *string   `bun:"tariff_class" json:"tariff_class,omitempty"`
	CustomerType     *string   `bun:"customer_type" json:"customer_type,omitempty"`
	SLTType          *string   `bun:"slt_type" json:"slt_type,omitempty"`
	TotalConsumption float64   `bun:"total_consumption" json:"total_consumption"`
	ActiveMeters     int       `bun:"active_meters" json:"active_meters"`
	TotalMeterCount  int       `bun:"total_meter_count" json:"total_meter_count"`
}

// ===================================================
// METER STATUS
// ===================================================

type AmrMeterStatusResult struct {
	MeterNumber     string     `bun:"meter_number" json:"meter_number"`
	ConsumptionDate *time.Time `bun:"consumption_date" json:"consumption_date"`
	Status          string     `bun:"status" json:"status"`
	Consumption     *float64   `bun:"consumption" json:"consumption"`
	ReadingCount    *int       `bun:"reading_count" json:"reading_count"`
	DayStartTime    *time.Time `bun:"day_start_time" json:"day_start_time"`
	DayEndTime      *time.Time `bun:"day_end_time" json:"day_end_time"`
	// from amr_customer_records
	Region         string `bun:"region" json:"region,omitempty"`
	District       string `bun:"district" json:"district,omitempty"`
	Community      string `bun:"community" json:"community,omitempty"`
	CustomerName   string `bun:"customer_name" json:"customer_name,omitempty"`
	AccountNo      string `bun:"account_no_" json:"account_no,omitempty"`
	TariffClass    string `bun:"tariffclassname" json:"tariff_class,omitempty"`
	ContractStatus string `bun:"contractstatus" json:"contract_status,omitempty"`
	ServiceType    string `bun:"servicetype" json:"service_type,omitempty"`
}

type AmrMeterStatusSummary struct {
	Total               int                    `json:"total"`
	Online              int                    `json:"online"`
	OfflineNoData       int                    `json:"offline_no_data"`
	OfflineNoRecord     int                    `json:"offline_no_record"`
	TotalOffline        int                    `json:"total_offline"`
	OnlinePercentage    float64                `json:"online_percentage"`
	OfflinePercentage   float64                `json:"offline_percentage"`
	AvgUptimePercentage float64                `json:"avg_uptime_percentage"`
	TotalConsumptionKWh float64                `json:"total_consumption_kwh"`
	FiltersApplied      map[string]interface{} `json:"filters_applied"`
}

type AmrMeterStatusTimelineEntry struct {
	Date    time.Time `bun:"date" json:"date"`
	Online  int       `bun:"online" json:"online"`
	Offline int       `bun:"offline" json:"offline"`
	Total   int       `bun:"total" json:"total"`
}

type AmrMeterStatusTimeline struct {
	Data      []AmrMeterStatusTimelineEntry `json:"data"`
	DateRange struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"date_range"`
}

type AmrMeterStatusDetailRecord struct {
	MeterNumber         string     `bun:"meter_number" json:"meter_number"`
	Region              string     `bun:"region" json:"region,omitempty"`
	District            string     `bun:"district" json:"district,omitempty"`
	Community           string     `bun:"community" json:"community,omitempty"`
	CustomerName        string     `bun:"customer_name" json:"customer_name,omitempty"`
	AccountNo           string     `bun:"account_no_" json:"account_no,omitempty"`
	TariffClass         string     `bun:"tariffclassname" json:"tariff_class,omitempty"`
	ContractStatus      string     `bun:"contractstatus" json:"contract_status,omitempty"`
	ServiceType         string     `bun:"servicetype" json:"service_type,omitempty"`
	Status              string     `bun:"status" json:"status"`
	LastConsumptionDate *time.Time `bun:"last_consumption_date" json:"last_consumption_date"`
	TotalConsumptionKWh float64    `bun:"total_consumption_kwh" json:"total_consumption_kwh"`
	UptimePercentage    float64    `bun:"uptime_percentage" json:"uptime_percentage"`
	DaysOffline         int        `bun:"days_offline" json:"days_offline"`
	LastReadingTime     *time.Time `bun:"last_reading_time" json:"last_reading_time"`
}

type AmrMeterStatusDetailResponse struct {
	Data       []AmrMeterStatusDetailRecord `json:"data"`
	Pagination struct {
		Page         int  `json:"page"`
		Limit        int  `json:"limit"`
		TotalRecords int  `json:"total_records"`
		TotalPages   int  `json:"total_pages"`
		HasMore      bool `json:"has_more"`
	} `json:"pagination"`
	FiltersApplied map[string]interface{} `json:"filters_applied"`
}

// ===================================================
// HEALTH
// ===================================================

type AmrMeterHealthSummary struct {
	TotalMeters             int                         `json:"total_meters"`
	OnlineMeters            int                         `json:"online_meters"`
	OfflineMeters           int                         `json:"offline_meters"`
	HealthPercentage        float64                     `json:"health_percentage"`
	AverageUptimePercentage float64                     `json:"average_uptime_percentage"`
	UptimeDistribution      AmrMeterUptimeDistribution  `json:"uptime_distribution"`
	ByTariffClass           []AmrHealthByTariffClass    `json:"by_tariff_class,omitempty"`
	ByRegion                []AmrHealthByRegion         `json:"by_region,omitempty"`
}

type AmrMeterUptimeDistribution struct {
	Excellent int `json:"excellent"` // >95%
	Good      int `json:"good"`      // 80-95%
	Poor      int `json:"poor"`      // 60-80%
	Critical  int `json:"critical"`  // <60%
}

type AmrHealthByTariffClass struct {
	TariffClass string  `bun:"tariff_class" json:"tariff_class"`
	Total       int     `bun:"total" json:"total"`
	Online      int     `bun:"online" json:"online"`
	Offline     int     `bun:"offline" json:"offline"`
	AvgUptime   float64 `bun:"avg_uptime" json:"avg_uptime"`
}

type AmrHealthByRegion struct {
	Region    string  `bun:"region" json:"region"`
	Total     int     `bun:"total" json:"total"`
	Online    int     `bun:"online" json:"online"`
	Offline   int     `bun:"offline" json:"offline"`
	AvgUptime float64 `bun:"avg_uptime" json:"avg_uptime"`
}

type AmrMeterHealthDetailRecord struct {
	MeterNumber             string     `bun:"meter_number" json:"meter_number"`
	Region                  string     `bun:"region" json:"region,omitempty"`
	District                string     `bun:"district" json:"district,omitempty"`
	Community               string     `bun:"community" json:"community,omitempty"`
	CustomerName            string     `bun:"customer_name" json:"customer_name,omitempty"`
	AccountNo               string     `bun:"account_no_" json:"account_no,omitempty"`
	TariffClass             string     `bun:"tariffclassname" json:"tariff_class,omitempty"`
	ContractStatus          string     `bun:"contractstatus" json:"contract_status,omitempty"`
	ServiceType             string     `bun:"servicetype" json:"service_type,omitempty"`
	Status                  string     `bun:"status" json:"status"`
	HealthCategory          string     `bun:"health_category" json:"health_category"`
	UptimePercentage        float64    `bun:"uptime_percentage" json:"uptime_percentage"`
	DaysOnline              int        `bun:"days_online" json:"days_online"`
	DaysOffline             int        `bun:"days_offline" json:"days_offline"`
	TotalDays               int        `bun:"total_days" json:"total_days"`
	LastSeenDate            *time.Time `bun:"last_seen_date" json:"last_seen_date"`
	TotalConsumptionKWh     float64    `bun:"total_consumption_kwh" json:"total_consumption_kwh"`
	AverageDailyConsumption float64    `bun:"avg_daily_consumption" json:"avg_daily_consumption"`
}

type AmrMeterHealthDetailResponse struct {
	Data       []AmrMeterHealthDetailRecord `json:"data"`
	Pagination struct {
		Page         int  `json:"page"`
		Limit        int  `json:"limit"`
		TotalRecords int  `json:"total_records"`
		TotalPages   int  `json:"total_pages"`
		HasMore      bool `json:"has_more"`
	} `json:"pagination"`
	Summary struct {
		HealthCategory string  `json:"health_category,omitempty"`
		AverageUptime  float64 `json:"average_uptime"`
		TotalOnline    int     `json:"total_online"`
		TotalOffline   int     `json:"total_offline"`
	} `json:"summary"`
	FiltersApplied map[string]interface{} `json:"filters_applied"`
}

// ===================================================
// CONSUMPTION BY REGION
// ===================================================

type AmrConsumptionByRegionEntry struct {
	Date                   time.Time `bun:"date" json:"date"`
	Region                 string    `bun:"region" json:"region"`
	TotalConsumptionKWh    float64   `bun:"total_consumption_kwh" json:"total_consumption_kwh"`
	MeterCount             int       `bun:"meter_count" json:"meter_count"`
	AvgConsumptionPerMeter float64   `bun:"avg_consumption_per_meter" json:"avg_consumption_per_meter"`
}

type AmrConsumptionByRegionResponse struct {
	Data    []AmrConsumptionByRegionEntry `json:"data"`
	Summary struct {
		TotalConsumptionKWh float64 `json:"total_consumption_kwh"`
		UniqueRegions       int     `json:"unique_regions"`
		DateRange           struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"date_range"`
	} `json:"summary"`
}
