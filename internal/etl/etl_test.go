package etl

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestQuoteIdent(t *testing.T) {
	valid := []string{"app", "meter_number", "_col", "Col1"}
	for _, s := range valid {
		got, err := quoteIdent(s)
		if err != nil {
			t.Errorf("quoteIdent(%q) unexpected error: %v", s, err)
		}
		want := `"` + s + `"`
		if got != want {
			t.Errorf("quoteIdent(%q) = %q, want %q", s, got, want)
		}
	}

	invalid := []string{"", "1col", "col; DROP TABLE x", "col name", `col"name`, "col-name"}
	for _, s := range invalid {
		if _, err := quoteIdent(s); err == nil {
			t.Errorf("quoteIdent(%q) expected error, got none", s)
		}
	}
}

func TestBuildInsertSQL_Plain(t *testing.T) {
	job := Job{
		DestSchema:  "app",
		DestTable:   "raw_invoices",
		DestColumns: []string{"id", "amount"},
	}
	batch := [][]interface{}{
		{int64(1), 10.5},
		{int64(2), 20.0},
	}
	query, args, err := buildInsertSQL(job, batch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantQuery := `INSERT INTO "app"."raw_invoices" ("id", "amount") VALUES ($1, $2), ($3, $4)`
	if query != wantQuery {
		t.Errorf("query = %q, want %q", query, wantQuery)
	}
	if len(args) != 4 || args[0] != int64(1) || args[3] != 20.0 {
		t.Errorf("args = %v, unexpected", args)
	}
	if strings.Contains(query, "ON CONFLICT") {
		t.Errorf("plain insert should not contain ON CONFLICT: %q", query)
	}
}

func TestBuildInsertSQL_Upsert(t *testing.T) {
	job := Job{
		DestSchema:      "app",
		DestTable:       "raw_invoices",
		DestColumns:     []string{"invoice_id", "amount", "updated_at"},
		ConflictColumns: []string{"invoice_id"},
	}
	batch := [][]interface{}{{"INV-1", 10.5, "2026-01-01"}}
	query, _, err := buildInsertSQL(job, batch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(query, `ON CONFLICT ("invoice_id") DO UPDATE SET "amount" = EXCLUDED."amount", "updated_at" = EXCLUDED."updated_at"`) {
		t.Errorf("query missing expected ON CONFLICT clause: %q", query)
	}
}

func TestBuildInsertSQL_RejectsBadIdentifier(t *testing.T) {
	job := Job{
		DestSchema:  "app",
		DestTable:   `raw_invoices"; DROP TABLE app.users; --`,
		DestColumns: []string{"id"},
	}
	if _, _, err := buildInsertSQL(job, [][]interface{}{{1}}); err == nil {
		t.Fatal("expected error for malicious dest_table, got none")
	}
}

func TestFormatWatermarkLiteral(t *testing.T) {
	cases := []struct {
		raw     string
		typ     WatermarkType
		want    string
		wantErr bool
	}{
		{"2026-07-29T00:00:00", WatermarkTimestamp, "'2026-07-29T00:00:00'", false},
		{"O'Brien", WatermarkString, "'O''Brien'", false},
		{"42", WatermarkInteger, "42", false},
		{"not-a-number", WatermarkInteger, "", true},
	}
	for _, c := range cases {
		got, err := formatWatermarkLiteral(c.raw, c.typ)
		if c.wantErr {
			if err == nil {
				t.Errorf("formatWatermarkLiteral(%q, %q) expected error, got none", c.raw, c.typ)
			}
			continue
		}
		if err != nil {
			t.Errorf("formatWatermarkLiteral(%q, %q) unexpected error: %v", c.raw, c.typ, err)
			continue
		}
		if got != c.want {
			t.Errorf("formatWatermarkLiteral(%q, %q) = %q, want %q", c.raw, c.typ, got, c.want)
		}
	}
}

func TestBuildQuery_FullRefreshRejectsWatermarkToken(t *testing.T) {
	job := Job{
		Name:        "j1",
		Mode:        ModeFullRefresh,
		SourceQuery: "SELECT * FROM t WHERE updated_at > {{WATERMARK}}",
	}
	if _, err := buildQuery(job, ""); err == nil {
		t.Fatal("expected error for full_refresh job referencing {{WATERMARK}}, got none")
	}
}

func TestBuildQuery_IncrementalSubstitutesToken(t *testing.T) {
	wt := WatermarkTimestamp
	job := Job{
		Name:          "j1",
		Mode:          ModeIncremental,
		WatermarkType: &wt,
		SourceQuery:   "SELECT * FROM t WHERE updated_at > {{WATERMARK}} ORDER BY updated_at",
	}
	query, err := buildQuery(job, "2026-01-01T00:00:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "SELECT * FROM t WHERE updated_at > '2026-01-01T00:00:00' ORDER BY updated_at"
	if query != want {
		t.Errorf("query = %q, want %q", query, want)
	}
}

func TestBuildQuery_IncrementalRequiresToken(t *testing.T) {
	wt := WatermarkInteger
	job := Job{
		Name:          "j1",
		Mode:          ModeIncremental,
		WatermarkType: &wt,
		SourceQuery:   "SELECT * FROM t",
	}
	if _, err := buildQuery(job, "0"); err == nil {
		t.Fatal("expected error for incremental job missing {{WATERMARK}}, got none")
	}
}

func TestNextTriggerTime_PicksSoonestAndRollsOverToTomorrow(t *testing.T) {
	now := time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC) // 02:00 UTC

	// One time later today, one already passed today -> picks today's later one.
	got, err := nextTriggerTime([]string{"01:00", "03:30"}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 9, 4, 3, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// All times already passed today -> rolls to the soonest tomorrow.
	got, err = nextTriggerTime([]string{"01:00", "01:30"}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want = time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNextTriggerTime_RejectsBadFormat(t *testing.T) {
	if _, err := nextTriggerTime([]string{"25:99"}, time.Now()); err == nil {
		t.Fatal("expected error for invalid trigger time, got none")
	}
	if _, err := nextTriggerTime(nil, time.Now()); err == nil {
		t.Fatal("expected error for empty trigger_times, got none")
	}
}

func TestNormalizeValue_BytesBecomeString(t *testing.T) {
	got := normalizeValue([]byte("hello"))
	if got != "hello" {
		t.Errorf("normalizeValue([]byte) = %v (%T), want string \"hello\"", got, got)
	}
	if normalizeValue(int64(5)) != int64(5) {
		t.Errorf("normalizeValue should pass non-[]byte values through unchanged")
	}
}

func TestIsReadOnlyQuery(t *testing.T) {
	valid := []string{
		"SELECT * FROM invoices",
		"  select id from t  ",
		"WITH x AS (SELECT 1) SELECT * FROM x",
		"SELECT COUNT(*) FROM invoices",
		"SELECT COUNT(DISTINCT customer_id) FROM invoices;",
	}
	for _, q := range valid {
		if err := isReadOnlyQuery(q); err != nil {
			t.Errorf("isReadOnlyQuery(%q) unexpected error: %v", q, err)
		}
	}

	invalid := []string{
		"",
		"DROP TABLE invoices",
		"DELETE FROM invoices",
		"UPDATE invoices SET amount = 0",
		"SELECT * FROM invoices; DROP TABLE invoices",
		"SELECT * FROM invoices WHERE 1=1; DELETE FROM invoices",
		"EXEC sp_who",
		"INSERT INTO invoices VALUES (1)",
	}
	for _, q := range invalid {
		if err := isReadOnlyQuery(q); err == nil {
			t.Errorf("isReadOnlyQuery(%q) expected error, got none", q)
		}
	}
}

func TestJobInputValidate_RejectsNonSelectSourceQuery(t *testing.T) {
	in := JobInput{
		Name:         "bad-job",
		SourceID:     "some-source",
		SourceQuery:  "DELETE FROM invoices",
		DestColumns:  []string{"id"},
		Mode:         ModeFullRefresh,
		TriggerTimes: []string{"01:00"},
	}
	if err := in.validate(); err == nil {
		t.Fatal("expected validation error for a non-SELECT source_query, got none")
	}
}

func TestJobInputValidate_IncrementalRequiresWatermarkInDestColumns(t *testing.T) {
	wt := WatermarkTimestamp
	col := "updated_at"
	in := JobInput{
		Name:            "job",
		SourceID:        "some-source",
		SourceQuery:     "SELECT id FROM t WHERE updated_at > {{WATERMARK}} ORDER BY updated_at",
		DestColumns:     []string{"id"}, // missing "updated_at"
		Mode:            ModeIncremental,
		WatermarkColumn: &col,
		WatermarkType:   &wt,
		TriggerTimes:    []string{"01:00"},
	}
	if err := in.validate(); err == nil {
		t.Fatal("expected validation error when watermark_column is missing from dest_columns, got none")
	}
}

// TestSourceJSONSerialization guards against the bug this had until it was
// caught by testing the real backend against the real frontend instead of
// mocked responses: without explicit json tags, encoding/json falls back
// to Go field names (PascalCase), silently breaking every snake_case
// field the frontend expects. Also confirms the encrypted password blob
// never appears in a response, even under its own field name.
func TestSourceJSONSerialization(t *testing.T) {
	src := Source{
		ID:                "src-1",
		Name:              "oracle_finance",
		Kind:              KindOracle,
		Host:              "oracle.internal",
		Port:              1521,
		DatabaseName:      "FINPROD",
		Username:          "etl_reader",
		PasswordEncrypted: []byte("super-secret-ciphertext"),
		ExtraParams:       map[string]string{},
		Enabled:           true,
		HasPassword:       true,
	}
	b, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := string(b)

	for _, key := range []string{
		`"id"`, `"name"`, `"kind"`, `"host"`, `"port"`,
		`"database_name"`, `"username"`, `"extra_params"`, `"enabled"`, `"has_password"`,
	} {
		if !strings.Contains(body, key) {
			t.Errorf("Source JSON missing expected snake_case key %s: %s", key, body)
		}
	}
	if strings.Contains(body, "PasswordEncrypted") || strings.Contains(body, "password_encrypted") {
		t.Errorf("Source JSON must never expose the encrypted password field: %s", body)
	}
	if strings.Contains(body, "super-secret-ciphertext") {
		t.Errorf("Source JSON must never expose ciphertext: %s", body)
	}
}

func TestJobJSONSerialization(t *testing.T) {
	job := Job{
		ID:           "job-1",
		Name:         "oracle_finance_invoices",
		SourceID:     "src-1",
		SourceQuery:  "SELECT 1",
		DestSchema:   "app",
		DestTable:    "raw_oracle_invoices",
		DestColumns:  []string{"id"},
		Mode:         ModeFullRefresh,
		TriggerTimes: []string{"01:00"},
		BatchSize:    5000,
	}
	b, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := string(b)
	for _, key := range []string{`"id"`, `"name"`, `"source_id"`, `"source_query"`, `"dest_schema"`, `"dest_table"`, `"dest_columns"`, `"mode"`, `"trigger_times"`, `"batch_size"`} {
		if !strings.Contains(body, key) {
			t.Errorf("Job JSON missing expected snake_case key %s: %s", key, body)
		}
	}
}

func TestSourceInputValidate_NoLongerRequiresPassword(t *testing.T) {
	// validate() itself doesn't check Password — that's enforced
	// separately by CreateSource (required) vs UpdateSource (optional,
	// omitted = keep existing), see service.go.
	in := SourceInput{
		Name: "s", Kind: KindPostgres, Host: "h", Port: 5432,
		DatabaseName: "d", Username: "u",
	}
	if err := in.validate(); err != nil {
		t.Errorf("validate() should not require a password, got error: %v", err)
	}
}

func TestWatermarkToString(t *testing.T) {
	if _, err := watermarkToString(nil); err == nil {
		t.Error("expected error for NULL watermark value")
	}
	if s, _ := watermarkToString("abc"); s != "abc" {
		t.Errorf("string passthrough failed: %q", s)
	}
	if s, _ := watermarkToString(int64(42)); s != "42" {
		t.Errorf("int64 formatting failed: %q", s)
	}
}
