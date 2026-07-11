package feeders

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
//	r.Mount("/feeders", feeders.Routes(db, logr.Logger))
func Routes(db *bun.DB, log *zap.Logger, mw ...func(http.Handler) http.Handler) chi.Router {
	h := NewHandler(NewService(db), log)

	r := chi.NewRouter()
	for _, m := range mw {
		r.Use(m)
	}

	r.Get("/", h.GetAllFeeders)
	r.Get("/stats", h.GetFeederStats)
	r.Get("/11kv", h.Get11kVFeeders)
	r.Get("/33kv", h.Get33kVFeeders)
	r.Get("/voltage/{voltage}", h.GetFeedersByVoltage)
	r.Get("/{circuitId}", h.GetFeederByCircuitID)

	return r
}
