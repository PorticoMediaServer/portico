package api

import (
	"crypto/tls"
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
	mine := []interfaceAddress{{"lo", ip("127.0.0.1")}, {"docker0", ip("172.17.0.1")}, {"eth1", ip("10.8.0.5")},
		{"eth0", ip("192.168.1.20")}, {"eth0", ip("203.0.113.4")}, {"eth0", ip("fd00::5")}}
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

// Container bridges, virtual machine bridges and VPN tunnels are never home addresses, and
// with no router found they cannot push the real home address out of the four.
func TestVirtualInterfacesAreNotHome(t *testing.T) {
	ip := netip.MustParseAddr
	networks := auth.Networks{Home: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16")}}
	mine := []interfaceAddress{{"docker0", ip("172.17.0.1")}, {"br-1a2b", ip("172.18.0.1")}, {"virbr0", ip("192.168.122.1")},
		{"wg0", ip("10.8.0.1")}, {"tun0", ip("10.9.0.1")}, {"veth12", ip("172.19.0.1")}, {"enp3s0", ip("192.168.1.20")}}
	if got := homeAddresses(mine, netip.Addr{}, networks); len(got) != 1 || got[0] != ip("192.168.1.20") {
		t.Fatalf("with no router found the home addresses are %v", got)
	}
	if got := homeAddresses(mine, ip("172.17.0.1"), networks); len(got) != 1 || got[0] != ip("192.168.1.20") {
		t.Fatalf("with the route to the router through a bridge the home addresses are %v", got)
	}
}

// Only an address the internet can route to is published as the public one.
func TestOnlyARoutableAddressIsPublishedAsPublic(t *testing.T) {
	for _, reported := range []string{"100.64.3.9", "0.0.0.0", "127.0.0.1", "169.254.1.1", "224.0.0.1", "10.0.0.1"} {
		if got := publish(nil, 32500, reported, 32500, ""); len(got) != 0 {
			t.Errorf("%s was published as the public address: %+v", reported, got)
		}
	}
	if got := publish(nil, 32500, "198.51.100.7", 32500, ""); len(got) != 1 {
		t.Errorf("a public address was not published: %+v", got)
	}
}

// Under Enhanced, the name this server publishes for its public address is one it answers at:
// an app away from home is given that name and nothing else. A zone alone puts Enhanced in force.
func TestEnhancedAnswersAtThePublishedPorticoName(t *testing.T) {
	s := newTestServer(t)
	owner := setupFrom(t, s, "127.0.0.1", SetupRemote{}).AccessToken
	reach(s, "POST", "/v1/setup/finish", owner, "127.0.0.1", "localhost", false, SetupFinish{})
	s.SetPorticoZone("ab12cd.portico.direct", func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &tls.Certificate{}, nil })
	var st RemoteAccessStatus
	jsonInto(reach(s, "GET", "/v1/admin/remote-access", owner, "127.0.0.1", "localhost", false, nil), &st)
	if st.RemoteSecurity.Active != "enhanced" {
		t.Fatalf("Enhanced is not in force with a Portico zone: %+v", st.RemoteSecurity)
	}
	name := publish(nil, 32500, "198.51.100.7", 32500, s.porticoZone())[0].Name
	if code := reach(s, "GET", "/v1/system", "", away, name+":32500", true, nil).Code; code != 200 {
		t.Errorf("the published name %s answered %d from outside under Enhanced", name, code)
	}
	for _, host := range []string{"media.example.com", name + ":8443", "198-51-100-7.other99.portico.direct:32500"} {
		if code := reach(s, "GET", "/v1/system", "", away, host, true, nil).Code; code != 403 {
			t.Errorf("%s answered %d under Enhanced", host, code)
		}
	}
	// With the hosted side's address and certificate as well, the published name still works.
	s.SetPorticoAddress("ab12cd.portico.direct", true, "")
	if code := reach(s, "GET", "/v1/system", "", away, name+":32500", true, nil).Code; code != 200 {
		t.Errorf("the published name answered %d once the Portico address was set", code)
	}
}
