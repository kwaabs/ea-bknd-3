package bxcconsumption

import (
	"testing"
	"time"
)

// TestParseBillMonth_AbbreviatedAndPaddedLabels guards the same bug class
// already fixed in botconsumption (see its identical test) and reported
// live again here: /bxc-consumption/detail for January 2026 returned
// {"data":[],"total":0} because bxcconsumption's service.go was copied
// from botconsumption before that fix landed — monthByName only recognized
// full month names, so an abbreviated label like "JAN-2026" (or one with
// stray whitespace) was silently excluded from resolveDateRangeToBillMonths's
// matched set every time, even when the row was real data, not missing
// data.
func TestParseBillMonth_AbbreviatedAndPaddedLabels(t *testing.T) {
	cases := []struct {
		raw       string
		wantYear  int
		wantMonth time.Month
	}{
		{"JAN-2026 ", 2026, time.January}, // the exact reported shape of bug
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
