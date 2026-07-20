package announcements

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var (
	ErrForbidden  = errors.New("forbidden")
	ErrNotFound   = errors.New("not found")
	ErrBadRequest = errors.New("bad request")
)

type Service struct {
	db           *bun.DB
	notifyEmails map[string]struct{}
}

func NewService(db *bun.DB, notifyEmails []string) *Service {
	allowed := make(map[string]struct{}, len(notifyEmails))
	for _, e := range notifyEmails {
		allowed[strings.ToLower(strings.TrimSpace(e))] = struct{}{}
	}
	return &Service{db: db, notifyEmails: allowed}
}

func (s *Service) IsAllowed(email string) bool {
	_, ok := s.notifyEmails[strings.ToLower(strings.TrimSpace(email))]
	return ok
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
	if !s.IsAllowed(email) {
		return nil, ErrForbidden
	}

	now := time.Now().UTC()
	row := &Announcement{
		ID:          uuid.New(),
		Body:        body,
		AuthorEmail: email,
		AuthorName:  req.AuthorName,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err := s.db.NewInsert().Model(row).Exec(ctx)
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (s *Service) SoftDelete(ctx context.Context, id uuid.UUID, email string) error {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || !s.IsAllowed(email) {
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
