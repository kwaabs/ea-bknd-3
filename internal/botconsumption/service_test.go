package botconsumption

import (
	"testing"
	"time"
)

// TestParseBillMonth_AbbreviatedAndPaddedLabels guards the exact bug
// reported live: app.bot_consumption had "JAN-2026 " (abbreviated month
// name, uppercase, trailing space) alongside "june-2026" (full name,
// lowercase) — a date-range query for January silently matched nothing
// because parseBillMonth only recognized full month names, so the
// January row was excluded from resolveDateRangeToBillMonths's matched
// set every time, even though it was real data, not missing data.
func TestParseBillMonth_AbbreviatedAndPaddedLabels(t *testing.T) {
	cases := []struct {
		raw       string
		wantYear  int
		wantMonth time.Month
	}{
		{"JAN-2026 ", 2026, time.January}, // the exact reported label
		{"june-2026", 2026, time.June},
		{"Jan-2026", 2026, time.January},
		{"jan-2026", 2026, time.January},
		{"  DEC-2025", 2025, time.December},
		{"Sept-2026", 2026, time.September},
		{"February-2026", 2026, time.February},
	}
	for _, c := range cases {
		got, ok := parseBillMonth(c.raw)
		if !ok {
			t.Errorf("parseBillMonth(%q) failed to parse, expected success", c.raw)
			continue
		}
		if got.Year() != c.wantYear || got.Month() != c.wantMonth {
			t.Errorf("parseBillMonth(%q) = %s %d, want %s %d", c.raw, got.Month(), got.Year(), c.wantMonth, c.wantYear)
		}
	}
}

func TestParseBillMonth_RejectsMalformed(t *testing.T) {
	invalid := []string{"", "2026", "notamonth-2026", "january-abc", "january"}
	for _, raw := range invalid {
		if _, ok := parseBillMonth(raw); ok {
			t.Errorf("parseBillMonth(%q) expected to fail, but it parsed", raw)
		}
	}
}
