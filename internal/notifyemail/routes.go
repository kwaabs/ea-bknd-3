package notifyemail

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
	"go.uber.org/zap"
)

// Routes wires the notify-emails domain — the shared allowlist backing
// admin-only routes across meters, announcements, and this package's own
// list-management endpoints. Callers should wrap this in JWTAuth (see
// internal/routes/route.go) since every handler here needs the
// authenticated caller's identity.
//
//	r.Mount("/notify-emails", notifyemail.Routes(db, svc, logr.Logger))
func Routes(db *bun.DB, svc *Service, log *zap.Logger, mw ...func(http.Handler) http.Handler) chi.Router {
	h := NewHandler(svc, db, log)

	r := chi.NewRouter()
	for _, m := range mw {
		r.Use(m)
	}

	r.Get("/me", h.Me)
	r.Get("/", h.List)
	r.Post("/", h.Add)
	r.Delete("/{email}", h.Remove)

	return r
}
