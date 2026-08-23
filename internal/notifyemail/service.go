package notifyemail

import (
	"context"
	"errors"
	"strings"

	"github.com/uptrace/bun"
)

// ErrForbidden means the caller isn't on the allowlist.
var ErrForbidden = errors.New("forbidden")

type Service struct {
	db *bun.DB
}

func NewService(db *bun.DB) *Service { return &Service{db: db} }

func normalize(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// IsAllowed reports whether email is on the allowlist.
func (s *Service) IsAllowed(ctx context.Context, email string) (bool, error) {
	email = normalize(email)
	if email == "" {
		return false, nil
	}
	return s.db.NewSelect().
		Model((*NotifyEmail)(nil)).
		Where("email = ?", email).
		Exists(ctx)
}

// List returns every allowlisted email, alphabetically.
func (s *Service) List(ctx context.Context) ([]NotifyEmail, error) {
	var rows []NotifyEmail
	if err := s.db.NewSelect().Model(&rows).OrderExpr("email").Scan(ctx); err != nil {
		return nil, err
	}
	return rows, nil
}

// Count returns how many emails are currently allowlisted — used to guard
// against removing the last one (which would lock everyone, including
// whoever's removing it, out of every admin-gated route).
func (s *Service) Count(ctx context.Context) (int, error) {
	return s.db.NewSelect().Model((*NotifyEmail)(nil)).Count(ctx)
}

// Add inserts a new allowlisted email (a no-op if it's already present).
// addedBy is the caller's own email, recorded for audit purposes only.
func (s *Service) Add(ctx context.Context, email, addedBy string) (*NotifyEmail, error) {
	email = normalize(email)
	if email == "" {
		return nil, errors.New("email required")
	}
	row := &NotifyEmail{Email: email, AddedBy: normalize(addedBy)}
	if _, err := s.db.NewInsert().Model(row).On("CONFLICT (email) DO NOTHING").Exec(ctx); err != nil {
		return nil, err
	}
	return row, nil
}

// Remove deletes an allowlisted email.
func (s *Service) Remove(ctx context.Context, email string) error {
	email = normalize(email)
	_, err := s.db.NewDelete().
		Model((*NotifyEmail)(nil)).
		Where("email = ?", email).
		Exec(ctx)
	return err
}
