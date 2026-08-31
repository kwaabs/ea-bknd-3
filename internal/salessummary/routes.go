package salessummary

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
//	r.Mount("/meters/consumption/customer-sales-summary",
//	    salessummary.Routes(db, logr.Logger))
//
// GET ?category=prepaid|postpaid&dateFrom=...&dateTo=...&groupBy=region|district&region=...&district=...
func Routes(db *bun.DB, log *zap.Logger, mw ...func(http.Handler) http.Handler) chi.Router {
	h := NewHandler(NewService(db), log)

	r := chi.NewRouter()
	for _, m := range mw {
		r.Use(m)
	}
	r.Get("/", h.Summary)
	return r
}
