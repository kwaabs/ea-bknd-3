package cache

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
)

const keyPrefix = "httpcache:v1:"

// TTLFunc decides the cache TTL for a given request. This lets volatile ranges
// (those that include today) expire quickly while immutable historical ranges
// stay cached for a long time.
type TTLFunc func(r *http.Request) time.Duration

// Middleware returns a chi-compatible middleware that caches successful GET JSON
// responses in Redis as gzip-compressed bytes (cache-aside / lazy loading).
//
// On a hit it serves the cached payload, sending the gzip bytes directly to
// clients that accept gzip. On a miss it transparently buffers the downstream
// response, stores it, and forwards it to the caller.
//
// If c is nil the middleware is a transparent pass-through, so routes can be
// wired unconditionally regardless of whether Redis is configured. Every Redis
// interaction is best-effort: a cache outage degrades to a direct DB hit and
// never fails the request.
func Middleware(c Cache, ttl TTLFunc, logr *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if c == nil {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				next.ServeHTTP(w, r)
				return
			}

			key := buildKey(r)

			getCtx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
			raw, ok, err := c.Get(getCtx, key)
			cancel()
			if err != nil && logr != nil {
				logr.Warn("cache get failed", zap.String("key", key), zap.Error(err))
			}
			if ok && writeCached(w, r, raw) {
				return
			}

			// Miss: buffer the downstream response so we can both store and serve it.
			rec := &recorder{ResponseWriter: w, status: http.StatusOK, buf: &bytes.Buffer{}}
			next.ServeHTTP(rec, r)

			if rec.status == http.StatusOK && rec.buf.Len() > 0 {
				if gz, gzErr := gzipBytes(rec.buf.Bytes()); gzErr == nil {
					setCtx, setCancel := context.WithTimeout(context.Background(), 1*time.Second)
					if setErr := c.Set(setCtx, key, gz, ttl(r)); setErr != nil && logr != nil {
						logr.Warn("cache set failed", zap.String("key", key), zap.Error(setErr))
					}
					setCancel()
				}
			}

			w.Header().Set("X-Cache", "MISS")
			w.WriteHeader(rec.status)
			_, _ = w.Write(rec.buf.Bytes())
		})
	}
}

// RecencyTTL returns a TTLFunc that uses a short TTL when the requested range
// includes today (data still landing) and a long TTL for fully historical
// ranges (immutable). It understands both "dateTo" and "date_to" query params.
func RecencyTTL(short, long time.Duration) TTLFunc {
	return func(r *http.Request) time.Duration {
		q := r.URL.Query()
		dateTo := q.Get("dateTo")
		if dateTo == "" {
			dateTo = q.Get("date_to")
		}
		if dateTo == "" {
			return short
		}
		t, err := time.Parse("2006-01-02", dateTo)
		if err != nil {
			return short
		}
		today := time.Now().UTC().Truncate(24 * time.Hour)
		if !t.Before(today) {
			return short
		}
		return long
	}
}

// buildKey produces a deterministic key from the path + normalized query string.
// Query keys are sorted and CSV values are split/sorted so that equivalent
// requests (e.g. region=A,B vs region=B,A) map to the same cache entry.
func buildKey(r *http.Request) string {
	q := r.URL.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString(r.URL.Path)
	sb.WriteByte('?')
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		for i, v := range vals {
			parts := strings.Split(v, ",")
			for j := range parts {
				parts[j] = strings.TrimSpace(parts[j])
			}
			sort.Strings(parts)
			vals[i] = strings.Join(parts, ",")
		}
		sort.Strings(vals)
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(strings.Join(vals, "|"))
		sb.WriteByte('&')
	}

	sum := sha1.Sum([]byte(sb.String()))
	return keyPrefix + hex.EncodeToString(sum[:])
}

// writeCached serves a stored (gzip) payload. Returns false if the entry is
// unusable so the caller can fall back to regenerating it.
func writeCached(w http.ResponseWriter, r *http.Request, gz []byte) bool {
	h := w.Header()
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", "application/json")
	}
	h.Set("X-Cache", "HIT")

	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		h.Set("Vary", "Accept-Encoding")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(gz)
		return true
	}

	body, err := gunzipBytes(gz)
	if err != nil {
		h.Del("X-Cache")
		return false
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	return true
}

// recorder buffers the downstream response instead of writing it directly so the
// middleware can capture the body for caching. Headers set by the handler land
// on the underlying ResponseWriter's header map (Header() is not overridden).
type recorder struct {
	http.ResponseWriter
	status      int
	buf         *bytes.Buffer
	wroteHeader bool
}

func (rec *recorder) WriteHeader(code int) {
	if !rec.wroteHeader {
		rec.status = code
		rec.wroteHeader = true
	}
}

func (rec *recorder) Write(b []byte) (int, error) {
	return rec.buf.Write(b)
}

func gzipBytes(in []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(in); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gunzipBytes(in []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(in))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}
