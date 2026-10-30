package remotemedia

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureBinding struct{ resources map[string][2]string }

func (b fixtureBinding) ResolveLocator(_ context.Context, locator string) (string, string, error) {
	identity, ok := b.resources[locator]
	if !ok {
		return "", "", mediasource.ErrIdentityRequired
	}
	return identity[0], identity[1], nil
}
func fixtureLocatorBinding(root, scope, object string) LocatorBinding {
	resources := map[string][2]string{}
	for _, path := range []string{"movie?token=old", "movie?token=new", "good", "etag", "total", "missing", "ignored", "short-range", "garbage", "encoding", "precondition", "denied", "weak", "extra", "truncated", "redirect", "target", "escape", "changed", "a", "redirect-b"} {
		resources[root+"/media/"+path] = [2]string{scope, object}
	}
	resources[root+"/media/b"] = [2]string{scope, "other-object"}
	return fixtureBinding{resources: resources}
}

// Each fixture owns an ephemeral loopback listener and in-memory bytes, so
// independent test packages never contend for the same fixed media port.
func versionFixture(t *testing.T, handler http.HandlerFunc) (string, Policy) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("media fixture listener unavailable: %v", err)
	}
	s := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	done := make(chan error, 1)
	go func() { done <- s.Serve(l) }()
	t.Cleanup(func() { _ = s.Close(); <-done })
	root := "http://" + l.Addr().String()
	approval, err := (Policy{}).Approve(context.Background(), root+"/media/")
	if err != nil {
		t.Fatal(err)
	}
	return root, Policy{Approvals: []Approval{approval}, ReadTimeout: time.Second}
}

func fixtureRepresentation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("ETag", `"version-1"`)
	if v := r.Header.Get("If-Match"); v != "" && v != `"version-1"` {
		w.WriteHeader(412)
		return
	}
	data := "0123456789"
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", "10")
		w.WriteHeader(200)
		return
	}
	if r.Header.Get("Range") == "" {
		w.Header().Set("Content-Length", "10")
		_, _ = io.WriteString(w, data)
		return
	}
	// Test oracle has explicit requests and expected byte spans, independent of
	// the production range parser and its clipping/suffix arithmetic.
	spans := map[string][2]int{"bytes=0-0": {0, 0}, "bytes=2-4": {2, 4}, "bytes=8-99": {8, 9}, "bytes=8-": {8, 9}, "bytes=-2": {8, 9}, "bytes=-99": {0, 9}}
	span, ok := spans[r.Header.Get("Range")]
	if !ok {
		w.Header().Set("Content-Range", "bytes */10")
		w.WriteHeader(416)
		return
	}
	w.Header().Set("Content-Range", "bytes "+strconv.Itoa(span[0])+"-"+strconv.Itoa(span[1])+"/10")
	w.Header().Set("Content-Length", strconv.Itoa(span[1]-span[0]+1))
	w.WriteHeader(206)
	_, _ = io.WriteString(w, data[span[0]:span[1]+1])
}

func TestVersionedDiscoveryConditionalReadsAndLocatorRenewal(t *testing.T) {
	var conditional atomic.Int32
	root, policy := versionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-Match") != "" {
			conditional.Add(1)
			if r.Header.Get("If-Match") != `"version-1"` {
				t.Error("incorrect conditional validator")
			}
		}
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("representation compression requested")
		}
		if r.Method == http.MethodHead && r.Header.Get("Range") != "" {
			t.Error("Range forwarded on HEAD")
		}
		fixtureRepresentation(w, r)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := DiscoverVersion(ctx, root+"/media/movie?token=old", policy, "root-id", "source-id", fixtureLocatorBinding(root, "root-id", "source-id"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if e := c.Version().Evidence(); e.Size != 10 || e.Revision != `"version-1"` || e.Object != "source-id" {
		t.Fatal("wrong source facts")
	}
	for _, tc := range []struct{ rangeValue, want string }{{"bytes=2-4", "234"}, {"bytes=8-99", "89"}, {"bytes=8-", "89"}, {"bytes=-2", "89"}, {"bytes=-99", "0123456789"}, {"", "0123456789"}} {
		resp, err := c.Open(ctx, "GET", tc.rangeValue)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(body) != tc.want {
			t.Fatalf("range %s got %q %v", tc.rangeValue, body, err)
		}
	}
	resp, err := c.Open(ctx, "HEAD", "bytes=2-4")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.ContentLength != 10 || resp.StatusCode != 200 {
		t.Fatal("HEAD did not describe full source")
	}
	renewed, err := c.RenewLocator(ctx, root+"/media/movie?token=new", policy)
	if err != nil {
		t.Fatal(err)
	}
	defer renewed.Close()
	if renewed.Version().ID() != c.Version().ID() {
		t.Fatal("URL renewal changed source identity")
	}
	if conditional.Load() != 8 {
		t.Fatalf("expected all eight post-discovery requests conditional, got %d", conditional.Load())
	}
	if _, err := c.RenewLocator(ctx, root+"/admin", policy); !errors.Is(err, ErrPolicy) {
		t.Fatal("renewal escaped approved root")
	}
	if _, err := c.RenewLocator(ctx, "http://169.254.169.254/latest", policy); !errors.Is(err, ErrPolicy) {
		t.Fatal("renewal reached metadata policy")
	}
}

func TestVersionedRejectsChangedOrMalformedRepresentation(t *testing.T) {
	root, policy := versionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/media/good" {
			fixtureRepresentation(w, r)
			return
		}
		w.Header().Set("ETag", `"version-1"`)
		w.Header().Set("Content-Range", "bytes 2-4/10")
		switch r.URL.Path {
		case "/media/etag":
			w.Header().Set("ETag", `"version-2"`)
		case "/media/total":
			w.Header().Set("Content-Range", "bytes 2-4/11")
		case "/media/missing":
			w.Header().Del("ETag")
		case "/media/ignored":
			w.Header().Del("Content-Range")
			w.WriteHeader(200)
			_, _ = io.WriteString(w, "0123456789")
			return
		case "/media/short-range":
			w.Header().Set("Content-Range", "bytes 2-2/10")
		case "/media/garbage":
			w.Header().Set("Content-Range", "bytes 2-4/10 garbage")
		case "/media/encoding":
			w.Header().Set("Content-Encoding", "gzip")
		case "/media/precondition":
			w.WriteHeader(412)
			return
		case "/media/denied":
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(206)
		_, _ = io.WriteString(w, "234")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	good, err := DiscoverVersion(ctx, root+"/media/good", policy, "root", "object", fixtureLocatorBinding(root, "root", "object"))
	if err != nil {
		t.Fatal(err)
	}
	defer good.Close()
	cases := []struct {
		path     string
		expected error
	}{{"etag", mediasource.ErrSourceChanged}, {"total", mediasource.ErrSourceChanged}, {"missing", mediasource.ErrSourceChanged}, {"ignored", ErrRepresentationRange}, {"short-range", ErrRepresentationRange}, {"garbage", ErrRepresentationRange}, {"encoding", ErrRepresentationRange}, {"precondition", mediasource.ErrSourceChanged}, {"denied", ErrDenied}}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			c, err := NewVersioned(ctx, root+"/media/"+tc.path, policy, good.Version(), good.binding)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if _, err := c.Open(ctx, "GET", "bytes=2-4"); !errors.Is(err, tc.expected) {
				t.Fatalf("got %v want %v", err, tc.expected)
			}
		})
	}
}

func TestVersionedDiscoveryRejectsUnstableIdentityAndBodySize(t *testing.T) {
	root, policy := versionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"version-1"`)
		w.Header().Set("Content-Range", "bytes 0-0/10")
		switch r.URL.Path {
		case "/media/weak":
			w.Header().Set("ETag", `W/"version-1"`)
		case "/media/missing":
			w.Header().Del("ETag")
		case "/media/ignored":
			w.Header().Del("Content-Range")
			w.WriteHeader(200)
			_, _ = io.WriteString(w, "0123456789")
			return
		case "/media/extra":
			w.WriteHeader(206)
			w.(http.Flusher).Flush()
			_, _ = io.WriteString(w, "01")
			return
		case "/media/truncated":
			w.Header().Set("Content-Length", "1")
			w.WriteHeader(206)
			return
		}
		w.WriteHeader(206)
		_, _ = io.WriteString(w, "0")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, path := range []string{"weak", "missing", "ignored", "extra", "truncated"} {
		if c, err := DiscoverVersion(ctx, root+"/media/"+path, policy, "root", "object", fixtureLocatorBinding(root, "root", "object")); err == nil {
			c.Close()
			t.Fatalf("unstable discovery accepted %s", path)
		}
	}
}

func TestVersionedDetectsTruncatedAndOverlongRangeBodies(t *testing.T) {
	root, policy := versionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/media/good" {
			fixtureRepresentation(w, r)
			return
		}
		w.Header().Set("ETag", `"version-1"`)
		w.Header().Set("Content-Range", "bytes 2-4/10")
		if r.URL.Path == "/media/truncated" {
			w.Header().Set("Content-Length", "3")
			w.WriteHeader(206)
			_, _ = io.WriteString(w, "2")
			return
		}
		w.WriteHeader(206)
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "2345")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	good, err := DiscoverVersion(ctx, root+"/media/good", policy, "root", "object", fixtureLocatorBinding(root, "root", "object"))
	if err != nil {
		t.Fatal(err)
	}
	defer good.Close()
	for _, tc := range []struct {
		path string
		want error
	}{{"truncated", io.ErrUnexpectedEOF}, {"extra", ErrRepresentationRange}} {
		c, err := NewVersioned(ctx, root+"/media/"+tc.path, policy, good.Version(), good.binding)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := c.Open(ctx, "GET", "bytes=2-4")
		if err != nil {
			c.Close()
			t.Fatal(err)
		}
		_, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		c.Close()
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.path, err, tc.want)
		}
	}
}

func TestVersionedRangeValidationAndUnsatisfiedRequest(t *testing.T) {
	root, policy := versionFixture(t, fixtureRepresentation)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := DiscoverVersion(ctx, root+"/media/good", policy, "root", "object", fixtureLocatorBinding(root, "root", "object"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, r := range []string{"bytes=4-2", "bytes=-0", "bytes=0-1,2-3", "bytes=9223372036854775808-", strings.Repeat("1", 129)} {
		if _, err := c.Open(ctx, "GET", r); !errors.Is(err, ErrRepresentationRange) {
			t.Fatalf("invalid range accepted %s", r)
		}
	}
	resp, err := c.Open(ctx, "GET", "bytes=10-")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 416 || resp.Header.Get("Content-Range") != "bytes */10" {
		t.Fatal("unsatisfied range metadata incorrect")
	}
	if _, err := c.Open(ctx, "POST", ""); !errors.Is(err, ErrPolicy) {
		t.Fatal("mutation method allowed")
	}
}

func TestVersionedRedirectsPreservePreconditionsAndPolicy(t *testing.T) {
	var conditionalRedirect atomic.Bool
	root, policy := versionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/media/redirect":
			http.Redirect(w, r, "/media/target", 302)
		case "/media/escape":
			http.Redirect(w, r, "/admin", 302)
		case "/media/changed":
			w.Header().Set("ETag", `"version-2"`)
			w.Header().Set("Content-Range", "bytes 0-0/10")
			w.WriteHeader(206)
			_, _ = io.WriteString(w, "x")
		case "/media/target":
			if r.Header.Get("If-Match") == `"version-1"` && r.Header.Get("Range") == "bytes=0-0" {
				conditionalRedirect.Store(true)
			}
			fixtureRepresentation(w, r)
		case "/media/good":
			fixtureRepresentation(w, r)
		default:
			t.Error("redirect escaped root")
			w.WriteHeader(500)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := DiscoverVersion(ctx, root+"/media/good", policy, "root", "object", fixtureLocatorBinding(root, "root", "object"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	renewed, err := c.RenewLocator(ctx, root+"/media/redirect", policy)
	if err != nil {
		t.Fatal(err)
	}
	renewed.Close()
	if !conditionalRedirect.Load() {
		t.Fatal("redirect lost validator or range")
	}
	if _, err := c.RenewLocator(ctx, root+"/media/escape", policy); !errors.Is(err, ErrPolicy) {
		t.Fatal("redirect policy was bypassed")
	}
	if _, err := c.RenewLocator(ctx, root+"/media/changed", policy); !errors.Is(err, mediasource.ErrSourceChanged) {
		t.Fatal("locator renewal switched representation")
	}
	resp, err := c.Open(ctx, "GET", "bytes=0-0")
	if err != nil {
		t.Fatal("failed renewal changed old adapter", err)
	}
	resp.Body.Close()
}

func TestVersionedLocatorIdentityRejectsETagCollision(t *testing.T) {
	var otherReads atomic.Int32
	root, policy := versionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/media/redirect-b" {
			http.Redirect(w, r, "/media/b", 302)
			return
		}
		if r.URL.Path == "/media/b" {
			otherReads.Add(1)
			// A different resource may legitimately reuse the same validator/size.
			w.Header().Set("ETag", `"version-1"`)
			w.Header().Set("Content-Range", "bytes 0-0/10")
			w.WriteHeader(206)
			_, _ = io.WriteString(w, "x")
			return
		}
		fixtureRepresentation(w, r)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	binding := fixtureLocatorBinding(root, "root", "object")
	c, err := DiscoverVersion(ctx, root+"/media/a", policy, "root", "object", binding)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, path := range []string{"b", "redirect-b"} {
		if next, err := c.RenewLocator(ctx, root+"/media/"+path, policy); !errors.Is(err, mediasource.ErrSourceChanged) {
			if next != nil {
				next.Close()
			}
			t.Fatalf("ETag collision accepted through %s: %v", path, err)
		}
	}
	if otherReads.Load() != 0 {
		t.Fatal("unbound object was fetched")
	}
	exact, err := ExactLocatorBinding(root+"/media/a", "root", "object")
	if err != nil {
		t.Fatal(err)
	}
	strict, err := NewVersioned(ctx, root+"/media/a", policy, c.Version(), exact)
	if err != nil {
		t.Fatal(err)
	}
	defer strict.Close()
	if next, err := strict.RenewLocator(ctx, root+"/media/a?token=changed", policy); !errors.Is(err, mediasource.ErrIdentityRequired) {
		if next != nil {
			next.Close()
		}
		t.Fatal("generic binding assumed a query parameter was only credentials")
	}
	if _, err := NewVersioned(ctx, root+"/media/a", policy, c.Version(), nil); !errors.Is(err, mediasource.ErrIdentityRequired) {
		t.Fatal("missing locator binding accepted")
	}
}
