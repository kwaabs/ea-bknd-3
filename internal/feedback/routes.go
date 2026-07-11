package feedback

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
//	r.Mount("/feedback", feedback.Routes(db, logr.Logger))
func Routes(db *bun.DB, log *zap.Logger, mw ...func(http.Handler) http.Handler) chi.Router {
	h := NewHandler(NewService(db), log)

	r := chi.NewRouter()
	for _, m := range mw {
		r.Use(m)
	}

	r.Post("/", h.CreateFeedback)
	r.Get("/", h.GetAllFeedback)
	r.Get("/user/{email}", h.GetFeedbackByEmail)
	r.Get("/{id}", h.GetFeedbackByID)
	r.Patch("/{id}/status", h.UpdateFeedbackStatus)
	r.Delete("/{id}", h.DeleteFeedback)

	return r
}
