package mmssales

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
	"go.uber.org/zap"
)

// Routes wires the whole domain and returns a router ready to Mount.
// Optional middleware (e.g. the response cache) is applied to all routes
// in this domain.
//
// In the app router:
//
//	r.Mount("/meters/consumption/mms-customer-sales",
//	    mmssales.Routes(db, logr.Logger, cacheMW))
func Routes(db *bun.DB, log *zap.Logger, mw ...func(http.Handler) http.Handler) chi.Router {
	h := NewHandler(NewService(db), log)

	r := chi.NewRouter()
	for _, m := range mw {
		r.Use(m)
	}
	r.Get("/detail", h.Detail)
	r.Get("/aggregate", h.Aggregate)
	return r
}
