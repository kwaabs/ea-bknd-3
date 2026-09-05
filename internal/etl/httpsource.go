// httpsource.go: the KindHTTPAPI extraction path — pulls paginated JSON
// out of an HTTP API instead of running SQL against a database. Produces
// the same shape of result (a RowSource, see run.go) as the SQL kinds, so
// runExtractQuery's batching/loading/watermark logic is shared unchanged
// between "a SQL SELECT streamed via database/sql" and "a paginated JSON
// API streamed page by page" — only how rows are fetched differs.
package etl

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// httpAPICreds is the decrypted form of an http_api Source: apiKey never
// goes on the wire (see signRequest) — only apiID and the signature it
// produces do.
type httpAPICreds struct {
	BaseURL string // e.g. "https://api.example.com", no trailing slash
	APIID   string
	APIKey  string
}

// signRequest computes the timestamp/signature pair for one HTTP request,
// per the API's documented recipe: signature = base64(
// HMAC-SHA256(key=apiKey, message=timestamp+apiID)). timestamp is
// generated fresh (not reused across a run's pages) since it doubles as
// replay protection on the API's side — a request built long after its
// timestamp was signed should look stale to a server enforcing a skew
// window, so each page fetch signs its own.
//
// The exact millisecond-precision, literal-"Z" format
// ("2006-01-02T15:04:05.000Z") matches JavaScript's
// `new Date().toISOString()` byte-for-byte, which is what the API's own
// Postman collection example uses to build the string that gets signed —
// matching it isn't required for our own signature to verify (we sign
// whatever we send, so we're internally consistent regardless of format),
// but it maximizes the chance the API's own timestamp parsing accepts it
// without surprises.
func signRequest(apiID, apiKey string) (timestamp, signature string) {
	timestamp = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	mac := hmac.New(sha256.New, []byte(apiKey))
	mac.Write([]byte(timestamp))
	mac.Write([]byte(apiID))
	signature = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return timestamp, signature
}

// formatWatermarkValue renders a raw watermark value for substitution into
// an HTTP query-parameter value. Unlike formatWatermarkLiteral (query.go,
// used by the SQL kinds), this is NOT a SQL context — the result becomes
// one url.Values entry, which net/url percent-encodes at serialize time,
// so no quoting of its own is wanted here. Still validates a
// WatermarkInteger actually parses as an integer, same as the SQL path,
// since a job's watermark column having drifted to a non-numeric value is
// a real configuration bug worth catching regardless of source kind.
func formatWatermarkValue(raw string, t WatermarkType) (string, error) {
	if t == WatermarkInteger {
		if _, err := strconv.ParseInt(raw, 10, 64); err != nil {
			return "", fmt.Errorf("etl: watermark value %q is not a valid integer: %w", raw, err)
		}
	}
	return raw, nil
}

// formatFilterValuePlain is formatFilterValue's (query.go) HTTP-context
// counterpart — plain values (no SQL quoting/escaping) joined into one
// query-parameter value by formatFilterLiteralPlain.
func formatFilterValuePlain(v interface{}) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case int32:
		return strconv.FormatInt(int64(t), 10), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(t), nil
	case time.Time:
		return t.Format(time.RFC3339Nano), nil
	case nil:
		return "", fmt.Errorf("etl: filter_query returned a NULL value")
	default:
		return fmt.Sprintf("%v", t), nil
	}
}

func formatFilterLiteralPlain(values []interface{}) (string, error) {
	parts := make([]string, len(values))
	for i, v := range values {
		lit, err := formatFilterValuePlain(v)
		if err != nil {
			return "", err
		}
		parts[i] = lit
	}
	return strings.Join(parts, ","), nil
}

// splitPathQuery parses job.SourceQuery (a path + optional query string,
// e.g. "/api/v1/sales?year=2026&month=5") into the bare path and its
// parsed query values. Token substitution happens on the parsed values
// (see substituteHTTPTokens) rather than on the raw text, so a
// substituted value containing "&"/"="/spaces can never corrupt the query
// string's structure — url.Values.Encode() percent-encodes it correctly
// at request time.
func splitPathQuery(sourceQuery string) (path string, values url.Values, err error) {
	path, rawQuery, _ := strings.Cut(sourceQuery, "?")
	values, err = url.ParseQuery(rawQuery)
	if err != nil {
		return "", nil, fmt.Errorf("etl: parse source_query as a URL path+query: %w", err)
	}
	return path, values, nil
}

// substituteHTTPTokens replaces {{WATERMARK}}/{{FILTER}} inside each query
// parameter value, in place. Enforces the identical "full_refresh must not
// reference {{WATERMARK}}, incremental must" contract buildQuery (query.go)
// applies to SQL jobs — same invariant, just checked against parsed query
// values instead of raw SQL text.
func substituteHTTPTokens(job Job, values url.Values, lastWatermark, filterLiteral string, hasFilter bool) error {
	hasWatermarkToken := false
	hasFilterToken := false
	for _, vs := range values {
		for _, v := range vs {
			if strings.Contains(v, watermarkToken) {
				hasWatermarkToken = true
			}
			if strings.Contains(v, filterToken) {
				hasFilterToken = true
			}
		}
	}

	var watermarkLiteral string
	if job.Mode == ModeIncremental {
		if job.WatermarkType == nil {
			return fmt.Errorf("etl: job %q is incremental but has no watermark_type", job.Name)
		}
		if !hasWatermarkToken {
			return fmt.Errorf("etl: job %q is incremental but its source_query never references %s", job.Name, watermarkToken)
		}
		lit, err := formatWatermarkValue(lastWatermark, *job.WatermarkType)
		if err != nil {
			return fmt.Errorf("etl: job %q: %w", job.Name, err)
		}
		watermarkLiteral = lit
	} else if hasWatermarkToken {
		return fmt.Errorf("etl: job %q is full_refresh but its source_query references %s", job.Name, watermarkToken)
	}

	if hasFilter && !hasFilterToken {
		return fmt.Errorf("etl: job %q has filter_query set but its source_query never references %s", job.Name, filterToken)
	}
	if !hasFilter && hasFilterToken {
		return fmt.Errorf("etl: job %q's source_query references %s but has no filter_query set", job.Name, filterToken)
	}

	for k, vs := range values {
		for i, v := range vs {
			if hasWatermarkToken {
				v = strings.ReplaceAll(v, watermarkToken, watermarkLiteral)
			}
			if hasFilterToken {
				v = strings.ReplaceAll(v, filterToken, filterLiteral)
			}
			vs[i] = v
		}
		values[k] = vs
	}
	return nil
}

// buildHTTPRequest resolves one job's (path, base query values) for the
// plain (non-filtered) path — the HTTP analog of buildQuery.
func buildHTTPRequest(job Job, lastWatermark string) (path string, values url.Values, err error) {
	path, values, err = splitPathQuery(job.SourceQuery)
	if err != nil {
		return "", nil, err
	}
	if err := substituteHTTPTokens(job, values, lastWatermark, "", false); err != nil {
		return "", nil, err
	}
	return path, values, nil
}

// buildFilteredHTTPRequest is buildHTTPRequest's counterpart for one
// filter_query chunk — the HTTP analog of buildFilteredQuery.
func buildFilteredHTTPRequest(job Job, filterLiteral string) (path string, values url.Values, err error) {
	path, values, err = splitPathQuery(job.SourceQuery)
	if err != nil {
		return "", nil, err
	}
	if err := substituteHTTPTokens(job, values, "", filterLiteral, true); err != nil {
		return "", nil, err
	}
	return path, values, nil
}

// lookupField reads one field out of a decoded JSON record by dot-path
// (e.g. "region.name" walks record["region"].(map[string]interface{})["name"]).
// Returns nil (not an error) for a missing/non-object intermediate path —
// same "absent means NULL" treatment normalizeValue-adjacent code
// elsewhere in this package gives a missing value, rather than failing an
// entire batch over one record's optional field.
func lookupField(record map[string]interface{}, path string) interface{} {
	parts := strings.Split(path, ".")
	var cur interface{} = record
	for _, p := range parts {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}

// lookupRecords extracts the array of records from a decoded page body at
// the job's RecordsPath (dot-path, e.g. "rows" or "data.items").
func lookupRecords(body map[string]interface{}, recordsPath string) ([]map[string]interface{}, error) {
	parts := strings.Split(recordsPath, ".")
	var cur interface{} = body
	for _, p := range parts {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("etl: records_path %q: %q is not an object in the response", recordsPath, p)
		}
		cur = m[p]
	}
	if cur == nil {
		// A JSON "null" at the records path (rather than "[]") is a
		// plausible way for a source to represent "no rows on this page" —
		// e.g. Go's own encoding/json renders a nil slice as null by
		// default, and other server-side JSON libraries commonly do the
		// same. Treated as zero records rather than an error so this
		// engine's own last-page detection (a page shorter than
		// PageSize) still fires normally instead of every empty final
		// page becoming a hard failure.
		return nil, nil
	}
	arr, ok := cur.([]interface{})
	if !ok {
		return nil, fmt.Errorf("etl: records_path %q did not resolve to a JSON array", recordsPath)
	}
	records := make([]map[string]interface{}, 0, len(arr))
	for i, el := range arr {
		rec, ok := el.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("etl: records_path %q: element %d is not a JSON object", recordsPath, i)
		}
		records = append(records, rec)
	}
	return records, nil
}

// maxHTTPPages is a defensive circuit breaker against an API that never
// signals "last page" the way this engine expects (e.g. always returning
// exactly PageSize rows due to a bug or an undocumented behavior change) —
// without it, a misbehaving source would make one job run forever instead
// of failing loudly. Chosen high enough (50M rows at the smallest sane
// page size) to never bind on any real, correctly-behaving API.
const maxHTTPPages = 100_000

// httpRowSource streams one job's paginated HTTP API results, fetching a
// page lazily whenever the previously-fetched page is exhausted. Its
// method set intentionally matches *sql.Rows's (Columns/Next/Scan/Err/
// Close) exactly, so it satisfies the RowSource interface in run.go
// without either side needing to know about the other's concrete type.
type httpRowSource struct {
	ctx         context.Context // the one job run's context, fixed for this source's lifetime
	client      *http.Client
	creds       httpAPICreds
	path        string
	baseValues  url.Values // static/templated params, fixed for the whole run
	fields      []string   // job.SourceFields — dot-paths, positional per DestColumns
	pageSize    int
	recordsPath string

	offset      int
	page        int
	allDone     bool // no more pages to fetch (last page was short)
	currentRows []map[string]interface{}
	idx         int // index into currentRows of the "current" row after Next()
	err         error
}

func newHTTPRowSource(ctx context.Context, client *http.Client, creds httpAPICreds, job Job, path string, baseValues url.Values) *httpRowSource {
	return &httpRowSource{
		ctx:         ctx,
		client:      client,
		creds:       creds,
		path:        path,
		baseValues:  baseValues,
		fields:      job.SourceFields,
		pageSize:    job.PageSize,
		recordsPath: job.RecordsPath,
		idx:         -1,
	}
}

func (h *httpRowSource) Columns() ([]string, error) {
	return h.fields, nil
}

// fetchJSONPage builds one signed, paginated GET (path+baseValues plus a
// fresh timestamp/signature and the given limit/offset), executes it
// against client, and returns the decoded JSON response body. Shared by
// httpRowSource.fetchPage (a real job run, RecordsPath already known) and
// testHTTPQuery (the wizard's interactive preview, before a job — and its
// RecordsPath — exists), so the request-building/signing/error-handling
// stays identical for both.
func fetchJSONPage(ctx context.Context, client *http.Client, creds httpAPICreds, path string, baseValues url.Values, limit, offset int) (map[string]interface{}, error) {
	values := url.Values{}
	for k, vs := range baseValues {
		values[k] = append([]string(nil), vs...)
	}
	timestamp, signature := signRequest(creds.APIID, creds.APIKey)
	values.Set("limit", strconv.Itoa(limit))
	values.Set("offset", strconv.Itoa(offset))
	values.Set("timestamp", timestamp)

	reqURL := strings.TrimSuffix(creds.BaseURL, "/") + "/" + strings.TrimPrefix(path, "/") + "?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("etl: build http request: %w", err)
	}
	req.Header.Set("api-id", creds.APIID)
	req.Header.Set("api-signature", signature)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("etl: http request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20)) // 64MB/page sanity cap
	if err != nil {
		return nil, fmt.Errorf("etl: read http response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("etl: http source returned status %d: %s", resp.StatusCode, truncateForError(respBody))
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("etl: parse http response as JSON: %w", err)
	}
	return parsed, nil
}

// fetchPage requests the page at h.offset and stores its records, or sets
// h.allDone if this page came back shorter than h.pageSize (the "last
// page" signal — this API has no total-count field to rely on instead).
func (h *httpRowSource) fetchPage(ctx context.Context) error {
	if h.page >= maxHTTPPages {
		return fmt.Errorf("etl: exceeded %d pages without the source signaling completion — refusing to page forever", maxHTTPPages)
	}

	parsed, err := fetchJSONPage(ctx, h.client, h.creds, h.path, h.baseValues, h.pageSize, h.offset)
	if err != nil {
		return err
	}
	records, err := lookupRecords(parsed, h.recordsPath)
	if err != nil {
		return err
	}

	h.currentRows = records
	h.idx = -1
	h.page++
	if len(records) < h.pageSize {
		h.allDone = true
	} else {
		h.offset += h.pageSize
	}
	return nil
}

// testHTTPQuery is TestQuery's (service.go) http_api branch: fetches ONE
// page (no pagination loop — this is an interactive preview, not a real
// pull) of sourceQuery literally, with no {{WATERMARK}}/{{FILTER}}
// substitution — same "tokens aren't substituted for a test" behavior the
// SQL kinds already have (see the wizard's own hint text). Auto-detects
// which top-level response field holds the record array (there's no
// records_path yet at this point in the wizard flow — no job exists to
// have one) and flattens the sampled records' fields into Columns/Rows in
// the same shape a SQL TestQuery returns, so the existing column-mapping
// UI works unchanged for either source kind.
func testHTTPQuery(ctx context.Context, creds httpAPICreds, sourceQuery string) (*TestQueryResult, error) {
	path, values, err := splitPathQuery(sourceQuery)
	if err != nil {
		return nil, err
	}

	started := time.Now()
	parsed, err := fetchJSONPage(ctx, httpClientForSources, creds, path, values, testQueryMaxRows, 0)
	if err != nil {
		return nil, err
	}

	recordsPath, records, err := autoDetectRecords(parsed)
	if err != nil {
		return nil, err
	}

	result := &TestQueryResult{
		Rows:                make([][]interface{}, 0, len(records)),
		DetectedRecordsPath: recordsPath,
		ElapsedMs:           time.Since(started).Milliseconds(),
	}
	if len(records) == 0 {
		return result, nil
	}

	// Column order comes from the union of every sampled record's keys —
	// one record's own flattenJSONKeys could miss a field that's
	// absent/null on that particular record but present on others (JSON
	// records in the same array aren't guaranteed to share identical keys
	// the way SQL rows share identical columns).
	colSet := map[string]bool{}
	var cols []string
	for _, rec := range records {
		for _, k := range flattenJSONKeys(rec, "", 0) {
			if !colSet[k] {
				colSet[k] = true
				cols = append(cols, k)
			}
		}
	}
	sort.Strings(cols)
	result.Columns = cols

	for i, rec := range records {
		if i >= testQueryMaxRows {
			result.Truncated = true
			break
		}
		row := make([]interface{}, len(cols))
		for j, c := range cols {
			row[j] = lookupField(rec, c)
		}
		result.Rows = append(result.Rows, row)
	}
	return result, nil
}

// autoDetectRecords finds the sole top-level JSON array field in a
// decoded response body and returns it (as the detected records path)
// plus its parsed elements. Errors if there isn't exactly one: zero means
// this response doesn't look like a paginated list at all, and more than
// one means the API is ambiguous enough that the operator needs to name
// the right one explicitly (by editing the job's records_path after
// creation).
func autoDetectRecords(parsed map[string]interface{}) (string, []map[string]interface{}, error) {
	var candidates []string
	for k, v := range parsed {
		if _, ok := v.([]interface{}); ok {
			candidates = append(candidates, k)
		}
	}
	sort.Strings(candidates)

	switch len(candidates) {
	case 0:
		keys := make([]string, 0, len(parsed))
		for k := range parsed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return "", nil, fmt.Errorf("etl: response has no top-level JSON array field to use as records — got fields: %s", strings.Join(keys, ", "))
	case 1:
		arr := parsed[candidates[0]].([]interface{})
		records := make([]map[string]interface{}, 0, len(arr))
		for i, el := range arr {
			rec, ok := el.(map[string]interface{})
			if !ok {
				return "", nil, fmt.Errorf("etl: field %q's array element %d is not a JSON object", candidates[0], i)
			}
			records = append(records, rec)
		}
		return candidates[0], records, nil
	default:
		return "", nil, fmt.Errorf("etl: response has multiple top-level JSON array fields (%s) — ambiguous which is the record list", strings.Join(candidates, ", "))
	}
}

// flattenJSONKeys returns dot-path keys for every leaf value in a decoded
// JSON object, sorted for a stable order (JSON object/map iteration order
// is otherwise random) — recurses into nested objects (e.g.
// "region.name"); arrays are left as opaque leaf values, not recursed
// into, since a per-element index isn't a meaningful single "column" to
// map to one destination column.
func flattenJSONKeys(m map[string]interface{}, prefix string, depth int) []string {
	if depth > 5 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []string
	for _, k := range keys {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if nested, ok := m[k].(map[string]interface{}); ok {
			out = append(out, flattenJSONKeys(nested, path, depth+1)...)
		} else {
			out = append(out, path)
		}
	}
	return out
}

func truncateForError(b []byte) string {
	const max = 500
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}

// Next matches *sql.Rows's signature (no context parameter) so
// httpRowSource satisfies the same RowSource interface run.go uses for
// the SQL kinds — it uses the context captured at construction
// (newHTTPRowSource) instead, which spans this source's whole lifetime
// (one job run), the same scope *sql.Rows's own internal context would
// have come from its originating QueryContext call.
func (h *httpRowSource) Next() bool {
	if h.err != nil {
		return false
	}
	h.idx++
	if h.idx < len(h.currentRows) {
		return true
	}
	if h.allDone {
		return false
	}
	if err := h.fetchPage(h.ctx); err != nil {
		h.err = err
		return false
	}
	h.idx++
	return h.idx < len(h.currentRows)
}

func (h *httpRowSource) Scan(dest ...interface{}) error {
	if h.idx < 0 || h.idx >= len(h.currentRows) {
		return fmt.Errorf("etl: Scan called with no current row")
	}
	if len(dest) != len(h.fields) {
		return fmt.Errorf("etl: Scan expected %d destinations, got %d", len(h.fields), len(dest))
	}
	row := h.currentRows[h.idx]
	for i, field := range h.fields {
		v := lookupField(row, field)
		ptr, ok := dest[i].(*interface{})
		if !ok {
			return fmt.Errorf("etl: Scan destination %d is not *interface{}", i)
		}
		*ptr = v
	}
	return nil
}

func (h *httpRowSource) Err() error   { return h.err }
func (h *httpRowSource) Close() error { return nil }
