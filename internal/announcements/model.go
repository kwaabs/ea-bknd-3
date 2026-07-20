package announcements

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Announcement maps to app.announcements — shared dashboard marquee messages.
type Announcement struct {
	bun.BaseModel `bun:"table:app.announcements,alias:ann"`

	ID          uuid.UUID `bun:"id,pk,type:uuid,default:gen_random_uuid()" json:"id"`
	Body        string    `bun:"body,notnull" json:"body"`
	AuthorEmail string    `bun:"author_email,notnull" json:"author_email"`
	AuthorName  *string   `bun:"author_name" json:"author_name,omitempty"`
	Active      bool      `bun:"active,notnull,default:true" json:"active"`
	CreatedAt   time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt   time.Time `bun:"updated_at,notnull,default:current_timestamp" json:"updated_at"`
}

type CreateAnnouncementRequest struct {
	Body        string  `json:"body"`
	AuthorEmail string  `json:"author_email"`
	AuthorName  *string `json:"author_name,omitempty"`
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
