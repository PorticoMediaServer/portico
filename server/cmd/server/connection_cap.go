package main

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/httpapi"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The admission lanes protect handlers, not accepted connections. Every accepted
// connection has buffers and goroutine state before any lane is consulted.
// HTTP/2 adds concurrent stream state and flow-control buffers, so transport
// memory cannot be budgeted as just two HTTP/1 buffers per connection.
//
// The cap is a listener, not a handler: a connection that is never accepted
// costs nothing at all. Past it the kernel's own listen backlog absorbs the
// burst, and past that the client sees a connection refusal — which is the
// honest answer when the server has no capacity left to explain itself with, and
// is what every HTTP client already retries.
//
// The absolute ceiling is 2,048; connectionBudget selects a smaller bound from
// the discoverable host/process memory using a conservative planning allowance.
// This is one part of overload protection, alongside stream and admission caps.

// maximumConnections is the upper bound even on large hosts.
const maximumConnections = 2048

// cappedListener bounds accepted connections. The bound is a counter rather
// than a fixed-size channel because the listener is serving from the moment the
// socket is bound, before the owner's saved connection limit can be read: the
// limit is set once more, in place, when the settings are open.
type cappedListener struct {
	net.Listener
	mu sync.Mutex
	// limit counts every socket. With a console reserve, ordinaryLimit is the
	// owner's share of it and limit is ordinaryLimit plus the reserve.
	limit         int
	open          int
	reserved      bool
	ordinaryLimit int
	ordinaryOpen  int
	// freed wakes an accept parked at the ceiling when a slot comes back or the
	// limit grows; one buffered signal is enough for the single accept loop.
	freed        chan struct{}
	ownerRequest atomic.Pointer[func(*http.Request) bool]
	// held is published so the diagnostics and the log can say how close to the
	// ceiling the server is running.
	held      atomic.Int64
	deferred  atomic.Uint64
	warned    sync.Once
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func capConnections(inner net.Listener, limit int) *cappedListener {
	if limit < 1 {
		limit = maximumConnections
	}
	return &cappedListener{Listener: inner, limit: limit, freed: make(chan struct{}, 1), done: make(chan struct{})}
}

// boundedDirectListener puts the socket cap under the TLS classifier. net/http
// must receive the classifier's *tls.Conn to recognize h2 and populate r.TLS.
func boundedDirectListener(raw net.Listener, limit int, build func(net.Listener) (*networking.DirectListener, error)) (*networking.DirectListener, error) {
	capped := capConnections(raw, limit)
	direct, err := build(capped)
	if err != nil {
		_ = capped.Close()
		return nil, err
	}
	return direct, nil
}

func (l *cappedListener) Accept() (net.Conn, error) {
	for waited := false; ; {
		select {
		case <-l.done:
			return nil, net.ErrClosed
		default:
		}
		if l.take() {
			break
		}
		// At the ceiling. Wait for a slot rather than accepting a connection there
		// is no room to serve: the backlog is a better queue than the heap.
		if !waited {
			waited = true
			l.deferred.Add(1)
			l.warned.Do(func() {
				log.Printf("Connection ceiling of %d reached; further connections wait in the listen backlog", l.ceiling())
			})
		}
		select {
		case <-l.freed:
		case <-l.done:
			return nil, net.ErrClosed
		}
	}
	conn, err := l.Listener.Accept()
	if err != nil {
		l.release()
		return nil, err
	}
	l.held.Add(1)
	return &cappedConn{Conn: conn, listener: l}, nil
}

func (l *cappedListener) take() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.open >= l.limit {
		return false
	}
	l.open++
	return true
}

func (l *cappedListener) release() {
	l.mu.Lock()
	l.open--
	l.mu.Unlock()
	l.wake()
}

func (l *cappedListener) wake() {
	select {
	case l.freed <- struct{}{}:
	default:
	}
}

func (l *cappedListener) ceiling() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limit
}

// Closing the transport must also wake an accept parked at the capacity ceiling.
// Otherwise http.Server.Shutdown waits forever for its serving listener to exit.
func (l *cappedListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.done)
		l.closeErr = l.Listener.Close()
	})
	return l.closeErr
}

// Held reports how many connections are open, and how many accepts have had to
// wait for a slot.
func (l *cappedListener) Held() (int64, uint64) { return l.held.Load(), l.deferred.Load() }

// cappedConn returns its slot exactly once, whatever closes it.
type cappedConn struct {
	net.Conn
	listener     *cappedListener
	once         sync.Once
	mu           sync.Mutex
	ordinaryHeld bool
	closed       bool
	idleTimer    *time.Timer
	idleEpoch    uint64
}

func (c *cappedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.idleEpoch++
		if c.idleTimer != nil {
			c.idleTimer.Stop()
			c.idleTimer = nil
		}
		ordinary := c.ordinaryHeld
		c.mu.Unlock()
		if ordinary {
			c.listener.mu.Lock()
			c.listener.ordinaryOpen--
			c.listener.mu.Unlock()
		}
		c.listener.held.Add(-1)
		c.listener.release()
	})
	return err
}

// SetDeadline and friends pass through; a wrapped connection that quietly lost
// its deadlines would defeat the write budgets the streaming routes set.
func (c *cappedConn) SetDeadline(t time.Time) error      { return c.Conn.SetDeadline(t) }
func (c *cappedConn) SetReadDeadline(t time.Time) error  { return c.Conn.SetReadDeadline(t) }
func (c *cappedConn) SetWriteDeadline(t time.Time) error { return c.Conn.SetWriteDeadline(t) }

// consoleConnectionReserve is separate from the owner's ordinary connection
// budget. Requests reveal their route only after TLS and HTTP headers arrive,
// so these bounded slots let the console reach authorization even at capacity.
const consoleConnectionReserve = 32

type connectionKey struct{}

func consoleConnections(inner net.Listener, limit int) *cappedListener {
	l := capConnections(inner, limit+consoleConnectionReserve)
	l.reserved = true
	l.ordinaryLimit = limit
	return l
}

// setOrdinaryLimit replaces the owner's share in place. Connections already
// open past a smaller limit finish normally; new ones wait until the count is
// back under it.
func (l *cappedListener) setOrdinaryLimit(limit int) {
	l.mu.Lock()
	l.ordinaryLimit = limit
	l.limit = limit + consoleConnectionReserve
	l.mu.Unlock()
	l.wake()
}

// ordinaryUsage reports the open ordinary connections and their limit.
func (l *cappedListener) ordinaryUsage() (open, limit int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ordinaryOpen, l.ordinaryLimit
}

// setOwnerRequest installs the owner check once identity is open; until then
// no request can prove it is the owner's.
func (l *cappedListener) setOwnerRequest(owner func(*http.Request) bool) {
	l.ownerRequest.Store(&owner)
}

func (l *cappedListener) context(ctx context.Context, conn net.Conn) context.Context {
	if c, ok := networking.RawConnection(conn).(*cappedConn); ok {
		return context.WithValue(ctx, connectionKey{}, c)
	}
	return ctx
}

func (l *cappedListener) protectConsole(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Context().Value(connectionKey{}).(*cappedConn)
		// Route authorization still runs normally. The reserve also carries the
		// console shell and sign-in, so an owner without a warm session can enter.
		console := !strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/v1/admin/") || strings.HasPrefix(r.URL.Path, "/v1/auth/") || strings.HasPrefix(r.URL.Path, "/v1/direct/") || r.URL.Path == "/v1/server" || r.URL.Path == "/v1/system" || r.URL.Path == "/v1/capabilities" || r.URL.Path == "/v1/readiness" || strings.HasPrefix(r.URL.Path, "/v1/sessions") || r.URL.Path == "/v1/networking/connection" || strings.HasPrefix(r.URL.Path, "/v1/setup")
		if owner := l.ownerRequest.Load(); !console && owner != nil && r.Method == http.MethodGet && (r.URL.Path == "/v1/bootstrap" || r.URL.Path == "/v1/preferences" || r.URL.Path == "/v1/console/registry") {
			console = (*owner)(r)
		}
		// The reserve is for a person at the console. A script with an API key
		// or access token takes an ordinary connection whatever it asks for, so
		// no number of keys can use up the owner's way in.
		if console && httpapi.CarriesAPICredential(r) {
			console = false
		}
		if c != nil && console && r.ProtoMajor < 2 {
			c.mu.Lock()
			ordinary := c.ordinaryHeld
			c.mu.Unlock()
			if !ordinary {
				w.Header().Set("Connection", "close")
			}
		}
		if c != nil && !console && !c.admitOrdinary() {
			w.Header().Set("Connection", "close")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"code":"server_busy","message":"The server is busy. Try again shortly.","retryable":true}}`)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (c *cappedConn) admitOrdinary() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ordinaryHeld || !c.listener.reserved {
		return true
	}
	if c.closed {
		return false
	}
	l := c.listener
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ordinaryOpen >= l.ordinaryLimit {
		return false
	}
	l.ordinaryOpen++
	c.ordinaryHeld = true
	return true
}

// HTTP/2 reports an initial idle state after its preface, before its first
// request. A short grace period admits that request while still reclaiming
// genuinely idle reserve sockets well before the ordinary keep-alive timeout.
const reserveIdleTimeout = 2 * time.Second

func (l *cappedListener) state(conn net.Conn, state http.ConnState) {
	c, ok := networking.RawConnection(conn).(*cappedConn)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.idleEpoch++
	if c.idleTimer != nil {
		c.idleTimer.Stop()
		c.idleTimer = nil
	}
	if state != http.StateIdle || c.ordinaryHeld || c.closed {
		return
	}
	epoch := c.idleEpoch
	c.idleTimer = time.AfterFunc(reserveIdleTimeout, func() {
		c.mu.Lock()
		expire := c.idleEpoch == epoch && !c.ordinaryHeld && !c.closed
		if expire {
			c.closed = true
		}
		c.mu.Unlock()
		if expire {
			_ = conn.Close()
		}
	})
}
