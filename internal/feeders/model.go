package feeders

import (
	"encoding/json"
	"github.com/uptrace/bun"
)

// Feeder represents the unified structure from OH and UG conductor/cable tables
type Feeder struct {
	bun.BaseModel  `bun:"table:feeders,alias:f"`
	Orientation    string          `bun:"orientation" json:"orientation"`
	CircuitID      string          `bun:"circuit_id" json:"circuit_id"`
	PhaseConfig    string          `bun:"phase_configuration" json:"phase_configuration"`
	ConductorType  string          `bun:"conductor_type" json:"conductor_type"`
	Geometry       json.RawMessage `bun:"geometry" json:"geometry"` // GeoJSON from ST_AsGeoJSON
}

// FeederFilterParams defines query parameters for filtering feeders
type FeederFilterParams struct {
	Orientations    []string // OH, UG
	CircuitIDs      []string
	PhaseConfigs    []string
	ConductorTypes  []string
	Voltages        []int    // 11, 33 (kV)
}

// FeederQueryResponse represents the response structure
type FeederQueryResponse struct {
	Data  []Feeder `json:"data"`
	Total int      `json:"total"`
}
