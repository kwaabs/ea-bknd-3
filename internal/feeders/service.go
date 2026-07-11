// package services

// import (
// 	"bknd-3/internal/models"
// 	"context"
// 	"fmt"
// 	"github.com/uptrace/bun"
// )

// type Service struct {
// 	db *bun.DB
// }

// func NewService(db *bun.DB) *Service {
// 	return &Service{db: db}
// }

// // Get11kVFeeders retrieves feeders from 11kV OH and UG tables
// func (s *Service) Get11kVFeeders(ctx context.Context, params FeederFilterParams) ([]Feeder, error) {
// 	var feeders []Feeder

// 	// Query for 11kV OH conductors
// 	q11OH := s.db.NewSelect().
// 		ColumnExpr("'OH' as orientation").
// 		ColumnExpr("circuit_id").
// 		ColumnExpr("phase_configuration").
// 		ColumnExpr("conductor_type").
// 		ColumnExpr("ST_AsGeoJSON(the_geom)::json as geometry").
// 		TableExpr("app.dbo_oh_conductor_11kv_evw")

// 	// Query for 11kV UG cables
// 	q11UG := s.db.NewSelect().
// 		ColumnExpr("'UG' as orientation").
// 		ColumnExpr("circuit_id").
// 		ColumnExpr("phase_configuration").
// 		ColumnExpr("conductor_type").
// 		ColumnExpr("ST_AsGeoJSON(the_geom)::json as geometry").
// 		TableExpr("app.dbo_ug_cable_11kv_evw")

// 	// Apply filters to both queries
// 	s.applyFilters(q11OH, params)
// 	s.applyFilters(q11UG, params)

// 	// Combine with UNION ALL
// 	err := s.db.NewSelect().
// 		TableExpr("(?) UNION ALL (?)", q11OH, q11UG).
// 		Scan(ctx, &feeders)

// 	if err != nil {
// 		return nil, fmt.Errorf("failed to query 11kV feeders: %w", err)
// 	}

// 	return feeders, nil
// }

// // Get33kVFeeders retrieves feeders from 33kV OH and UG tables
// func (s *Service) Get33kVFeeders(ctx context.Context, params FeederFilterParams) ([]Feeder, error) {
// 	var feeders []Feeder

// 	// Query for 33kV OH conductors
// 	q33OH := s.db.NewSelect().
// 		ColumnExpr("'OH' as orientation").
// 		ColumnExpr("circuit_id").
// 		ColumnExpr("phase_configuration").
// 		ColumnExpr("conductor_type").
// 		ColumnExpr("ST_AsGeoJSON(the_geom)::json as geometry").
// 		TableExpr("app.dbo_oh_conductor_33kv_evw")

// 	// Query for 33kV UG cables
// 	q33UG := s.db.NewSelect().
// 		ColumnExpr("'UG' as orientation").
// 		ColumnExpr("circuit_id").
// 		ColumnExpr("phase_configuration").
// 		ColumnExpr("conductor_type").
// 		ColumnExpr("ST_AsGeoJSON(the_geom)::json as geometry").
// 		TableExpr("app.dbo_ug_cable_33kv_evw")

// 	// Apply filters to both queries
// 	s.applyFilters(q33OH, params)
// 	s.applyFilters(q33UG, params)

// 	// Combine with UNION ALL
// 	err := s.db.NewSelect().
// 		TableExpr("(?) UNION ALL (?)", q33OH, q33UG).
// 		Scan(ctx, &feeders)

// 	if err != nil {
// 		return nil, fmt.Errorf("failed to query 33kV feeders: %w", err)
// 	}

// 	return feeders, nil
// }

// // GetAllFeeders retrieves feeders from all OH and UG tables (11kV and 33kV)
// func (s *Service) GetAllFeeders(ctx context.Context, params FeederFilterParams) ([]Feeder, error) {
// 	var feeders []Feeder

// 	// Query for 11kV OH conductors
// 	q11OH := s.db.NewSelect().
// 		ColumnExpr("'OH' as orientation").
// 		ColumnExpr("circuit_id").
// 		ColumnExpr("phase_configuration").
// 		ColumnExpr("conductor_type").
// 		ColumnExpr("ST_AsGeoJSON(the_geom)::json as geometry").
// 		TableExpr("app.dbo_oh_conductor_11kv_evw")

// 	// Query for 11kV UG cables
// 	q11UG := s.db.NewSelect().
// 		ColumnExpr("'UG' as orientation").
// 		ColumnExpr("circuit_id").
// 		ColumnExpr("phase_configuration").
// 		ColumnExpr("conductor_type").
// 		ColumnExpr("ST_AsGeoJSON(the_geom)::json as geometry").
// 		TableExpr("app.dbo_ug_cable_11kv_evw")

// 	// Query for 33kV OH conductors
// 	q33OH := s.db.NewSelect().
// 		ColumnExpr("'OH' as orientation").
// 		ColumnExpr("circuit_id").
// 		ColumnExpr("phase_configuration").
// 		ColumnExpr("conductor_type").
// 		ColumnExpr("ST_AsGeoJSON(the_geom)::json as geometry").
// 		TableExpr("app.dbo_oh_conductor_33kv_evw")

// 	// Query for 33kV UG cables
// 	q33UG := s.db.NewSelect().
// 		ColumnExpr("'UG' as orientation").
// 		ColumnExpr("circuit_id").
// 		ColumnExpr("phase_configuration").
// 		ColumnExpr("conductor_type").
// 		ColumnExpr("ST_AsGeoJSON(the_geom)::json as geometry").
// 		TableExpr("app.dbo_ug_cable_33kv_evw")

// 	// Apply filters to all queries
// 	queries := []*bun.SelectQuery{q11OH, q11UG, q33OH, q33UG}
// 	for _, q := range queries {
// 		s.applyFilters(q, params)
// 	}


// 	// Combine all with UNION ALL
// 	err := s.db.NewSelect().
// 		TableExpr("(?) UNION ALL (?) UNION ALL (?) UNION ALL (?)", q11OH, q11UG, q33OH, q33UG).
// 		Scan(ctx, &feeders)

// 	if err != nil {
// 		return nil, fmt.Errorf("failed to query feeders: %w", err)
// 	}

// 	return feeders, nil
// }

// // GetFeedersByVoltage retrieves feeders for a specific voltage level (11kV or 33kV)
// func (s *Service) GetFeedersByVoltage(ctx context.Context, voltage int, params FeederFilterParams) ([]Feeder, error) {
// 	switch voltage {
// 	case 11:
// 		return s.Get11kVFeeders(ctx, params)
// 	case 33:
// 		return s.Get33kVFeeders(ctx, params)
// 	default:
// 		return nil, fmt.Errorf("invalid voltage level: %d (expected 11 or 33)", voltage)
// 	}
// }

// // GetFeederByCircuitID retrieves a specific feeder by circuit ID
// func (s *Service) GetFeederByCircuitID(ctx context.Context, circuitID string) (*Feeder, error) {
// 	params := FeederFilterParams{
// 		CircuitIDs: []string{circuitID},
// 	}

// 	feeders, err := s.GetAllFeeders(ctx, params)
// 	if err != nil {
// 		return nil, err
// 	}

// 	if len(feeders) == 0 {
// 		return nil, fmt.Errorf("feeder not found: %s", circuitID)
// 	}

// 	return &feeders[0], nil
// }

// // applyFilters applies filter parameters to a query
// func (s *Service) applyFilters(q *bun.SelectQuery, params FeederFilterParams) {
// 	if len(params.CircuitIDs) > 0 {
// 		q.Where("circuit_id IN (?)", bun.In(params.CircuitIDs))
// 	}

// 	if len(params.PhaseConfigs) > 0 {
// 		q.Where("phase_configuration IN (?)", bun.In(params.PhaseConfigs))
// 	}

// 	if len(params.ConductorTypes) > 0 {
// 		q.Where("conductor_type IN (?)", bun.In(params.ConductorTypes))
// 	}
// }

// // GetFeederStats retrieves summary statistics for feeders
// func (s *Service) GetFeederStats(ctx context.Context, params FeederFilterParams) (map[string]interface{}, error) {
// 	feeders, err := s.GetAllFeeders(ctx, params)
// 	if err != nil {
// 		return nil, err
// 	}

// 	stats := make(map[string]interface{})
// 	stats["total"] = len(feeders)

// 	// Count by orientation
// 	orientationCounts := make(map[string]int)
// 	for _, f := range feeders {
// 		orientationCounts[f.Orientation]++
// 	}
// 	stats["by_orientation"] = orientationCounts

// 	// Count by phase configuration
// 	phaseConfigCounts := make(map[string]int)
// 	for _, f := range feeders {
// 		phaseConfigCounts[f.PhaseConfig]++
// 	}
// 	stats["by_phase_config"] = phaseConfigCounts

// 	// Count by conductor type
// 	conductorTypeCounts := make(map[string]int)
// 	for _, f := range feeders {
// 		conductorTypeCounts[f.ConductorType]++
// 	}
// 	stats["by_conductor_type"] = conductorTypeCounts

// 	return stats, nil
// }


package feeders

import (
	"context"
	"fmt"
	"github.com/uptrace/bun"
)

type Service struct {
	db *bun.DB
}

func NewService(db *bun.DB) *Service {
	return &Service{db: db}
}

// Get11kVFeeders retrieves feeders from 11kV OH and UG tables
func (s *Service) Get11kVFeeders(ctx context.Context, params FeederFilterParams) ([]Feeder, error) {
	var feeders []Feeder

	// Query for 11kV OH conductors
	q11OH := s.db.NewSelect().
		ColumnExpr("'OH' as orientation").
		ColumnExpr("circuit_id").
		ColumnExpr("phase_configuration").
		ColumnExpr("conductor_type").
		ColumnExpr("ST_AsGeoJSON(the_geom)::jsonb as geometry").
		TableExpr("app.dbo_oh_conductor_11kv_evw")

	// Query for 11kV UG cables
	q11UG := s.db.NewSelect().
		ColumnExpr("'UG' as orientation").
		ColumnExpr("circuit_id").
		ColumnExpr("phase_configuration").
		ColumnExpr("conductor_type").
		ColumnExpr("ST_AsGeoJSON(the_geom)::jsonb as geometry").
		TableExpr("app.dbo_ug_cable_11kv_evw")

	// Apply filters to both queries
	s.applyFilters(q11OH, params)
	s.applyFilters(q11UG, params)

	// Combine with UNION ALL — alias required for PG15 compatibility
	err := s.db.NewSelect().
		TableExpr("((?) UNION ALL (?)) AS combined", q11OH, q11UG).
		Scan(ctx, &feeders)

	if err != nil {
		return nil, fmt.Errorf("failed to query 11kV feeders: %w", err)
	}

	return feeders, nil
}

// Get33kVFeeders retrieves feeders from 33kV OH and UG tables
func (s *Service) Get33kVFeeders(ctx context.Context, params FeederFilterParams) ([]Feeder, error) {
	var feeders []Feeder

	// Query for 33kV OH conductors
	q33OH := s.db.NewSelect().
		ColumnExpr("'OH' as orientation").
		ColumnExpr("circuit_id").
		ColumnExpr("phase_configuration").
		ColumnExpr("conductor_type").
		ColumnExpr("ST_AsGeoJSON(the_geom)::jsonb as geometry").
		TableExpr("app.dbo_oh_conductor_33kv_evw")

	// Query for 33kV UG cables
	q33UG := s.db.NewSelect().
		ColumnExpr("'UG' as orientation").
		ColumnExpr("circuit_id").
		ColumnExpr("phase_configuration").
		ColumnExpr("conductor_type").
		ColumnExpr("ST_AsGeoJSON(the_geom)::jsonb as geometry").
		TableExpr("app.dbo_ug_cable_33kv_evw")

	// Apply filters to both queries
	s.applyFilters(q33OH, params)
	s.applyFilters(q33UG, params)

	// Combine with UNION ALL — alias required for PG15 compatibility
	err := s.db.NewSelect().
		TableExpr("((?) UNION ALL (?)) AS combined", q33OH, q33UG).
		Scan(ctx, &feeders)

	if err != nil {
		return nil, fmt.Errorf("failed to query 33kV feeders: %w", err)
	}

	return feeders, nil
}

// GetAllFeeders retrieves feeders from all OH and UG tables (11kV and 33kV)
func (s *Service) GetAllFeeders(ctx context.Context, params FeederFilterParams) ([]Feeder, error) {
	var feeders []Feeder

	// Query for 11kV OH conductors
	q11OH := s.db.NewSelect().
		ColumnExpr("'OH' as orientation").
		ColumnExpr("circuit_id").
		ColumnExpr("phase_configuration").
		ColumnExpr("conductor_type").
		ColumnExpr("ST_AsGeoJSON(the_geom)::jsonb as geometry").
		TableExpr("app.dbo_oh_conductor_11kv_evw")

	// Query for 11kV UG cables
	q11UG := s.db.NewSelect().
		ColumnExpr("'UG' as orientation").
		ColumnExpr("circuit_id").
		ColumnExpr("phase_configuration").
		ColumnExpr("conductor_type").
		ColumnExpr("ST_AsGeoJSON(the_geom)::jsonb as geometry").
		TableExpr("app.dbo_ug_cable_11kv_evw")

	// Query for 33kV OH conductors
	q33OH := s.db.NewSelect().
		ColumnExpr("'OH' as orientation").
		ColumnExpr("circuit_id").
		ColumnExpr("phase_configuration").
		ColumnExpr("conductor_type").
		ColumnExpr("ST_AsGeoJSON(the_geom)::jsonb as geometry").
		TableExpr("app.dbo_oh_conductor_33kv_evw")

	// Query for 33kV UG cables
	q33UG := s.db.NewSelect().
		ColumnExpr("'UG' as orientation").
		ColumnExpr("circuit_id").
		ColumnExpr("phase_configuration").
		ColumnExpr("conductor_type").
		ColumnExpr("ST_AsGeoJSON(the_geom)::jsonb as geometry").
		TableExpr("app.dbo_ug_cable_33kv_evw")

	// Apply filters to all queries
	queries := []*bun.SelectQuery{q11OH, q11UG, q33OH, q33UG}
	for _, q := range queries {
		s.applyFilters(q, params)
	}

	// Combine all with UNION ALL — alias required for PG15 compatibility
	err := s.db.NewSelect().
		TableExpr("((?) UNION ALL (?) UNION ALL (?) UNION ALL (?)) AS combined", q11OH, q11UG, q33OH, q33UG).
		Scan(ctx, &feeders)

	if err != nil {
		return nil, fmt.Errorf("failed to query feeders: %w", err)
	}

	return feeders, nil
}

// GetFeedersByVoltage retrieves feeders for a specific voltage level (11kV or 33kV)
func (s *Service) GetFeedersByVoltage(ctx context.Context, voltage int, params FeederFilterParams) ([]Feeder, error) {
	switch voltage {
	case 11:
		return s.Get11kVFeeders(ctx, params)
	case 33:
		return s.Get33kVFeeders(ctx, params)
	default:
		return nil, fmt.Errorf("invalid voltage level: %d (expected 11 or 33)", voltage)
	}
}

// GetFeederByCircuitID retrieves a specific feeder by circuit ID
func (s *Service) GetFeederByCircuitID(ctx context.Context, circuitID string) (*Feeder, error) {
	params := FeederFilterParams{
		CircuitIDs: []string{circuitID},
	}

	feeders, err := s.GetAllFeeders(ctx, params)
	if err != nil {
		return nil, err
	}

	if len(feeders) == 0 {
		return nil, fmt.Errorf("feeder not found: %s", circuitID)
	}

	return &feeders[0], nil
}

// applyFilters applies filter parameters to a query
func (s *Service) applyFilters(q *bun.SelectQuery, params FeederFilterParams) {
	if len(params.CircuitIDs) > 0 {
		q.Where("circuit_id IN (?)", bun.In(params.CircuitIDs))
	}

	if len(params.PhaseConfigs) > 0 {
		q.Where("phase_configuration IN (?)", bun.In(params.PhaseConfigs))
	}

	if len(params.ConductorTypes) > 0 {
		q.Where("conductor_type IN (?)", bun.In(params.ConductorTypes))
	}
}

// GetFeederStats retrieves summary statistics for feeders
func (s *Service) GetFeederStats(ctx context.Context, params FeederFilterParams) (map[string]interface{}, error) {
	feeders, err := s.GetAllFeeders(ctx, params)
	if err != nil {
		return nil, err
	}

	stats := make(map[string]interface{})
	stats["total"] = len(feeders)

	// Count by orientation
	orientationCounts := make(map[string]int)
	for _, f := range feeders {
		orientationCounts[f.Orientation]++
	}
	stats["by_orientation"] = orientationCounts

	// Count by phase configuration
	phaseConfigCounts := make(map[string]int)
	for _, f := range feeders {
		phaseConfigCounts[f.PhaseConfig]++
	}
	stats["by_phase_config"] = phaseConfigCounts

	// Count by conductor type
	conductorTypeCounts := make(map[string]int)
	for _, f := range feeders {
		conductorTypeCounts[f.ConductorType]++
	}
	stats["by_conductor_type"] = conductorTypeCounts

	return stats, nil
}
