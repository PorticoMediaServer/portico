package networking

import (
	"bufio"
	"crypto/tls"
	"errors"
	"net"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"sync"
	"sync/atomic"
	"time"
)

// DirectListener serves ordinary publicly trusted TLS on the server's existing
// port, alongside plain HTTP from any address.
// Peer admission is not authentication: downstream pairing, route proofs and
// session authorization still apply.
// Classification and total accepted sockets are bounded separately.
type DirectListener struct {
	net.Listener
	config   *tls.Config
	ready    chan net.Conn
	done     chan struct{}
	classify chan struct{}
	once     sync.Once
	mu       sync.Mutex
	pending  map[*countedTLSConn]struct{}
	err      error
	// source is replaced once when a deferred listener learns its certificates.
	source atomic.Pointer[certificateSource]
}

// certificateSource is the pair of TLS callbacks one set of certificate
// owners implies. verify is nil when no certificate can be served.
type certificateSource struct {
	get    func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	verify func(tls.ConnectionState) error
}

func NewDirectListener(inner net.Listener, manager *CertificateManager) (*DirectListener, error) {
	return newDirectListener(inner, manager, nil, nil)
}

// NewDirectListenerWithCustom serves an owner-supplied certificate for its
// domain in front of the Hosted manager. A nil manager with a configured
// custom certificate still serves (unclaimed / Direct Sign-In); other names
// behave exactly as before.
func NewDirectListenerWithCustom(inner net.Listener, manager *CertificateManager, custom *CustomCertificate) (*DirectListener, error) {
	return newDirectListener(inner, manager, nil, custom)
}

// NewDirectListenerWithTLSConfig lets an embedding server supply its own
// certificate source while keeping the same socket classifier and ALPN path.
// The caller retains responsibility for the certificate's trust policy.
// NewDeferredDirectListener starts classifying at once, before the server
// knows its certificates: plain HTTP is served from the first connection, and a
// TLS handshake fails as it would with no certificate until UseCertificates.
func NewDeferredDirectListener(inner net.Listener) (*DirectListener, error) {
	return newDirectListener(inner, nil, nil, nil)
}

// UseCertificates installs the certificate owners on a listener that is
// already serving. Handshakes that start afterwards use them.
func (l *DirectListener) UseCertificates(manager *CertificateManager, custom *CustomCertificate) {
	l.source.Store(newCertificateSource(manager, custom))
}

func NewDirectListenerWithTLSConfig(inner net.Listener, config *tls.Config) (*DirectListener, error) {
	if config == nil {
		return nil, ErrInvalid
	}
	return newDirectListener(inner, nil, config, nil)
}

func newDirectListener(inner net.Listener, manager *CertificateManager, override *tls.Config, custom *CustomCertificate) (*DirectListener, error) {
	if inner == nil {
		return nil, ErrInvalid
	}
	l := &DirectListener{Listener: inner, ready: make(chan net.Conn, 64), done: make(chan struct{}), classify: make(chan struct{}, 64), pending: map[*countedTLSConn]struct{}{}}
	l.source.Store(newCertificateSource(manager, custom))
	l.config = &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) { return l.source.Load().get(hello) },
		VerifyConnection: func(state tls.ConnectionState) error {
			if verify := l.source.Load().verify; verify != nil {
				return verify(state)
			}
			return nil
		}}
	if override != nil {
		l.config = override.Clone()
		if l.config.MinVersion < tls.VersionTLS12 {
			l.config.MinVersion = tls.VersionTLS12
		}
		l.config.NextProtos = []string{"h2", "http/1.1"}
	}
	supervise.Go("networking.direct-listener.accept", func() { l.acceptLoop() })
	return l, nil
}
func newCertificateSource(manager *CertificateManager, custom *CustomCertificate) *certificateSource {
	source := &certificateSource{get: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return nil, ErrUnavailable }}
	if manager != nil {
		source.get = manager.GetCertificate
	}
	if custom != nil {
		managerGet := source.get
		source.get = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if cert, e := custom.Get(hello); e == nil && cert != nil {
				return cert, nil
			} else if hello != nil && custom.Serves(hello.ServerName) {
				// The name is the custom certificate's, even though it failed
				// to load: fail here rather than falling through to a manager
				// that cannot serve it either.
				return nil, errCustomCertificate
			}
			return managerGet(hello)
		}
	}
	// Resumed sessions must still be fenced by live claim authority. Go rotates
	// in-memory ticket keys automatically; no ticket key is persisted. A custom
	// certificate is not claim-fenced.
	if manager != nil || custom != nil {
		source.verify = func(state tls.ConnectionState) error {
			// A58: a full handshake already ran GetCertificate (and its
			// authority fence) moments ago; only a resumed session skipped it.
			if !state.DidResume {
				return nil
			}
			if custom != nil {
				cert, err := custom.Get(&tls.ClientHelloInfo{ServerName: state.ServerName})
				if err != nil {
					return err
				}
				if cert != nil {
					return nil
				}
			}
			if manager == nil {
				return ErrUnavailable
			}
			_, e := manager.GetCertificate(&tls.ClientHelloInfo{ServerName: state.ServerName})
			return e
		}
	}
	return source
}
func (l *DirectListener) acceptLoop() {
	for {
		raw, e := l.Listener.Accept()
		if e != nil {
			l.mu.Lock()
			if l.err == nil {
				l.err = e
			}
			l.mu.Unlock()
			l.Close()
			return
		}
		c := &countedTLSConn{Conn: raw, closed: func() {}}
		l.mu.Lock()
		l.pending[c] = struct{}{}
		l.mu.Unlock()
		select {
		case <-l.done:
			l.forget(c)
			c.Close()
			return
		default:
		}
		select {
		case l.classify <- struct{}{}:
			supervise.Go("networking.direct-listener.classify", func() { l.classifyConnection(c) })
		case <-l.done:
			l.forget(c)
			c.Close()
			return
		}
	}
}
func (l *DirectListener) forget(c *countedTLSConn) { l.mu.Lock(); delete(l.pending, c); l.mu.Unlock() }
func (l *DirectListener) classifyConnection(c *countedTLSConn) {
	defer func() { <-l.classify }()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReaderSize(c, 1024)
	first, e := reader.Peek(1)
	if e != nil {
		l.forget(c)
		c.Close()
		return
	}
	buffered := &bufferedTLSConn{Conn: c, reader: reader, owner: c}
	var conn net.Conn
	if first[0] == 0x16 {
		conn = tls.Server(buffered, l.config)
	} else if first[0] >= 'A' && first[0] <= 'Z' {
		conn = buffered
	} else {
		l.forget(c)
		c.Close()
		return
	}
	c.SetReadDeadline(time.Time{})
	select {
	case l.ready <- conn:
	case <-l.done:
		l.forget(c)
		c.Close()
	}
}

func (l *DirectListener) Accept() (net.Conn, error) {
	select {
	case <-l.done:
		return nil, l.closedError()
	case conn := <-l.ready:
		var raw *countedTLSConn
		switch c := conn.(type) {
		case *bufferedTLSConn:
			raw = c.owner
		case *tls.Conn:
			if buffered, ok := c.NetConn().(*bufferedTLSConn); ok {
				raw = buffered.owner
			}
		}
		if raw != nil {
			l.forget(raw)
		}
		select {
		case <-l.done:
			conn.Close()
			return nil, l.closedError()
		default:
			return conn, nil
		}
	}
}
func (l *DirectListener) closedError() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	return net.ErrClosed
}
func (l *DirectListener) Close() error {
	var result error
	l.once.Do(func() {
		close(l.done)
		result = l.Listener.Close()
		l.mu.Lock()
		pending := make([]*countedTLSConn, 0, len(l.pending))
		for c := range l.pending {
			pending = append(pending, c)
		}
		l.mu.Unlock()
		for _, c := range pending {
			c.Close()
			l.forget(c)
		}
	})
	if errors.Is(result, net.ErrClosed) {
		return nil
	}
	return result
}

type countedTLSConn struct {
	net.Conn
	once   sync.Once
	closed func()
}

func (c *countedTLSConn) Close() error { e := c.Conn.Close(); c.once.Do(c.closed); return e }

type bufferedTLSConn struct {
	net.Conn
	reader *bufio.Reader
	owner  *countedTLSConn
}

func (c *bufferedTLSConn) Read(b []byte) (int, error) { return c.reader.Read(b) }

// RawConnection unwraps the classifier without hiding *tls.Conn from net/http.
func RawConnection(c net.Conn) net.Conn {
	switch v := c.(type) {
	case *tls.Conn:
		return RawConnection(v.NetConn())
	case *bufferedTLSConn:
		return RawConnection(v.Conn)
	case *countedTLSConn:
		return RawConnection(v.Conn)
	default:
		return c
	}
}
