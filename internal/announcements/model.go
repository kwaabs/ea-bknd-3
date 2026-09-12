package announcements

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// KindRegular announcements roll on the dashboard marquee, same as before
// this field existed. KindSpecial ones are pulled out of the marquee
// entirely and shown only via the announcements dialog (see
// sql/announcements_kind.sql) — a separate, higher-visibility channel for
// things the marquee's constant rotation risks burying.
const (
	KindRegular = "regular"
	KindSpecial = "special"
)

// Announcement maps to app.announcements — shared dashboard marquee messages
// (kind=regular) and dialog-only special announcements (kind=special).
type Announcement struct {
	bun.BaseModel `bun:"table:app.announcements,alias:ann"`

	ID          uuid.UUID `bun:"id,pk,type:uuid,default:gen_random_uuid()" json:"id"`
	Body        string    `bun:"body,notnull" json:"body"`
	AuthorEmail string    `bun:"author_email,notnull" json:"author_email"`
	AuthorName  *string   `bun:"author_name" json:"author_name,omitempty"`
	Kind        string    `bun:"kind,notnull,default:'regular'" json:"kind"`
	Active      bool      `bun:"active,notnull,default:true" json:"active"`
	CreatedAt   time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt   time.Time `bun:"updated_at,notnull,default:current_timestamp" json:"updated_at"`
}

type CreateAnnouncementRequest struct {
	Body        string  `json:"body"`
	AuthorEmail string  `json:"author_email"`
	AuthorName  *string `json:"author_name,omitempty"`
	// Kind is optional; empty means KindRegular (see Service.Create).
	Kind string `json:"kind,omitempty"`
}

type DeleteAnnouncementRequest struct {
	AuthorEmail string `json:"author_email"`
}

type ListResponse struct {
	Success bool            `json:"success"`
	Count   int             `json:"count"`
	Data    []*Announcement `json:"data"`
}

type MessageResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type SingleResponse struct {
	Success bool          `json:"success"`
	Data    *Announcement `json:"data"`
}
