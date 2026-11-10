// Package inputbridge hands one source to one sandboxed media tool over a
// private loopback address.
//
// The decoder's probe and analysis commands run with no filesystem access to
// the library and with their network confined to one loopback endpoint. The
// bridge is that endpoint: a range server for exactly one source, at an address
// only the tool is told. The server keeps the file open and reads it itself;
// the tool never learns its path.
package inputbridge

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/supervise"
)

// ErrInput says the source cannot be served.
var ErrInput = errors.New("media input unavailable")

// Bridge serves one source until it is closed.
type Bridge struct {
	url      string
	path     string
	source   io.ReaderAt
	size     int64
	server   *http.Server
	ipv4     *net.TCPListener
	ipv6     *net.TCPListener
	requests chan struct{}
	done     chan struct{}
}

// Open starts serving a source of a known size on a fresh loopback port, on
// both address families so the tool reaches it however it resolves loopback.
// The bridge stops when ctx ends or Close is called.
func Open(ctx context.Context, source io.ReaderAt, size int64) (*Bridge, error) {
	if ctx == nil || ctx.Err() != nil || source == nil || size <= 0 {
		return nil, ErrInput
	}
	ipv4, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	ipv6, err := net.ListenTCP("tcp6", &net.TCPAddr{IP: net.IPv6loopback, Port: ipv4.Addr().(*net.TCPAddr).Port})
	if err != nil {
		ipv4.Close()
		return nil, err
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		ipv4.Close()
		ipv6.Close()
		return nil, err
	}
	b := &Bridge{
		source: source, size: size, ipv4: ipv4, ipv6: ipv6,
		path: "/input/" + hex.EncodeToString(secret[:]), requests: make(chan struct{}, 8), done: make(chan struct{}),
	}
	b.url = "http://" + ipv4.Addr().String() + b.path
	b.server = &http.Server{
		Handler: b, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	for _, listener := range []net.Listener{ipv4, ipv6} {
		served := listener
		supervise.Go("inputbridge.serve", func() { _ = b.server.Serve(served) })
	}
	stop := context.AfterFunc(ctx, func() { _ = b.Close() })
	supervise.Go("inputbridge.owner", func() { <-b.done; stop() })
	return b, nil
}

// URL is a private capability for the one tool it is given to. Never log it.
func (b *Bridge) URL() string { return b.url }

// Reserve keeps the bridge's port from being bound by anything else until the
// tool that was given it has really exited. Each tool run takes its own.
func (b *Bridge) Reserve() (*decoder.EndpointReservation, error) {
	return decoder.ReserveEndpoints(b.ipv4, b.ipv6)
}

// Close stops serving and drops open connections. It is safe to call twice.
func (b *Bridge) Close() error {
	select {
	case <-b.done:
		return nil
	default:
		close(b.done)
	}
	return b.server.Close()
}

func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.URL.Path), []byte(b.path)) != 1 || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(r.Header.Values("Range")) > 1 || len(r.Header.Get("Range")) > 128 {
		http.Error(w, "range invalid", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	select {
	case b.requests <- struct{}{}:
		defer func() { <-b.requests }()
	default:
		http.Error(w, "input busy", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", time.Time{}, io.NewSectionReader(b.source, 0, b.size))
}
