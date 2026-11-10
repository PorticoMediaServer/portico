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
	if open, until := (Hours{}).Open(at("2026-11-11T03:00:00Z")); !open || !until.IsZero() {
		t.Error("an account with no viewing hours is limited")
	}
	// Weekdays 16:00 to 20:00 in Halifax (UTC-4 in November), and Friday 21:00 to 01:00.
	hours := Hours{Timezone: "America/Halifax", Windows: []Window{
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
	for name, bad := range map[string]Hours{
		"an unknown timezone":   {Timezone: "Mars/Olympus", Windows: []Window{{Days: []int{1}, EndMinute: 60}}},
		"a day that is not one": {Windows: []Window{{Days: []int{7}, EndMinute: 60}}},
		"a window of no length": {Windows: []Window{{Days: []int{1}, StartMinute: 60, EndMinute: 60}}},
		"a window with no days": {Windows: []Window{{EndMinute: 60}}},
	} {
		if bad.Valid() {
			t.Errorf("%s was accepted", name)
		}
	}
}

// Relaxed, Standard and Strict are the same rules with different numbers.
func TestStrictness(t *testing.T) {
	p := DefaultPolicy()
	if p.Problem() != "" {
		t.Fatalf("the default policy is refused at %s", p.Problem())
	}
	waits := func(name string, failures ...int) (out []time.Duration) {
		p.PasswordAttempts = name
		for _, n := range failures {
			out = append(out, p.Passwords().Limit.Delay(n))
		}
		return out
	}
	for name, want := range map[string][]time.Duration{
		"relaxed":  {0, 0, time.Second, time.Minute},
		"standard": {0, time.Second, 32 * time.Second, time.Minute},
		"strict":   {10 * time.Second, 40 * time.Second, 15 * time.Minute, 15 * time.Minute},
	} {
		if got := waits(name, 3, 5, 10, 30); got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
			t.Errorf("%s passwords wait %v after 3, 5, 10 and 30 wrong, want %v", name, got, want)
		}
	}
	for name, want := range map[string][3]time.Duration{
		"relaxed": {0, 30 * time.Second, 0}, "standard": {2 * time.Second, 5 * time.Minute, time.Minute}, "strict": {2 * time.Second, 15 * time.Minute, 5 * time.Minute},
	} {
		p.PINAttempts = name
		if got := [3]time.Duration{p.PINs().Device.Delay(1), p.PINs().Device.Delay(5), p.PINs().Profile.Delay(10)}; got != want {
			t.Errorf("%s PINs wait %v, want %v", name, got, want)
		}
	}
	if (PasswordRules{SourceFailures: 15}).SourceDelay(15) != SourceBlock || passwordStrictness["relaxed"].SourceDelay(59) != 0 {
		t.Error("a network is not blocked at its number of failures")
	}
}
