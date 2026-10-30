package subtitlevideo

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// This adapter borrows one retained input for exactly one physical decoder. It
// does not fetch URLs, reconnect, or expose its private token to a viewer. The
// same source input survives across segment jobs; overlapping extents are checked
// by their acquisition-level digest ledger as well as before/after observation.
type bridge struct {
	url         string
	reservation *decoder.EndpointReservation
	server      *http.Server
	listeners   []*net.TCPListener
	wg          sync.WaitGroup
	mu          sync.Mutex
	closing     bool
	handlers    sync.WaitGroup
	cancel      context.CancelFunc
}
type extentLedger struct {
	mu      sync.Mutex
	digests map[int64][32]byte
}

func (l *extentLedger) check(offset int64, b []byte) error {
	sum := sha256.Sum256(b)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.digests == nil {
		l.digests = map[int64][32]byte{}
	}
	if old, ok := l.digests[offset]; ok && old != sum {
		return subtitles.ErrConflict
	}
	l.digests[offset] = sum
	return nil
}
func openBridge(ctx context.Context, input subtitles.RenderInput, ledger *extentLedger) (*bridge, error) {
	var a, b *net.TCPListener
	var e error
	for attempt := 0; attempt < 8; attempt++ {
		a, e = net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if e != nil {
			return nil, e
		}
		b, e = net.ListenTCP("tcp6", &net.TCPAddr{IP: net.IPv6loopback, Port: a.Addr().(*net.TCPAddr).Port})
		if e == nil {
			break
		}
		a.Close()
	}
	if e != nil {
		return nil, e
	}
	reservation, e := decoder.ReserveEndpoints(a, b)
	if e != nil {
		a.Close()
		b.Close()
		return nil, e
	}
	life, cancel := context.WithCancel(ctx)
	token := sha256.Sum256([]byte(identity.Token()))
	path := fmt.Sprintf("/input/%x", token)
	bridge := &bridge{reservation: reservation, listeners: []*net.TCPListener{a, b}, cancel: cancel, url: "http://" + a.Addr().String() + path}
	gate := make(chan struct{}, 4)
	bridge.server = &http.Server{ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 8192, BaseContext: func(net.Listener) context.Context { return life }, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bridge.mu.Lock()
		if bridge.closing {
			bridge.mu.Unlock()
			return
		}
		bridge.handlers.Add(1)
		bridge.mu.Unlock()
		defer bridge.handlers.Done()
		if r.URL.Path != path || r.URL.RawQuery != "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.NotFound(w, r)
			return
		}
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-r.Context().Done():
			return
		}
		// A decoder hangs up mid-response whenever it seeks or has read all it
		// needs. That ends this response, never a read already sent to the
		// source: the observed source reader gives up on itself when a read it
		// is serving is cancelled, which would lose the input for every later
		// request of the same analysis. Source calls run under the bridge's own
		// lifetime; the request's only stops further calls.
		if r.Context().Err() != nil || input.Validate(life) != nil {
			http.Error(w, "source changed", 409)
			return
		}
		size := input.Size()
		start, end := int64(0), size-1
		status := 200
		if h := r.Header.Get("Range"); h != "" {
			if !strings.HasPrefix(h, "bytes=") || strings.Contains(h, ",") {
				http.Error(w, "range", 416)
				return
			}
			a, b, ok := strings.Cut(strings.TrimPrefix(h, "bytes="), "-")
			if !ok || a == "" {
				http.Error(w, "range", 416)
				return
			}
			var e error
			start, e = strconv.ParseInt(a, 10, 64)
			if e != nil {
				http.Error(w, "range", 416)
				return
			}
			if b != "" {
				end, e = strconv.ParseInt(b, 10, 64)
				if e != nil {
					http.Error(w, "range", 416)
					return
				}
			}
			if start < 0 || start >= size || end < start || end >= size {
				http.Error(w, "range", 416)
				return
			}
			status = 206
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(status)
		if r.Method == http.MethodHead {
			return
		}
		const chunk int64 = 1 << 20
		for pos := start; pos <= end; {
			base := (pos / chunk) * chunk
			n := min(chunk, size-base)
			if r.Context().Err() != nil {
				return
			}
			data, e := input.ReadExtent(life, base, n)
			if e != nil || ledger.check(base, data) != nil {
				return
			}
			offset := pos - base
			take := min(int64(len(data))-offset, end-pos+1)
			if take <= 0 {
				return
			}
			if _, e = w.Write(data[offset : offset+take]); e != nil {
				return
			}
			pos += take
		}
	})}
	for _, listener := range bridge.listeners {
		bridge.wg.Add(1)
		l := listener
		supervise.Go("subtitlevideo.bridge.serve", func() { defer bridge.wg.Done(); _ = bridge.server.Serve(l) })
	}
	return bridge, nil
}
func (b *bridge) Close() {
	b.mu.Lock()
	b.closing = true
	b.mu.Unlock()
	b.cancel()
	_ = b.server.Close()
	b.wg.Wait()
	b.handlers.Wait()
	_ = b.reservation.Close()
}
