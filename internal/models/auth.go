package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type User struct {
	bun.BaseModel `bun:"table:app.users"`
	ID            uuid.UUID  `bun:",pk,type:uuid,default:uuid_generate_v4()" json:"id"`
	Email         string     `json:"email"`
	PasswordHash  string     `json:"password_hash"`
	TokenVersion  int        `bun:"token_version" json:"token_version"`
	Roles         []string   `json:"roles" bun:"type:text[]"`
	Provider      string     `json:"provider"`
	Name          string     `json:"name"`
	CreatedAt     time.Time  `json:"created_at" bun:",nullzero"`    // Add nullzero tag
	LastLoginAt   *time.Time `json:"last_login_at" bun:",nullzero"` // Add nullzero tag
}

type RefreshToken struct {
	bun.BaseModel `bun:"table:app.refresh_tokens"`
	ID            uuid.UUID `bun:",pk,type:uuid,default:uuid_generate_v4()" json:"id"`
	UserID        uuid.UUID `bun:"type:uuid,notnull" json:"user_id"`
	JTI           string    `bun:",notnull" json:"jti"`
	TokenHash     string    `bun:",notnull" json:"-"` // Don't expose hash
	DeviceInfo    *string   `json:"device_info,omitempty"`
	Revoked       bool      `bun:",notnull,default:false" json:"revoked"`
	CreatedAt     time.Time `bun:",nullzero,notnull,default:current_timestamp" json:"created_at"`
	ExpiresAt     time.Time `bun:",notnull" json:"expires_at"`
}
