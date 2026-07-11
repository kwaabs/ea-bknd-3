package serviceareas

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/uptrace/bun"
)

type Service struct {
	db *bun.DB
}

func NewService(db *bun.DB) *Service {
	return &Service{db: db}
}

// GetServiceAreas returns ECG service areas with geographic boundaries
func (s *Service) GetServiceAreas(
	ctx context.Context,
	params ServiceAreaQueryParams,
) (*ServiceAreasResponse, error) {

	// Build query with ST_AsGeoJSON to convert PostGIS geometry to GeoJSON
	q := s.db.NewSelect().
		Column("id").
		Column("region").
		Column("district").
		ColumnExpr("ST_AsGeoJSON(the_geom) as geojson").
		TableExpr("app.dbo_ecg AS dr").
		Where("the_geom IS NOT NULL") // Only return areas with valid geometry

	// Apply filters
	if len(params.Regions) > 0 {
		// Convert to lowercase for case-insensitive comparison
		lowerRegions := make([]string, len(params.Regions))
		for i, r := range params.Regions {
			lowerRegions[i] = strings.ToLower(r)
		}
		q = q.Where("LOWER(region) IN (?)", bun.In(lowerRegions))
	}

	if len(params.Districts) > 0 {
		// Convert to lowercase for case-insensitive comparison
		lowerDistricts := make([]string, len(params.Districts))
		for i, d := range params.Districts {
			lowerDistricts[i] = strings.ToLower(d)
		}
		q = q.Where("LOWER(district) IN (?)", bun.In(lowerDistricts))
	}

	// Order by region and district for consistent results
	q = q.OrderExpr("region ASC, district ASC")

	// Execute query
	var results []struct {
		ID       int     `bun:"id"`
		Region   *string `bun:"region"`
		District *string `bun:"district"`
		GeoJSON  string  `bun:"geojson"`
	}

	if err := q.Scan(ctx, &results); err != nil {
		return nil, err
	}

	// Convert to GeoJSON FeatureCollection format
	features := make([]ServiceAreaGeoJSON, 0, len(results))

	for _, row := range results {
		// Parse the GeoJSON geometry string
		var geometry map[string]interface{}
		if err := json.Unmarshal([]byte(row.GeoJSON), &geometry); err != nil {
			// Skip invalid geometries
			continue
		}

		// Build properties
		properties := map[string]interface{}{
			"id":       row.ID,
			"region":   row.Region,
			"district": row.District,
		}

		// Create GeoJSON feature
		feature := ServiceAreaGeoJSON{
			ID:         row.ID,
			Region:     row.Region,
			District:   row.District,
			Type:       "Feature",
			Geometry:   geometry,
			Properties: properties,
		}

		features = append(features, feature)
	}

	// Build response as GeoJSON FeatureCollection
	response := &ServiceAreasResponse{
		Type:     "FeatureCollection",
		Features: features,
		Count:    len(features),
	}

	return response, nil
}

// GetServiceAreaByID returns a single service area by ID
func (s *Service) GetServiceAreaByID(
	ctx context.Context,
	id int,
) (*ServiceAreaGeoJSON, error) {

	var result struct {
		ID       int     `bun:"id"`
		Region   *string `bun:"region"`
		District *string `bun:"district"`
		GeoJSON  string  `bun:"geojson"`
	}

	err := s.db.NewSelect().
		Column("id").
		Column("region").
		Column("district").
		ColumnExpr("ST_AsGeoJSON(the_geom) as geojson").
		TableExpr("app.dbo_ecg AS sa").
		Where("id = ?", id).
		Where("the_geom IS NOT NULL").
		Scan(ctx, &result)

	if err != nil {
		return nil, err
	}

	// Parse the GeoJSON geometry string
	var geometry map[string]interface{}
	if err := json.Unmarshal([]byte(result.GeoJSON), &geometry); err != nil {
		return nil, err
	}

	// Build properties
	properties := map[string]interface{}{
		"id":       result.ID,
		"region":   result.Region,
		"district": result.District,
	}

	// Create GeoJSON feature
	feature := &ServiceAreaGeoJSON{
		ID:         result.ID,
		Region:     result.Region,
		District:   result.District,
		Type:       "Feature",
		Geometry:   geometry,
		Properties: properties,
	}

	return feature, nil
}

// GetUniqueRegions returns a list of unique regions
func (s *Service) GetUniqueRegions(ctx context.Context) ([]string, error) {
	var regions []string

	err := s.db.NewSelect().
		ColumnExpr("DISTINCT region").
		TableExpr("app.dbo_ecg").
		Where("region IS NOT NULL").
		OrderExpr("region ASC").
		Scan(ctx, &regions)

	return regions, err
}

// GetUniqueDistricts returns a list of unique districts, optionally filtered by region
func (s *Service) GetUniqueDistricts(
	ctx context.Context,
	region string,
) ([]string, error) {
	var districts []string

	q := s.db.NewSelect().
		ColumnExpr("DISTINCT district").
		TableExpr("app.dbo_ecg").
		Where("district IS NOT NULL")

	if region != "" {
		q = q.Where("LOWER(region) = ?", strings.ToLower(region))
	}

	err := q.OrderExpr("district ASC").Scan(ctx, &districts)

	return districts, err
}
