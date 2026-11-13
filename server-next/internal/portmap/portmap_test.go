package portmap

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests talk only to fake routers on 127.0.0.1, inside the test. No real router is asked.

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

var loopback = netip.MustParseAddr("127.0.0.1")

// fakePMP answers PCP, or NAT-PMP only (answering PCP with NAT-PMP's "unsupported version").
type fakePMP struct {
	external netip.Addr
	conn     *net.UDPConn
	pcp      bool
	mu       sync.Mutex
	asked    []string // "map 32400->32500 for 7200s", "delete 32400"
	refuse   bool
	assigns  int // an external port other than the one asked for, when not 0
	nonces   map[uint16]string
}

func newFakePMP(t *testing.T, pcp bool) *fakePMP {
	return newFakePMPAt(t, pcp, loopback)
}

func newFakePMPAt(t *testing.T, pcp bool, ip netip.Addr) *fakePMP {
	conn, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(ip, 0)))
	if err != nil {
		t.Fatal(err)
	}
	external := netip.MustParseAddr("198.51.100.7")
	if ip.Is6() {
		external = ip
	}
	f := &fakePMP{conn: conn, pcp: pcp, external: external}
	t.Cleanup(func() { conn.Close() })
	go f.serve()
	return f
}

func (f *fakePMP) port() int { return f.conn.LocalAddr().(*net.UDPAddr).Port }

func (f *fakePMP) note(s string) {
	f.mu.Lock()
	f.asked = append(f.asked, s)
	f.mu.Unlock()
}

func (f *fakePMP) serve() {
	buf := make([]byte, 1100)
	for {
		n, from, err := f.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		req := append([]byte(nil), buf[:n]...)
		var resp []byte
		switch {
		case req[0] == 2 && !f.pcp:
			resp = []byte{0, 0x81, 0, 1, 0, 0, 0, 0} // NAT-PMP: unsupported version
		case req[0] == 2 && n == 60:
			resp = make([]byte, 60)
			resp[0], resp[1] = 2, 0x81
			if f.external.Is6() && (netip.AddrFrom16([16]byte(req[8:24])) != from.AddrPort().Addr() || [16]byte(req[44:60]) != [16]byte{}) {
				resp[3] = 2 // The client must name its own IPv6 source, with an IPv6 hint.
			}
			life := binary.BigEndian.Uint32(req[4:])
			internal, external := binary.BigEndian.Uint16(req[40:]), binary.BigEndian.Uint16(req[42:])
			f.mu.Lock()
			if f.assigns != 0 {
				external = uint16(f.assigns)
			}
			if f.refuse {
				resp[3] = 2 // NOT_AUTHORIZED
			}
			// As RFC 6887 asks, a request for a mapping already held under another nonce is
			// refused, and so is its removal.
			if f.nonces == nil {
				f.nonces = map[uint16]string{}
			}
			if held, ok := f.nonces[internal]; ok && held != string(req[24:36]) {
				resp[3] = 2
			} else if life == 0 {
				delete(f.nonces, internal)
			} else {
				f.nonces[internal] = string(req[24:36])
			}
			f.mu.Unlock()
			binary.BigEndian.PutUint32(resp[4:], life)
			copy(resp[24:36], req[24:36])
			resp[36] = 6
			binary.BigEndian.PutUint16(resp[40:], internal)
			binary.BigEndian.PutUint16(resp[42:], external)
			copy(resp[44:60], netip.AddrFrom16(f.external.As16()).AsSlice())
			if life == 0 {
				f.note(fmt.Sprintf("delete %d", internal))
			} else {
				f.note(fmt.Sprintf("map %d->%d for %ds", internal, external, life))
			}
		case req[0] == 0 && req[1] == 0:
			resp = []byte{0, 128, 0, 0, 0, 0, 0, 1, 198, 51, 100, 8}
		case req[0] == 0 && req[1] == 2 && n == 12:
			internal, external, life := binary.BigEndian.Uint16(req[4:]), binary.BigEndian.Uint16(req[6:]), binary.BigEndian.Uint32(req[8:])
			resp = make([]byte, 16)
			resp[1] = 130
			binary.BigEndian.PutUint16(resp[8:], internal)
			binary.BigEndian.PutUint16(resp[10:], external)
			binary.BigEndian.PutUint32(resp[12:], life)
			if life == 0 {
				f.note(fmt.Sprintf("delete %d", internal))
			} else {
				f.note(fmt.Sprintf("map %d->%d for %ds", internal, external, life))
			}
		default:
			continue
		}
		f.conn.WriteToUDP(resp, from)
	}
}

// fakeUPnP is an Internet Gateway Device: an SSDP responder and its HTTP control point.
type fakeUPnP struct {
	ipv6        bool
	pinhole     string
	allowed     bool
	firewallOff bool
	malformed   bool
	ssdp        *net.UDPConn
	http        net.Listener
	location    string // what the SSDP answer points at
	permanent   bool   // only permanent mappings (error 725 for a lease)
	mu          sync.Mutex
	asked       []string
}

func newFakeUPnP(t *testing.T) *fakeUPnP {
	return newFakeUPnPAt(t, loopback)
}

func newFakeUPnPAt(t *testing.T, ip netip.Addr) *fakeUPnP {
	ssdp, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(ip, 0)))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeUPnP{ssdp: ssdp, http: ln, ipv6: ip.Is6(), allowed: true}
	f.location = "http://" + ln.Addr().String() + "/desc.xml"
	srv := &http.Server{Handler: http.HandlerFunc(f.handle), ReadHeaderTimeout: time.Second}
	go srv.Serve(ln)
	t.Cleanup(func() { ssdp.Close(); srv.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := ssdp.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if strings.HasPrefix(string(buf[:n]), "M-SEARCH") {
				f.mu.Lock()
				location := f.location
				f.mu.Unlock()
				ssdp.WriteToUDP([]byte("HTTP/1.1 200 OK\r\nST: urn:schemas-upnp-org:device:InternetGatewayDevice:1\r\nLOCATION: "+location+"\r\n\r\n"), from)
			}
		}
	}()
	return f
}

func (f *fakeUPnP) ssdpAddr() netip.AddrPort { return f.ssdp.LocalAddr().(*net.UDPAddr).AddrPort() }

func (f *fakeUPnP) handle(w http.ResponseWriter, r *http.Request) {
	service := "urn:schemas-upnp-org:service:WANIPConnection:1"
	if f.ipv6 {
		service = ipv6FirewallService
	}
	if r.URL.Path == "/desc.xml" {
		fmt.Fprintf(w, `<?xml version="1.0"?><root><device><deviceList><device><deviceList><device><serviceList>
<service><serviceType>%s</serviceType><controlURL>/ctl/IPConn</controlURL></service>
</serviceList></device></deviceList></device></deviceList></device></root>`, service)
		return
	}
	if f.ipv6 {
		f.handleIPv6(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	action := strings.TrimSuffix(strings.TrimPrefix(r.Header.Get("SOAPAction"), `"`+service+"#"), `"`)
	text := string(body)
	field := func(name string) string {
		_, rest, _ := strings.Cut(text, "<"+name+">")
		v, _, _ := strings.Cut(rest, "</"+name+">")
		return v
	}
	switch action {
	case "AddPortMapping":
		f.mu.Lock()
		permanent := f.permanent
		f.mu.Unlock()
		if permanent && field("NewLeaseDuration") != "0" {
			w.WriteHeader(500)
			io.WriteString(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><detail>`+
				`<UPnPError><errorCode>725</errorCode></UPnPError></detail></s:Fault></s:Body></s:Envelope>`)
			return
		}
		f.note(fmt.Sprintf("map %s->%s for %ss to %s", field("NewInternalPort"), field("NewExternalPort"), field("NewLeaseDuration"), field("NewInternalClient")))
	case "DeletePortMapping":
		f.note("delete " + field("NewExternalPort"))
	case "GetExternalIPAddress":
		io.WriteString(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetExternalIPAddressResponse>`+
			`<NewExternalIPAddress>198.51.100.9</NewExternalIPAddress></u:GetExternalIPAddressResponse></s:Body></s:Envelope>`)
		return
	}
	io.WriteString(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body/></s:Envelope>`)
}

func (f *fakeUPnP) note(s string) {
	f.mu.Lock()
	f.asked = append(f.asked, s)
	f.mu.Unlock()
}

// closedUDP is a loopback port nothing listens on: a router that speaks none of PCP and NAT-PMP.
func closedUDP(t *testing.T) int {
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	c.Close()
	return port
}

func mapperFor(r Router) *Mapper {
	m := New(quiet, func() Router { return r })
	return m
}

// A router that speaks PCP opens the port with PCP; one that speaks only NAT-PMP is asked
// with NAT-PMP after it says so; one that speaks only UPnP is found with SSDP. Each says
// where the server can be reached, and the port it opened is closed again when asked.
func TestEachProtocolOpensThePort(t *testing.T) {
	ctx := t.Context()
	for _, c := range []struct {
		name, protocol, external string
		router                   func() (Router, func() []string)
	}{
		{"PCP", "PCP", "198.51.100.7", func() (Router, func() []string) {
			f := newFakePMP(t, true)
			return Router{Gateway: loopback, PMPPort: f.port()}, func() []string { f.mu.Lock(); defer f.mu.Unlock(); return f.asked }
		}},
		{"NAT-PMP", "NAT-PMP", "198.51.100.8", func() (Router, func() []string) {
			f := newFakePMP(t, false)
			return Router{Gateway: loopback, PMPPort: f.port()}, func() []string { f.mu.Lock(); defer f.mu.Unlock(); return f.asked }
		}},
		{"UPnP", "UPnP", "198.51.100.9", func() (Router, func() []string) {
			f := newFakeUPnP(t)
			return Router{Gateway: loopback, PMPPort: closedUDP(t), SSDP: f.ssdpAddr()}, func() []string { f.mu.Lock(); defer f.mu.Unlock(); return f.asked }
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, asked := c.router()
			m := mapperFor(r)
			m.Set(true, 32400, 32500)
			if next := m.round(ctx); next != time.Hour {
				t.Errorf("the next renewal is in %s, want an hour", next)
			}
			st := m.Status()
			if st.State != "open" || st.Protocol != c.protocol || st.ExternalPort != 32500 || st.ExternalIP != c.external {
				t.Fatalf("status %+v", st)
			}
			m.remove(ctx)
			got := asked()
			if len(got) != 2 || !strings.HasPrefix(got[0], "map 32400->32500 for 7200s") || got[1] != "delete 32400" && got[1] != "delete 32500" {
				t.Fatalf("the router was asked %q", got)
			}
		})
	}
}

// A router that keeps only permanent UPnP mappings gets one; a router that opens a different
// port says so; a router that refuses, or one that speaks nothing, is reported plainly and
// the server never goes past the router's address.
func TestRouterAnswersThatAreNotAYes(t *testing.T) {
	ctx := t.Context()
	u := newFakeUPnP(t)
	u.mu.Lock()
	u.permanent = true
	u.mu.Unlock()
	m := mapperFor(Router{Gateway: loopback, PMPPort: closedUDP(t), SSDP: u.ssdpAddr()})
	m.Set(true, 32400, 32500)
	if next := m.round(ctx); next != 0 || m.Status().State != "open" {
		t.Fatalf("a permanent mapping: next %s, status %+v", next, m.Status())
	}

	p := newFakePMP(t, true)
	p.mu.Lock()
	p.assigns = 40123
	p.mu.Unlock()
	m = mapperFor(Router{Gateway: loopback, PMPPort: p.port()})
	m.Set(true, 32400, 32500)
	m.round(ctx)
	if st := m.Status(); st.ExternalPort != 40123 || !strings.Contains(st.Problem, "opened port 40123 instead of 32500") {
		t.Fatalf("a different port: %+v", m.Status())
	}

	p.mu.Lock()
	p.refuse = true
	p.mu.Unlock()
	m.round(ctx)
	if st := m.Status(); st.State != "failed" || !strings.Contains(st.Problem, "PCP result 2") {
		t.Fatalf("a refusal: %+v", st)
	}

	// An SSDP answer that points somewhere other than the router is not followed.
	rogue := newFakeUPnP(t)
	rogue.mu.Lock()
	rogue.location = "http://127.0.0.2:1/desc.xml"
	rogue.mu.Unlock()
	m = mapperFor(Router{Gateway: loopback, PMPPort: closedUDP(t), SSDP: rogue.ssdpAddr()})
	m.Set(true, 32400, 32500)
	start := time.Now()
	m.round(ctx)
	if st := m.Status(); st.State != "failed" || !strings.Contains(st.Problem, "did not answer PCP, NAT-PMP or UPnP") {
		t.Fatalf("nothing answering: %+v", st)
	}
	if took := time.Since(start); took > attempt {
		t.Fatalf("asking took %s", took)
	}

	m = mapperFor(Router{})
	m.Set(true, 32400, 32500)
	m.round(ctx)
	if st := m.Status(); st.State != "failed" || !strings.Contains(st.Problem, "could not be found") {
		t.Fatalf("no router: %+v", st)
	}
}

// Turning mapping off closes the port, and so does the server stopping.
func TestOffAndStopCloseThePort(t *testing.T) {
	f := newFakePMP(t, true)
	m := mapperFor(Router{Gateway: loopback, PMPPort: f.port()})
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	waitFor := func(state string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); m.Status().State != state; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("status stayed %+v, want %s", m.Status(), state)
			}
		}
	}
	m.Set(true, 32400, 32500)
	waitFor("open")
	m.Set(false, 32400, 32500)
	waitFor("off")
	m.Set(true, 32400, 32501)
	waitFor("open")
	stop()
	<-done
	f.mu.Lock()
	defer f.mu.Unlock()
	// The same mapping asked for twice in a row is a renewal, which a router accepts: what
	// matters is that each mapping is opened and then closed.
	var asked []string
	for _, a := range f.asked {
		if len(asked) == 0 || asked[len(asked)-1] != a {
			asked = append(asked, a)
		}
	}
	want := []string{"map 32400->32500 for 7200s", "delete 32400", "map 32400->32501 for 7200s", "delete 32400"}
	if fmt.Sprint(asked) != fmt.Sprint(want) {
		t.Fatalf("the router was asked %q, want %q", f.asked, want)
	}
}

// A PCP router accepts the renewal and the removal of a mapping only under the nonce it was
// made with, and a re-ask that fails keeps what the router still holds, so it is still removed.
func TestPCPRenewsAndRemovesUnderItsNonce(t *testing.T) {
	ctx := t.Context()
	f := newFakePMP(t, true)
	m := mapperFor(Router{Gateway: loopback, PMPPort: f.port()})
	m.Set(true, 32400, 32500)
	m.round(ctx)
	m.round(ctx) // the renewal at half time
	if st := m.Status(); st.State != "open" {
		t.Fatalf("the renewal was refused: %+v", st)
	}
	f.mu.Lock()
	f.refuse = true
	f.mu.Unlock()
	m.round(ctx)
	if st := m.Status(); st.State != "failed" {
		t.Fatalf("a refused re-ask: %+v", st)
	}
	f.mu.Lock()
	f.refuse = false
	f.mu.Unlock()
	m.remove(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, open := f.nonces[32400]; open {
		t.Fatalf("the mapping is still open on the router after removal; asked %q", f.asked)
	}
}
