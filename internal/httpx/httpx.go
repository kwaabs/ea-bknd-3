// Package httpx holds the small, hot-path HTTP helpers shared by every
// handler: JSON writing and query-param parsing (pagination, CSV lists,
// dates). All clamping/validation lives here exactly once.
package httpx

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// JSON writes v as a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Error writes a JSON error envelope: {"error": "..."}.
func Error(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, map[string]string{"error": msg})
}

// Pagination holds normalized page/limit values. Parse it once per request
// with ParsePagination; services never re-validate.
type Pagination struct {
	Page  int
	Limit int
}

// Offset returns the SQL offset for the current page.
func (p Pagination) Offset() int { return (p.Page - 1) * p.Limit }

// TotalPages computes the page count for a total row count.
func (p Pagination) TotalPages(total int) int {
	if p.Limit <= 0 {
		return 0
	}
	return (total + p.Limit - 1) / p.Limit
}

// ParsePagination reads ?page= and ?limit= with defaults and clamping.
// This is the single source of truth for limit abuse prevention.
func ParsePagination(q url.Values, defLimit, maxLimit int) Pagination {
	p := Pagination{Page: 1, Limit: defLimit}
	if v, err := strconv.Atoi(q.Get("page")); err == nil && v > 0 {
		p.Page = v
	}
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 {
		p.Limit = v
	}
	if p.Limit > maxLimit {
		p.Limit = maxLimit
	}
	return p
}

// CSV splits a comma-separated query param into trimmed, non-empty values.
// Returns nil for an absent/empty param so services can use len()==0 checks.
func CSV(q url.Values, key string) []string {
	raw := q.Get(key)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Date reads a YYYY-MM-DD date from the first non-empty param among names
// (lets you keep backward-compatible aliases like dateFrom/dateTimeFrom).
// An absent param returns a zero time and no error.
func Date(q url.Values, names ...string) (time.Time, error) {
	for _, n := range names {
		if v := q.Get(n); v != "" {
			return time.Parse("2006-01-02", v)
		}
	}
	return time.Time{}, nil
}
