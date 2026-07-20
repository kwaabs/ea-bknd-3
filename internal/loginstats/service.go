package loginstats

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

type Service struct {
	db *bun.DB
}

func NewService(db *bun.DB) *Service {
	return &Service{db: db}
}

// GetStats aggregates app.login_events between from and to (inclusive) into
// day/provider/user breakdowns plus a short list of the most recent events.
func (s *Service) GetStats(ctx context.Context, from, to time.Time) (*StatsResponse, error) {
	resp := &StatsResponse{
		Success: true,
		From:    from.Format("2006-01-02"),
		To:      to.Format("2006-01-02"),
	}

	total, err := s.db.NewSelect().
		TableExpr("app.login_events").
		Where("created_at >= ? AND created_at < ?", from, to).
		Count(ctx)
	if err != nil {
		return nil, err
	}
	resp.TotalLogins = total

	err = s.db.NewSelect().
		TableExpr("app.login_events").
		ColumnExpr("count(DISTINCT email)").
		Where("created_at >= ? AND created_at < ?", from, to).
		Scan(ctx, &resp.UniqueUsers)
	if err != nil {
		return nil, err
	}

	err = s.db.NewSelect().
		TableExpr("app.login_events").
		ColumnExpr("to_char(created_at::date, 'YYYY-MM-DD') AS date").
		ColumnExpr("count(*) AS count").
		Where("created_at >= ? AND created_at < ?", from, to).
		GroupExpr("created_at::date").
		OrderExpr("created_at::date ASC").
		Scan(ctx, &resp.ByDay)
	if err != nil {
		return nil, err
	}

	err = s.db.NewSelect().
		TableExpr("app.login_events").
		ColumnExpr("provider").
		ColumnExpr("count(*) AS count").
		Where("created_at >= ? AND created_at < ?", from, to).
		GroupExpr("provider").
		OrderExpr("count DESC").
		Scan(ctx, &resp.ByProvider)
	if err != nil {
		return nil, err
	}

	err = s.db.NewSelect().
		TableExpr("app.login_events").
		ColumnExpr("email").
		ColumnExpr("max(name) AS name").
		// most recently used provider for this user in range
		ColumnExpr("(array_agg(provider ORDER BY created_at DESC))[1] AS provider").
		ColumnExpr("count(*) AS login_count").
		ColumnExpr("max(created_at) AS last_login_at").
		Where("created_at >= ? AND created_at < ?", from, to).
		GroupExpr("email").
		OrderExpr("login_count DESC").
		Limit(50).
		Scan(ctx, &resp.ByUser)
	if err != nil {
		return nil, err
	}

	err = s.db.NewSelect().
		TableExpr("app.login_events").
		ColumnExpr("email, name, provider, device_info, created_at").
		Where("created_at >= ? AND created_at < ?", from, to).
		OrderExpr("created_at DESC").
		Limit(20).
		Scan(ctx, &resp.RecentEvents)
	if err != nil {
		return nil, err
	}

	if resp.ByDay == nil {
		resp.ByDay = []DailyCount{}
	}
	if resp.ByProvider == nil {
		resp.ByProvider = []ProviderCount{}
	}
	if resp.ByUser == nil {
		resp.ByUser = []UserFrequency{}
	}
	if resp.RecentEvents == nil {
		resp.RecentEvents = []RecentEvent{}
	}

	return resp, nil
}
