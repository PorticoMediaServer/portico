package remotegate

import (
	"testing"
	"time"
)

func TestRefusalsCountByReasonAndAgeOutOfTheirWindow(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 30, 0, 0, time.UTC)
	r := &Refusals{now: func() time.Time { return now }}
	r.Count(ReasonName)
	r.Count(ReasonName)
	r.Count(ReasonAccess)
	r.Count("unknown")
	now = now.Add(-30 * time.Hour)
	r.Count(ReasonHTTP)
	now = now.Add(30 * time.Hour)
	if total, by := r.Totals(24); total != 3 || by[ReasonName] != 2 || by[ReasonAccess] != 1 || by[ReasonHTTP] != 0 || len(by) != len(RefusalReasons) {
		t.Fatalf("24 h: %d %v", total, by)
	}
	if total, by := r.Totals(7 * 24); total != 4 || by[ReasonHTTP] != 1 {
		t.Fatalf("7 d: %d %v", total, by)
	}
	// A week later the same ring slots are reused, never added to.
	now = now.Add(7 * 24 * time.Hour)
	r.Count(ReasonIP)
	if total, by := r.Totals(7 * 24); total != 1 || by[ReasonIP] != 1 {
		t.Fatalf("a week on: %d %v", total, by)
	}
	var none *Refusals
	none.Count(ReasonName)
	if total, by := none.Totals(24); total != 0 || len(by) != len(RefusalReasons) {
		t.Fatalf("nil: %d %v", total, by)
	}
}

func TestNameReasonCountsAddressesAndMissingNamesAsIP(t *testing.T) {
	for host, want := range map[string]string{
		"": ReasonIP, "198.51.100.7": ReasonIP, "198.51.100.7:32500": ReasonIP, "[2001:db8::1]:443": ReasonIP,
		"media.example.com": ReasonHTTP, "Media.Example.com:80": ReasonHTTP,
	} {
		if got := NameReason(host, ReasonHTTP); got != want {
			t.Errorf("%q: %s, want %s", host, got, want)
		}
	}
}
