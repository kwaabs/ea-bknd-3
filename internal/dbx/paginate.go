// Package dbx holds shared database query helpers: generic pagination and
// reusable filter builders. Every list endpoint uses the same Page envelope
// and the same execution path.
package dbx

import (
	"context"

	"bknd-3/internal/httpx"

	"github.com/uptrace/bun"
)

// Page is the standard paginated response envelope.
type Page[T any] struct {
	Data       []T `json:"data"`
	Total      int `json:"total"`
	Page       int `json:"page"`
	Limit      int `json:"limit"`
	TotalPages int `json:"total_pages"`
}

// Paginate applies limit/offset and executes the data query and its COUNT
// via bun's ScanAndCount, which runs both statements concurrently on the
// connection pool. Compared to the old sequential count-then-scan pattern,
// wall-clock latency drops to max(scan, count) instead of scan + count.
//
// The query passed in should already have filters, columns, and ordering
// applied; bun strips ORDER BY / LIMIT / OFFSET / columns for the count side.
func Paginate[T any](ctx context.Context, q *bun.SelectQuery, p httpx.Pagination) (*Page[T], error) {
	var data []T
	total, err := q.Limit(p.Limit).Offset(p.Offset()).ScanAndCount(ctx, &data)
	if err != nil {
		return nil, err
	}
	if data == nil {
		data = []T{} // JSON "data": [] instead of null
	}
	return &Page[T]{
		Data:       data,
		Total:      total,
		Page:       p.Page,
		Limit:      p.Limit,
		TotalPages: p.TotalPages(total),
	}, nil
}
