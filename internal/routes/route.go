package routes

import (
	"bknd-3/internal/amrcustomer"
	"bknd-3/internal/announcements"
	"bknd-3/internal/auth"
	"bknd-3/internal/botconsumption"
	"bknd-3/internal/bxcconsumption"
	"bknd-3/internal/cache"
	"bknd-3/internal/comments"
	"bknd-3/internal/config"
	"bknd-3/internal/etl"
	"bknd-3/internal/feedback"
	"bknd-3/internal/feeders"
	"bknd-3/internal/handlers"
	"bknd-3/internal/logger"
	"bknd-3/internal/loginstats"
	"bknd-3/internal/meters"
	authmw "bknd-3/internal/middleware"
	"bknd-3/internal/mmssales"
	"bknd-3/internal/notifyemail"
	"bknd-3/internal/pnsconsumption"
	"bknd-3/internal/salessummary"
	"bknd-3/internal/serviceareas"
	"bknd-3/internal/services"
	"bknd-3/internal/zeusbilling"
	"bknd-3/internal/zeussales"

	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/go-chi/cors"
)

func NewRouter(db *bun.DB, cfg *config.Config, logr *logger.Logger, c cache.Cache, etlEngine *etl.Engine) (http.Handler, *services.AuthService) {
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

	// Gates the meters-admin write endpoints (Create/Update/SoftDelete) —
	// first real use of JWTAuth in this codebase; existing /meters reads
	// stay unauthenticated.
	authMW := authmw.NewAuthMiddleware(jwtMgr.PublicKey(), authSvc, logr.Logger)

	authHandler := handlers.NewAuthHandler(authSvc, logr, cfg)
	notifyEmailSvc := notifyemail.NewService(db)
	meterHandler := meters.NewHandler(meters.NewService(db, notifyEmailSvc), logr.Logger)
	meterMetricsHandler := handlers.NewMeterMetricsHandler(meterMetricsSvc, logr.Logger)
	etlHandler := etl.NewHandler(etl.NewService(db, notifyEmailSvc, etlEngine, cfg.ETLCredentialsKey), logr.Logger)

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
				// New, improved Zeus source data (app.zeus_sales) — additive,
				// does not read from or replace customer-sales-zeus above.
				r.Mount("/zeus-billing", zeusbilling.Routes(db, logr.Logger))
				// Bot-ingested consumption source (app.bot_consumption) —
				// independent of Zeus/MMS/AMR, no shared keys with either.
				r.Mount("/bot-consumption", botconsumption.Routes(db, logr.Logger))
				// Another bot-ingested legacy consumption source
				// (app.bxc_consumption), structurally identical to
				// bot-consumption above but its own independent table.
				r.Mount("/bxc-consumption", bxcconsumption.Routes(db, logr.Logger))
				// PNS-ingested legacy consumption source
				// (app.pns_consumption) — independent of Zeus/MMS/AMR/BOT/
				// BXC. Unlike those, region/district here are only
				// available as opaque regionid/districtid codes (no name
				// lookup exists yet) — see the package doc comment.
				r.Mount("/pns-consumption", pnsconsumption.Routes(db, logr.Logger))
				// Canonical cross-source Prepaid/Postpaid totals — merges
				// Zeus/MMS/BOT/BXC (and whatever's added next) server-side
				// so every frontend consumer reads one number instead of
				// each re-deriving its own "Zeus + MMS" copy by hand. See
				// internal/salessummary's package doc for why this exists.
				r.Mount("/customer-sales-summary", salessummary.Routes(db, logr.Logger))
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

		// Meter management (add/edit/retire) — notify-emails allowlist
		// only, identity derived from the JWT session rather than a
		// client-supplied field. Kept as its own route group (not nested
		// under the public /meters block above) so JWTAuth only applies
		// here.
		r.Route("/meters/admin", func(r chi.Router) {
			r.Use(authMW.JWTAuth)
			r.Post("/", meterHandler.CreateMeter)
			r.Put("/{id}", meterHandler.UpdateMeter)
			r.Delete("/{id}", meterHandler.SoftDeleteMeter)
		})

		// Express-feeder pairing CRUD — same allowlist/JWT gating as
		// /meters/admin above. app.express_feeders pairs two meters
		// (sending/receiving) under one feeder_name; it has no admin
		// surface anywhere else in this codebase.
		r.Route("/express-feeders/admin", func(r chi.Router) {
			r.Use(authMW.JWTAuth)
			r.Get("/", meterHandler.ListExpressFeeders)
			r.Post("/", meterHandler.CreateExpressFeeder)
			r.Put("/{id}", meterHandler.UpdateExpressFeeder)
			r.Delete("/{id}", meterHandler.SoftDeleteExpressFeeder)
		})

		// ETL admin — CRUD for app.etl_sources/app.etl_jobs, ad-hoc
		// read-only test queries, run-now, and run history. Same
		// allowlist/JWT gating as /meters/admin and /express-feeders/admin
		// above. See internal/etl and ETL.md.
		r.Route("/etl/admin", func(r chi.Router) {
			r.Use(authMW.JWTAuth)
			r.Get("/sources", etlHandler.ListSources)
			r.Post("/sources", etlHandler.CreateSource)
			r.Put("/sources/{id}", etlHandler.UpdateSource)
			r.Delete("/sources/{id}", etlHandler.DeleteSource)
			r.Post("/sources/{id}/test-connection", etlHandler.TestSourceConnection)
			r.Post("/sources/test-connection", etlHandler.TestSourceConnectionDraft)

			r.Get("/jobs", etlHandler.ListJobs)
			r.Post("/jobs", etlHandler.CreateJob)
			r.Put("/jobs/{id}", etlHandler.UpdateJob)
			r.Delete("/jobs/{id}", etlHandler.DeleteJob)
			r.Post("/jobs/{id}/run", etlHandler.RunJobNow)
			r.Get("/jobs/{id}/runs", etlHandler.ListJobRuns)
			r.Get("/jobs/{id}/state", etlHandler.GetJobState)

			r.Get("/dest-tables", etlHandler.ListDestTables)
			r.Get("/dest-tables/{table}/columns", etlHandler.ListDestTableColumns)

			r.Post("/test-query", etlHandler.TestQuery)
		})

		r.Mount("/feeders", feeders.Routes(db, logr.Logger))
		r.Mount("/feedback", feedback.Routes(db, logr.Logger))

		r.Route("/energy-balance", func(r chi.Router) {
			// In your router setup, add these routes:
			r.Get("/regional", meterHandler.GetRegionalEnergyBalance)
			r.Get("/regional/summary", meterHandler.GetRegionalEnergyBalanceSummary)
		})

		// The shared notify-emails allowlist that gates /meters/admin,
		// /express-feeders/admin, and /announcements' write routes — see
		// internal/notifyemail. JWTAuth here so ContextUserIDKey is
		// populated for every route in this package, including /me.
		r.Route("/notify-emails", func(r chi.Router) {
			r.Use(authMW.JWTAuth)
			r.Mount("/", notifyemail.Routes(db, notifyEmailSvc, logr.Logger))
		})

		r.Mount("/comments", comments.Routes(db, logr.Logger))
		r.Mount("/announcements", announcements.Routes(db, notifyEmailSvc, logr.Logger))
		r.Mount("/admin/login-stats", loginstats.Routes(db, logr.Logger))
		r.Mount("/service-areas", serviceareas.Routes(db, logr.Logger))
		// Same Redis response cache as Zeus/MMS consumption aggregates.
		r.Mount("/amr", amrcustomer.Routes(db, logr.Logger, cacheMW))

	})

	return r, authSvc
}
