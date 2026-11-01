package networking

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/remotegate"
)

// peerListener is a real TCP listener whose connections report another peer
// address, so a test on loopback can play a client from outside or at home.
// Bytes, TLS and HTTP all go through the real stack.
type peerListener struct {
	net.Listener
	peer string
}

func (l *peerListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &listenerPeerConn{c, &net.TCPAddr{IP: net.ParseIP(l.peer), Port: 42000}}, nil
}

type gatedServer struct {
	addr  string
	gate  *remotegate.Gate
	roots *x509.CertPool
}

// startGated serves "ok" behind a DirectListener with the gate, whose only
// certificate covers own.example.com.
func startGated(t *testing.T, peer, security, access string) gatedServer {
	t.Helper()
	fixture := httptest.NewUnstartedServer(http.NotFoundHandler())
	fixture.EnableHTTP2 = false
	fixture.StartTLS()
	certificate := fixture.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(fixture.Certificate())
	fixture.Close()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := NewDirectListener(&peerListener{inner, peer}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture certificate stands in for the Portico one: it is served only
	// for own.example.com, as the real manager serves only its names.
	listener.source.Store(&certificateSource{get: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if hello.ServerName == "own.example.com" {
			return &certificate, nil
		}
		return nil, errUnknownName
	}})
	gate := remotegate.New()
	gate.Set(remotegate.NewPolicy(remotegate.Settings{Security: security, Access: access, Home: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}}))
	listener.UseGate(gate)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }), ReadHeaderTimeout: 2 * time.Second}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return gatedServer{addr: inner.Addr().String(), gate: gate, roots: roots}
}

// plain sends one HTTP/1.1 request and returns every byte the server wrote.
func plain(t *testing.T, addr, host string) string {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: "+host+"\r\nConnection: close\r\n\r\n")
	out, _ := io.ReadAll(c)
	return string(out)
}

// overTLS makes one HTTPS request with the given server name ("" sends none,
// like a client connecting by IP).
func overTLS(t *testing.T, s gatedServer, serverName string) (string, error) {
	t.Helper()
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	config := &tls.Config{RootCAs: s.roots, ServerName: serverName}
	if serverName == "" {
		config.InsecureSkipVerify = true
	}
	c, err := tls.DialWithDialer(dialer, "tcp", s.addr, config)
	if err != nil {
		return "", err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	host := serverName
	if host == "" {
		host = "203.0.113.1"
	}
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: "+host+"\r\nConnection: close\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return string(body), nil
}

// counted waits briefly for the listener's own goroutines to finish counting.
func counted(t *testing.T, g *remotegate.Gate, want map[string]int64) {
	t.Helper()
	var by map[string]int64
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		_, by = g.Refusals.Totals(24)
		same := true
		for _, reason := range remotegate.RefusalReasons {
			if by[reason] != want[reason] {
				same = false
			}
		}
		if same {
			return
		}
	}
	t.Fatalf("refusals by reason: %v, want %v", by, want)
}

func refused(g *remotegate.Gate) []string {
	names, _ := g.Refused.Snapshot()
	out := []string{}
	for _, n := range names {
		out = append(out, n.Name)
	}
	return out
}

func TestRemoteGateEnhancedRefusesPlainHTTPFromOutsideWithNothing(t *testing.T) {
	s := startGated(t, "203.0.113.9", remotegate.SecurityEnhanced, remotegate.AccessOn)
	if got := plain(t, s.addr, "myserver.mydomain.example"); got != "" {
		t.Fatalf("a refused connection wrote %q", got)
	}
	if got := refused(s.gate); len(got) != 1 || got[0] != "myserver.mydomain.example" {
		t.Fatalf("refused names: %v", got)
	}
	if got := plain(t, s.addr, "203.0.113.1"); got != "" {
		t.Fatalf("a refused connection by IP wrote %q", got)
	}
	counted(t, s.gate, map[string]int64{remotegate.ReasonHTTP: 1, remotegate.ReasonIP: 1})
}

func TestRemoteGateEnhancedAnswersHome(t *testing.T) {
	s := startGated(t, "192.168.1.20", remotegate.SecurityEnhanced, remotegate.AccessOn)
	if got := plain(t, s.addr, "anything"); !strings.Contains(got, "200 OK") || !strings.HasSuffix(got, "ok") {
		t.Fatalf("home plain HTTP: %q", got)
	}
}

func TestRemoteGateStandardAnswersPlainHTTPFromOutside(t *testing.T) {
	s := startGated(t, "203.0.113.9", remotegate.SecurityStandard, remotegate.AccessOn)
	if got := plain(t, s.addr, "myserver.mydomain.example"); !strings.HasSuffix(got, "ok") {
		t.Fatalf("Standard plain HTTP from outside: %q", got)
	}
	if got := refused(s.gate); len(got) != 0 {
		t.Fatalf("Standard recorded %v", got)
	}
}

func TestRemoteGateTLSNames(t *testing.T) {
	s := startGated(t, "203.0.113.9", remotegate.SecurityEnhanced, remotegate.AccessOn)
	if body, err := overTLS(t, s, "own.example.com"); err != nil || body != "ok" {
		t.Fatalf("own name: %q, %v", body, err)
	}
	// A name the server has no certificate for fails in the handshake, before
	// any HTTP, and is noted for the owner.
	// The alert is unrecognized_name, as for any server without that name.
	if _, err := overTLS(t, s, "media.example.org"); err == nil || !strings.Contains(err.Error(), "unrecognized name") {
		t.Fatalf("unknown name: %v", err)
	}
	// A client connecting by IP sends no server name: refused the same way.
	if _, err := overTLS(t, s, ""); err == nil || !strings.Contains(err.Error(), "unrecognized name") {
		t.Fatalf("no server name (connecting by IP): %v", err)
	}
	if got := refused(s.gate); len(got) != 1 || got[0] != "media.example.org" {
		t.Fatalf("refused names: %v", got)
	}
	counted(t, s.gate, map[string]int64{remotegate.ReasonCertificate: 1, remotegate.ReasonIP: 1})
}

func TestRemoteGateClosedRefusesEverythingFromOutside(t *testing.T) {
	for _, access := range []string{remotegate.AccessOff, remotegate.AccessPrivate} {
		for _, security := range []string{remotegate.SecurityEnhanced, remotegate.SecurityStandard} {
			s := startGated(t, "203.0.113.9", security, access)
			if got := plain(t, s.addr, "own.example.com"); got != "" {
				t.Fatalf("%s/%s plain: %q", security, access, got)
			}
			if _, err := overTLS(t, s, "own.example.com"); err == nil {
				t.Fatalf("%s/%s TLS answered", security, access)
			}
			counted(t, s.gate, map[string]int64{remotegate.ReasonAccess: 2})
		}
	}
	// The tailnet counts as home in Private only and On; Off is home only.
	for _, access := range []string{remotegate.AccessPrivate, remotegate.AccessOn} {
		s := startGated(t, "100.101.102.103", remotegate.SecurityEnhanced, access)
		if got := plain(t, s.addr, "box"); !strings.HasSuffix(got, "ok") {
			t.Fatalf("tailnet peer, %s: %q", access, got)
		}
	}
	if got := plain(t, startGated(t, "100.101.102.103", remotegate.SecurityEnhanced, remotegate.AccessOff).addr, "box"); got != "" {
		t.Fatalf("tailnet peer, off: %q", got)
	}
}

// Until the settings are installed the gate refuses every outside
// connection, before any byte, and still answers loopback: no route, startup
// page or otherwise, is served outside in that window.
func TestRemoteGateBeforeSettingsRefusesOutside(t *testing.T) {
	serve := func(peer string) string {
		inner, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listener, err := NewDirectListener(&peerListener{inner, peer}, nil)
		if err != nil {
			t.Fatal(err)
		}
		listener.UseGate(remotegate.New())
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }), ReadHeaderTimeout: 2 * time.Second}
		go server.Serve(listener)
		t.Cleanup(func() { server.Close() })
		return inner.Addr().String()
	}
	outside := serve("203.0.113.9")
	if got := plain(t, outside, "own.example.com"); got != "" {
		t.Fatalf("outside, before settings: %q", got)
	}
	if body, err := overTLS(t, gatedServer{addr: outside}, ""); err == nil {
		t.Fatalf("outside TLS, before settings: %q", body)
	}
	if got := plain(t, serve("127.0.0.1"), "localhost"); !strings.HasSuffix(got, "ok") {
		t.Fatalf("loopback, before settings: %q", got)
	}
}

// TestRemoteGateLegitimateSetupsTLS is the handshake half of httpapi's
// TestLegitimateSetups: real TCP and TLS through the DirectListener, in
// Enhanced and Standard. The certificate here covers own.example.com only.
func TestRemoteGateLegitimateSetupsTLS(t *testing.T) {
	for _, c := range []struct {
		setup              string
		peer, serverName   string
		enhanced, standard bool   // answered
		recorded           string // in Enhanced, "name reason"
	}{
		{"Portico app at its own name", "203.0.113.9", "own.example.com", true, true, ""},
		{"LAN client at its own name", "192.168.1.20", "own.example.com", true, true, ""},
		// No certificate can name an IP, so HTTPS by IP fails the handshake
		// in both modes, at home and outside (as before O-169). HTTP by IP
		// works at home; the browser names the alert.
		{"LAN client by IP over HTTPS", "192.168.1.20", "", false, false, ""},
		{"outside client by raw IP over HTTPS", "203.0.113.9", "", false, false, ""},
		// A DDNS name with no certificate fails the handshake in both modes;
		// in Enhanced the owner sees it with reason certificate.
		{"DDNS name, no certificate, HTTPS", "203.0.113.9", "myserver.duckdns.example", false, false, "myserver.duckdns.example certificate"},
	} {
		for _, mode := range []string{remotegate.SecurityEnhanced, remotegate.SecurityStandard} {
			s := startGated(t, c.peer, mode, remotegate.AccessOn)
			_, err := overTLS(t, s, c.serverName)
			want := c.enhanced
			if mode == remotegate.SecurityStandard {
				want = c.standard
			}
			if (err == nil) != want {
				t.Errorf("%s, %s: answered %v (%v), want %v", c.setup, mode, err == nil, err, want)
			}
			got := ""
			if names, _ := s.gate.Refused.Snapshot(); len(names) == 1 {
				got = names[0].Name + " " + names[0].Reason
			}
			wantRecorded := c.recorded
			if mode == remotegate.SecurityStandard {
				wantRecorded = ""
			}
			if got != wantRecorded {
				t.Errorf("%s, %s: recorded %q, want %q", c.setup, mode, got, wantRecorded)
			}
		}
	}
	// Plain HTTP by raw IP from outside: closed in Enhanced (not recorded, an
	// IP isn't a name to allow), answered in Standard.
	if got := plain(t, startGated(t, "203.0.113.9", remotegate.SecurityEnhanced, remotegate.AccessOn).addr, "203.0.113.1:32500"); got != "" {
		t.Fatalf("Enhanced, raw IP over HTTP: %q", got)
	}
	if got := plain(t, startGated(t, "203.0.113.9", remotegate.SecurityStandard, remotegate.AccessOn).addr, "203.0.113.1:32500"); !strings.HasSuffix(got, "ok") {
		t.Fatalf("Standard, raw IP over HTTP: %q", got)
	}
}

// Docker bridge mode through the real listener: the only detected network is the
// bridge, and a laptop at 192.168.1.20 still reaches the server over plain HTTP in
// Enhanced, while a public peer gets nothing.
func TestRemoteGateDockerBridgeAnswersTheLAN(t *testing.T) {
	bridge := remotegate.NewPolicy(remotegate.Settings{Security: remotegate.SecurityEnhanced, Access: remotegate.AccessOn, Home: []netip.Prefix{netip.MustParsePrefix("172.17.0.0/16")}})
	lan := startGated(t, "192.168.1.20", remotegate.SecurityEnhanced, remotegate.AccessOn)
	lan.gate.Set(bridge)
	if got := plain(t, lan.addr, "192.168.1.50:32500"); !strings.HasSuffix(got, "ok") {
		t.Fatalf("LAN laptop in bridge mode: %q", got)
	}
	public := startGated(t, "203.0.113.9", remotegate.SecurityEnhanced, remotegate.AccessOn)
	public.gate.Set(bridge)
	if got := plain(t, public.addr, "192.168.1.50:32500"); got != "" {
		t.Fatalf("public peer answered: %q", got)
	}
}
