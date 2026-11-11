package auth

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

// Who a request came from. A forwarded address is believed only from a proxy the owner
// listed, and read only from the end that proxy wrote.
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
		{"the proxy's own entry cannot be read", "10.0.0.2:4000", []string{"198.51.100.7, unknown"}, "10.0.0.2"},
		{"junk a client sent first does not make it the proxy", "10.0.0.2:4000", []string{"not an address, 198.51.100.7"}, "198.51.100.7"},
		{"nor does a very long list", "10.0.0.2:4000", []string{strings.Repeat("192.168.1.10, ", 40) + "198.51.100.7"}, "198.51.100.7"},
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

// A proxy on the home network that the owner has not listed must not make everyone who
// comes through it count as at home.
func TestUnlistedProxyIsNeverHome(t *testing.T) {
	n := Networks{Home: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("192.168.1.0/24")},
		Proxies: []netip.Prefix{netip.MustParsePrefix("192.168.1.2/32")}}
	for name, c := range map[string]struct {
		peer, forwarded string
		home            bool
	}{
		"straight from home":                        {"192.168.1.20", "", true},
		"through an unlisted proxy on this machine": {"127.0.0.1", "203.0.113.9", false},
		"the same, claiming a home address":         {"127.0.0.1", "192.168.1.20", false},
		"through the listed proxy, from home":       {"192.168.1.2", "192.168.1.20", true},
		"through the listed proxy, from away":       {"192.168.1.2", "203.0.113.9", false},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.peer + ":4000"
		if c.forwarded != "" {
			r.Header.Set("X-Forwarded-For", c.forwarded)
		}
		if got := n.FromHome(r); got != c.home {
			t.Errorf("%s: from home is %v, want %v", name, got, c.home)
		}
	}
}

// A listed proxy is believed about where its visitors are, and when it does not say, they
// are away: the proxy's own address is usually a home one and must not stand for them.
func TestListedProxyThatGivesNoAddressIsNeverHome(t *testing.T) {
	n := Networks{Home: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("192.168.1.0/24")},
		Proxies: []netip.Prefix{netip.MustParsePrefix("192.168.1.2/32"), netip.MustParsePrefix("127.0.0.1/32")}}
	for name, c := range map[string]struct {
		peer      string
		forwarded []string
		home      bool
	}{
		"a forwarded home address":    {"192.168.1.2", []string{"192.168.1.20"}, true},
		"a forwarded outside address": {"192.168.1.2", []string{"203.0.113.9"}, false},
		"no header":                   {"192.168.1.2", nil, false},
		"no header, on this machine":  {"127.0.0.1", nil, false},
		"an empty header":             {"192.168.1.2", []string{""}, false},
		"junk":                        {"192.168.1.2", []string{"unknown"}, false},
		"junk after a home address":   {"192.168.1.2", []string{"192.168.1.20, _hidden"}, false},
		"only the proxies themselves": {"192.168.1.2", []string{"127.0.0.1"}, false},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.peer + ":4000"
		for _, line := range c.forwarded {
			r.Header.Add("X-Forwarded-For", line)
		}
		if got := n.FromHome(r); got != c.home {
			t.Errorf("%s: from home is %v, want %v", name, got, c.home)
		}
		// Wrong guesses are still counted against something: the proxy's own address.
		if addr, ok := n.Client(r); !c.home && len(c.forwarded) == 0 && (!ok || addr.String() != c.peer) {
			t.Errorf("%s: counted against %v, want the proxy %s", name, addr, c.peer)
		}
	}
}

// An address on this machine or a private network that sends X-Forwarded-For and is not a
// trusted proxy is the owner's own proxy, not yet listed. Its header is still not believed.
func TestUnlistedLocalProxy(t *testing.T) {
	n := Networks{Home: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, Proxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.2/32")}}
	for name, c := range map[string]struct {
		peer, forwarded string
		want            bool
	}{
		"this machine, with the header":      {"127.0.0.1", "203.0.113.9", true},
		"a private address, with the header": {"172.18.0.3", "203.0.113.9", true},
		"this machine, straight":             {"127.0.0.1", "", false},
		"a stranger sending the header":      {"203.0.113.5", "192.168.1.20", false},
		"the listed proxy":                   {"10.0.0.2", "203.0.113.9", false},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.peer + ":4000"
		if c.forwarded != "" {
			r.Header.Set("X-Forwarded-For", c.forwarded)
		}
		if got := n.UnlistedLocalProxy(r); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
		if addr, _ := n.Client(r); c.want && addr.String() != c.peer {
			t.Errorf("%s: the header was believed: the visitor is taken to be %v", name, addr)
		}
		if c.want && n.FromHome(r) {
			t.Errorf("%s: counts as coming from home", name)
		}
	}
}

// While the server is guessing what is home, the one address every visitor can arrive from
// in a container or behind some routers, the default gateway, is not home; the rest of that
// network is. Once the owner lists their home networks, their list alone decides.
func TestTheGatewayIsNotHomeByDefault(t *testing.T) {
	inContainer := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t010011AC\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
		"eth0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"
	gateway := gatewayIn(inContainer)
	if gateway.String() != "172.17.0.1" {
		t.Fatalf("the gateway read from the route table is %v, want 172.17.0.1", gateway)
	}
	if got := gatewayIn("Iface\tDestination\tGateway\n"); got.IsValid() {
		t.Errorf("a table with no default route gave the gateway %v", got)
	}
	home := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("172.17.0.0/16")}
	guessed, chosen := Networks{Home: home, Gateway: gateway}, Networks{Home: home}
	for addr, want := range map[string]bool{"172.17.0.1": false, "::ffff:172.17.0.1": false, "172.17.0.9": true, "127.0.0.1": true, "203.0.113.5": false} {
		if got := guessed.AtHome(netip.MustParseAddr(addr)); got != want {
			t.Errorf("while guessing, %s at home: %v, want %v", addr, got, want)
		}
	}
	if !chosen.AtHome(gateway) {
		t.Error("the owner listed a network holding the gateway, and it does not count as home")
	}
}

// A container runtime is recognised in the first process's control group, and the host's own
// bridges are told apart from a container's interfaces.
func TestContainerSigns(t *testing.T) {
	for text, want := range map[string]bool{
		"0::/system.slice/docker-1a2b.scope\n":             true,
		"12:pids:/kubepods/besteffort/pod1234/abcd\n":      true,
		"0::/machine.slice/libpod-5678.scope/container\n":  true,
		"0::/init.scope\n":                                 false,
		"0::/user.slice/user-1000.slice/session-2.scope\n": false,
	} {
		if got := runtimeIn(text); got != want {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
	for name, want := range map[string]bool{"docker0": true, "br-1a2b3c": true, "veth12ab": true, "podman0": true, "eth0": false, "enp3s0": false, "lo": false} {
		if got := hostBridge(name); got != want {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
}
