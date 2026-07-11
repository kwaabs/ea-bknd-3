package feedback

import (
	"time"

	"github.com/uptrace/bun"
)

type FeedbackType string
type FeedbackStatus string

const (
	FeedbackTypeComment   FeedbackType = "COMMENT"
	FeedbackTypeComplaint FeedbackType = "COMPLAINT"

	FeedbackStatusPending    FeedbackStatus = "PENDING"
	FeedbackStatusInProgress FeedbackStatus = "IN_PROGRESS"
	FeedbackStatusResolved   FeedbackStatus = "RESOLVED"
)

type FeedbackReply struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Comments  string    `json:"comments"`
	ParentID  int64     `json:"parent_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateFeedbackRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Type     string `json:"type" validate:"omitempty,oneof=COMMENT COMPLAINT"`
	Comments string `json:"comments" validate:"required"`
	ParentID *int64 `json:"parent_id"`
}

type UpdateStatusRequest struct {
	Status string `json:"status" validate:"required,oneof=PENDING IN_PROGRESS RESOLVED"`
}

type FeedbackListResponse struct {
	Success bool        `json:"success"`
	Count   int         `json:"count"`
	Limit   int         `json:"limit"`
	Offset  int         `json:"offset"`
	Data    []*Feedback `json:"data"`
}

type FeedbackResponse struct {
	Success bool      `json:"success"`
	Data    *Feedback `json:"data"`
}

type MessageResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type StatusUpdateResponse struct {
	Success bool      `json:"success"`
	Message string    `json:"message"`
	Data    *Feedback `json:"data"`
}

// Helper functions to create string pointers for status values
func StringPtr(s string) *string {
	return &s
}

type Feedback struct {
	bun.BaseModel `bun:"table:app.feedback,alias:fbk"`

	ID        int64     `bun:"id,pk,autoincrement" json:"id"`
	Email     string    `bun:"email,notnull" json:"email"`
	Type      *string   `bun:"type" json:"type"`
	Comments  *string   `bun:"comments,notnull" json:"comments"`
	Status    *string   `bun:"status" json:"status"`
	ParentID  *int64    `bun:"parent_id" json:"parent_id,omitempty"`
	CreatedAt time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt time.Time `bun:"updated_at,notnull,default:current_timestamp" json:"updated_at"`

	Replies []*Feedback `bun:"rel:has-many,join:id=parent_id" json:"replies,omitempty"`
}

// Constants for valid values
const (
	StatusPending    = "PENDING"
	StatusInProgress = "IN_PROGRESS"
	StatusResolved   = "RESOLVED"

	TypeComment   = "COMMENT"
	TypeComplaint = "COMPLAINT"
)
