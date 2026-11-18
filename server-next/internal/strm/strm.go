// Package strm reads what a `.strm` file names, for a library the owner switched `.strm`
// files on for. The server, not the app, fetches the target, on the home network or the
// internet (Justin, 18 Nov 2026); only this reader fetches it, and decoders receive bytes
// through inputbridge, never these URLs. Apps are never given the address.
package strm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const PointerLimit = 8 << 10
const ProbeLimit = 16 << 20

const chunkSize = 8 << 20

// ReadTimeout bounds a probe range or an active stream read, never a player pause.
// Tests shorten this fixed production limit.
var ReadTimeout = 30 * time.Second

// These fixed errors are safe for an owner's problem line or a log. Never wrap a
// transport/decoder error: it may include a URL, redirect, token or response text.
var (
	ErrPointer     = errors.New("not a single HTTP or HTTPS URL")
	ErrRefused     = errors.New("refused redirect")
	ErrUnavailable = errors.New("not reachable")
	ErrRedirects   = errors.New("too many redirects")
	ErrRange       = errors.New("not seekable")
	ErrLimit       = errors.New("target read limit exceeded")
	ErrMedia       = errors.New("not a media file")
)

func Parse(raw []byte) (string, error) {
	if len(raw) > PointerLimit || !utf8.Valid(raw) {
		return "", ErrPointer
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(raw), "\ufeff"))
	if strings.ContainsAny(text, "\r\n\x00") {
		return "", ErrPointer
	}
	u, err := url.Parse(text)
	if err != nil || !allowedURL(u) {
		return "", ErrPointer
	}
	return u.String(), nil
}

// A name and password in the address (a NAS that asks for one) are sent as ffmpeg would.
func allowedURL(u *url.URL) bool {
	return u != nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.Fragment == "" && !strings.Contains(u.Hostname(), "%")
}

// Client has no proxy, cookies or shared credentials.
type Client struct {
	http *http.Client
}

func New() *Client {
	// A bounded chunk has its own deadline; connections are not kept between chunks.
	tr := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, DisableKeepAlives: true,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second,
		MaxResponseHeaderBytes: 64 << 10, DisableCompression: true}
	return &Client{http: &http.Client{Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return ErrRedirects
		}
		// An https address never goes on over plain http, where its name and password,
		// if it has them, would travel in the clear.
		if !allowedURL(req.URL) || via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return ErrRefused
		}
		req.Header.Del("Referer")
		return nil
	}}}
}

// File holds one finite target, fetched by checked byte ranges. Size comes from
// validated Content-Range, not Content-Type or Content-Length. A probe still
// decides whether those bytes are media.
type File struct {
	client  *Client
	ctx     context.Context
	cancel  context.CancelFunc
	address string
	size    int64
	etag    string
	mu      sync.Mutex
	budget  int64 // -1 for playback
}

// Target is what a .strm names, as the server reads it: a File, or a Playlist.
type Target interface {
	io.ReaderAt
	io.Closer
	Size() int64
	LimitProbe()
}

func (c *Client) Open(ctx context.Context, address string) (Target, error) {
	u, err := url.Parse(address)
	if err != nil || !allowedURL(u) {
		return nil, ErrPointer
	}
	held, cancel := context.WithCancel(ctx)
	f := &File{client: c, ctx: held, cancel: cancel, address: address, budget: -1}
	resp, done, err := f.request(0, 0, false)
	if errors.Is(err, ErrRange) {
		// A playlist is often written as it is asked for, with no byte ranges.
		cancel()
		return c.openPlaylist(ctx, address)
	}
	if err != nil {
		cancel()
		return nil, err
	}
	defer done()
	defer resp.Body.Close()
	f.size, err = contentRange(resp.Header.Get("Content-Range"), 0, 0, 0)
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 2))
	if err != nil || readErr != nil || len(raw) != 1 {
		cancel()
		return nil, ErrRange
	}
	if tag := resp.Header.Get("ETag"); tag != "" && !strings.HasPrefix(tag, "W/") {
		f.etag = tag
	}
	prefix := make([]byte, min(f.size, 512))
	if _, err := f.ReadAt(prefix, 0); err != nil {
		cancel()
		return nil, ErrUnavailable
	}
	prefix = bytes.TrimSpace(bytes.TrimPrefix(prefix, []byte("\xef\xbb\xbf")))
	if bytes.HasPrefix(prefix, []byte("#EXTM3U")) {
		cancel()
		return c.openPlaylist(ctx, address)
	}
	if bytes.HasPrefix(prefix, []byte("<MPD")) || bytes.HasPrefix(prefix, []byte("<?xml")) && bytes.Contains(prefix, []byte("<MPD")) {
		cancel()
		return nil, ErrRange
	}
	return f, nil
}

func (f *File) Size() int64  { return f.size }
func (f *File) Close() error { f.cancel(); return nil }
func (f *File) LimitProbe()  { f.mu.Lock(); f.budget = ProbeLimit - 1 - min(f.size, 512); f.mu.Unlock() }

func (f *File) request(start, end int64, streaming bool) (*http.Response, context.CancelFunc, error) {
	var ctx context.Context
	var cancel context.CancelFunc
	if streaming {
		ctx, cancel = context.WithCancel(f.ctx)
	} else {
		ctx, cancel = context.WithTimeout(f.ctx, ReadTimeout)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", f.address, nil)
	if err != nil {
		cancel()
		return nil, nil, ErrPointer
	}
	req.Header.Set("Range", "bytes="+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10))
	if f.etag != "" {
		req.Header.Set("If-Match", f.etag)
	}
	resp, err := f.client.do(req)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Encoding") != "" {
		resp.Body.Close()
		cancel()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			return nil, nil, ErrUnavailable
		}
		return nil, nil, ErrRange
	}
	if f.size > 0 {
		if _, err = contentRange(resp.Header.Get("Content-Range"), start, end, f.size); err != nil {
			resp.Body.Close()
			cancel()
			return nil, nil, ErrRange
		}
		if f.etag != "" && resp.Header.Get("ETag") != f.etag {
			resp.Body.Close()
			cancel()
			return nil, nil, ErrUnavailable
		}
	}
	return resp, cancel, nil
}

// do sends a request and answers a failure with one of the fixed errors.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		for _, safe := range []error{ErrRefused, ErrRedirects} {
			if errors.Is(err, safe) {
				return nil, safe
			}
		}
		return nil, ErrUnavailable
	}
	return resp, nil
}

func contentRange(value string, start, end, size int64) (int64, error) {
	prefix := "bytes " + strconv.FormatInt(start, 10) + "-" + strconv.FormatInt(end, 10) + "/"
	text, ok := strings.CutPrefix(value, prefix)
	total, err := strconv.ParseInt(text, 10, 64)
	if !ok || err != nil || total <= end || size > 0 && total != size {
		return 0, ErrRange
	}
	return total, nil
}

func (f *File) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if off < 0 {
		return 0, ErrRange
	}
	if off >= f.size {
		return 0, io.EOF
	}
	want := min(int64(len(p)), f.size-off)
	f.mu.Lock()
	if f.budget >= 0 {
		if want > f.budget {
			f.mu.Unlock()
			return 0, ErrLimit
		}
		f.budget -= want
	}
	f.mu.Unlock()
	resp, done, err := f.request(off, off+want-1, false)
	if err != nil {
		return 0, err
	}
	defer done()
	defer resp.Body.Close()
	n, err := io.ReadFull(resp.Body, p[:want])
	if err != nil {
		return n, ErrUnavailable
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// Stream times only active reads from the source: a paused player does not
// spend its timeout. The remote reader requests the next bounded chunk at EOF.
func (f *File) Stream(off int64) (io.ReadCloser, error) {
	if off < 0 || off >= f.size {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	end := off + min(int64(chunkSize), f.size-off) - 1
	resp, cancel, err := f.request(off, end, true)
	if err != nil {
		return nil, err
	}
	return &body{Reader: io.LimitReader(resp.Body, end-off+1), remaining: end - off + 1, cancel: cancel, close: func() error { cancel(); return resp.Body.Close() }}, nil
}

type body struct {
	cancel    context.CancelFunc
	remaining int64
	io.Reader
	close func() error
}

func (b *body) Read(p []byte) (int, error) {
	timer := time.AfterFunc(ReadTimeout, b.cancel)
	defer timer.Stop()
	n, err := b.Reader.Read(p)
	b.remaining -= int64(n)
	if err != nil && (err != io.EOF || b.remaining > 0) {
		err = ErrUnavailable
	}
	return n, err
}

func (b *body) Close() error { return b.close() }

// Pointer reads only the small regular file already opened by its library source.
func Pointer(file io.ReaderAt, size int64) (string, error) {
	if size <= 0 || size > PointerLimit {
		return "", ErrPointer
	}
	raw := make([]byte, size)
	n, err := file.ReadAt(raw, 0)
	if err != nil && !(err == io.EOF && n == len(raw)) {
		return "", ErrUnavailable
	}
	return Parse(raw)
}
