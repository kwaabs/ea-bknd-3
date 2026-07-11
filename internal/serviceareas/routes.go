package serviceareas

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
//	r.Mount("/service-areas", serviceareas.Routes(db, logr.Logger))
func Routes(db *bun.DB, log *zap.Logger, mw ...func(http.Handler) http.Handler) chi.Router {
	h := NewHandler(NewService(db), log)

	r := chi.NewRouter()
	for _, m := range mw {
		r.Use(m)
	}

	r.Get("/", h.GetServiceAreas)
	r.Get("/{id}", h.GetServiceAreaByID)
	r.Get("/meta/regions", h.GetRegions)
	r.Get("/meta/districts", h.GetDistricts)

	return r
}
