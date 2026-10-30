package remotemedia

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"strings"
	"sync/atomic"
	"testing"
)

func davParser() *DAV { return &DAV{config: DAVConfig{Root: "https://dav.example/media/"}} }
func davObject(href string) string {
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop><d:resourcetype/><d:getcontentlength>10</d:getcontentlength><d:getetag>"v1"</d:getetag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}
func davXML(body string) string { return `<d:multistatus xmlns:d="DAV:">` + body + `</d:multistatus>` }
func TestDAVHrefConfinementBeforeNormalization(t *testing.T) {
	d := davParser()
	for _, raw := range []string{"/media/a.mp4", "https://dav.example/media/a.mp4", "a.mp4"} {
		got, e := d.Href(raw, "")
		if e != nil || got != "a.mp4" {
			t.Fatal(raw, got, e)
		}
	}
	for _, raw := range []string{"/media/../media/a.mp4", "/media/%2e%2e/media/a.mp4", "//other.example/media/a.mp4", "/media/a%2fb.mp4", "/media/%252e%252e/a.mp4", "/outside/a.mp4", "/media/a.mp4?secret=x", "/media/a.mp4#fragment", "/media/sub/a.mp4", ""} {
		if _, e := d.Href(raw, ""); e == nil {
			t.Fatalf("accepted unsafe href %q", raw)
		}
	}
	got, e := d.href("a.mp4", "a.mp4", true)
	if e != nil || got != "a.mp4" {
		t.Fatal("relative Depth:0", got, e)
	}
}
func TestDAVBoundedXMLAndContinuation(t *testing.T) {
	d := davParser()
	p, e := d.parse(strings.NewReader(davXML(davObject("/media/a.mp4"))), "", false)
	if e != nil || len(p.Entries) != 1 || p.Entries[0].Size != 10 || p.Entries[0].ETag != `"v1"` {
		t.Fatal(p, e)
	}
	partial := davXML(davObject("/media/a.mp4") + `<d:response><d:href>/media/</d:href><d:status>HTTP/1.1 507 Insufficient Storage</d:status><d:error><d:number-of-matches-within-limits/></d:error></d:response><d:sync-token>opaque-next</d:sync-token>`)
	p, e = d.parse(strings.NewReader(partial), "", true)
	if e != nil || !p.More || p.Token != "opaque-next" {
		t.Fatal(p, e)
	}
	bad := []string{`<!DOCTYPE a [<!ENTITY x "unsafe">]>` + davXML(davObject("/media/a.mp4")), davXML(davObject("/media/a.mp4") + davObject("a.mp4")), "junk" + davXML(""), davXML("") + "junk", davXML(strings.Repeat("<x>", 30) + strings.Repeat("</x>", 30)), davXML("<x>" + strings.Repeat("a", 4<<20) + "</x>")}
	for i, raw := range bad {
		if _, e = d.parse(strings.NewReader(raw), "", false); e == nil {
			t.Fatalf("accepted malformed/budget case %d", i)
		}
	}
	deleted := davXML(`<d:response><d:href>/media/gone.mp4</d:href><d:status>HTTP/1.1 404 Not Found</d:status></d:response><d:sync-token>after-delete</d:sync-token>`)
	p, e = d.parse(strings.NewReader(deleted), "", true)
	if e != nil || len(p.Entries) != 1 || !p.Entries[0].Deleted {
		t.Fatal(p, e)
	}
	if _, e = d.parse(strings.NewReader(deleted), "", false); e == nil {
		t.Fatal("Depth:1 404 was treated as authoritative absence")
	}
}
func TestDAVAuthenticatedConditionalRangesAndRotation(t *testing.T) {
	var version atomic.Value
	version.Store(`"v1"`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "user" || p != "private-password" {
			w.WriteHeader(401)
			return
		}
		if r.Method == "PROPFIND" {
			if r.Header.Get("Depth") != "0" {
				t.Error("depth")
			}
			w.WriteHeader(207)
			io.WriteString(w, davXML(davObject("/media/movie.mp4")))
			return
		}
		etag := version.Load().(string)
		w.Header().Set("ETag", etag)
		if match := r.Header.Get("If-Match"); match != "" && match != etag {
			w.WriteHeader(412)
			return
		}
		if r.Method == "HEAD" {
			w.Header().Set("Content-Length", "10")
			return
		}
		var first, last int
		if _, e := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &first, &last); e != nil || first < 0 || last > 9 || first > last {
			w.WriteHeader(416)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/10", first, last))
		w.Header().Set("Content-Length", fmt.Sprint(last-first+1))
		w.WriteHeader(206)
		io.WriteString(w, "0123456789"[first:last+1])
	}))
	defer server.Close()
	ctx := context.Background()
	if _, e := ValidateDAVConfig(ctx, DAVConfig{Root: server.URL + "/media/"}, nil); !errors.Is(e, ErrPolicy) {
		t.Fatal("HTTP requires explicit approval", e)
	}
	cfg, e := ValidateDAVConfig(ctx, DAVConfig{Root: server.URL + "/media/", Username: "user", Password: "private-password", InsecureLocal: true}, nil)
	if e != nil {
		t.Fatal(e)
	}
	d, e := NewDAV(cfg, nil)
	if e != nil {
		t.Fatal(e)
	}
	entry, e := d.Stat(ctx, "movie.mp4")
	if e != nil || entry.Size != 10 {
		t.Fatal(entry, e)
	}
	v, e := d.OpenVersion(ctx, "movie.mp4", "root", "object")
	if e != nil {
		t.Fatal(e)
	}
	defer v.Close()
	response, e := v.Open(ctx, "GET", "bytes=2-4")
	if e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(response.Body)
	response.Body.Close()
	if e != nil || string(raw) != "234" {
		t.Fatal(string(raw), e)
	}
	version.Store(`"v2"`)
	if _, e = v.Open(ctx, "GET", "bytes=2-4"); !errors.Is(e, mediasource.ErrSourceChanged) {
		t.Fatal("changed ETag", e)
	}
}
