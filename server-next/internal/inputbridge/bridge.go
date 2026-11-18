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
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/supervise"
)

// ErrInput says the source cannot be served.
var ErrInput = errors.New("media input unavailable")

// Bridge serves one source until it is closed.
type Bridge struct {
	url  string
	path string
	// served is what the bridge serves: the source at path, then a title's parts at path/0,
	// path/1 and on (OpenParts).
	served  []served
	server  *http.Server
	ipv4    *net.TCPListener // nil on a host with IPv6 loopback only
	ipv6    *net.TCPListener // nil on a host with no IPv6 loopback
	done    chan struct{}
	closing sync.Once
}

// served is one source and the requests being answered from it.
type served struct {
	source   io.ReaderAt
	size     int64
	requests chan struct{}
}

func serve(source io.ReaderAt, size int64) served {
	return served{source: source, size: size, requests: make(chan struct{}, 8)}
}

// Open starts serving a source of a known size on a fresh loopback port. The
// tool is told the IPv4 address. The same port on IPv6 loopback is held too, so
// nothing else can sit there, unless the host has no IPv6 loopback at all (many
// containers), where there is nothing to hold. On a host with IPv6 loopback only,
// the tool is told [::1].
// The bridge stops when ctx ends or Close is called.
func Open(ctx context.Context, source io.ReaderAt, size int64) (*Bridge, error) {
	if source == nil || size <= 0 {
		return nil, ErrInput
	}
	b, err := listen(ctx)
	if err != nil {
		return nil, err
	}
	b.served = []served{serve(source, size)}
	b.start(ctx)
	return b, nil
}

// Part is one file of a title in several files, and its length in seconds on the title's clock.
type Part struct {
	Source   io.ReaderAt
	Size     int64
	Duration float64
}

// OpenParts serves a title in several files to one tool as Open serves one file: each part at
// PartURL(n), and at URL a list for FFmpeg's concat demuxer that names the parts in order with
// their lengths, so the tool reads them as one title on the title's own clock. The tool still
// learns no path, and reaches nothing but this bridge.
func OpenParts(ctx context.Context, parts []Part) (*Bridge, error) {
	if len(parts) == 0 {
		return nil, ErrInput
	}
	for _, part := range parts {
		if part.Source == nil || part.Size <= 0 || part.Duration <= 0 {
			return nil, ErrInput
		}
	}
	b, err := listen(ctx)
	if err != nil {
		return nil, err
	}
	var list strings.Builder
	list.WriteString("ffconcat version 1.0\n")
	b.served = []served{{}}
	for n, part := range parts {
		fmt.Fprintf(&list, "file '%s'\nduration %.6f\n", b.PartURL(n), part.Duration)
		b.served = append(b.served, serve(part.Source, part.Size))
	}
	b.served[0] = serve(strings.NewReader(list.String()), int64(list.Len()))
	b.start(ctx)
	return b, nil
}

// PartURL is where OpenParts serves part n. Like URL, never log it.
func (b *Bridge) PartURL(n int) string { return b.url + "/" + strconv.Itoa(n) }

// listen binds a fresh loopback port and makes the bridge's private address on it.
func listen(ctx context.Context) (*Bridge, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrInput
	}
	var listeners []net.Listener
	ipv4, err := listenIPv4(0)
	var ipv6 *net.TCPListener
	if err != nil {
		// 127.0.0.1 could not be bound. Bind [::1], then the same port on IPv4: a host that
		// has IPv4 loopback after all (its ports ran out a moment ago) is refused rather than
		// left with this port unheld there; a host with IPv6 loopback only is told [::1].
		if ipv6, err = listenIPv6(0); err != nil {
			return nil, err
		}
		port := ipv6.Addr().(*net.TCPAddr).Port
		if ipv4, err = listenIPv4(port); err == nil {
			listeners = append(listeners, ipv4, ipv6)
		} else if probe, probeErr := listenIPv4(0); probeErr == nil {
			probe.Close()
			ipv6.Close()
			return nil, err
		} else {
			ipv4 = nil
			listeners = append(listeners, ipv6)
		}
	} else {
		listeners = append(listeners, ipv4)
		ipv6, err = listenIPv6(ipv4.Addr().(*net.TCPAddr).Port)
		if err == nil {
			listeners = append(listeners, ipv6)
		} else if probe, probeErr := listenIPv6(0); probeErr == nil {
			// IPv6 loopback works, so this port is taken there by something else.
			probe.Close()
			ipv4.Close()
			return nil, err
		} else {
			ipv6 = nil
		}
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		for _, listener := range listeners {
			listener.Close()
		}
		return nil, err
	}
	b := &Bridge{ipv4: ipv4, ipv6: ipv6, path: "/input/" + hex.EncodeToString(secret[:]), done: make(chan struct{})}
	told := listeners[0].Addr().String()
	b.url = "http://" + told + b.path
	return b, nil
}

// start serves what the bridge was given, until ctx ends or Close is called.
func (b *Bridge) start(ctx context.Context) {
	b.server = &http.Server{
		Handler: b, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	for _, listener := range []*net.TCPListener{b.ipv4, b.ipv6} {
		if listener != nil {
			supervise.Go("inputbridge.serve", func() { _ = b.server.Serve(listener) })
		}
	}
	stop := context.AfterFunc(ctx, func() { _ = b.Close() })
	supervise.Go("inputbridge.owner", func() { <-b.done; stop() })
}

// listenIPv4 and listenIPv6 are replaced in tests to stand in for a host without one of
// the two loopbacks.
var listenIPv4 = func(port int) (*net.TCPListener, error) {
	return net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
}

var listenIPv6 = func(port int) (*net.TCPListener, error) {
	return net.ListenTCP("tcp6", &net.TCPAddr{IP: net.IPv6loopback, Port: port})
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
	// The context ending and the caller can both close at the same moment.
	var err error
	b.closing.Do(func() {
		close(b.done)
		err = b.server.Close()
	})
	return err
}

// which is the source an address asks for, or -1 for none: the private path itself, or one of
// a title's parts beneath it.
func (b *Bridge) which(path string) int {
	if len(path) < len(b.path) || subtle.ConstantTimeCompare([]byte(path[:len(b.path)]), []byte(b.path)) != 1 {
		return -1
	}
	if part := path[len(b.path):]; part != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(part, "/"))
		if err != nil || n < 0 || n+1 >= len(b.served) || part != "/"+strconv.Itoa(n) {
			return -1
		}
		return n + 1
	}
	return 0
}

func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := b.which(r.URL.Path)
	if n < 0 || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	source := b.served[n]
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
	case source.requests <- struct{}{}:
		defer func() { <-source.requests }()
	default:
		http.Error(w, "input busy", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", time.Time{}, io.NewSectionReader(source.source, 0, source.size))
}
