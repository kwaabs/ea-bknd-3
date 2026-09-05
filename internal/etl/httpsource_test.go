package etl

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// TestSignRequest_MatchesDocumentedRecipe verifies signRequest against an
// independently computed HMAC — signature = base64(HMAC-SHA256(key=apiKey,
// message=timestamp+apiID)) — the exact recipe from the source API's
// Postman collection (crypto.algo.HMAC.create(SHA256, apiKey).update(
// timestamp).update(apiId).finalize(), base64-encoded). Getting this wrong
// in any way (field order, missing a value, wrong encoding) means every
// single request fails auth, so this is worth pinning down explicitly
// rather than trusting the implementation reads the recipe correctly.
func TestSignRequest_MatchesDocumentedRecipe(t *testing.T) {
	apiID := "my-api-id"
	apiKey := "super-secret-key"

	timestamp, signature := signRequest(apiID, apiKey)

	mac := hmac.New(sha256.New, []byte(apiKey))
	mac.Write([]byte(timestamp))
	mac.Write([]byte(apiID))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if signature != want {
		t.Errorf("signature = %q, want %q (message = timestamp+apiID = %q)", signature, want, timestamp+apiID)
	}

	// Matches JS's `new Date().toISOString()` format exactly: milliseconds,
	// literal "Z", e.g. "2026-09-05T16:52:42.783Z".
	if _, err := time.Parse("2006-01-02T15:04:05.000Z", timestamp); err != nil {
		t.Errorf("timestamp %q does not match the expected ISO-8601-with-millis-and-Z format: %v", timestamp, err)
	}
}

func TestAutoDetectRecords(t *testing.T) {
	single := map[string]interface{}{
		"limit":  float64(2000),
		"offset": float64(0),
		"rows":   []interface{}{map[string]interface{}{"a": "1"}},
	}
	path, records, err := autoDetectRecords(single)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "rows" || len(records) != 1 {
		t.Errorf("got path=%q records=%v, want path=rows, 1 record", path, records)
	}

	noArray := map[string]interface{}{"limit": float64(2000), "offset": float64(0)}
	if _, _, err := autoDetectRecords(noArray); err == nil {
		t.Error("expected error for a response with no top-level array field, got none")
	}

	twoArrays := map[string]interface{}{
		"rows":  []interface{}{},
		"other": []interface{}{},
	}
	if _, _, err := autoDetectRecords(twoArrays); err == nil {
		t.Error("expected error for a response with multiple top-level array fields, got none")
	}
}

func TestFlattenJSONKeys_NestedAndSorted(t *testing.T) {
	rec := map[string]interface{}{
		"customerName": "ABU MILLICENT",
		"region":       map[string]interface{}{"name": "Accra East", "code": "01"},
		"billAmount":   392.33,
	}
	got := flattenJSONKeys(rec, "", 0)
	want := []string{"billAmount", "customerName", "region.code", "region.name"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
			break
		}
	}
}

func TestBuildHTTPRequest_WatermarkSubstitution(t *testing.T) {
	wt := WatermarkInteger
	job := Job{
		Name:          "j1",
		Mode:          ModeIncremental,
		WatermarkType: &wt,
		SourceQuery:   "/api/v1/sales?year=2026&month={{WATERMARK}}",
	}
	path, values, err := buildHTTPRequest(job, "5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/api/v1/sales" {
		t.Errorf("path = %q, want /api/v1/sales", path)
	}
	if values.Get("year") != "2026" || values.Get("month") != "5" {
		t.Errorf("values = %v, want year=2026 month=5", values)
	}
}

func TestBuildHTTPRequest_FullRefreshRejectsWatermarkToken(t *testing.T) {
	job := Job{
		Name:        "j1",
		Mode:        ModeFullRefresh,
		SourceQuery: "/api/v1/sales?month={{WATERMARK}}",
	}
	if _, _, err := buildHTTPRequest(job, ""); err == nil {
		t.Fatal("expected error for full_refresh job referencing {{WATERMARK}}, got none")
	}
}

func TestBuildFilteredHTTPRequest_Substitution(t *testing.T) {
	job := Job{
		Name:        "j1",
		Mode:        ModeFullRefresh,
		SourceQuery: "/api/v1/sales?year=2026&ids={{FILTER}}",
	}
	path, values, err := buildFilteredHTTPRequest(job, "1,2,3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/api/v1/sales" || values.Get("ids") != "1,2,3" || values.Get("year") != "2026" {
		t.Errorf("path=%q values=%v, want /api/v1/sales with ids=1,2,3 year=2026", path, values)
	}
}

// TestHTTPRowSource_PaginatesSignsAndExtractsFields is the closest thing
// to an end-to-end proof of the whole http_api extraction path: a real
// httptest.Server mimicking the source API's exact contract (api-id/
// api-signature headers, timestamp/limit/offset query params, {limit,
// offset, rows: [...]} envelope), verifying on the SERVER side that every
// request's signature is valid for its own timestamp (catching any subtle
// off-by-one in what gets signed), and on the CLIENT side that
// httpRowSource correctly pages through 3 pages of 2-row responses (5
// records, page size 2 -> pages of 2,2,1) and extracts fields — including
// a nested dot-path field — into the right positions.
func TestHTTPRowSource_PaginatesSignsAndExtractsFields(t *testing.T) {
	const apiID = "test-id"
	const apiKey = "test-key"

	total := 5
	pageSize := 2
	var requests []*http.Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Clone(context.Background()))

		gotID := r.Header.Get("api-id")
		gotSig := r.Header.Get("api-signature")
		timestamp := r.URL.Query().Get("timestamp")
		if gotID != apiID {
			t.Errorf("request missing/wrong api-id header: %q", gotID)
		}
		mac := hmac.New(sha256.New, []byte(apiKey))
		mac.Write([]byte(timestamp))
		mac.Write([]byte(gotID))
		wantSig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		if gotSig != wantSig {
			t.Errorf("request has an invalid api-signature for its own timestamp %q: got %q want %q", timestamp, gotSig, wantSig)
		}
		if r.URL.Query().Get("year") != "2026" {
			t.Errorf("static query param 'year' missing/wrong: %v", r.URL.Query())
		}

		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if limit != pageSize {
			t.Errorf("limit = %d, want %d", limit, pageSize)
		}

		var rows []map[string]interface{}
		for i := offset; i < offset+limit && i < total; i++ {
			rows = append(rows, map[string]interface{}{
				"_id":          fmt.Sprintf("id-%d", i),
				"customerName": fmt.Sprintf("Customer %d", i),
				"billAmount":   float64(100 + i),
				"region":       map[string]interface{}{"name": "Accra East"},
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"limit": limit, "offset": offset, "rows": rows,
		})
	}))
	defer srv.Close()

	job := Job{
		SourceFields: []string{"_id", "customerName", "billAmount", "region.name"},
		RecordsPath:  "rows",
		PageSize:     pageSize,
	}
	creds := httpAPICreds{BaseURL: srv.URL, APIID: apiID, APIKey: apiKey}
	values := url.Values{"year": {"2026"}}
	src := newHTTPRowSource(context.Background(), srv.Client(), creds, job, "/api/v1/sales", values)

	var gotIDs []string
	var gotNames []string
	count := 0
	for src.Next() {
		count++
		dest := make([]interface{}, 4)
		destPtrs := make([]interface{}, 4)
		for i := range dest {
			destPtrs[i] = &dest[i]
		}
		if err := src.Scan(destPtrs...); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		gotIDs = append(gotIDs, dest[0].(string))
		gotNames = append(gotNames, dest[3].(string)) // region.name
		if amt, ok := dest[2].(float64); !ok || amt < 100 {
			t.Errorf("row %d: billAmount = %v, want a float64 >= 100", count, dest[2])
		}
	}
	if err := src.Err(); err != nil {
		t.Fatalf("unexpected error after iteration: %v", err)
	}

	if count != total {
		t.Fatalf("extracted %d rows, want %d", count, total)
	}
	for i, id := range gotIDs {
		want := fmt.Sprintf("id-%d", i)
		if id != want {
			t.Errorf("row %d id = %q, want %q", i, id, want)
		}
	}
	for _, n := range gotNames {
		if n != "Accra East" {
			t.Errorf("nested field region.name = %q, want %q", n, "Accra East")
		}
	}

	// 5 records at page size 2 -> pages [0:2],[2:4],[4:5] = 3 requests,
	// the last one short (1 < pageSize) signaling completion.
	if len(requests) != 3 {
		t.Errorf("made %d requests, want 3 (pages of 2,2,1)", len(requests))
	}
}

// TestHTTPRowSource_EmptyFirstPage confirms a source with zero matching
// records terminates cleanly (one request, Next() immediately false) —
// the boundary case maxHTTPPages guards on the other end.
func TestHTTPRowSource_EmptyFirstPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"limit": 500, "offset": 0, "rows": []interface{}{}})
	}))
	defer srv.Close()

	job := Job{SourceFields: []string{"id"}, RecordsPath: "rows", PageSize: 500}
	creds := httpAPICreds{BaseURL: srv.URL, APIID: "id", APIKey: "key"}
	src := newHTTPRowSource(context.Background(), srv.Client(), creds, job, "/api/v1/sales", url.Values{})

	if src.Next() {
		t.Fatal("expected Next() to return false immediately for an empty result set")
	}
	if err := src.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestHTTPRowSource_NonOKStatusSurfacesAsError confirms an auth failure
// (or any non-2xx) becomes a real Go error rather than being silently
// treated as an empty/valid page — important since a signature bug that
// makes every request fail auth must show up loudly, not as "0 rows,
// looks done."
func TestHTTPRowSource_NonOKStatusSurfacesAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid signature"}`))
	}))
	defer srv.Close()

	job := Job{SourceFields: []string{"id"}, RecordsPath: "rows", PageSize: 500}
	creds := httpAPICreds{BaseURL: srv.URL, APIID: "id", APIKey: "key"}
	src := newHTTPRowSource(context.Background(), srv.Client(), creds, job, "/api/v1/sales", url.Values{})

	if src.Next() {
		t.Fatal("expected Next() to return false on a 401 response")
	}
	if err := src.Err(); err == nil {
		t.Fatal("expected Err() to report the 401, got nil")
	}
}
