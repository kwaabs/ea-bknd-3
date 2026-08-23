// Package notifyemail is a small, self-contained domain package for
// app.notify_emails — the single allowlist of emails permitted to reach
// admin-gated routes across the backend (meters/express-feeders admin,
// announcements, and this package's own list-management routes). Replaces
// what used to be a static NOTIFY_EMAILS env var, duplicated by a
// hardcoded array on the frontend (src/lib/notify-config.ts) — this table
// is now the one source of truth for both, editable without a redeploy.
package notifyemail

import (
	"time"

	"github.com/uptrace/bun"
)

// NotifyEmail mirrors a row from app.notify_emails.
type NotifyEmail struct {
	bun.BaseModel `bun:"table:app.notify_emails"`
	Email         string    `bun:",pk" json:"email"`
	AddedBy       string    `bun:"added_by" json:"added_by,omitempty"`
	CreatedAt     time.Time `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
}
