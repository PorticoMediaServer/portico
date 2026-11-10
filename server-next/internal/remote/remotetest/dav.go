// Package remotetest is a small WebDAV server for tests: an in-memory tree of files behind a
// user name and password, which counts what it is asked and can say "slow down".
package remotetest

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// DAV is a WebDAV server holding files by path (forward slashes, no leading slash) under
// /dav/. Folders are the ones the paths imply.
type DAV struct {
	URL            string // the address to give a source: http://127.0.0.1:port/dav/
	User, Password string
	// Listings and Reads count the PROPFIND and GET requests answered.
	Listings, Reads atomic.Int64
	// SlowDowns is how many of the next requests are answered 429 with Retry-After: 1.
	SlowDowns atomic.Int64
	// NoRanges makes GET ignore Range and send the whole file.
	NoRanges atomic.Bool

	mu       sync.Mutex
	files    map[string][]byte
	modified time.Time
}

// NewDAV starts a server for the test with the given files.
func NewDAV(t testing.TB, files map[string]string) *DAV {
	d := &DAV{User: "viewer", Password: "pw-" + fmt.Sprint(time.Now().UnixNano()), files: map[string][]byte{}, modified: time.Unix(1700000000, 0).UTC()}
	for name, body := range files {
		d.files[name] = []byte(body)
	}
	srv := httptest.NewServer(d)
	t.Cleanup(srv.Close)
	d.URL = srv.URL + "/dav/"
	return d
}

// Put adds or replaces a file; Remove takes one away.
func (d *DAV) Put(name, body string) { d.mu.Lock(); d.files[name] = []byte(body); d.mu.Unlock() }
func (d *DAV) Remove(name string)    { d.mu.Lock(); delete(d.files, name); d.mu.Unlock() }

func (d *DAV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if user, password, ok := r.BasicAuth(); !ok || user != d.User || password != d.Password {
		w.Header().Set("WWW-Authenticate", `Basic realm="dav"`)
		http.Error(w, "sign in", http.StatusUnauthorized)
		return
	}
	if d.SlowDowns.Add(-1) >= 0 {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "slow down", http.StatusTooManyRequests)
		return
	}
	d.SlowDowns.Store(0)
	rel, ok := strings.CutPrefix(r.URL.Path, "/dav/")
	if !ok && r.URL.Path != "/dav" {
		http.NotFound(w, r)
		return
	}
	rel = strings.Trim(rel, "/")
	d.mu.Lock()
	defer d.mu.Unlock()
	switch r.Method {
	case "PROPFIND":
		d.Listings.Add(1)
		d.propfind(w, r, rel)
	case http.MethodGet, http.MethodHead:
		d.Reads.Add(1)
		body, ok := d.files[rel]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if d.NoRanges.Load() {
			r.Header.Del("Range")
		}
		http.ServeContent(w, r, "", d.modified, bytes.NewReader(body))
	default:
		http.Error(w, "not allowed", http.StatusMethodNotAllowed)
	}
}

func (d *DAV) propfind(w http.ResponseWriter, r *http.Request, rel string) {
	type child struct {
		dir  bool
		size int
	}
	children := map[string]child{}
	isDir := rel == ""
	for name, body := range d.files {
		if name == rel {
			children[""] = child{size: len(body)}
			continue
		}
		inside, ok := strings.CutPrefix(name, rel+"/")
		if rel == "" {
			inside, ok = name, true
		}
		if !ok {
			continue
		}
		isDir = true
		first, _, nested := strings.Cut(inside, "/")
		if nested {
			children[first] = child{dir: true}
		} else {
			children[first] = child{size: len(body)}
		}
	}
	if !isDir && len(children) == 0 {
		http.NotFound(w, r)
		return
	}
	var out strings.Builder
	out.WriteString(`<?xml version="1.0" encoding="utf-8"?><D:multistatus xmlns:D="DAV:">`)
	entry := func(p string, c child) {
		href := (&url.URL{Path: path.Join("/dav", p)}).EscapedPath()
		props := fmt.Sprintf(`<D:getcontentlength>%d</D:getcontentlength><D:resourcetype/>`, c.size)
		if c.dir {
			href += "/"
			props = `<D:resourcetype><D:collection/></D:resourcetype>`
		}
		fmt.Fprintf(&out, `<D:response><D:href>%s</D:href><D:propstat><D:prop>%s<D:getlastmodified>%s</D:getlastmodified></D:prop>`+
			`<D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`, href, props, d.modified.Format(http.TimeFormat))
	}
	if !isDir {
		entry(rel, children[""])
	} else {
		entry(rel, child{dir: true})
		if r.Header.Get("Depth") != "0" {
			names := make([]string, 0, len(children))
			for name := range children {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				entry(path.Join(rel, name), children[name])
			}
		}
	}
	out.WriteString(`</D:multistatus>`)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	w.Write([]byte(out.String()))
}
