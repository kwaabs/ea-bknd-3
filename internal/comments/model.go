package comments

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Reaction represents a single emoji reaction on a comment.
type Reaction struct {
	Emoji   string      `json:"emoji"`
	Count   int         `json:"count"`
	UserIDs []uuid.UUID `json:"user_ids"`
}

// Comment maps to app.comments in Postgres.
type Comment struct {
	bun.BaseModel `bun:"table:app.comments"`

	ID           uuid.UUID   `bun:",pk,type:uuid,default:uuid_generate_v4()" json:"id"`
	Body         string      `bun:",notnull"                                 json:"body"`
	AuthorID     uuid.UUID   `bun:"type:uuid,notnull"                        json:"author_id"`
	AuthorName   string      `bun:"-"                                        json:"author_name"`             // joined from users
	AuthorAvatar *string     `bun:"-"                                        json:"author_avatar,omitempty"` // nullable
	ParentID     *uuid.UUID  `bun:"type:uuid"                                json:"parent_id"`
	Reactions    []Reaction  `bun:"type:jsonb,nullzero"                      json:"reactions"`
	Resolved     bool        `bun:",notnull,default:false"                   json:"resolved"`
	ResolvedBy   *uuid.UUID  `bun:"type:uuid"                                json:"resolved_by"`
	ResolvedAt   *time.Time  `bun:",nullzero"                                json:"resolved_at"`
	Mentions     []uuid.UUID `bun:"type:uuid[],nullzero"                     json:"mentions"`
	Deleted      bool        `bun:",notnull,default:false"                   json:"-"`                     // internal soft-delete flag
	ReplyCount   int         `bun:"-"                                        json:"reply_count,omitempty"` // computed, top-level only
	CreatedAt    time.Time   `bun:",nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt    time.Time   `bun:",nullzero,notnull,default:current_timestamp" json:"updated_at"`
}

// CommentListParams holds query-string filters for GET /api/comments.
type CommentListParams struct {
	Page     int
	Limit    int
	Resolved *bool // nil = no filter
}

// MentionableUser is the lightweight user shape for @mention lookups.
type MentionableUser struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Avatar *string   `json:"avatar,omitempty"`
}
