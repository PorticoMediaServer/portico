// Package linearinput is the private input adapter for shared live media work.
// It is not a player, media endpoint, or independent authorization system.
package linearinput

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/privateoutput"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

var (
	ErrUnavailable = errors.New("The live source is unavailable.")
	ErrRetryWindow = errors.New("The live source retry window expired.")
	ErrWindow      = errors.New("The live source input window advanced.")
)

const maxManifest = 1 << 20
const maxSegment = 64 << 20

// Bound slice and timestamp overhead even when the source returns tiny reads.
const maxRawBlocks = 64 << 10

type resource struct {
	url   string
	depth int
	kind  string
	ext   string
}

// mediaExtensions are the segment extensions a route keeps from its source
// URL. FFmpeg's HLS demuxer (7.1+, extension_picky) refuses a segment whose
// URL has no allowed extension, or one that doesn't match its format, so every
// route carries one: the source's when it's one of these, "ts" otherwise (an
// extensionless IPTV segment is almost always MPEG-TS); manifests are "m3u8"
// and keys "key".
var mediaExtensions = map[string]bool{"ts": true, "mpegts": true, "mpg": true, "mpeg": true, "aac": true, "ac3": true, "eac3": true, "ec3": true, "mp3": true, "mp4": true, "m4s": true, "m4a": true, "m4v": true, "mov": true, "cmfv": true, "cmfa": true, "fmp4": true, "vtt": true, "webvtt": true}

func routeExtension(kind string, u *url.URL) string {
	switch kind {
	case "manifest":
		return "m3u8"
	case "key":
		return "key"
	}
	if ext := strings.ToLower(strings.TrimPrefix(path.Ext(u.Path), ".")); mediaExtensions[ext] {
		return ext
	}
	return "ts"
}

type Span struct {
	First, Last  time.Time
	Bytes        int64
	Gaps         int
	Reconnecting bool
	Ended        bool
}

// Gateway retains the one raw tuner connection for probe and capture readers.
// For HLS, every nested URI is replaced by an opaque private route and independently
// passes the same DNS-pinned policy. No source URL escapes in an error/header.
type Gateway struct {
	captions      CaptionDeclarations
	rawTimes      []time.Time
	resourceSlots chan struct{}
	gapBaseline   int
	shared        *Gateway
	release       func()
	closeOnce     sync.Once
	input         livechannels.Input
	cache         map[string]*cachedResource
	cacheBytes    int64
	output        *privateoutput.Route
	closing       bool
	ctx           context.Context
	cancel        context.CancelFunc
	policy        remotemedia.Policy
	secret        string
	address       string
	mu            sync.Mutex
	resources     map[string]resource
	reverse       map[string]string
	hls           bool
	first         []byte
	raw           [][]byte
	base, total   int64
	span          Span
	failure       error
	done          bool
	changed       chan struct{}
	active        int
	clients       map[*remotemedia.Client]bool
	bodies        map[io.ReadCloser]bool
	http          *http.Server
	v4, v6        *net.TCPListener
	wg            sync.WaitGroup
}

func token() (string, error) {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", ErrUnavailable
	}
	return hex.EncodeToString(b[:]), nil
}
func openInput(ctx context.Context, in livechannels.Input) (*Gateway, error) {
	child, cancel := context.WithCancel(ctx)
	g := &Gateway{resourceSlots: make(chan struct{}, 4), input: in, cache: map[string]*cachedResource{}, ctx: child, cancel: cancel, policy: in.Policy, resources: map[string]resource{}, reverse: map[string]string{}, clients: map[*remotemedia.Client]bool{}, bodies: map[io.ReadCloser]bool{}, changed: make(chan struct{})}
	var e error
	g.secret, e = token()
	if e != nil {
		cancel()
		return nil, e
	}
	g.v4, e = net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		cancel()
		return nil, ErrUnavailable
	}
	port := g.v4.Addr().(*net.TCPAddr).Port
	g.v6, e = net.ListenTCP("tcp6", &net.TCPAddr{IP: net.IPv6loopback, Port: port})
	if e != nil {
		g.v4.Close()
		cancel()
		return nil, ErrUnavailable
	}
	g.address = "http://127.0.0.1:" + strconv.Itoa(port)
	client, response, reader, e := g.openRetry(false)
	if e != nil {
		g.Close()
		return nil, e
	}
	prefix, _ := reader.Peek(7) // openRetry already admitted this buffered prefix.
	g.hls = bytes.Equal(prefix, []byte("#EXTM3U"))
	finalURL := in.Locator
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	if g.hls {
		data, e := io.ReadAll(io.LimitReader(reader, maxManifest+1))
		response.Body.Close()
		client.Close()
		g.untrack(client, response.Body)
		if e != nil || len(data) > maxManifest {
			g.Close()
			return nil, ErrFormat
		}
		g.first = data
		entry := &cachedResource{ready: make(chan struct{}), data: data, location: finalURL, expires: time.Now().Add(500 * time.Millisecond)}
		close(entry.ready)
		g.cache["manifest\n"+finalURL+"\n"] = entry
		g.cacheBytes = int64(len(data))
		g.resources[g.secret] = resource{finalURL, 0, "manifest", "m3u8"}
	} else {
		// MPEG-TS framing is inspected by the qualified probe; no guessed codec facts.
		g.wg.Add(1)
		supervise.Go("linearinput.gateway.pump", func() {
			defer g.wg.Done()
			defer response.Body.Close()
			defer client.Close()
			defer g.untrack(client, response.Body)
			g.pump(reader)
		})
	}
	g.http = &http.Server{Handler: http.HandlerFunc(g.serve), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8 << 10, BaseContext: func(net.Listener) context.Context { return child }}
	g.wg.Add(2)
	supervise.Go("linearinput.gateway.serve4", func() { defer g.wg.Done(); _ = g.http.Serve(g.v4) })
	supervise.Go("linearinput.gateway.serve6", func() { defer g.wg.Done(); _ = g.http.Serve(g.v6) })
	return g, nil
}
func (g *Gateway) HLS() bool { return g.hls }
func (g *Gateway) URL() string {
	if g.hls {
		return g.address + "/input/" + g.secret + ".m3u8"
	}
	return g.address + "/input/" + g.secret
}
func (g *Gateway) Reserve() (*decoder.EndpointReservation, error) {
	return decoder.ReserveEndpoints(g.v4, g.v6)
}
func (g *Gateway) RetryExpired() bool {
	// A reader reports the original acquisition it owns, never a newer registry entry.
	if g.shared != nil {
		return g.shared.RetryExpired()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return errors.Is(g.failure, ErrRetryWindow)
}
func (g *Gateway) Snapshot() Span { g.mu.Lock(); defer g.mu.Unlock(); return g.span }
func (g *Gateway) signal()        { close(g.changed); g.changed = make(chan struct{}) }
func (g *Gateway) pump(r io.Reader) {
	buf := make([]byte, 64<<10)
	for {
		n, e := r.Read(buf)
		g.mu.Lock()
		if n > 0 {
			g.span.Reconnecting = false
			now := time.Now()
			if g.span.First.IsZero() {
				g.span.First = now
			}
			if !g.span.Last.IsZero() && now.Sub(g.span.Last) > 3*time.Second {
				g.span.Gaps++
			}
			g.span.Last = now
			g.span.Bytes += int64(n)
			block := make([]byte, n)
			copy(block, buf[:n])
			g.raw = append(g.raw, block)
			g.rawTimes = append(g.rawTimes, now)
			g.total += int64(n)
			seconds := g.input.StreamBufferSeconds
			limit := min(int64(128<<20), max(int64(1<<20), int64(seconds)*4<<20))
			for len(g.raw) > 1 && (len(g.raw) > maxRawBlocks || g.total-g.base > limit || seconds > 0 && now.Sub(g.rawTimes[0]) > time.Duration(seconds)*time.Second) {
				g.base += int64(len(g.raw[0]))
				g.raw[0] = nil
				g.rawTimes[0] = time.Time{}
				g.raw = g.raw[1:]
				g.rawTimes = g.rawTimes[1:]
			}
		}
		g.signal()
		g.mu.Unlock()
		if e != nil {
			if g.ctx.Err() != nil {
				return
			}
			g.mu.Lock()
			for b := range g.bodies {
				_ = b.Close()
				delete(g.bodies, b)
			}
			for c := range g.clients {
				c.Close()
				delete(g.clients, c)
			}
			g.span.Gaps++
			g.span.Reconnecting = true
			g.mu.Unlock()
			_, _, next, err := g.openRetry(true)
			if g.ctx.Err() != nil {
				return
			}
			recovered := err == nil
			if recovered {
				r = next
			}
			if !recovered {
				g.mu.Lock()
				g.done = true
				g.span.Ended = true
				g.failure = err
				g.signal()
				g.mu.Unlock()
				return
			}
		}
	}
}
func (g *Gateway) fetch(ctx context.Context, locator, rng string) (*remotemedia.Client, *http.Response, error) {
	c, e := remotemedia.New(ctx, locator, g.policy)
	if e != nil {
		return nil, nil, ErrUnavailable
	}
	g.mu.Lock()
	if g.ctx.Err() != nil {
		g.mu.Unlock()
		c.Close()
		return nil, nil, ErrUnavailable
	}
	g.clients[c] = true
	g.mu.Unlock()
	r, e := c.Open(ctx, http.MethodGet, rng)
	if e != nil {
		g.mu.Lock()
		delete(g.clients, c)
		g.mu.Unlock()
		c.Close()
		return nil, nil, ErrUnavailable
	}
	if r.StatusCode != 200 && r.StatusCode != 206 || (r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity") {
		var refused error = ErrFormat
		advice := retryAfter(r.Header.Get("Retry-After"), g.retryWindow())
		switch r.StatusCode {
		case http.StatusRequestTimeout, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			refused = &retryableInput{after: advice}
		case http.StatusTooManyRequests:
			// A rate refusal is replayed only after an actual bounded supplier hint.
			if advice > 0 {
				refused = &retryableInput{after: advice}
			}
		}
		r.Body.Close()
		c.Close()
		g.mu.Lock()
		delete(g.clients, c)
		g.mu.Unlock()
		return nil, nil, refused
	}
	g.mu.Lock()
	g.bodies[r.Body] = true
	g.mu.Unlock()
	return c, r, nil
}
func (g *Gateway) untrack(c *remotemedia.Client, b io.ReadCloser) {
	g.mu.Lock()
	delete(g.clients, c)
	delete(g.bodies, b)
	g.mu.Unlock()
}
func (g *Gateway) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	if g.closing {
		g.mu.Unlock()
		http.Error(w, "retired", 410)
		return
	}
	g.wg.Add(1)
	out := g.output
	g.mu.Unlock()
	defer g.wg.Done()
	if strings.HasPrefix(r.URL.Path, "/output/") {
		if out == nil {
			http.NotFound(w, r)
			return
		}
		out.ServeHTTP(w, r)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != "GET" || r.URL.RawQuery != "" || r.URL.RawPath != "" || r.URL.Fragment != "" || len(r.URL.Path) < 71 || len(r.URL.Path) > 80 || !strings.HasPrefix(r.URL.Path, "/input/") {
		http.Error(w, "Unavailable", 404)
		return
	}
	// "/input/<64 hex>" for a raw stream; "/input/<64 hex>.<ext>" for an HLS route.
	id, ext, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/input/"), ".")
	if len(id) != 64 {
		http.Error(w, "Unavailable", 404)
		return
	}
	g.mu.Lock()
	if g.active >= 16 {
		g.mu.Unlock()
		http.Error(w, "Busy", 503)
		return
	}
	g.active++
	g.mu.Unlock()
	defer func() { g.mu.Lock(); g.active--; g.mu.Unlock() }()
	if !g.hls {
		if id != g.secret || ext != "" || r.Header.Get("Range") != "" {
			http.Error(w, "Unavailable", 404)
			return
		}
		w.Header().Set("Content-Type", "video/mp2t")
		if g.shared != nil {
			g.shared.serveRawFor(w, r, g)
		} else {
			g.serveRawFor(w, r, g)
		}
		return
	}
	g.mu.Lock()
	res, ok := g.resources[id]
	ok = ok && ext == res.ext
	first := []byte(nil)
	if id == g.secret && g.first != nil {
		first = g.first
		g.first = nil
	}
	g.mu.Unlock()
	if !ok {
		http.Error(w, "Unavailable", 404)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	var data []byte
	location := res.url
	contentRange := ""
	if first != nil {
		data = first
	} else {
		// A manifest is rewritten (every URI becomes a longer gateway route) and a key
		// is checked whole, so neither is ever fetched or served as a byte range: a
		// forwarded Range would put the original's Content-Range on the rewritten
		// body, and the client would read a manifest cut mid-URI (FFmpeg 7.1+ refuses
		// the truncated segment URI as "not in allowed_segment_extensions"; older
		// builds silently dropped it). Only media bytes pass a Range through.
		rng := r.Header.Get("Range")
		if res.kind != "media" {
			rng = ""
		}
		owner := g
		if g.shared != nil {
			owner = g.shared
		}
		fetched, e := owner.resourceBytes(ctx, res, rng)
		if e != nil {
			g.mu.Lock()
			g.span.Gaps++
			g.mu.Unlock()
			http.Error(w, "Source unavailable", 502)
			return
		}
		data, location, contentRange = fetched.data, fetched.location, fetched.contentRange
	}
	if res.kind == "manifest" {
		rewritten, e := g.rewrite(data, location, res.depth)
		if e != nil {
			http.Error(w, "Invalid source", 502)
			return
		}
		data = rewritten
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	} else if res.kind == "key" {
		if len(data) != 16 {
			http.Error(w, "Invalid key", 502)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		g.mu.Lock()
		now := time.Now()
		if g.span.First.IsZero() {
			g.span.First = now
		}
		if !g.span.Last.IsZero() && now.Sub(g.span.Last) > 15*time.Second {
			g.span.Gaps++
		}
		g.span.Last = now
		g.span.Bytes += int64(len(data))
		g.mu.Unlock()
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if contentRange != "" {
		w.Header().Set("Content-Range", contentRange)
		w.WriteHeader(206)
	}
	_, _ = w.Write(data)
}
func (g *Gateway) serveRawFor(w http.ResponseWriter, r *http.Request, reader *Gateway) {
	g.mu.Lock()
	// A newly tuned reader starts at the current stream, rather than replaying
	// another viewer's retained input history and misreporting capture coverage.
	offset := g.total
	if len(g.raw) > 0 {
		offset -= int64(len(g.raw[len(g.raw)-1]))
	}
	g.mu.Unlock()
	for {
		g.mu.Lock()
		if offset < g.base {
			g.mu.Unlock()
			return
		}
		at := g.base
		var data []byte
		for _, b := range g.raw {
			if offset < at+int64(len(b)) {
				data = b[offset-at:]
				break
			}
			at += int64(len(b))
		}
		done, changed := g.done, g.changed
		g.mu.Unlock()
		if len(data) > 0 {
			n, e := w.Write(data)
			gaps := g.Snapshot().Gaps
			reader.mu.Lock()
			now := time.Now()
			if reader.span.First.IsZero() {
				reader.span.First = now
			}
			reader.span.Last = now
			reader.span.Bytes += int64(n)
			reader.span.Gaps = max(0, gaps-reader.gapBaseline)
			reader.mu.Unlock()
			offset += int64(n)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			if e != nil {
				return
			}
			continue
		}
		if done {
			return
		}
		select {
		case <-g.ctx.Done():
			return
		case <-r.Context().Done():
			return
		case <-changed:
		}
	}
}

// StopInput closes upstream readers at a scheduled padded boundary. EOF is a
// normal locally chosen recording boundary; it is not evidence of source EOF.
func (g *Gateway) StopInput() {
	g.cancel()
	g.mu.Lock()
	bodies := []io.ReadCloser{}
	for b := range g.bodies {
		bodies = append(bodies, b)
	}
	g.done = true
	g.signal()
	g.mu.Unlock()
	for _, b := range bodies {
		_ = b.Close()
	}
}
func (g *Gateway) Close() error {
	g.closeOnce.Do(g.close)
	return nil
}
func (g *Gateway) close() {
	g.mu.Lock()
	g.closing = true
	g.mu.Unlock()
	g.StopInput()
	if g.http != nil {
		_ = g.http.Close()
	}
	if g.v4 != nil {
		_ = g.v4.Close()
	}
	if g.v6 != nil {
		_ = g.v6.Close()
	}
	g.mu.Lock()
	clients := []*remotemedia.Client{}
	for c := range g.clients {
		clients = append(clients, c)
	}
	g.mu.Unlock()
	for _, c := range clients {
		c.Close()
	}
	g.wg.Wait()
	if g.release != nil {
		g.release()
	}
}
func (g *Gateway) child(raw, base, kind string, depth int) (string, error) {
	if depth > 6 || raw == "" || len(raw) > 8192 || strings.ContainsAny(raw, "\x00\r\n") {
		return "", ErrFormat
	}
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.Fragment != "" {
		return "", ErrFormat
	}
	b, e := url.Parse(base)
	if e != nil {
		return "", ErrFormat
	}
	u = b.ResolveReference(u)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", ErrFormat
	}
	locator := u.String()
	key := kind + "\n" + locator
	g.mu.Lock()
	defer g.mu.Unlock()
	if old := g.reverse[key]; old != "" {
		return g.address + "/input/" + old + "." + g.resources[old].ext, nil
	}
	// A bounded manifest may churn over time. Do not evict a route that an active
	// decoder could still hold. Exhaustion fails explicitly instead of bypassing
	// validation or exposing upstream URIs. A later gateway incarnation can reset.
	if len(g.resources) >= 16384 {
		return "", ErrFormat
	}
	id, e := token()
	if e != nil {
		return "", e
	}
	ext := routeExtension(kind, u)
	g.resources[id] = resource{locator, depth, kind, ext}
	g.reverse[key] = id
	return g.address + "/input/" + id + "." + ext, nil
}

func (g *Gateway) AttachOutput(handler http.Handler) (string, error) {
	out, e := privateoutput.New(handler)
	if e != nil {
		return "", e
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closing || g.output != nil || g.ctx.Err() != nil {
		return "", ErrUnavailable
	}
	g.output = out
	return g.address + out.Prefix, nil
}
