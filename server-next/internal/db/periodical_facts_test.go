package db

import (
	"testing"
	"time"
)

func TestIssueFrequencyFromTheTypicalGap(t *testing.T) {
	day := func(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
	for want, dates := range map[string][]string{
		"monthly": {"1929-01-01", "1929-02-01", "1929-03-01", "1929-05-01", "1929-06-01"}, // a missing issue
		"weekly":  {"2026-11-01", "2026-11-08", "2026-11-15"},
		"daily":   {"1912-04-15", "1912-04-16", "1912-04-17"},
		"":        {"1846-05-14", "1846-07-09", "1846-08-27"}, // no rhythm
	} {
		var in []time.Time
		for _, d := range dates {
			in = append(in, day(d))
		}
		if got := issueFrequency(in); got != want {
			t.Errorf("%v: %q, want %q", dates, got, want)
		}
	}
	if got := issueFrequency([]time.Time{day("2026-01-01"), day("2026-02-01")}); got != "" {
		t.Errorf("two issues: %q", got)
	}
}
