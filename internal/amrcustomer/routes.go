package amrcustomer

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
	"go.uber.org/zap"
)

// Routes wires the whole domain and returns a router ready to Mount.
//
// In the app router:
//
//	r.Mount("/amr", amrcustomer.Routes(db, logr.Logger))
func Routes(db *bun.DB, log *zap.Logger, mw ...func(http.Handler) http.Handler) chi.Router {
	h := NewHandler(NewService(db), log)

	r := chi.NewRouter()
	for _, m := range mw {
		r.Use(m)
	}

	r.Get("/consumption/daily", h.GetDailyConsumption)
	r.Get("/consumption/aggregate", h.GetAggregatedConsumption)

	r.Get("/status", h.GetMeterStatus)
	r.Get("/status/summary", h.GetMeterStatusSummary)
	r.Get("/status/timeline", h.GetMeterStatusTimeline)
	r.Get("/status/details", h.GetMeterStatusDetails)

	r.Get("/health/summary", h.GetMeterHealthSummary)
	r.Get("/health/details", h.GetMeterHealthDetails)

	r.Get("/meters/{meterNumber}", h.GetMeterByNumber)

	r.Get("/filters/regions", h.GetRegions)
	r.Get("/filters/districts", h.GetDistricts)
	r.Get("/filters/communities", h.GetCommunities)
	r.Get("/filters/tariff-classes", h.GetTariffClasses)
	r.Get("/filters/contract-statuses", h.GetContractStatuses)
	r.Get("/filters/customer-types", h.GetCustomerTypes)
	r.Get("/filters/service-types", h.GetServiceTypes)

	return r
}
