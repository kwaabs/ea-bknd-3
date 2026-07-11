package comments

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
	"go.uber.org/zap"
)

// ErrNotFound is returned when a comment doesn't exist or was deleted.
var ErrNotFound = errors.New("comment not found")

// ErrForbidden is returned when a user tries to mutate another user's comment.
var ErrForbidden = errors.New("forbidden")

type Service struct {
	db   *bun.DB
	logr *zap.Logger
}

func NewService(db *bun.DB, logr *zap.Logger) *Service {
	return &Service{db: db, logr: logr}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// hydrateAuthors joins author name (and avatar if you add one to users later)
// onto a slice of comments in a single query.
func (s *Service) hydrateAuthors(ctx context.Context, comments []*Comment) error {
	if len(comments) == 0 {
		return nil
	}

	ids := make([]uuid.UUID, 0, len(comments))
	for _, c := range comments {
		ids = append(ids, c.AuthorID)
	}

	var users []struct {
		ID   uuid.UUID `bun:"id"`
		Name string    `bun:"name"`
	}
	err := s.db.NewSelect().
		TableExpr("app.users").
		ColumnExpr("id, name").
		Where("id IN (?)", bun.In(ids)).
		Scan(ctx, &users)
	if err != nil {
		return err
	}

	nameByID := make(map[uuid.UUID]string, len(users))
	for _, u := range users {
		nameByID[u.ID] = u.Name
	}
	for _, c := range comments {
		c.AuthorName = nameByID[c.AuthorID]
	}
	return nil
}

// resolveBody returns the display body — "[deleted]" for soft-deleted comments.
func resolveBody(c *Comment) {
	if c.Deleted {
		c.Body = "[deleted]"
	}
}

// ─── list top-level comments ─────────────────────────────────────────────────

func (s *Service) ListComments(ctx context.Context, p CommentListParams) ([]*Comment, int, error) {
	if p.Limit <= 0 {
		p.Limit = 20
	}
	if p.Page <= 0 {
		p.Page = 1
	}
	offset := (p.Page - 1) * p.Limit

	q := s.db.NewSelect().
		Model((*Comment)(nil)).
		Where("parent_id IS NULL").
		OrderExpr("created_at DESC").
		Limit(p.Limit).
		Offset(offset)

	if p.Resolved != nil {
		q = q.Where("resolved = ?", *p.Resolved)
	}

	var comments []*Comment
	total, err := q.ScanAndCount(ctx, &comments)
	if err != nil {
		return nil, 0, err
	}

	// Attach reply counts
	if len(comments) > 0 {
		parentIDs := make([]uuid.UUID, len(comments))
		for i, c := range comments {
			parentIDs[i] = c.ID
		}

		var counts []struct {
			ParentID uuid.UUID `bun:"parent_id"`
			Count    int       `bun:"count"`
		}
		err = s.db.NewSelect().
			TableExpr("app.comments").
			ColumnExpr("parent_id, COUNT(*) AS count").
			Where("parent_id IN (?)", bun.In(parentIDs)).
			Where("deleted = false").
			GroupExpr("parent_id").
			Scan(ctx, &counts)
		if err != nil {
			s.logr.Warn("failed to fetch reply counts", zap.Error(err))
		}

		countByID := make(map[uuid.UUID]int, len(counts))
		for _, rc := range counts {
			countByID[rc.ParentID] = rc.Count
		}
		for _, c := range comments {
			c.ReplyCount = countByID[c.ID]
			resolveBody(c)
		}
	}

	if err := s.hydrateAuthors(ctx, comments); err != nil {
		s.logr.Warn("failed to hydrate comment authors", zap.Error(err))
	}

	return comments, total, nil
}

// ─── replies ─────────────────────────────────────────────────────────────────

func (s *Service) ListReplies(ctx context.Context, parentID uuid.UUID) ([]*Comment, error) {
	var replies []*Comment
	err := s.db.NewSelect().
		Model(&replies).
		Where("parent_id = ?", parentID).
		OrderExpr("created_at ASC").
		Scan(ctx)
	if err != nil {
		return nil, err
	}

	for _, c := range replies {
		resolveBody(c)
	}

	if err := s.hydrateAuthors(ctx, replies); err != nil {
		s.logr.Warn("failed to hydrate reply authors", zap.Error(err))
	}

	return replies, nil
}

// ─── create ───────────────────────────────────────────────────────────────────

func (s *Service) CreateComment(ctx context.Context, authorID uuid.UUID, body string, mentions []uuid.UUID, parentID *uuid.UUID) (*Comment, error) {
	// If this is a reply, verify the parent exists and is top-level (one level deep only)
	if parentID != nil {
		var parent Comment
		err := s.db.NewSelect().Model(&parent).Where("id = ?", *parentID).Scan(ctx)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		if parent.ParentID != nil {
			return nil, fmt.Errorf("replies can only be one level deep")
		}
	}

	if mentions == nil {
		mentions = []uuid.UUID{}
	}

	now := time.Now().UTC()
	c := &Comment{
		ID:        uuid.New(),
		Body:      body,
		AuthorID:  authorID,
		ParentID:  parentID,
		Reactions: []Reaction{},
		Mentions:  mentions,
		CreatedAt: now,
		UpdatedAt: now,
	}

	_, err := s.db.NewInsert().Model(c).Exec(ctx)
	if err != nil {
		return nil, err
	}

	// Hydrate author name for the response
	if err := s.hydrateAuthors(ctx, []*Comment{c}); err != nil {
		s.logr.Warn("failed to hydrate new comment author", zap.Error(err))
	}

	return c, nil
}

// ─── edit ─────────────────────────────────────────────────────────────────────

func (s *Service) EditComment(ctx context.Context, commentID uuid.UUID, requestingUserID uuid.UUID, newBody string) (*Comment, error) {
	var c Comment
	err := s.db.NewSelect().Model(&c).Where("id = ?", commentID).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if c.Deleted {
		return nil, ErrNotFound
	}
	if c.AuthorID != requestingUserID {
		return nil, ErrForbidden
	}

	c.Body = newBody
	c.UpdatedAt = time.Now().UTC()

	_, err = s.db.NewUpdate().Model(&c).Column("body", "updated_at").Where("id = ?", c.ID).Exec(ctx)
	if err != nil {
		return nil, err
	}

	if err := s.hydrateAuthors(ctx, []*Comment{&c}); err != nil {
		s.logr.Warn("failed to hydrate edited comment author", zap.Error(err))
	}

	return &c, nil
}

// ─── soft delete ──────────────────────────────────────────────────────────────

func (s *Service) DeleteComment(ctx context.Context, commentID uuid.UUID, requestingUserID uuid.UUID) error {
	var c Comment
	err := s.db.NewSelect().Model(&c).Where("id = ?", commentID).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if c.Deleted {
		return ErrNotFound
	}
	if c.AuthorID != requestingUserID {
		return ErrForbidden
	}

	now := time.Now().UTC()
	_, err = s.db.NewUpdate().
		TableExpr("app.comments").
		Set("deleted = true, updated_at = ?", now).
		Where("id = ?", commentID).
		Exec(ctx)
	return err
}

// ─── reactions ────────────────────────────────────────────────────────────────

// ToggleReaction adds the emoji for the user if not present, removes it if already present.
// It updates the count accordingly and removes the reaction entry entirely when count hits 0.
func (s *Service) ToggleReaction(ctx context.Context, commentID uuid.UUID, userID uuid.UUID, emoji string) (*Comment, error) {
	var c Comment
	err := s.db.NewSelect().Model(&c).Where("id = ? AND deleted = false", commentID).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	// Find or create the reaction entry for this emoji
	found := false
	for i, r := range c.Reactions {
		if r.Emoji == emoji {
			found = true
			// Toggle: remove if already reacted, add if not
			alreadyReacted := false
			newUserIDs := make([]uuid.UUID, 0, len(r.UserIDs))
			for _, uid := range r.UserIDs {
				if uid == userID {
					alreadyReacted = true
				} else {
					newUserIDs = append(newUserIDs, uid)
				}
			}
			if alreadyReacted {
				c.Reactions[i].UserIDs = newUserIDs
				c.Reactions[i].Count = len(newUserIDs)
			} else {
				c.Reactions[i].UserIDs = append(r.UserIDs, userID)
				c.Reactions[i].Count = len(c.Reactions[i].UserIDs)
			}
			// Drop the reaction entry if no users remain
			if c.Reactions[i].Count == 0 {
				c.Reactions = append(c.Reactions[:i], c.Reactions[i+1:]...)
			}
			break
		}
	}
	if !found {
		c.Reactions = append(c.Reactions, Reaction{
			Emoji:   emoji,
			Count:   1,
			UserIDs: []uuid.UUID{userID},
		})
	}

	reactionsJSON, err := json.Marshal(c.Reactions)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	_, err = s.db.NewUpdate().
		TableExpr("app.comments").
		Set("reactions = ?", string(reactionsJSON)).
		Set("updated_at = ?", now).
		Where("id = ?", commentID).
		Exec(ctx)
	if err != nil {
		return nil, err
	}
	c.UpdatedAt = now

	if err := s.hydrateAuthors(ctx, []*Comment{&c}); err != nil {
		s.logr.Warn("failed to hydrate reacted comment author", zap.Error(err))
	}

	return &c, nil
}

// ─── resolve ──────────────────────────────────────────────────────────────────

func (s *Service) SetResolved(ctx context.Context, commentID uuid.UUID, resolverID uuid.UUID, resolved bool) (*Comment, error) {
	var c Comment
	err := s.db.NewSelect().Model(&c).Where("id = ? AND deleted = false", commentID).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	now := time.Now().UTC()
	c.Resolved = resolved
	c.UpdatedAt = now

	if resolved {
		c.ResolvedBy = &resolverID
		c.ResolvedAt = &now
	} else {
		c.ResolvedBy = nil
		c.ResolvedAt = nil
	}

	_, err = s.db.NewUpdate().Model(&c).
		Column("resolved", "resolved_by", "resolved_at", "updated_at").
		Where("id = ?", c.ID).
		Exec(ctx)
	if err != nil {
		return nil, err
	}

	if err := s.hydrateAuthors(ctx, []*Comment{&c}); err != nil {
		s.logr.Warn("failed to hydrate resolved comment author", zap.Error(err))
	}

	return &c, nil
}

// ─── mentionable users ────────────────────────────────────────────────────────

func (s *Service) GetMentionableUsers(ctx context.Context) ([]MentionableUser, error) {
	var users []MentionableUser
	err := s.db.NewSelect().
		TableExpr("app.users").
		ColumnExpr("id, name").
		OrderExpr("name ASC").
		Scan(ctx, &users)
	if err != nil {
		return nil, err
	}
	return users, nil
}
