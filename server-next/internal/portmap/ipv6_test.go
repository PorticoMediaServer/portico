package portmap

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestIPv6DefaultRouteUsesItsStableGlobalAddress(t *testing.T) {
	route := strings.Repeat("0", 32) + " 00 " + strings.Repeat("0", 32) + " 00 fe800000000000000000000000000001 00000064 00000000 00000000 00000001 eth0\n"
	routes := strings.NewReader(route)
	addresses := strings.NewReader("26060000000000000000000000000001 02 40 00 01 eth0\n" +
		"26060000000000000000000000000002 03 40 00 80 eth1\n" +
		"26060000000000000000000000000003 02 40 00 20 eth0\n" +
		"26060000000000000000000000000004 02 40 00 80 eth0\n")
	r := readIPv6Router(routes, addresses, netip.Addr{})
	if r.Gateway.String() != "fe80::1%eth0" || r.Client.String() != "2606::4" || r.SSDP.String() != "[ff02::c%eth0]:1900" {
		t.Fatal("used another interface, a privacy address or a deprecated address", r)
	}
	onlyTemporary := strings.NewReader("26060000000000000000000000000001 02 40 00 01 eth0\n")
	if got := readIPv6Router(strings.NewReader(route), onlyTemporary, netip.Addr{}); got.Client.String() != "2606::1" {
		t.Fatal("an interface with only a temporary global address was refused", got)
	}
	bound := netip.MustParseAddr("2606::1")
	addresses = strings.NewReader("26060000000000000000000000000004 02 40 00 80 eth0\n26060000000000000000000000000001 02 40 00 01 eth0\n")
	if got := readIPv6Router(strings.NewReader(route), addresses, bound); got.Client != bound {
		t.Fatal("ignored the address the server actually listens on", got)
	}
	for _, address := range []string{"http://[fe80::1]/ctl", "http://[fe80::1%25eth0]/ctl"} {
		u, _ := url.Parse(address)
		if !routerURL(u, r.Gateway) || !strings.Contains(u.String(), "%25eth0") {
			t.Fatal("the known router scope was lost", u)
		}
	}
	for _, address := range []string{"http://[fe80::2]/ctl", "http://[fe80::1%25eth1]/ctl", "http://user@[fe80::1]/ctl", "http://example.com/ctl"} {
		u, _ := url.Parse(address)
		if routerURL(u, r.Gateway) {
			t.Fatal("followed a control URL outside the router and its interface", address)
		}
	}
}

func TestIPv6PCPOpensRenewsAndClosesTheListener(t *testing.T) {
	ip := netip.MustParseAddr("::1")
	f := newFakePMPAt(t, true, ip)
	r := Router{Gateway: ip, Client: ip, PMPPort: f.port()}
	m := NewIPv6(quiet, func() Router { return r })
	m.Set(true, 32500, 32500)
	var observed []string
	m.OnChange = func() { observed = append(observed, m.Status().State) }
	for range 2 {
		if next := m.round(t.Context()); next != time.Hour || m.Status().State != "open" || m.Status().ExternalIP != "::1" {
			t.Fatal("IPv6 opening/renewal failed", next, m.Status())
		}
	}
	m.Set(false, 32500, 32500)
	m.round(t.Context())
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.asked) != 3 || f.asked[2] != "delete 32500" || strings.Join(observed, ",") != "open,open,off" {
		t.Fatal(f.asked, observed)
	}
}

func (f *fakeUPnP) handleIPv6(w http.ResponseWriter, r *http.Request) {
	service := ipv6FirewallService
	action := strings.TrimSuffix(strings.TrimPrefix(r.Header.Get("SOAPAction"), `"`+service+"#"), `"`)
	data, _ := io.ReadAll(r.Body)
	field := func(name string) string {
		_, rest, _ := strings.Cut(string(data), "<"+name+">")
		value, _, _ := strings.Cut(rest, "</"+name+">")
		return value
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	fault := func(code int) {
		w.WriteHeader(500)
		fmt.Fprintf(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><detail>`+
			`<UPnPError><errorCode>%d</errorCode></UPnPError></detail></s:Fault></s:Body></s:Envelope>`, code)
	}
	if f.malformed {
		io.WriteString(w, "not a SOAP answer")
		return
	}
	answer := ""
	switch action {
	case "GetFirewallStatus":
		answer = fmt.Sprintf("<FirewallEnabled>%t</FirewallEnabled><InboundPinholeAllowed>%t</InboundPinholeAllowed>", !f.firewallOff, f.allowed)
	case "AddPinhole":
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if host != field("InternalClient") || field("InternalPort") != "32500" || field("Protocol") != "6" {
			fault(606) // An unauthenticated control point may open only its own address.
			return
		}
		f.pinhole = "42"
		f.asked = append(f.asked, "open "+field("InternalClient")+":"+field("InternalPort"))
		answer = "<UniqueID>42</UniqueID>"
	case "UpdatePinhole", "DeletePinhole":
		if f.pinhole == "" || field("UniqueID") != f.pinhole {
			fault(704)
			return
		}
		if action == "DeletePinhole" {
			f.pinhole = ""
		}
		f.asked = append(f.asked, action+" 42")
	}
	fmt.Fprintf(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:%sResponse xmlns:u="%s">%s</u:%sResponse></s:Body></s:Envelope>`, action, service, answer, action)
}

func TestIPv6RefusalAndMalformedReplyAreNotAnOpening(t *testing.T) {
	ip := netip.MustParseAddr("::1")
	f := newFakeUPnPAt(t, ip)
	p := newFakePMPAt(t, false, ip)
	r := Router{Gateway: ip, Client: ip, PMPPort: p.port(), SSDP: f.ssdpAddr()}
	m := NewIPv6(quiet, func() Router { return r })
	m.Set(true, 32500, 32500)
	f.mu.Lock()
	f.allowed = false
	f.mu.Unlock()
	m.round(t.Context())
	if m.Status().State != "failed" || m.Status().Problem == "" || m.held != nil {
		t.Fatal("the router's refusal was hidden", m.Status())
	}
	f.mu.Lock()
	f.allowed, f.malformed = true, true
	f.mu.Unlock()
	m.round(t.Context())
	if m.Status().State != "failed" || m.held != nil {
		t.Fatal("an invalid HTTP 200 was treated as an opening", m.Status())
	}
	f.mu.Lock()
	f.malformed = false
	f.mu.Unlock()
	m.round(t.Context())
	if m.Status().State != "open" {
		t.Fatal("a new check did not recover", m.Status())
	}
	m.Set(false, 32500, 32500)
	m.round(t.Context())
}

func TestIPv6AlreadyOpenFirewallNeedsNoPinhole(t *testing.T) {
	ip := netip.MustParseAddr("::1")
	f := newFakeUPnPAt(t, ip)
	p := newFakePMPAt(t, false, ip)
	f.mu.Lock()
	f.firewallOff = true
	f.mu.Unlock()
	r := Router{Gateway: ip, Client: ip, PMPPort: p.port(), SSDP: f.ssdpAddr()}
	m := NewIPv6(quiet, func() Router { return r })
	m.Set(true, 32500, 32500)
	m.round(t.Context())
	if m.Status().State != "open" || m.held == nil || m.held.pinhole != "" {
		t.Fatal("the already open firewall was treated as unavailable", m.Status())
	}
	m.Set(false, 32500, 32500)
	m.round(t.Context())
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.asked) != 0 {
		t.Fatal("changed a firewall that needed no opening", f.asked)
	}
}

func TestIPv6UPnPUsesTheClientAddressAndRecoversARouterRestart(t *testing.T) {
	ip := netip.MustParseAddr("::1")
	f := newFakeUPnPAt(t, ip)
	p := newFakePMPAt(t, false, ip)
	r := Router{Gateway: ip, Client: ip, PMPPort: p.port(), SSDP: f.ssdpAddr()}
	m := NewIPv6(quiet, func() Router { return r })
	m.Set(true, 32500, 32500)
	for range 2 {
		m.round(t.Context())
		if st := m.Status(); st.State != "open" || st.Protocol != "UPnP IPv6" || st.ExternalPort != 32500 {
			t.Fatal(st)
		}
	}
	f.mu.Lock()
	f.pinhole = "" // The router forgot the lease at restart.
	f.mu.Unlock()
	m.round(t.Context())
	m.Set(false, 32500, 32500)
	m.round(t.Context())
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Join(f.asked, ",") != "open ::1:32500,UpdatePinhole 42,open ::1:32500,DeletePinhole 42" || m.Status().State != "off" {
		t.Fatal(f.asked, m.Status())
	}
}
