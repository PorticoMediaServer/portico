package networking

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type listenerPeerConn struct {
	net.Conn
	peer net.Addr
}

func (c *listenerPeerConn) RemoteAddr() net.Addr { return c.peer }

type listenerFixture struct {
	incoming chan net.Conn
	done     chan struct{}
	once     sync.Once
}

func (l *listenerFixture) Accept() (net.Conn, error) {
	select {
	case c := <-l.incoming:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *listenerFixture) Close() error { l.once.Do(func() { close(l.done) }); return nil }
func (l *listenerFixture) Addr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: 19410}
}
func newListenerPeer(t *testing.T, ip string) (*DirectListener, net.Conn) {
	t.Helper()
	raw := &listenerFixture{incoming: make(chan net.Conn, 1), done: make(chan struct{})}
	// An unconfigured manager suffices: plaintext never consults certificate state.
	l, err := NewDirectListener(raw, &CertificateManager{})
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	raw.incoming <- &listenerPeerConn{server, &net.TCPAddr{IP: net.ParseIP(ip), Port: 42000}}
	t.Cleanup(func() { client.Close(); l.Close() })
	return l, client
}
func TestDirectListenerLANRecoveryWithoutCertificate(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "192.168.1.20", "fe80::1234", "100.64.0.1", "203.0.113.12", "2001:db8::1"} {
		t.Run(ip, func(t *testing.T) {
			l, client := newListenerPeer(t, ip)
			// Downstream authorization remains the authority. This response must reach
			// the LAN client even while certificate issuance is pending or unavailable.
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.TLS != nil {
					t.Error("HTTP incorrectly marked TLS")
				}
				http.Error(w, "pairing required", http.StatusUnauthorized)
			})}
			defer server.Close()
			go server.Serve(l)
			client.SetDeadline(time.Now().Add(2 * time.Second))
			if _, err := io.WriteString(client, "GET /recovery HTTP/1.1\r\nHost: private\r\nConnection: close\r\n\r\n"); err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(client), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("authorization bypass: %d", response.StatusCode)
			}
		})
	}
}
func TestDirectListenerTLSDetectionPreserved(t *testing.T) {
	l, client := newListenerPeer(t, "203.0.113.12")
	client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Write([]byte{0x16}); err != nil {
		t.Fatal(err)
	}
	c, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, ok := c.(*tls.Conn); !ok {
		t.Fatal("TLS wrapper lost")
	}
}

func TestDirectListenerHTTP2AndTLSResumption(t *testing.T) {
	// A local fixture certificate is the only trust root used by this client.
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := fixture.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(fixture.Certificate())
	fixture.Close()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := NewDirectListener(inner, nil)
	if err != nil {
		t.Fatal(err)
	}
	listener.config.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &certificate, nil }
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") })}
	defer server.Close()
	go server.Serve(listener)
	transport := &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: &tls.Config{RootCAs: roots, ClientSessionCache: tls.NewLRUClientSessionCache(4)}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for attempt := 0; attempt < 2; attempt++ {
		response, err := client.Get("https://" + inner.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.ProtoMajor != 2 || response.TLS == nil || response.TLS.NegotiatedProtocol != "h2" {
			t.Fatal("HTTP/2 not negotiated")
		}
		if attempt == 1 && !response.TLS.DidResume {
			t.Fatal("TLS session did not resume")
		}
		transport.CloseIdleConnections()
	}
}
