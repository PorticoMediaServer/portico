package auth

import (
	"net/http"
	"net/netip"
	"testing"
)

// Who a request came from. A forwarded address is believed only from a proxy the owner
// listed, read from the end that proxy wrote, and never when the header cannot be read.
func TestClientAddress(t *testing.T) {
	proxies, err := ParseNetworks([]string{"10.0.0.2", "172.18.0.0/16"})
	if err != nil {
		t.Fatal(err)
	}
	n := Networks{Proxies: proxies}
	for _, c := range []struct {
		name, peer string
		forwarded  []string
		want       string
	}{
		{"no proxy", "203.0.113.5:4000", nil, "203.0.113.5"},
		{"a stranger's header is ignored", "203.0.113.5:4000", []string{"192.168.1.10"}, "203.0.113.5"},
		{"a trusted proxy is believed", "10.0.0.2:4000", []string{"198.51.100.7"}, "198.51.100.7"},
		{"what the client wrote before the proxy's entry is not", "10.0.0.2:4000", []string{"192.168.1.10, 198.51.100.7"}, "198.51.100.7"},
		{"a chain of trusted proxies", "10.0.0.2:4000", []string{"198.51.100.7, 172.18.0.3"}, "198.51.100.7"},
		{"two header lines", "10.0.0.2:4000", []string{"192.168.1.10", "198.51.100.7"}, "198.51.100.7"},
		{"a header that cannot be read", "10.0.0.2:4000", []string{"198.51.100.7, unknown"}, "10.0.0.2"},
		{"a trusted proxy that sent nothing", "10.0.0.2:4000", nil, "10.0.0.2"},
		{"IPv6 through a proxy", "[::ffff:10.0.0.2]:4000", []string{"2001:db8::1"}, "2001:db8::1"},
	} {
		r := &http.Request{RemoteAddr: c.peer, Header: http.Header{}}
		for _, line := range c.forwarded {
			r.Header.Add("X-Forwarded-For", line)
		}
		if got, ok := n.Client(r); !ok || got.String() != c.want {
			t.Errorf("%s: got %v, want %s", c.name, got, c.want)
		}
	}
}

// Home is what the owner listed and nothing else; until they choose, a public address is
// never home and this machine always is.
func TestHome(t *testing.T) {
	home, err := ParseNetworks([]string{"192.168.1.0/24", "100.64.0.0/10", "fd00::/8"})
	if err != nil {
		t.Fatal(err)
	}
	n := Networks{Home: home}
	for addr, want := range map[string]bool{"192.168.1.20": true, "::ffff:192.168.1.20": true, "100.101.102.103": true, "fd00::5": true,
		"192.168.2.20": false, "127.0.0.1": false, "203.0.113.5": false} {
		if got := n.AtHome(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s at home: %v, want %v", addr, got, want)
		}
	}
	byDefault := Networks{Home: DefaultHome()}
	if !byDefault.AtHome(netip.MustParseAddr("127.0.0.1")) || byDefault.AtHome(netip.MustParseAddr("203.0.113.5")) {
		t.Errorf("the default home networks are %v", byDefault.Home)
	}
	for _, bad := range []string{"", "home", "192.168.1.0/33", "fe80::1%eth0"} {
		if _, err := ParseNetworks([]string{bad}); err == nil {
			t.Errorf("%q was read as a network", bad)
		}
	}
}
