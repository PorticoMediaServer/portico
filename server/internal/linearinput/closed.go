package linearinput

import (
	"context"
	"io"
	"net"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

// Closed is a finite, pinned descriptor adapter for a managed artifact awaiting
// validation. It establishes no source identity or playback authorization. Its
// owner must retain the descriptor and verified byte identity through Publish.
type Closed struct {
	url      string
	server   *http.Server
	v4, v6   *net.TCPListener
	wg       sync.WaitGroup
	mu       sync.Mutex
	handlers sync.WaitGroup
	closing  bool
}

func OpenClosed(reader io.ReaderAt, size int64) (*Closed, error) {
	if reader == nil || size <= 0 {
		return nil, ErrFormat
	}
	key, e := token()
	if e != nil {
		return nil, e
	}
	b := &Closed{}
	b.v4, e = net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		return nil, e
	}
	port := b.v4.Addr().(*net.TCPAddr).Port
	b.v6, e = net.ListenTCP("tcp6", &net.TCPAddr{IP: net.IPv6loopback, Port: port})
	if e != nil {
		b.v4.Close()
		return nil, e
	}
	b.url = "http://127.0.0.1:" + strconv.Itoa(port) + "/input/" + key
	slots := make(chan struct{}, 2)
	b.server = &http.Server{ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8 << 10, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		if b.closing {
			b.mu.Unlock()
			http.Error(w, "Unavailable", 503)
			return
		}
		b.handlers.Add(1)
		b.mu.Unlock()
		defer b.handlers.Done()
		if (r.Method != "GET" && r.Method != "HEAD") || r.URL.Path != "/input/"+key || r.URL.RawPath != "" || r.URL.RawQuery != "" {
			http.Error(w, "Unavailable", 404)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "Busy", 503)
			return
		}
		// ServeContent handles a single byte range. Multipart ranges and unbounded
		// header counts are not part of this private decoder capability.
		if strings.Contains(r.Header.Get("Range"), ",") {
			http.Error(w, "Invalid range", 416)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "video/mp2t")
		http.ServeContent(w, r, "capture.ts", time.Time{}, io.NewSectionReader(reader, 0, size))
	})}
	b.wg.Add(2)
	supervise.Go("linearinput.closed.serve4", func() { defer b.wg.Done(); _ = b.server.Serve(b.v4) })
	supervise.Go("linearinput.closed.serve6", func() { defer b.wg.Done(); _ = b.server.Serve(b.v6) })
	return b, nil
}
func (b *Closed) URL() string { return b.url }
func (b *Closed) Reserve() (*decoder.EndpointReservation, error) {
	return decoder.ReserveEndpoints(b.v4, b.v6)
}
func (b *Closed) Close() error {
	b.mu.Lock()
	b.closing = true
	b.mu.Unlock()
	_ = b.server.Close()
	_ = b.v4.Close()
	_ = b.v6.Close()
	b.wg.Wait()
	b.handlers.Wait()
	return nil
}
func (b *Closed) Shutdown(ctx context.Context) error {
	b.mu.Lock()
	b.closing = true
	b.mu.Unlock()
	e := b.server.Shutdown(ctx)
	if e != nil {
		_ = b.server.Close()
	}
	b.wg.Wait()
	b.handlers.Wait()
	return e
}
