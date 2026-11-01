package remotegate

import (
	"net/netip"
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

// Docker bridge mode: the container detects only its bridge, yet a laptop on the
// owner's LAN is home, and so is every private and link-local range. A public peer
// stays outside, and the tailnet's IPv6 range stays outside while remote access is Off.
func TestHomeAlwaysIncludesEveryPrivateRange(t *testing.T) {
	bridge := []netip.Prefix{netip.MustParsePrefix("172.17.0.0/16")}
	for _, access := range []string{AccessOn, AccessPrivate, AccessOff} {
		p := NewPolicy(Settings{Security: SecurityEnhanced, Access: access, Home: bridge})
		for _, raw := range []string{"192.168.1.20", "10.1.2.3", "172.31.0.5", "172.17.0.1", "169.254.3.3", "fd00::20", "fe80::1", "127.0.0.2", "::1"} {
			if !p.Home(netip.MustParseAddr(raw)) {
				t.Errorf("%s: %s is not home", access, raw)
			}
		}
		for _, raw := range []string{"203.0.113.9", "8.8.8.8", "2001:db8::1", "172.32.0.1"} {
			if p.Home(netip.MustParseAddr(raw)) {
				t.Errorf("%s: public %s counted as home", access, raw)
			}
		}
		tailnetV6 := p.Home(netip.MustParseAddr("fd7a:115c:a1e0::5"))
		if tailnetV6 != (access != AccessOff) {
			t.Errorf("%s: tailnet IPv6 home=%v", access, tailnetV6)
		}
	}
	if LocalAddr(netip.MustParseAddr("fd7a:115c:a1e0::5")) || LocalAddr(netip.MustParseAddr("100.100.1.1")) || !LocalAddr(netip.MustParseAddr("192.168.1.20")) {
		t.Fatal("LocalAddr is the fixed private set, without the tailnet")
	}
	// Enhanced answers plain HTTP from the LAN laptop and refuses it from a public peer.
	p := NewPolicy(Settings{Security: SecurityEnhanced, Access: AccessOn, Home: bridge})
	if p.Socket(netip.MustParseAddr("192.168.1.20"), false) != Admit || p.Socket(netip.MustParseAddr("203.0.113.9"), false) == Admit {
		t.Fatal("socket decision ignores the always-local ranges")
	}
}
