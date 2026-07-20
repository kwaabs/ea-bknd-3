package announcements

import (
	"net/http"

	"bknd-3/internal/config"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
	"go.uber.org/zap"
)

// Routes wires the announcements domain.
//
//	r.Mount("/announcements", announcements.Routes(db, cfg, logr.Logger))
func Routes(db *bun.DB, cfg *config.Config, log *zap.Logger, mw ...func(http.Handler) http.Handler) chi.Router {
	h := NewHandler(NewService(db, cfg.NotifyEmails), log)

	r := chi.NewRouter()
	for _, m := range mw {
		r.Use(m)
	}

	r.Get("/", h.ListActive)
	r.Post("/", h.Create)
	r.Delete("/{id}", h.SoftDelete)

	return r
}
