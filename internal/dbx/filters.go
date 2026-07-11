package dbx

import (
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// NOTE on safety: the col arguments below are compile-time constants written
// by us (column names), never user input. User-supplied values only ever
// travel through bun.In / bound parameters.

// InLower adds `lower(col) IN (...)`, lowercasing the values, for a
// case-insensitive multi-value filter. No-op when vals is empty.
// Pair each filtered column with a functional index on lower(col) —
// see sql/indexes_mms_customer_sales.sql — or the filter forces a seq scan.
func InLower(q *bun.SelectQuery, col string, vals []string) *bun.SelectQuery {
	if len(vals) == 0 {
		return q
	}
	lowered := make([]string, len(vals))
	for i, v := range vals {
		lowered[i] = strings.ToLower(v)
	}
	return q.Where("lower("+col+") IN (?)", bun.In(lowered))
}

// In adds a plain `col IN (...)` filter. No-op when vals is empty.
func In(q *bun.SelectQuery, col string, vals []string) *bun.SelectQuery {
	if len(vals) == 0 {
		return q
	}
	return q.Where(col+" IN (?)", bun.In(vals))
}

// DateRange adds `col >= from` / `col <= to` for non-zero bounds.
func DateRange(q *bun.SelectQuery, col string, from, to time.Time) *bun.SelectQuery {
	if !from.IsZero() {
		q = q.Where(col+" >= ?", from)
	}
	if !to.IsZero() {
		q = q.Where(col+" <= ?", to)
	}
	return q
}
