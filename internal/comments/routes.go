package comments

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
//	r.Mount("/comments", comments.Routes(db, logr.Logger))
func Routes(db *bun.DB, log *zap.Logger, mw ...func(http.Handler) http.Handler) chi.Router {
	h := NewHandler(NewService(db, log), log)

	r := chi.NewRouter()
	for _, m := range mw {
		r.Use(m)
	}

	r.Get("/", h.ListComments)
	r.Post("/", h.CreateComment)
	r.Get("/{id}/replies", h.ListReplies)
	r.Post("/{id}/replies", h.CreateReply)
	r.Patch("/{id}", h.EditComment)
	r.Delete("/{id}", h.DeleteComment)
	r.Post("/{id}/reactions", h.ToggleReaction)
	r.Patch("/{id}/resolve", h.ResolveComment)

	r.Get("/users/mentionable", h.GetMentionableUsers)

	return r
}
