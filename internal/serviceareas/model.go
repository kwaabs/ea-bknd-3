package serviceareas

import (
	"github.com/uptrace/bun"
)

// ServiceArea represents an ECG service area with geographic boundaries
type ServiceArea struct {
	bun.BaseModel `bun:"table:dbo_ecg,alias:sa"`

	ID       int     `bun:"id,pk" json:"id"`
	Region   *string `bun:"region" json:"region"`
	District *string `bun:"district" json:"district"`
	TheGeom  string  `bun:"the_geom,type:geometry" json:"the_geom"` // PostGIS geometry as string
}

// ServiceAreaGeoJSON represents a service area in GeoJSON format
type ServiceAreaGeoJSON struct {
	ID         int                    `json:"id"`
	Region     *string                `json:"region"`
	District   *string                `json:"district"`
	Type       string                 `json:"type"`
	Geometry   map[string]interface{} `json:"geometry"`
	Properties map[string]interface{} `json:"properties"`
}

// ServiceAreasResponse represents the API response
type ServiceAreasResponse struct {
	Type     string               `json:"type"` // "FeatureCollection"
	Features []ServiceAreaGeoJSON `json:"features"`
	Count    int                  `json:"count"`
}

// ServiceAreaQueryParams for filtering service areas
type ServiceAreaQueryParams struct {
	Regions   []string
	Districts []string
}
