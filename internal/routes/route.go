package routes

import (
	"bknd-3/internal/amrcustomer"
	"bknd-3/internal/announcements"
	"bknd-3/internal/auth"
	"bknd-3/internal/cache"
	"bknd-3/internal/comments"
	"bknd-3/internal/config"
	"bknd-3/internal/feedback"
	"bknd-3/internal/feeders"
	"bknd-3/internal/handlers"
	"bknd-3/internal/logger"
	"bknd-3/internal/loginstats"
	"bknd-3/internal/meters"
	"bknd-3/internal/mmssales"
	"bknd-3/internal/serviceareas"
	"bknd-3/internal/services"
	"bknd-3/internal/zeussales"

	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/go-chi/cors"
)

func NewRouter(db *bun.DB, cfg *config.Config, logr *logger.Logger, c cache.Cache) http.Handler {
	r := chi.NewRouter()

	// Response cache for heavy, idempotent GET endpoints. No-op when c is nil.
	cacheMW := cache.Middleware(c, cache.RecencyTTL(cfg.CacheTTLShort, cfg.CacheTTLLong), logr.Logger)

	// Basic middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	// CORS middleware with config
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: cfg.AllowedOrigins,
		AllowedMethods: []string{
			"GET", "POST", "PUT", "DELETE", "OPTIONS",
		},
		AllowedHeaders: []string{
			"Accept", "Authorization", "Content-Type", "X-CSRF-Token",
		},
		ExposedHeaders: []string{
			"Link",
		},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// init JWT manager
	jwtMgr, err := auth.NewJWTManager(cfg.JWTPrivateKeyPath, cfg.JWTPublicKeyPath, "yourapp")
	if err != nil {
		logr.Fatal("failed to init jwt manager", zap.Error(err))
	}

	// auth service (service handles DB checks like token_version)
	authSvc := services.NewAuthService(db, jwtMgr, cfg, logr)
	meterMetricsSvc := services.NewMeterMetricsService(db)

	authHandler := handlers.NewAuthHandler(authSvc, logr, cfg)
	meterHandler := meters.NewHandler(meters.NewService(db), logr.Logger)
	meterMetricsHandler := handlers.NewMeterMetricsHandler(meterMetricsSvc, logr.Logger)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("ok"))
		if err != nil {
			return
		}
	})

	r.Route("/api/v1", func(r chi.Router) {

		r.Route("/auth", func(r chi.Router) {
			// Public routes — refresh/logout use the refresh cookie (or body),
			// not the short-lived access JWT, so they must stay unauthenticated.
			r.Post("/login", authHandler.LoginLocal)
			r.Post("/ldap", authHandler.LoginLDAP)
			r.Post("/azure", authHandler.LoginAzureAD)
			r.Post("/refresh", authHandler.Refresh)
			r.Post("/logout", authHandler.Logout)
		})

		r.Route("/meters", func(r chi.Router) {
			// Basic meter operations (JWT protection can be re-enabled later)
			r.Route("/metadata", func(r chi.Router) {
				r.Get("/regions", meterHandler.GetRegions)
				r.Get("/districts", meterHandler.GetDistricts)
				r.Get("/stations", meterHandler.GetStations)
				r.Get("/locations", meterHandler.GetLocations)

				r.Get("/boundary-points", meterHandler.GetBoundaryPoints)
				r.Get("/voltages", meterHandler.GetVoltages)
			})

			// Geometry endpoints
			r.Get("/geometries/districts", meterHandler.GetDistrictGeometries)
			r.Get("/geometries/regions", meterHandler.GetRegionGeometries)

			// Timeseries endpoints
			r.Get("/consumption/districts-timeseries", meterHandler.GetDistrictTimeseriesConsumption)

			// Basic meter operations
			r.Get("/", meterHandler.QueryMeters)
			r.Get("/{id}", meterHandler.GetMeterByID)

			// ✅ STATUS ENDPOINTS - NEW OPTIMIZED STRUCTURE
			r.Route("/status", func(r chi.Router) {
				// NEW - Phase 1 (Critical)
				r.Get("/summary", meterHandler.GetMeterStatusSummary)   // < 1 KB
				r.Get("/timeline", meterHandler.GetMeterStatusTimeline) // < 50 KB
				r.Get("/details", meterHandler.GetMeterStatusDetails)   // 25 KB per page

				// Keep existing for backward compatibility
				r.Get("/", meterHandler.GetMeterStatus)             // DEPRECATED
				r.Get("/counts", meterHandler.GetMeterStatusCounts) // DEPRECATED
			})

			// ✅ HEALTH ENDPOINT - NEW (Phase 2, Optional)
			r.Route("/health", func(r chi.Router) {
				r.Get("/metrics", meterHandler.GetMeterHealthMetrics)
				r.Get("/summary", meterHandler.GetMeterHealthSummary)
				r.Get("/summary/details", meterHandler.GetMeterHealthDetails)
			})

			// Keep existing readings routes unchanged
			r.Route("/readings", func(r chi.Router) {
				r.Get("/metrics", meterMetricsHandler.GetMeterMetrics)
				r.With(cacheMW).Get("/aggregated", meterHandler.GetAggregatedReadings)
				r.With(cacheMW).Get("/consumption", meterHandler.GetDailyConsumption)
			})

			// customer-sales-zeus routes are registered inside the cached
			// /consumption group below (single source of truth).

			// ✅ CONSUMPTION ENDPOINTS - ENHANCED
			r.Route("/consumption", func(r chi.Router) {
				// Cache all heavy consumption GETs (Redis-backed, gzip). No-op if disabled.
				r.Use(cacheMW)

				// NEW - Phase 2
				r.Get("/by-region", meterHandler.GetConsumptionByRegion)       // Regional supply patterns
				r.Get("/regional-map", meterHandler.GetRegionalMapConsumption) // Regional supply patterns

				// Keep ALL existing routes unchanged
				r.Get("/daily", meterHandler.GetDailyConsumption)
				r.Get("/aggregate", meterHandler.GetAggregatedConsumption)

				r.Get("/daily/regional", meterHandler.GetRegionalBoundaryDailyConsumption)
				r.Get("/aggregate/regional", meterHandler.GetRegionalBoundaryAggregatedConsumption)
				r.Get("/daily/district", meterHandler.GetDistrictBoundaryDailyConsumption)
				r.Get("/aggregate/district", meterHandler.GetDistrictBoundaryAggregatedConsumption)
				r.Get("/daily/bsp", meterHandler.GetBSPDailyConsumption)
				r.Get("/aggregate/bsp", meterHandler.GetBSPAggregatedConsumption)
				r.Get("/daily/pss", meterHandler.GetPSSDailyConsumption)
				r.Get("/aggregate/pss", meterHandler.GetPSSAggregatedConsumption)
				r.Get("/daily/ss", meterHandler.GetSSDailyConsumption)
				r.Get("/aggregate/ss", meterHandler.GetSSAggregatedConsumption)
				r.Get("/daily/feeder-trafo", meterHandler.GetFeederDailyConsumption)
				r.Get("/aggregate/feeder-trafo", meterHandler.GetFeederAggregatedConsumption)
				r.Get("/daily/dtx", meterHandler.GetDTXDailyConsumption)
				r.Get("/aggregate/dtx", meterHandler.GetDTXAggregatedConsumption)
				r.Get("/top-bottom-consumers", meterHandler.GetTopBottomConsumers)
				r.Get("/daily/express-feeder", meterHandler.GetExpressFeederDailyConsumption)
				r.Get("/aggregate/express-feeder", meterHandler.GetExpressFeederAggregatedConsumption)

				r.Mount("/customer-sales-zeus", zeussales.Routes(db, logr.Logger))
				r.Mount("/mms-customer-sales", mmssales.Routes(db, logr.Logger))
			})

			// ✅ NEW: Spatial service area routes
			r.Route("/spatial", func(r chi.Router) {
				r.Get("/", meterHandler.GetMetersWithServiceArea)
				r.Get("/mismatch", meterHandler.GetMeterSpatialMismatch)
				r.Get("/stats", meterHandler.GetMeterSpatialStats)

				// ✅ NEW: Aggregation/count endpoints
				r.Get("/counts", meterHandler.GetMeterSpatialCounts) // Flexible grouping
				r.Get("/counts/by-region", meterHandler.GetMeterSpatialCountsByRegion)
				r.Get("/counts/by-district", meterHandler.GetMeterSpatialCountsByDistrict)
				r.Get("/counts/by-type", meterHandler.GetMeterSpatialCountsByType)
			})
		})

		r.Mount("/feeders", feeders.Routes(db, logr.Logger))
		r.Mount("/feedback", feedback.Routes(db, logr.Logger))

		r.Route("/energy-balance", func(r chi.Router) {
			// In your router setup, add these routes:
			r.Get("/regional", meterHandler.GetRegionalEnergyBalance)
			r.Get("/regional/summary", meterHandler.GetRegionalEnergyBalanceSummary)
		})

		r.Mount("/comments", comments.Routes(db, logr.Logger))
		r.Mount("/announcements", announcements.Routes(db, cfg, logr.Logger))
		r.Mount("/admin/login-stats", loginstats.Routes(db, logr.Logger))
		r.Mount("/service-areas", serviceareas.Routes(db, logr.Logger))
		r.Mount("/amr", amrcustomer.Routes(db, logr.Logger))

	})

	return r
}
