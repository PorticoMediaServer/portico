package auth

import (
	"testing"
	"time"
)

// Viewing hours are read in the account's own timezone, and a window may run past midnight.
func TestViewingHours(t *testing.T) {
	at := func(text string) time.Time {
		parsed, err := time.Parse(time.RFC3339, text)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	if open, until := (ViewingHours{}).Open(at("2026-11-11T03:00:00Z")); !open || !until.IsZero() {
		t.Error("an account with no viewing hours is limited")
	}
	// Weekdays 16:00 to 20:00 in Halifax (UTC-4 in November), and Friday 21:00 to 01:00.
	hours := ViewingHours{Timezone: "America/Halifax", Windows: []ViewingWindow{
		{Days: []int{1, 2, 3, 4, 5}, StartMinute: 16 * 60, EndMinute: 20 * 60},
		{Days: []int{5}, StartMinute: 21 * 60, EndMinute: 60},
	}}
	if !hours.Valid() {
		t.Fatal("the hours are refused")
	}
	for _, c := range []struct {
		now, until string
	}{
		{"2026-11-11T19:59:59Z", ""},                     // Wednesday 15:59 in Halifax
		{"2026-11-11T20:00:00Z", "2026-11-12T00:00:00Z"}, // Wednesday 16:00
		{"2026-11-11T23:59:00Z", "2026-11-12T00:00:00Z"}, // Wednesday 19:59
		{"2026-11-12T00:00:00Z", ""},                     // Wednesday 20:00
		{"2026-11-14T20:30:00Z", ""},                     // Saturday afternoon
		{"2026-11-14T04:30:00Z", "2026-11-14T05:00:00Z"}, // half past midnight on Saturday: Friday's late window
		{"2026-11-15T04:30:00Z", ""},                     // the same time a day later
	} {
		open, until := hours.Open(at(c.now))
		if open != (c.until != "") || open && !until.Equal(at(c.until)) {
			t.Errorf("at %s: open %v until %s, want until %q", c.now, open, until.UTC().Format(time.RFC3339), c.until)
		}
	}
	for name, bad := range map[string]ViewingHours{
		"an unknown timezone":   {Timezone: "Mars/Olympus", Windows: []ViewingWindow{{Days: []int{1}, EndMinute: 60}}},
		"a day that is not one": {Windows: []ViewingWindow{{Days: []int{7}, EndMinute: 60}}},
		"a window of no length": {Windows: []ViewingWindow{{Days: []int{1}, StartMinute: 60, EndMinute: 60}}},
		"a window with no days": {Windows: []ViewingWindow{{EndMinute: 60}}},
	} {
		if bad.Valid() {
			t.Errorf("%s was accepted", name)
		}
	}
}

// Relaxed, Standard and Strict are the same rules with different numbers.
