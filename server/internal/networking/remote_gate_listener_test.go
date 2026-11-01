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
		}
	}
	// Private only counts the tailnet as home.
	s := startGated(t, "100.101.102.103", remotegate.SecurityEnhanced, remotegate.AccessPrivate)
	if got := plain(t, s.addr, "box"); !strings.HasSuffix(got, "ok") {
		t.Fatalf("tailnet peer in Private only: %q", got)
	}
}

func TestRemoteGateWithoutPolicyJudgesNothing(t *testing.T) {
	// A listener whose gate has no policy yet serves as before.
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := NewDirectListener(&peerListener{inner, "203.0.113.9"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	listener.UseGate(remotegate.New())
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") })}
	go server.Serve(listener)
	defer server.Close()
	if got := plain(t, inner.Addr().String(), "x.example"); !strings.HasSuffix(got, "ok") {
		t.Fatalf("no policy: %q", got)
	}
}
