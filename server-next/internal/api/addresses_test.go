package api

import (
	"net/netip"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
)

// A phone at home is given the server's own home address first, at the port the server
// listens on and under its Portico name, so it reaches the server over HTTPS without going
// through the router; the public address follows at the public port. Addresses that are not
// on the home networks, the machine itself, and public addresses of the machine are not home.
func TestHomeAddressesComeFirst(t *testing.T) {
	ip := netip.MustParseAddr
	networks := auth.Networks{Home: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24"), netip.MustParsePrefix("10.8.0.0/16")}}
	mine := []netip.Addr{ip("127.0.0.1"), ip("172.17.0.1"), ip("10.8.0.5"), ip("192.168.1.20"), ip("203.0.113.4"), ip("fd00::5")}
	home := homeAddresses(mine, ip("192.168.1.20"), networks)
	if len(home) != 2 || home[0] != ip("192.168.1.20") || home[1] != ip("10.8.0.5") {
		t.Fatalf("the home addresses are %v", home)
	}
	got := publish(home, 32400, "198.51.100.7", 40000, "ab12cd.portico.direct")
	want := []PublishedAddress{
		{Address: "192.168.1.20", Port: 32400, Home: true, Name: "192-168-1-20.ab12cd.portico.direct"},
		{Address: "10.8.0.5", Port: 32400, Home: true, Name: "10-8-0-5.ab12cd.portico.direct"},
		{Address: "198.51.100.7", Port: 40000, Name: "198-51-100-7.ab12cd.portico.direct"},
	}
	if len(got) != len(want) {
		t.Fatalf("published %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("address %d is %+v, want %+v", i, got[i], want[i])
		}
	}
	// With no Portico zone (local sign-in) the addresses carry no name; a private address
	// is never given as the public one.
	if got := publish(home, 32400, "10.0.0.1", 32500, ""); len(got) != 2 || got[0].Name != "" {
		t.Fatalf("with no zone: %+v", got)
	}
}

// A server that listens only on this machine publishes nothing.
func TestLoopbackServerPublishesNoAddresses(t *testing.T) {
	s := newTestServer(t)
	if got := s.PublishedAddresses(); len(got) != 0 {
		t.Fatalf("a loopback-only server publishes %+v", got)
	}
}
