package remote

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// ErrSignIn says the source refused its user name and password, or its key: its page says
// "Needs its sign-in again".
var ErrSignIn = errors.New("the remote source refused its sign-in")

// ErrNoRanges says the source answers a request for part of a file with the whole file, so
// Portico cannot read it the way it reads remote files.
var ErrNoRanges = errors.New("the remote source cannot send part of a file")

// WebDAV is a folder on a WebDAV server: a NAS, a debrid service, rclone serving a remote.
type WebDAV struct {
	base           *url.URL // the address the owner gave, its path ending in a slash
	user, password string
	client         *http.Client
}

// NewWebDAV reaches the WebDAV address with a user name and password, either of which may
// be empty. Over http:// the password travels as it is typed, which is what a WebDAV
// server on a home network expects; the owner chooses the address.
func NewWebDAV(address, user, password string) (*WebDAV, error) {
	base, err := url.Parse(strings.TrimSpace(address))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("a WebDAV address starts with http:// or https:// and has no user name, query or # in it")
	}
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	base.RawPath = ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	client := &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		// A debrid service sends a file's bytes from another host. The password never goes
		// to it (Go drops it when the host changes), and never over http after https.
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" && via[0].URL.Scheme == "https" {
			return errors.New("the WebDAV server redirected from https to http")
		}
		return nil
	}}
	return &WebDAV{base: base, user: user, password: password, client: client}, nil
}

// at is the address of a path within the source. A path is relative to the address the
// owner gave and cannot climb out of it.
func (w *WebDAV) at(p string, dir bool) (*url.URL, error) {
	p = strings.Trim(p, "/")
	if p != "" && (path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") || strings.ContainsRune(p, 0)) {
		return nil, fmt.Errorf("%q is not a path inside the source", p)
	}
	u := *w.base
	u.Path += p
	if dir && p != "" {
		u.Path += "/"
	}
	return &u, nil
}

func (w *WebDAV) request(ctx context.Context, method string, u *url.URL, body string, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if w.user != "" || w.password != "" {
		req.SetBasicAuth(w.user, w.password)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	if err := statusError(resp); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

// statusError turns an answer that is not a success into an error the rest of the server
// acts on: a slow-down, a refused sign-in, or a plain failure.
func statusError(resp *http.Response) error {
	switch code := resp.StatusCode; {
	case code < 300:
		return nil
	case code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable && resp.Header.Get("Retry-After") != "":
		return &SlowDown{After: retryAfter(resp.Header.Get("Retry-After"))}
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return ErrSignIn
	default:
		return fmt.Errorf("the remote source answered %s", resp.Status)
	}
}

// retryAfter reads a Retry-After header, in seconds or as a date, or 0 when it names no time.
func retryAfter(v string) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		return max(time.Until(at), 0)
	}
	return 0
}

const propfind = `<?xml version="1.0" encoding="utf-8"?><d:propfind xmlns:d="DAV:"><d:prop>` +
	`<d:resourcetype/><d:getcontentlength/><d:getlastmodified/></d:prop></d:propfind>`

// maxListing bounds one folder's listing: about 100,000 files.
const maxListing = 64 << 20

type multistatus struct {
	Responses []struct {
		Href      string `xml:"DAV: href"`
		Propstats []struct {
			Status string `xml:"DAV: status"`
			Prop   struct {
				Collection *struct{} `xml:"DAV: resourcetype>collection"`
				Length     string    `xml:"DAV: getcontentlength"`
				Modified   string    `xml:"DAV: getlastmodified"`
			} `xml:"DAV: prop"`
		} `xml:"DAV: propstat"`
	} `xml:"DAV: response"`
}

// List asks for one folder with one PROPFIND of depth 1.
func (w *WebDAV) List(ctx context.Context, dir string) ([]Entry, error) {
	u, err := w.at(dir, true)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := w.request(ctx, "PROPFIND", u, propfind, http.Header{"Depth": {"1"}, "Content-Type": {"application/xml; charset=utf-8"}})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, fmt.Errorf("the WebDAV server answered a folder listing with %s", resp.Status)
	}
	limited := &io.LimitedReader{R: resp.Body, N: maxListing + 1}
	var listing multistatus
	if err := xml.NewDecoder(limited).Decode(&listing); err != nil || limited.N <= 0 {
		return nil, errors.New("the WebDAV server's folder listing could not be read")
	}
	folder := strings.TrimSuffix(u.Path, "/")
	out := make([]Entry, 0, len(listing.Responses))
	for _, r := range listing.Responses {
		href, err := url.Parse(r.Href)
		if err != nil {
			return nil, errors.New("the WebDAV server's folder listing could not be read")
		}
		// An href is a path on the server, or a whole address on it.
		at := strings.TrimSuffix(href.Path, "/")
		if at == folder {
			continue
		}
		parent, name := path.Split(at)
		if strings.TrimSuffix(parent, "/") != folder {
			// Not something in this folder: the listing is not the one asked for.
			return nil, errors.New("the WebDAV server listed something outside the folder")
		}
		if name == "" || name == "." || name == ".." || strings.ContainsRune(name, 0) {
			continue
		}
		entry, found := Entry{Name: name}, false
		for _, p := range r.Propstats {
			if !strings.Contains(p.Status, " 200") {
				continue
			}
			found = true
			entry.Dir = entry.Dir || p.Prop.Collection != nil
			if n, err := strconv.ParseInt(strings.TrimSpace(p.Prop.Length), 10, 64); err == nil && n >= 0 {
				entry.Size = n
			}
			if t, err := http.ParseTime(strings.TrimSpace(p.Prop.Modified)); err == nil {
				entry.ModifiedNS = t.UnixNano()
			}
		}
		if found {
			out = append(out, entry)
		}
	}
	return out, nil
}

// Open learns the file's size by asking for its first byte, which also shows the server
// sends parts of files.
func (w *WebDAV) Open(ctx context.Context, p string) (File, error) {
	u, err := w.at(p, false)
	if err != nil {
		return nil, err
	}
	resp, err := w.request(ctx, http.MethodGet, u, "", http.Header{"Range": {"bytes=0-0"}})
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	size, ok := rangeTotal(resp)
	if !ok {
		return nil, ErrNoRanges
	}
	return &davFile{w: w, u: u, size: size, ctx: ctx}, nil
}

// rangeTotal reads the whole size from a 206 answer's Content-Range.
func rangeTotal(resp *http.Response) (int64, bool) {
	if resp.StatusCode != http.StatusPartialContent {
		return 0, false
	}
	_, total, ok := strings.Cut(resp.Header.Get("Content-Range"), "/")
	n, err := strconv.ParseInt(total, 10, 64)
	return n, ok && err == nil && n >= 0
}

// davFile is an open file. Like a throttled file it lives as long as the work that opened
// it, so its reads end with that work.
type davFile struct {
	w    *WebDAV
	u    *url.URL
	size int64
	ctx  context.Context
}

func (f *davFile) Size() int64  { return f.size }
func (f *davFile) Close() error { return nil }

// ReadAt is one ranged GET.
func (f *davFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if off >= f.size || len(p) == 0 {
		return 0, io.EOF
	}
	want := min(int64(len(p)), f.size-off)
	body, err := f.get(off, want)
	if err != nil {
		return 0, err
	}
	defer body.Close()
	n, err := io.ReadFull(body, p[:want])
	if err == nil && want < int64(len(p)) {
		err = io.EOF
	}
	return n, err
}

// Stream is one GET for everything from off.
func (f *davFile) Stream(off int64) (io.ReadCloser, error) {
	if off >= f.size {
		return io.NopCloser(strings.NewReader("")), nil
	}
	return f.get(off, 0)
}

// get opens length bytes from off, or everything from off when length is 0. It is one
// request; the caller reads and closes the body.
func (f *davFile) get(off, length int64) (io.ReadCloser, error) {
	spec := fmt.Sprintf("bytes=%d-", off)
	if length > 0 {
		spec += strconv.FormatInt(off+length-1, 10)
	}
	resp, err := f.w.request(f.ctx, http.MethodGet, f.u, "", http.Header{"Range": {spec}})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusPartialContent || !strings.HasPrefix(resp.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-", off)) {
		resp.Body.Close()
		return nil, ErrNoRanges
	}
	return resp.Body, nil
}
