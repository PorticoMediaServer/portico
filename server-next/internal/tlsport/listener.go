package tlsport

import (
	"bufio"
	"io"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"
)

// sniffTimeout is how long a new connection has to send its first byte. It matches the
// server's limit on slow request headers.
const sniffTimeout = 10 * time.Second

// Listen wraps ln so that a connection that opens with a TLS handshake is answered over TLS
// with config, and any other is plain HTTP. The first byte of a TLS connection is always 22
// (a handshake record), which no HTTP request starts with. Each new connection is looked at in
// its own goroutine, so a slow one never holds up the others.
func Listen(ln net.Listener, config *tls.Config) net.Listener {
	l := &listener{Listener: ln, config: config, ready: make(chan net.Conn), done: make(chan struct{})}
	go l.acceptLoop()
	return l
}

type listener struct {
	net.Listener
	config *tls.Config
	ready  chan net.Conn
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	err    error
}

func (l *listener) acceptLoop() {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			l.mu.Lock()
			l.err = err
			l.mu.Unlock()
			l.Close()
			return
		}
		go l.sniff(c)
	}
}

func (l *listener) sniff(c net.Conn) {
	c.SetReadDeadline(time.Now().Add(sniffTimeout))
	br := bufio.NewReaderSize(c, 1024)
	first, err := br.Peek(1)
	c.SetReadDeadline(time.Time{})
	if err != nil {
		c.Close()
		return
	}
	var out net.Conn = &peeked{Conn: c, r: br}
	if first[0] == 22 {
		out = tls.Server(out, l.config)
	}
	select {
	case l.ready <- out:
	case <-l.done:
		c.Close()
	}
}

func (l *listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ready:
		return c, nil
	case <-l.done:
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.err != nil {
			return nil, l.err
		}
		return nil, net.ErrClosed
	}
}

func (l *listener) Close() error {
	var err error
	l.once.Do(func() {
		close(l.done)
		err = l.Listener.Close()
	})
	return err
}

// peeked is a connection whose first bytes were read to look at and are read again.
type peeked struct {
	net.Conn
	r *bufio.Reader
}

func (p *peeked) Read(b []byte) (int, error) { return p.r.Read(b) }

// ReadFrom keeps the kernel's file-to-socket copy (sendfile) that net/http uses for media:
// io.Copy hands over to the TCP connection's own ReadFrom. Writes never pass the peek buffer.
func (p *peeked) ReadFrom(r io.Reader) (int64, error) { return io.Copy(p.Conn, r) }
