package httpapi

import (
	"fmt"
	"net/http/httptest"
	"testing"
)

func TestTrustedProxyAddressFences(t *testing.T) {
	trusted, err := ParseTrustedProxyCIDRs("127.0.0.1/32,::1/128")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, peer string
		values     []string
		want       string
	}{
		{"direct spoof", "192.0.2.5:1000", []string{"198.51.100.2"}, "192.0.2.5"},
		{"trusted single", "127.0.0.1:1000", []string{"198.51.100.2"}, "198.51.100.2"},
		{"trusted ipv6", "[::1]:1000", []string{"2001:db8::2"}, "2001:db8::2"},
		{"comma chain", "127.0.0.1:1000", []string{"198.51.100.2, 198.51.100.3"}, "198.51.100.3"},
		{"duplicate", "127.0.0.1:1000", []string{"198.51.100.2", "198.51.100.2"}, "198.51.100.2"},
		{"host port", "127.0.0.1:1000", []string{"198.51.100.2:123"}, "127.0.0.1"},
		{"missing", "127.0.0.1:1000", nil, "127.0.0.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/auth/login", nil)
			r.RemoteAddr = c.peer
			r.Header["X-Forwarded-For"] = c.values
			if got := clientAddress(r, trusted); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
	r := httptest.NewRequest("POST", "/v1/auth/login", nil)
	r.RemoteAddr = "127.0.0.1:1000"
	r.Header.Set("X-Forwarded-For", "198.51.100.2")
	if got := clientAddress(r, nil); got != "127.0.0.1" {
		t.Fatal("default trusted forwarded header", got)
	}
	r.Header["x-forwarded-for"] = []string{"198.51.100.3"}
	if got := clientAddress(r, trusted); got != "127.0.0.1" {
		t.Fatal("mixed case duplicate trusted", got)
	}
	for _, value := range []string{"0.0.0.0/0", "::/0", "::ffff:0:0/96", "invalid"} {
		if _, err := ParseTrustedProxyCIDRs(value); err == nil {
			t.Fatal("accepted unsafe config", value)
		}
	}
}
func TestProxyClientsHaveIndependentRateBuckets(t *testing.T) {
	trusted, _ := ParseTrustedProxyCIDRs("127.0.0.1/32")
	limiter := limiter{trustedProxies: trusted}
	r := httptest.NewRequest("POST", "/v1/auth/login", nil)
	r.RemoteAddr = "127.0.0.1:1000"
	r.Header.Set("X-Forwarded-For", "198.51.100.2")
	for i := 0; i < 20; i++ {
		if !limiter.allow(r) {
			t.Fatal("early limit")
		}
	}
	if limiter.allow(r) {
		t.Fatal("missing rate fence")
	}
	r.Header.Set("X-Forwarded-For", "198.51.100.3")
	if !limiter.allow(r) {
		t.Fatal("separate proxy client shares rate bucket")
	}
}

// Finding 10. The limiter's table was bounded and, once full, answered "no" to
// everybody: one client with a supply of addresses could stop every sign-in on
// the server. The table must now be a bound on memory and nothing else.
func TestAFloodOfAddressesNeverRefusesAnHonestClient(t *testing.T) {
	limiter := limiter{}
	honest := httptest.NewRequest("POST", "/v1/direct/sign-in", nil)
	honest.RemoteAddr = "192.168.1.40:5000"
	if !limiter.allow(honest) {
		t.Fatal("the first request of all was refused")
	}
	// Far more distinct addresses than either table holds, spread over many
	// networks so that neither bound is reached by accident.
	for i := 0; i < rateAddressEntries*3; i++ {
		flood := httptest.NewRequest("POST", "/v1/direct/sign-in", nil)
		flood.RemoteAddr = fmt.Sprintf("[2001:db8:%x:%x::%x]:5000", i/256, i%256, i)
		limiter.allow(flood)
	}
	if got := len(limiter.entries); got > rateAddressEntries {
		t.Fatalf("the address table grew to %d entries against a bound of %d", got, rateAddressEntries)
	}
	if got := len(limiter.networks); got > rateNetworkEntries {
		t.Fatalf("the network table grew to %d entries against a bound of %d", got, rateNetworkEntries)
	}
	for i := 0; i < 18; i++ {
		if !limiter.allow(honest) {
			t.Fatalf("an honest client was refused attempt %d after a flood of other addresses", i+2)
		}
	}
}

// The per-address ceiling has not moved, because a real client's behaviour must
// not change: twenty attempts a minute, then a 429.
func TestThePerAddressCeilingIsUnchanged(t *testing.T) {
	limiter := limiter{}
	r := httptest.NewRequest("POST", "/v1/direct/sign-in", nil)
	r.RemoteAddr = "203.0.113.9:5000"
	for i := 0; i < rateAttemptsPerAddress; i++ {
		if !limiter.allow(r) {
			t.Fatalf("attempt %d was refused below the ceiling", i+1)
		}
	}
	if limiter.allow(r) {
		t.Fatal("the ceiling did not apply")
	}
	// A different path is a different bucket, as it always was.
	other := httptest.NewRequest("POST", "/v1/setup", nil)
	other.RemoteAddr = "203.0.113.9:5000"
	if !limiter.allow(other) {
		t.Fatal("two routes now share one bucket")
	}
}

// A household is not a botnet: two hundred devices behind one router signing in
// after a restart must all be allowed, which is why the network ceiling is far
// above the address one rather than equal to it.
func TestAHouseholdSigningInTogetherIsNotRateLimited(t *testing.T) {
	limiter := limiter{}
	for device := 0; device < 200; device++ {
		r := httptest.NewRequest("POST", "/v1/direct/sign-in", nil)
		r.RemoteAddr = fmt.Sprintf("192.168.1.%d:5000", device%254+1)
		for attempt := 0; attempt < 2; attempt++ {
			if !limiter.allow(r) {
				t.Fatalf("device %d was rate limited on attempt %d while its household signed in", device, attempt+1)
			}
		}
	}
}

// An attacker with a /64 — which is what every IPv6 customer is given — cannot
// evade the per-address count by using a new address each time.
func TestAddressRotationInsideOneNetworkIsStillCounted(t *testing.T) {
	limiter := limiter{}
	allowed := 0
	for i := 0; i < rateAttemptsPerNetwork*2; i++ {
		r := httptest.NewRequest("POST", "/v1/direct/sign-in", nil)
		r.RemoteAddr = fmt.Sprintf("[2001:db8:1:1::%x]:5000", i)
		if limiter.allow(r) {
			allowed++
		}
	}
	if allowed > rateAttemptsPerNetwork {
		t.Fatalf("%d attempts from one network were allowed against a ceiling of %d", allowed, rateAttemptsPerNetwork)
	}
	if allowed < rateAttemptsPerNetwork/2 {
		t.Fatalf("only %d attempts were allowed; the network ceiling is biting far below where it should", allowed)
	}
}
