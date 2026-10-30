package networking

import (
	"testing"
	"time"
)

func TestRetryAtSpreadsAFleetAndNeverUndercutsRetryAfter(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	// At the cap a fleet that failed together must be spread over minutes, not seconds.
	earliest, latest := now.Add(time.Hour), now
	for range 2000 {
		at := RetryAt(now, 5*time.Second, 99, 8, time.Time{})
		if at.Before(earliest) {
			earliest = at
		}
		if at.After(latest) {
			latest = at
		}
	}
	base := 5 * time.Second << 8
	if earliest.Before(now.Add(base)) || latest.After(now.Add(base+base/2)) {
		t.Fatal("outside the exponential delay plus half", earliest.Sub(now), latest.Sub(now))
	}
	if spread := latest.Sub(earliest); spread < 5*time.Minute {
		t.Fatal("a capped backoff of ~21 minutes must spread a herd by minutes, got", spread)
	}
	// Retry-After is a floor, and everyone given the same floor must not return together.
	floor := now.Add(time.Hour)
	first, varied := RetryAt(now, 5*time.Second, 1, 8, floor), false
	for range 200 {
		at := RetryAt(now, 5*time.Second, 1, 8, floor)
		if at.Before(floor) {
			t.Fatal("undercut Retry-After", at.Sub(now))
		}
		varied = varied || !at.Equal(first)
	}
	if !varied {
		t.Fatal("every caller given the same Retry-After returned at the same instant")
	}
	// A floor earlier than the computed delay changes nothing.
	if at := RetryAt(now, 5*time.Second, 3, 8, now.Add(time.Second)); at.Before(now.Add(40 * time.Second)) {
		t.Fatal(at.Sub(now))
	}
}
