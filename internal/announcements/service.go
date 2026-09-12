package announcements

import (
	"context"
	"errors"
	"strings"
	"time"

	"bknd-3/internal/notifyemail"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var (
	ErrForbidden   = errors.New("forbidden")
	ErrNotFound    = errors.New("not found")
	ErrBadRequest  = errors.New("bad request")
	ErrInvalidKind = errors.New("invalid kind")
	ErrBodyTooLong = errors.New("body too long")
)

type Service struct {
	db           *bun.DB
	notifyEmails *notifyemail.Service
}

// NewService constructs the announcements service. notifyEmails is backed
// by the shared app.notify_emails table (internal/notifyemail) rather than
// a static list frozen at boot.
func NewService(db *bun.DB, notifyEmails *notifyemail.Service) *Service {
	return &Service{db: db, notifyEmails: notifyEmails}
}

func (s *Service) IsAllowed(ctx context.Context, email string) (bool, error) {
	return s.notifyEmails.IsAllowed(ctx, email)
}

func (s *Service) ListActive(ctx context.Context, limit int) ([]*Announcement, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	var rows []*Announcement
	err := s.db.NewSelect().
		Model(&rows).
		Where("active = TRUE").
		Order("created_at DESC").
		Limit(limit).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Service) Create(ctx context.Context, req *CreateAnnouncementRequest) (*Announcement, error) {
	body := strings.TrimSpace(req.Body)
	email := strings.TrimSpace(strings.ToLower(req.AuthorEmail))
	if body == "" || email == "" {
		return nil, ErrBadRequest
	}
	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		kind = KindRegular
	}
	if kind != KindRegular && kind != KindSpecial {
		return nil, ErrInvalidKind
	}
	// Regular bodies are plain text shown on a single-line marquee ticker.
	// Special bodies are serialized TipTap JSON (see the frontend's
	// RichAnnouncementEditor) — inherently bulkier than the visible text
	// it represents, so it gets a much higher cap; RICH_ANNOUNCEMENT_MAX_CHARS
	// there caps the visible text, this caps the stored payload as a
	// backstop against a client bypassing that client-side check entirely.
	maxLen := 500
	if kind == KindSpecial {
		maxLen = 20000
	}
	if len(body) > maxLen {
		return nil, ErrBodyTooLong
	}
	allowed, err := s.IsAllowed(ctx, email)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrForbidden
	}

	now := time.Now().UTC()
	row := &Announcement{
		ID:          uuid.New(),
		Body:        body,
		AuthorEmail: email,
		AuthorName:  req.AuthorName,
		Kind:        kind,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err = s.db.NewInsert().Model(row).Exec(ctx)
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (s *Service) SoftDelete(ctx context.Context, id uuid.UUID, email string) error {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return ErrForbidden
	}
	allowed, err := s.IsAllowed(ctx, email)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}

	res, err := s.db.NewUpdate().
		Model((*Announcement)(nil)).
		Set("active = FALSE").
		Set("updated_at = ?", time.Now().UTC()).
		Where("id = ? AND active = TRUE", id).
		Exec(ctx)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
