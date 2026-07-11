package meters

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

type Meter struct {
	bun.BaseModel `bun:"table:app.meters,alias:mtr"`

	ID                    string     `bun:",pk,type:uuid,default:uuid_generate_v4()" json:"id"`
	MeterNumber           string     `json:"meter_number"`
	MeterType             string     `json:"meter_type"`
	SPN                   *string    `json:"spn"`
	MeterBrand            *string    `json:"meter_brand"`
	Location              *string    `json:"location"`
	DigitalAddress        *string    `json:"digital_address"`
	Status                *string    `json:"status"`
	MeteringPoint         *string    `json:"metering_point"`
	BoundaryMeteringPoint *string    `json:"boundary_metering_point"`
	Incomer               *string    `json:"incomer"`
	Region                *string    `json:"region"`
	District              *string    `json:"district"`
	Station               *string    `json:"station"`
	CreatedAt             *time.Time `json:"created_at"`
	UpdatedAt             *time.Time `json:"updated_at"`
	MultiplyFactor        *float64   `json:"multiply_factor"`
	CTRatioPrimary        *float64   `json:"ct_ratio_primary"`
	CTRatioSecondary      *float64   `json:"ct_ratio_secondary"`
	VTRatioPrimary        *float64   `json:"vt_ratio_primary"`
	VTRatioSecondary      *float64   `json:"vt_ratio_secondary"`
	Latitude              *float64   `json:"latitude"`
	Longitude             *float64   `json:"longitude"`
	VoltageKV             *float64   `json:"voltage_kv"`
	FeederPanelName       *string    `json:"feeder_panel_name"`
}

type MeterReadingDaily struct {
	bun.BaseModel `bun:"table:meter_readings_daily,alias:mrd"`

	MeterNumber string            `bun:"meter_number,pk" json:"meter_number"`
	DataItemID  string            `bun:"data_item_id,pk" json:"data_item_id"`
	ReadingDate time.Time         `bun:"reading_date,pk" json:"reading_date"`
	TotalVal    float64           `bun:"total_val" json:"total_val"`
	RecordCount int               `bun:"record_count" json:"record_count"`
	Records     []json.RawMessage `bun:"records" json:"records"` // jsonb
	LastUpdate  time.Time         `bun:"last_update" json:"last_update"`
}

type AggregatedReading struct {
	TotalImportKWh  float64 `bun:"total_import_kwh" json:"total_import_kwh"`
	TotalExportKWh  float64 `bun:"total_export_kwh" json:"total_export_kwh"`
	TotalImportKVah float64 `bun:"total_import_kvah" json:"total_import_kvah"`
	TotalExportKVah float64 `bun:"total_export_kvah" json:"total_export_kvah"`
	TotalImportKVar float64 `bun:"total_import_kvar" json:"total_import_kvar"`
	TotalExportKVar float64 `bun:"total_export_kvar" json:"total_export_kvar"`
	MeterCount      int     `bun:"meter_count" json:"meter_count"`
	ReadingCount    int     `bun:"reading_count" json:"reading_count"`
}

type TimeSeriesReading struct {
	Date            time.Time `bun:"date" json:"date"`
	TotalImportKWh  float64   `bun:"total_import_kwh" json:"total_import_kwh"`
	TotalExportKWh  float64   `bun:"total_export_kwh" json:"total_export_kwh"`
	TotalImportKVah float64   `bun:"total_import_kvah" json:"total_import_kvah"`
	TotalExportKVah float64   `bun:"total_export_kvah" json:"total_export_kvah"`
	TotalImportKVar float64   `bun:"total_import_kvar" json:"total_import_kvar"`
	TotalExportKVar float64   `bun:"total_export_kvar" json:"total_export_kvar"`
	// optional: per meter type fields if stackByMeterType=true

	// dynamic stacked values: e.g., PSS_import_kwh, BSP_export_kvah
	Extra map[string]float64 `json:"-"`
}

// MarshalJSON allows Extra fields to be serialized dynamically
func (t TimeSeriesReading) MarshalJSON() ([]byte, error) {
	type Alias TimeSeriesReading
	aux := make(map[string]interface{})

	// copy static fields
	aux["date"] = t.Date
	aux["total_import_kwh"] = t.TotalImportKWh
	aux["total_export_kwh"] = t.TotalExportKWh
	aux["total_import_kvah"] = t.TotalImportKVah
	aux["total_export_kvah"] = t.TotalExportKVah
	aux["total_import_kvar"] = t.TotalImportKVar
	aux["total_export_kvar"] = t.TotalExportKVar

	// add dynamic stacked fields
	for k, v := range t.Extra {
		aux[k] = v
	}

	return json.Marshal(aux)
}

type ByMeterTypeReading struct {
	MeterType       string  `bun:"meter_type" json:"meter_type"`
	TotalImportKWh  float64 `bun:"total_import_kwh" json:"total_import_kwh"`
	TotalExportKWh  float64 `bun:"total_export_kwh" json:"total_export_kwh"`
	TotalImportKVah float64 `bun:"total_import_kvah" json:"total_import_kvah"`
	TotalExportKVah float64 `bun:"total_export_kvah" json:"total_export_kvah"`
	TotalImportKVar float64 `bun:"total_import_kvar" json:"total_import_kvar"`
	TotalExportKVar float64 `bun:"total_export_kvar" json:"total_export_kvar"`
	ReadingCount    int     `bun:"reading_count" json:"reading_count"`
}

type AggregatedResult struct {
	Aggregated  AggregatedReading    `json:"aggregated"`
	TimeSeries  []TimeSeriesReading  `json:"timeSeries"`
	ByMeterType []ByMeterTypeReading `json:"byMeterType,omitempty"`
	MeterTypes  []string             `json:"meterTypes,omitempty"`
}

type AggregatedQueryParams struct {
	DateFrom         string
	DateTo           string
	Regions          []string
	Districts        []string
	Stations         []string
	Voltages         []float64
	Locations        []string
	BoundaryPoints   []string
	MeterTypes       []string
	GroupBy          string // "day"
	StackByMeterType bool
}

type DailyConsumptionResults struct {
	ConsumptionDate       time.Time `bun:"consumption_date" json:"consumption_date"`
	MeterNumber           string    `bun:"meter_number" json:"meter_number"`
	DayStartReading       float64   `bun:"day_start_reading" json:"day_start_reading"`
	DayEndReading         float64   `bun:"day_end_reading" json:"day_end_reading"`
	ConsumedEnergy        float64   `bun:"consumed_energy" json:"consumed_energy"`
	SystemName            string    `bun:"system_name" json:"system_name"`
	Region                string    `bun:"region" json:"region,omitempty"`
	District              string    `bun:"district" json:"district,omitempty"`
	Station               string    `bun:"station" json:"station,omitempty"`
	Location              string    `bun:"location" json:"location,omitempty"`
	FeederPanelName       string    `bun:"feeder_panel_name" json:"feeder_panel_name,omitempty"`
	IC_OG                 string    `bun:"ic_og" json:"ic_og,omitempty"`
	VoltageKv             string    `bun:"voltage_kv" json:"voltage_kv,omitempty"`
	MultiplyFactor        string    `bun:"multiply_factor" json:"multiply_factor,omitempty"`
	MeterType             string    `bun:"meter_type" json:"meter_type,omitempty"`
	BoundaryMeteringPoint string    `bun:"boundary_metering_point" json:"boundary_metering_point,omitempty"`
}

type ReadingFilterParams struct {
	MeterNumber           []string
	Regions               []string
	Districts             []string
	Stations              []string
	Locations             []string
	Voltages              []string
	BoundaryMeteringPoint []string
	MeterTypes            []string
	DateFrom              time.Time
	DateTo                time.Time
	// Feeder secondary filters
	SendingRegions     []string
	SendingDistricts   []string
	ReceivingRegions   []string
	ReceivingDistricts []string
}

type AggregatedConsumptionResult struct {
	SystemName            *string `bun:"system_name" json:"system_name,omitempty"`
	BoundaryMeteringPoint *string `bun:"boundary_metering_point" json:"boundary_metering_point,omitempty"`
	Region                *string `bun:"region" json:"region,omitempty"`
	District              *string `bun:"district" json:"district,omitempty"`
	Station               *string `bun:"station" json:"station,omitempty"`
	FeederPanelName       *string `bun:"feeder_panel_name" json:"feeder_panel_name,omitempty"`
	Location              string  `bun:"location" json:"location,omitempty"`
	IC_OG                 string  `bun:"ic_og" json:"ic_og,omitempty"`

	ActiveMeters          int `bun:"active_meters" json:"active_meters,omitempty"`
	TotalMeterCount       int `bun:"total_meter_count" json:"total_meter_count,omitempty"`
	TotalMetersByRegion   int `bun:"total_meters_by_region" json:"total_meters_by_region,omitempty"`
	TotalMetersByDistrict int `bun:"total_meters_by_district" json:"total_meters_by_district,omitempty"`
	AllMetersCount        int `bun:"all_meters_count" json:"all_meters_count,omitempty"`

	MeterType        *string   `bun:"meter_type" json:"meter_type,omitempty"`
	GroupPeriod      time.Time `bun:"group_period" json:"group_period"`
	TotalConsumption float64   `bun:"total_consumption" json:"total_consumption"`
}

type MeterStatusResult struct {
	ConsumptionDate       *time.Time `bun:"consumption_date" json:"consumption_date"`
	LastConsumptionDate   *time.Time `bun:"last_consumption_date" json:"last_consumption_date,omitempty"`
	MeterNumber           string     `bun:"meter_number" json:"meter_number"`
	MeterType             *string    `bun:"meter_type" json:"meter_type,omitempty"`
	BoundaryMeteringPoint *string    `bun:"boundary_metering_point" json:"boundary_metering_point,omitempty"`
	Station               *string    `bun:"station" json:"station,omitempty"`
	FeederPanelName       *string    `bun:"feeder_panel_name" json:"feeder_panel_name,omitempty"`
	Location              string     `bun:"location" json:"location,omitempty"`
	Region                string     `bun:"region" json:"region,omitempty"`
	District              string     `bun:"district" json:"district,omitempty"`
	Status                string     `bun:"status" json:"status"`
	Consumption           *float64   `bun:"consumption" json:"consumption"`
	TotalConsumption      *float64   `bun:"total_consumption" json:"total_consumption,omitempty"`
	ReadingCount          *int       `bun:"reading_count" json:"reading_count"`
	TotalReadingCount     *int       `bun:"total_reading_count" json:"total_reading_count,omitempty"`
	DayStartTime          *time.Time `bun:"day_start_time" json:"day_start_time"`
	FirstDayStartTime     *time.Time `bun:"first_day_start_time" json:"first_day_start_time,omitempty"`
	DayEndTime            *time.Time `bun:"day_end_time" json:"day_end_time"`
	LastDayEndTime        *time.Time `bun:"last_day_end_time" json:"last_day_end_time,omitempty"`
}

// Add these to models/meter.go

// MeterStatusSummary represents aggregated status counts and metrics
type MeterStatusSummary struct {
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

// MeterStatusTimelineEntry represents daily status counts
type MeterStatusTimelineEntry struct {
	Date    time.Time `bun:"date" json:"date"`
	Online  int       `bun:"online" json:"online"`
	Offline int       `bun:"offline" json:"offline"`
	Total   int       `bun:"total" json:"total"`
}

// MeterStatusTimeline represents the timeline response
type MeterStatusTimeline struct {
	Data      []MeterStatusTimelineEntry `json:"data"`
	DateRange struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"date_range"`
}

// MeterStatusDetailRecord represents a single meter's aggregated status over a date range
type MeterStatusDetailRecord struct {
	MeterNumber         string     `bun:"meter_number" json:"meter_number"`
	MeterType           *string    `bun:"meter_type" json:"meter_type"`
	Region              string     `bun:"region" json:"region,omitempty"`
	District            string     `bun:"district" json:"district,omitempty"`
	Station             *string    `bun:"station" json:"station,omitempty"`
	FeederPanelName     *string    `bun:"feeder_panel_name" json:"feeder_panel_name,omitempty"`
	Location            string     `bun:"location" json:"location,omitempty"`
	Voltage             string     `bun:"voltage_kv" json:"voltage_kv,omitempty"`
	IC_OG               string     `bun:"ic_og" json:"ic_og,omitempty"`
	BoundaryPoint       *string    `bun:"boundary_metering_point" json:"boundary_metering_point,omitempty"`
	Status              string     `bun:"status" json:"status,omitempty"`
	LastConsumptionDate *time.Time `bun:"last_consumption_date" json:"last_consumption_date"`
	TotalConsumptionKWh float64    `bun:"total_consumption_kwh" json:"total_consumption_kwh"`
	UptimePercentage    float64    `bun:"uptime_percentage" json:"uptime_percentage"`
	DaysOffline         int        `bun:"days_offline" json:"days_offline"`
	LastReadingTime     *time.Time `bun:"last_reading_time" json:"last_reading_time"`
}

// MeterStatusDetailResponse represents paginated detail response
type MeterStatusDetailResponse struct {
	Data       []MeterStatusDetailRecord `json:"data"`
	Pagination struct {
		Page         int  `json:"page"`
		Limit        int  `json:"limit"`
		TotalRecords int  `json:"total_records"`
		TotalPages   int  `json:"total_pages"`
		HasMore      bool `json:"has_more"`
	} `json:"pagination"`
	FiltersApplied map[string]interface{} `json:"filters_applied"`
}

// ConsumptionByRegionEntry represents consumption aggregated by region for a date
type ConsumptionByRegionEntry struct {
	Date                   time.Time `bun:"date" json:"date"`
	Region                 string    `bun:"region" json:"region"`
	TotalConsumptionKWh    float64   `bun:"total_consumption_kwh" json:"total_consumption_kwh"`
	MeterCount             int       `bun:"meter_count" json:"meter_count"`
	AvgConsumptionPerMeter float64   `bun:"avg_consumption_per_meter" json:"avg_consumption_per_meter"`
}

// ConsumptionByRegionResponse represents the by-region response
type ConsumptionByRegionResponse struct {
	Data    []ConsumptionByRegionEntry `json:"data"`
	Summary struct {
		TotalConsumptionKWh float64 `json:"total_consumption_kwh"`
		UniqueRegions       int     `json:"unique_regions"`
		DateRange           struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"date_range"`
	} `json:"summary"`
}

// MeterHealthMetrics represents health breakdown
type MeterHealthMetrics struct {
	TotalMeters            int                 `json:"total_meters"`
	HealthyMeters          int                 `json:"healthy_meters"`
	WarningMeters          int                 `json:"warning_meters"`
	CriticalMeters         int                 `json:"critical_meters"`
	HealthPercentage       float64             `json:"health_percentage"`
	AvgUptime              float64             `json:"avg_uptime"`
	MetersWithNoData7Days  int                 `json:"meters_with_no_data_7days"`
	MetersWithNoData30Days int                 `json:"meters_with_no_data_30days"`
	BreakdownByType        []MeterHealthByType `json:"breakdown_by_type"`
}

// MeterHealthByType represents health metrics per meter type
type MeterHealthByType struct {
	MeterType string `bun:"meter_type" json:"meter_type"`
	Total     int    `bun:"total" json:"total"`
	Healthy   int    `bun:"healthy" json:"healthy"`
	Warning   int    `bun:"warning" json:"warning"`
	Critical  int    `bun:"critical" json:"critical"`
}

// StatusDetailQueryParams for paginated details
type StatusDetailQueryParams struct {
	ReadingFilterParams // Embed existing filter params
	Page                int
	Limit               int
	Search              string
	Status              string
	SortBy              string
	SortOrder           string
}

// MeterWithServiceArea represents a meter with its associated service area
type MeterWithServiceArea struct {
	// Meter fields
	ID                    string     `bun:"id" json:"id"`
	MeterNumber           string     `bun:"meter_number" json:"meter_number"`
	MeterType             string     `bun:"meter_type" json:"meter_type"`
	SPN                   *string    `bun:"spn" json:"spn,omitempty"`
	MeterBrand            *string    `bun:"meter_brand" json:"meter_brand,omitempty"`
	Location              *string    `bun:"location" json:"location,omitempty"`
	DigitalAddress        *string    `bun:"digital_address" json:"digital_address,omitempty"`
	Status                *string    `bun:"status" json:"status,omitempty"`
	MeteringPoint         *string    `bun:"metering_point" json:"metering_point,omitempty"`
	BoundaryMeteringPoint *string    `bun:"boundary_metering_point" json:"boundary_metering_point,omitempty"`
	Incomer               *string    `bun:"incomer" json:"incomer,omitempty"`
	Region                *string    `bun:"region" json:"region,omitempty"`
	District              *string    `bun:"district" json:"district,omitempty"`
	Station               *string    `bun:"station" json:"station,omitempty"`
	CreatedAt             *time.Time `bun:"created_at" json:"created_at,omitempty"`
	UpdatedAt             *time.Time `bun:"updated_at" json:"updated_at,omitempty"`
	MultiplyFactor        *float64   `bun:"multiply_factor" json:"multiply_factor,omitempty"`
	CTRatioPrimary        *float64   `bun:"ct_ratio_primary" json:"ct_ratio_primary,omitempty"`
	CTRatioSecondary      *float64   `bun:"ct_ratio_secondary" json:"ct_ratio_secondary,omitempty"`
	VTRatioPrimary        *float64   `bun:"vt_ratio_primary" json:"vt_ratio_primary,omitempty"`
	VTRatioSecondary      *float64   `bun:"vt_ratio_secondary" json:"vt_ratio_secondary,omitempty"`
	Latitude              *float64   `bun:"latitude" json:"latitude,omitempty"`
	Longitude             *float64   `bun:"longitude" json:"longitude,omitempty"`
	VoltageKV             *float64   `bun:"voltage_kv" json:"voltage_kv,omitempty"`
	FeederPanelName       *string    `bun:"feeder_panel_name" json:"feeder_panel_name,omitempty"`
	IC_OG                 *string    `bun:"ic_og" json:"ic_og,omitempty"`
	UdisID                *string    `bun:"udis_id" json:"udis_id,omitempty"`

	// Service area fields (from dbo_ecg spatial join)
	ServiceAreaDistrict *string `bun:"service_area_district" json:"service_area_district"`
	ServiceAreaRegion   *string `bun:"service_area_region" json:"service_area_region"`
}

// MeterSpatialJoinParams for filtering spatial join results
type MeterSpatialJoinParams struct {
	Page              int
	Limit             int
	MeterTypes        []string
	Regions           []string
	Districts         []string
	ServiceAreaRegion []string // Filter by service area region
	HasCoordinates    *bool    // Filter meters with/without coordinates
	Search            string
	SortBy            string
	SortOrder         string
}

// MeterSpatialCount represents aggregated counts by service area
type MeterSpatialCount struct {
	ServiceAreaRegion   *string `bun:"service_area_region" json:"service_area_region,omitempty"`
	ServiceAreaDistrict *string `bun:"service_area_district" json:"service_area_district,omitempty"`
	MeterType           *string `bun:"meter_type" json:"meter_type,omitempty"`
	TotalMeters         int     `bun:"total_meters" json:"total_meters,omitempty"`
	MetersWithCoords    int     `bun:"meters_with_coords" json:"meters_with_coords,omitempty"`
	MetersInServiceArea int     `bun:"meters_in_service_area" json:"meters_in_service_area,omitempty"`
	MetersMismatched    int     `bun:"meters_mismatched" json:"meters_mismatched,omitempty"`
}

// MeterSpatialCountParams for filtering counts
type MeterSpatialCountParams struct {
	GroupBy    string // "region", "district", "meter_type", or combinations
	MeterTypes []string
	Regions    []string
	Districts  []string
}

// MeterSpatialCountResponse represents the aggregated response
type MeterSpatialCountResponse struct {
	Data    []MeterSpatialCount `json:"data"`
	Summary struct {
		TotalMeters        int     `json:"total_meters,omitempty"`
		TotalRegions       int     `json:"total_regions,omitempty"`
		TotalDistricts     int     `json:"total_districts,omitempty"`
		AvgMetersPerRegion float64 `json:"avg_meters_per_region,omitempty"`
		MismatchPercentage float64 `json:"mismatch_percentage,omitempty"`
	} `json:"summary"`
}

// MeterWithServiceAreaResult represents the query result for spatial queries
type MeterWithServiceAreaResult struct {
	Data []MeterWithServiceArea `json:"data"`
	Meta any                    `json:"meta"`
}

// MeterConsumption represents individual meter consumption
type MeterConsumption struct {
	MeterNumber           string  `bun:"meter_number" json:"meter_number"`
	MeterType             string  `bun:"meter_type" json:"meter_type"`
	Location              string  `bun:"location" json:"location,omitempty"`
	Region                string  `bun:"region" json:"region,omitempty"`
	District              string  `bun:"district" json:"district,omitempty"`
	Station               string  `bun:"station" json:"station,omitempty"`
	FeederPanelName       string  `bun:"feeder_panel_name" json:"feeder_panel_name,omitempty"`
	VoltagekV             string  `bun:"voltage_kv" json:"voltage_kv,omitempty"`
	MeteringPoint         *string `bun:"metering_point" json:"metering_point,omitempty"`
	BoundaryMeteringPoint *string `bun:"boundary_metering_point" json:"boundary_metering_point,omitempty"`
	TotalImportKwh        float64 `bun:"total_import_kwh" json:"total_import_kwh"`
	TotalExportKwh        float64 `bun:"total_export_kwh" json:"total_export_kwh"`
	ReadingCount          int     `bun:"reading_count" json:"reading_count"`
}

// MeterTypeConsumers represents top/bottom consumers per meter type
type MeterTypeConsumers struct {
	MeterType            string            `json:"meter_type"`
	MeterCount           int               `json:"meter_count"`
	TopImportConsumer    *MeterConsumption `json:"top_import_consumer"`
	BottomImportConsumer *MeterConsumption `json:"bottom_import_consumer"`
	TopExportConsumer    *MeterConsumption `json:"top_export_consumer"`
	BottomExportConsumer *MeterConsumption `json:"bottom_export_consumer"`
}

// MeterHealthSummary represents overall meter health statistics
type MeterHealthSummary struct {
	TotalMeters             int                      `json:"total_meters"`
	OnlineMeters            int                      `json:"online_meters"`
	OfflineMeters           int                      `json:"offline_meters"`
	HealthPercentage        float64                  `json:"health_percentage"`
	AverageUptimePercentage float64                  `json:"average_uptime_percentage"`
	ByMeterType             []MeterHealthByMeterType `json:"by_meter_type,omitempty"`
	UptimeDistribution      MeterUptimeDistribution  `json:"uptime_distribution"`
}

// MeterHealthByMeterType represents health stats per meter type
type MeterHealthByMeterType struct {
	MeterType string  `bun:"meter_type" json:"meter_type"`
	Total     int     `bun:"total" json:"total"`
	Online    int     `bun:"online" json:"online"`
	Offline   int     `bun:"offline" json:"offline"`
	AvgUptime float64 `bun:"avg_uptime" json:"avg_uptime"`
}

// MeterUptimeDistribution represents uptime categories
type MeterUptimeDistribution struct {
	Excellent int `json:"excellent"` // >95% uptime
	Good      int `json:"good"`      // 80-95%
	Poor      int `json:"poor"`      // 60-80%
	Critical  int `json:"critical"`  // <60%
}

// MeterHealthDetailParams for filtering and pagination
type MeterHealthDetailParams struct {
	ReadingFilterParams
	Page           int
	Limit          int
	Search         string
	HealthCategory string // "excellent", "good", "poor", "critical", "online", "offline"
	SortBy         string // "meter_number", "uptime", "meter_type", "last_seen"
	SortOrder      string
}

// MeterHealthDetailRecord represents a single meter's health details
type MeterHealthDetailRecord struct {
	MeterNumber             string     `bun:"meter_number" json:"meter_number"`
	MeterType               *string    `bun:"meter_type" json:"meter_type"`
	Region                  string     `bun:"region" json:"region,omitempty"`
	District                string     `bun:"district" json:"district,omitempty"`
	Station                 *string    `bun:"station" json:"station,omitempty"`
	FeederPanelName         *string    `bun:"feeder_panel_name" json:"feeder_panel_name,omitempty"`
	Location                string     `bun:"location" json:"location,omitempty"`
	VoltageKv               string     `bun:"voltage_kv" json:"voltage_kv,omitempty"`
	BoundaryPoint           *string    `bun:"boundary_metering_point" json:"boundary_metering_point,omitempty"`
	Status                  string     `bun:"status" json:"status"`                   // "ONLINE" or "OFFLINE"
	HealthCategory          string     `bun:"health_category" json:"health_category"` // "excellent", "good", "poor", "critical"
	UptimePercentage        float64    `bun:"uptime_percentage" json:"uptime_percentage"`
	DaysOnline              int        `bun:"days_online" json:"days_online"`
	DaysOffline             int        `bun:"days_offline" json:"days_offline"`
	TotalDays               int        `bun:"total_days" json:"total_days"`
	LastSeenDate            *time.Time `bun:"last_seen_date" json:"last_seen_date"`
	TotalConsumptionKWh     float64    `bun:"total_consumption_kwh" json:"total_consumption_kwh"`
	AverageDailyConsumption float64    `bun:"avg_daily_consumption" json:"avg_daily_consumption"`
}

// MeterHealthDetailResponse represents paginated health detail response
type MeterHealthDetailResponse struct {
	Data       []MeterHealthDetailRecord `json:"data"`
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

// RegionalMapParams for filtering regional map data
type RegionalMapParams struct {
	DateFrom  time.Time
	DateTo    time.Time
	MeterType []string
	Region    string
	District  string
	Location  string
}

// DistrictMapConsumption represents consumption for a district on a specific date
type DistrictMapConsumptionByType struct {
	Date                  time.Time `bun:"consumption_date" json:"date"`
	MeterType             string    `bun:"meter_type" json:"meter_type"`
	MeterNumber           string    `bun:"meter_number" json:"meter_number"`
	Station               string    `bun:"station" json:"station,omitempty"`
	VoltageKV             string    `bun:"voltage_kv" json:"voltage_kv,omitempty"`
	Location              string    `bun:"location" json:"location,omitempty"`
	IC_OG                 string    `bun:"ic_og" json:"ic_og,omitempty"`
	BoundaryMeteringPoint string    `bun:"boundary_metering_point" json:"boundary_metering_point,omitempty"`
	FeederPanelName       string    `bun:"feeder_panel_name" json:"feeder_panel_name,omitempty"`
	TotalImportKWh        float64   `bun:"total_import_kwh" json:"total_import_kwh"`
	TotalExportKWh        float64   `bun:"total_export_kwh" json:"total_export_kwh"`
	NetConsumptionKWh     float64   `json:"net_consumption_kwh"`
}

// DistrictMapData represents a district with its GeoJSON and consumption timeseries
type DistrictMapData struct {
	District   string                         `bun:"district" json:"district"`
	Region     string                         `bun:"region" json:"region"`
	GeoJSON    json.RawMessage                `bun:"geojson" json:"geojson"`
	Timeseries []DistrictMapConsumptionByType `json:"timeseries"`
}

// RegionalMapResponse represents the complete response
type RegionalMapResponse struct {
	Districts []DistrictMapData `json:"districts"`
}

// DistrictGeometry represents simplified district boundary with center point
type DistrictGeometry struct {
	DistrictCode string          `bun:"district_code" json:"district_code"`
	District     string          `bun:"district" json:"district"`
	Region       string          `bun:"region" json:"region"`
	CenterLat    float64         `bun:"center_lat" json:"center_lat"`
	CenterLng    float64         `bun:"center_lng" json:"center_lng"`
	GeoJSON      json.RawMessage `bun:"geojson" json:"geojson"`
}

// DistrictGeometryResponse represents the geometry API response
type DistrictGeometryResponse struct {
	Version   string             `json:"version"`
	Districts []DistrictGeometry `json:"districts"`
}

// DistrictTimeseriesEntry represents daily consumption for a district
type DistrictTimeseriesEntry struct {
	Timestamp         time.Time `bun:"timestamp" json:"timestamp"`
	TotalImportKWh    float64   `bun:"total_import_kwh" json:"total_import_kwh"`
	TotalExportKWh    float64   `bun:"total_export_kwh" json:"total_export_kwh"`
	NetConsumptionKWh float64   `json:"net_consumption_kwh"`
}

// DistrictTimeseriesData represents timeseries for a single district
type DistrictTimeseriesData struct {
	District   string                    `bun:"district" json:"district"`
	Region     string                    `bun:"region" json:"region"`
	Timeseries []DistrictTimeseriesEntry `json:"timeseries"`
}

// DistrictTimeseriesResponse represents the timeseries API response
type DistrictTimeseriesResponse struct {
	Districts []DistrictTimeseriesData `json:"districts"`
}

// DistrictConsumptionParams for filtering district consumption
type DistrictConsumptionParams struct {
	DateFrom  time.Time
	DateTo    time.Time
	MeterType []string
	Region    []string
	District  []string
}

type EnergyBalanceParams struct {
	DateFrom              time.Time
	DateTo                time.Time
	MeterNumber           []string
	Regions               []string
	Districts             []string
	Stations              []string
	Locations             []string
	BoundaryMeteringPoint []string
	MeterTypes            []string // BSP, DTX, REGIONAL_BOUNDARY
	Voltages              []float64
}

// BoundaryMeterDetail shows individual boundary meter information
type BoundaryMeterDetail struct {
	MeterNumber           string  `json:"meter_number"`
	BoundaryMeteringPoint string  `json:"boundary_metering_point,omitempty"` // Raw value for reference
	ConnectedRegion       string  `json:"connected_region,omitempty"`        // Parsed and validated
	Location              string  `json:"location,omitempty"`
	VoltageKV             float64 `json:"voltage_kv,omitempty"`
	ImportKWh             float64 `json:"import_kwh"`
	ExportKWh             float64 `json:"export_kwh"`
	NetFlow               float64 `json:"net_flow"` // Positive = net import, Negative = net export
	ReadingCount          int     `json:"reading_count"`
	DataQuality           string  `json:"data_quality"` // "complete", "partial", "incomplete"
}

// InternalConsumptionDetail breaks down internal generation vs distribution
type InternalConsumptionDetail struct {
	BSPImport     float64 `json:"bsp_import"`      // Bulk Supply Points
	DTXImport     float64 `json:"dtx_import"`      // Distribution Transformers
	NetInternal   float64 `json:"net_internal"`    // BSP - DTX
	BSPMeterCount int     `json:"bsp_meter_count"` // Coverage indicator
	DTXMeterCount int     `json:"dtx_meter_count"` // Coverage indicator
}

// CrossBoundaryFlowDetail provides boundary flow information
type CrossBoundaryFlowDetail struct {
	BoundaryMeters          []BoundaryMeterDetail       `json:"boundary_meters"`           // Individual meter details
	ByConnectedRegion       map[string]RegionFlowDetail `json:"by_connected_region"`       // Aggregated by validated regions only
	TotalImportKWh          float64                     `json:"total_import_kwh"`          // Total import from boundaries
	TotalExportKWh          float64                     `json:"total_export_kwh"`          // Total export to boundaries
	NetCrossBoundaryFlow    float64                     `json:"net_cross_boundary_flow"`   // Net import (positive) or export (negative)
	BoundaryMeterCount      int                         `json:"boundary_meter_count"`      // Number of boundary meters
	IsNetImporter           bool                        `json:"is_net_importer"`           // True if net importing
	CrossBoundaryDependency float64                     `json:"cross_boundary_dependency"` // % of consumption from cross-boundary
	DominantFlowDirection   string                      `json:"dominant_flow_direction"`
}

// BalanceAnalysis provides insights into regional energy balance
type BalanceAnalysis struct {
	InternalSufficiency   float64  `json:"internal_sufficiency"`    // % of consumption met internally
	CrossBoundaryReliance float64  `json:"cross_boundary_reliance"` // % reliance on cross-boundary flows
	BalanceType           string   `json:"balance_type"`            // "self_sufficient", "net_importer", "net_exporter", "balanced"
	Flags                 []string `json:"flags,omitempty"`         // Notable conditions
}

// RegionalEnergyBalance represents complete energy balance for a region on a date
type RegionalEnergyBalance struct {
	Region              string                    `json:"region"`
	Date                time.Time                 `json:"date"`
	InternalConsumption InternalConsumptionDetail `json:"internal_consumption"`
	CrossBoundaryFlows  CrossBoundaryFlowDetail   `json:"cross_boundary_flows"`
	TotalNetConsumption float64                   `json:"total_net_consumption"`
	BalanceAnalysis     BalanceAnalysis           `json:"balance_analysis"`
}

// EnergyBalanceResponse represents the complete API response
type EnergyBalanceResponse struct {
	Data    []RegionalEnergyBalance `json:"data"`
	Summary struct {
		DateRange struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"date_range"`
		TotalRegions int `json:"total_regions"`
		Metrics      struct {
			TotalBSPImport          float64 `json:"total_bsp_import"`
			TotalDTXImport          float64 `json:"total_dtx_import"`
			TotalInternalNet        float64 `json:"total_internal_net"`
			TotalCrossBoundaryNet   float64 `json:"total_cross_boundary_net"`
			TotalNetConsumption     float64 `json:"total_net_consumption"`
			AverageDailyConsumption float64 `json:"average_daily_consumption"`
		} `json:"metrics"`
	} `json:"summary"`
}

// RegionalEnergyBalanceSummary represents aggregated balance by region (all dates combined)
type RegionalEnergyBalanceSummary struct {
	Region                  string  `json:"region"`
	TotalBSPImport          float64 `json:"total_bsp_import"`
	TotalDTXImport          float64 `json:"total_dtx_import"`
	TotalInternalNet        float64 `json:"total_internal_net"`
	TotalCrossBoundaryNet   float64 `json:"total_cross_boundary_net"`
	TotalNetConsumption     float64 `json:"total_net_consumption"`
	DayCount                int     `json:"day_count"`
	AverageDailyConsumption float64 `json:"average_daily_consumption"`
}

// Updated models with location breakdown

// LocationFlowDetail shows flow details for a specific location
type LocationFlowDetail struct {
	Location  string  `json:"location"`
	ImportKWh float64 `json:"import_kwh"`
	ExportKWh float64 `json:"export_kwh"`
	NetFlow   float64 `json:"net_flow"`
}

// RegionFlowDetail shows aggregated flow with a specific neighboring region
type RegionFlowDetail struct {
	ConnectedRegion     string                        `json:"connected_region"`
	TotalImportFromThem float64                       `json:"total_import_from_them"`
	TotalExportToThem   float64                       `json:"total_export_to_them"`
	NetFlow             float64                       `json:"net_flow"`
	FlowBalance         string                        `json:"flow_balance"` // "importing", "exporting", "balanced"
	ByLocation          map[string]LocationFlowDetail `json:"by_location"`  // Breakdown by location
}

// RegionGeometry represents simplified regional boundary with center point
type RegionGeometry struct {
	RegionCode string          `bun:"region_code" json:"region_code,omitempty"`
	Region     string          `bun:"region" json:"region"`
	CenterLat  float64         `bun:"center_lat" json:"center_lat"`
	CenterLng  float64         `bun:"center_lng" json:"center_lng"`
	GeoJSON    json.RawMessage `bun:"geojson" json:"geojson"`
}

// RegionGeometryResponse represents the geometry API response
type RegionGeometryResponse struct {
	Version string           `json:"version"`
	Regions []RegionGeometry `json:"regions"`
}

// // BoundaryMeteringPointWithLocations represents a boundary metering point and its associated locations
// type BoundaryMeteringPointWithLocations struct {
// 	BoundaryMeteringPoint string   `json:"boundary_metering_point"`
// 	Locations             []string `json:"locations"`
// }

// // DistrictWithBoundaries represents a district with its boundary metering points
// type DistrictWithBoundaries struct {
// 	DistrictName           string                                `json:"district_name"`
// 	BoundaryMeteringPoints []BoundaryMeteringPointWithLocations  `json:"boundary_metering_points"`
// 	MeterCount             int                                   `json:"meter_count"`
// }

// // RegionMetadata contains comprehensive metadata for a region
// type RegionMetadata struct {
// 	Region                         string                                `json:"region"`
// 	ECGDistricts                   []string                              `json:"ecg_districts"`                       // Districts from ECG spatial boundaries
// 	MeterDistricts                 []string                              `json:"meter_districts"`                     // Districts from meter assignments
// 	Stations                       []string                              `json:"stations"`                            // For BSP, DTX, PSS, SS meters
// 	MeterTypes                     []string                              `json:"meter_types"`
// 	RegionalBoundaryMeteringPoints []BoundaryMeteringPointWithLocations  `json:"regional_boundary_metering_points"`   // REGIONAL_BOUNDARY with locations
// 	Districts                      []DistrictWithBoundaries              `json:"districts"`                           // Districts with DISTRICT_BOUNDARY points
// 	TotalMeterCount                int                                   `json:"total_meter_count"`
// 	MeterCountByType               map[string]int                        `json:"meter_count_by_type"`
// }

// // DistrictMetadata contains comprehensive metadata for a district within a region
// type DistrictMetadata struct {
// 	Region                 string                                `json:"region"`
// 	District               string                                `json:"district"`
// 	Stations               []string                              `json:"stations"`
// 	MeterTypes             []string                              `json:"meter_types"`
// 	BoundaryMeteringPoints []BoundaryMeteringPointWithLocations  `json:"boundary_metering_points"`   // DISTRICT_BOUNDARY with locations
// 	VoltageLevels          []float64                             `json:"voltage_levels"`
// 	TotalMeterCount        int                                   `json:"total_meter_count"`
// 	MeterCountByType       map[string]int                        `json:"meter_count_by_type"`
// }

type ExpressFeederDailyConsumptionResult struct {
	ConsumptionDate time.Time `bun:"consumption_date" json:"consumption_date"`
	MeterNumber     string    `bun:"meter_number" json:"meter_number"`
	DayStartReading float64   `bun:"day_start_reading" json:"day_start_reading"`
	DayEndReading   float64   `bun:"day_end_reading" json:"day_end_reading"`
	ConsumedEnergy  float64   `bun:"consumed_energy" json:"consumed_energy"`
	SystemName      string    `bun:"system_name" json:"system_name"`
	MeterType       string    `bun:"meter_type" json:"meter_type,omitempty"`
	MultiplyFactor  string    `bun:"multiply_factor" json:"multiply_factor,omitempty"`
	VoltageKv       string    `bun:"voltage_kv" json:"voltage_kv,omitempty"`
	// Sending end
	SapVersion           string `bun:"sap_version" json:"sap_version,omitempty"`
	FeederName           string `bun:"feeder_name" json:"feeder_name,omitempty"`
	SendingStation       string `bun:"sending_station" json:"sending_station,omitempty"`
	SendingTypeOfStation string `bun:"sending_type_of_station" json:"sending_type_of_station,omitempty"`
	SendingCode          string `bun:"sending_code" json:"sending_code,omitempty"`
	SendingRegion        string `bun:"sending_region" json:"sending_region,omitempty"`
	SendingDistrict      string `bun:"sending_district" json:"sending_district,omitempty"`
	// Receiving end
	ReceivingStation       string `bun:"receiving_station" json:"receiving_station,omitempty"`
	ReceivingTypeOfStation string `bun:"receiving_type_of_station" json:"receiving_type_of_station,omitempty"`
	ReceivingCode          string `bun:"receiving_code" json:"receiving_code,omitempty"`
	ReceivingRegion        string `bun:"receiving_region" json:"receiving_region,omitempty"`
	ReceivingDistrict      string `bun:"receiving_district" json:"receiving_district,omitempty"`
	Comments               string `bun:"comments" json:"comments,omitempty"`
}

type ExpressFeederFilterParams struct {
	SendingRegions     []string
	SendingDistricts   []string
	ReceivingRegions   []string
	ReceivingDistricts []string
}

type ExpressFeederMeterDetail struct {
	MeterType      string  `json:"meter_type,omitempty"`
	MultiplyFactor string  `json:"multiply_factor,omitempty"`
	VoltageKv      string  `json:"voltage_kv,omitempty"`
	ImportKwh      float64 `json:"import_kwh"`
	ExportKwh      float64 `json:"export_kwh"`
	NetKwh         float64 `json:"net_kwh"`
}

type ExpressFeederDailyResult struct {
	ConsumptionDate        time.Time                 `json:"consumption_date"`
	FeederName             string                    `json:"feeder_name"`
	SapVersion             string                    `json:"sap_version"`
	Comments               string                    `json:"comments,omitempty"`
	SendingMeterNumber     string                    `json:"sending_meter_number,omitempty"`
	SendingStation         string                    `json:"sending_station,omitempty"`
	SendingTypeOfStation   string                    `json:"sending_type_of_station,omitempty"`
	SendingCode            string                    `json:"sending_code,omitempty"`
	SendingRegion          string                    `json:"sending_region,omitempty"`
	SendingDistrict        string                    `json:"sending_district,omitempty"`
	ReceivingMeterNumber   string                    `json:"receiving_meter_number,omitempty"`
	ReceivingStation       string                    `json:"receiving_station,omitempty"`
	ReceivingTypeOfStation string                    `json:"receiving_type_of_station,omitempty"`
	ReceivingCode          string                    `json:"receiving_code,omitempty"`
	ReceivingRegion        string                    `json:"receiving_region,omitempty"`
	ReceivingDistrict      string                    `json:"receiving_district,omitempty"`
	SendingMeter           *ExpressFeederMeterDetail `json:"sending_meter"`
	ReceivingMeter         *ExpressFeederMeterDetail `json:"receiving_meter"`
}

type ExpressFeederMeterAgg struct {
	ImportKwh float64 `json:"import_kwh"`
	ExportKwh float64 `json:"export_kwh"`
	NetKwh    float64 `json:"net_kwh"`
}

type ExpressFeederAggregatedConsumptionResult struct {
	GroupPeriod            time.Time              `json:"group_period"`
	FeederName             string                 `json:"feeder_name,omitempty"`
	SapVersion             string                 `json:"sap_version,omitempty"`
	MeterType              string                 `json:"meter_type,omitempty"`
	SendingMeterNumber     string                 `json:"sending_meter_number,omitempty"`
	SendingStation         string                 `json:"sending_station,omitempty"`
	SendingTypeOfStation   string                 `json:"sending_type_of_station,omitempty"`
	SendingCode            string                 `json:"sending_code,omitempty"`
	SendingRegion          string                 `json:"sending_region,omitempty"`
	SendingDistrict        string                 `json:"sending_district,omitempty"`
	ReceivingMeterNumber   string                 `json:"receiving_meter_number,omitempty"`
	ReceivingStation       string                 `json:"receiving_station,omitempty"`
	ReceivingTypeOfStation string                 `json:"receiving_type_of_station,omitempty"`
	ReceivingCode          string                 `json:"receiving_code,omitempty"`
	ReceivingRegion        string                 `json:"receiving_region,omitempty"`
	ReceivingDistrict      string                 `json:"receiving_district,omitempty"`
	SendingMeter           *ExpressFeederMeterAgg `json:"sending_meter"`
	ReceivingMeter         *ExpressFeederMeterAgg `json:"receiving_meter"`
}


