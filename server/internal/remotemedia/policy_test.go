package remotemedia

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type resolverFunc func(context.Context, string, string) ([]netip.Addr, error)

func (f resolverFunc) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f(ctx, network, host)
}
func TestPrivateRootsRedirectsAndMetadataAreExplicitlyFenced(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/media/redirect":
			http.Redirect(w, r, "/media/file", 302)
		case "/media/escape":
			http.Redirect(w, r, "/admin", 302)
		case "/media/metadata":
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", 302)
		case "/media/file":
			_, _ = w.Write([]byte("movie"))
		default:
			t.Error("escaped approved root")
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	if _, e := New(ctx, server.URL+"/media/file", Policy{}); !errors.Is(e, ErrPolicy) {
		t.Fatal("unapproved LAN allowed", e)
	}
	a, e := (Policy{}).Approve(ctx, server.URL+"/media/")
	if e != nil {
		t.Fatal(e)
	}
	policy := Policy{Approvals: []Approval{a}}
	c, e := New(ctx, server.URL+"/media/redirect", policy)
	if e != nil {
		t.Fatal(e)
	}
	resp, e := c.Open(ctx, "GET", "")
	if e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(resp.Body)
	resp.Body.Close()
	c.Close()
	if e != nil || string(raw) != "movie" {
		t.Fatal(e, string(raw))
	}
	for _, path := range []string{"/admin", "/media/../admin", "/media/%252e%252e/admin"} {
		if _, e = New(ctx, server.URL+path, policy); !errors.Is(e, ErrPolicy) {
			t.Fatal("path escaped", path, e)
		}
	}
	for _, path := range []string{"/media/escape", "/media/metadata"} {
		c, e = New(ctx, server.URL+path, policy)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = c.Open(ctx, "GET", ""); !errors.Is(e, ErrPolicy) {
			t.Fatal("redirect escaped", path, e)
		}
		c.Close()
	}
	for _, url := range []string{"http://169.254.169.254/", "http://100.100.100.200/", "http://[fd00:ec2::254]/", "http://[fd00:ec2::23]/", "http://[fd20:ce::254]/"} {
		if _, e = (Policy{}).Approve(ctx, url); !errors.Is(e, ErrPolicy) {
			t.Fatal("metadata endpoint approved", url, e)
		}
	}
}
func TestDNSChangesAreRevalidatedAgainstApprovedAddresses(t *testing.T) {
	ip := netip.MustParseAddr("192.168.1.20")
	resolver := resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) { return []netip.Addr{ip}, nil })
	p := Policy{Resolver: resolver}
	a, e := p.Approve(context.Background(), "http://nas.test:8080/media/")
	if e != nil {
		t.Fatal(e)
	}
	p.Approvals = []Approval{a}
	if _, e = New(context.Background(), "http://nas.test:8080/media/movie.mp4", p); e != nil {
		t.Fatal(e)
	}
	ip = netip.MustParseAddr("192.168.1.21")
	if _, e = New(context.Background(), "http://nas.test:8080/media/movie.mp4", p); !errors.Is(e, ErrPolicy) {
		t.Fatal("DNS rebind allowed", e)
	}
	ip = netip.MustParseAddr("169.254.169.254")
	if _, e = New(context.Background(), "http://nas.test:8080/media/movie.mp4", p); !errors.Is(e, ErrPolicy) {
		t.Fatal("DNS metadata rebind allowed", e)
	}
}
func TestDescriptorRulesAndBodyReadTimeout(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "http://user:password@example.com/a", "http://a/x\nhttp://b/y", "http://a/%252e%252e/secret"} {
		if _, e := ParseDescriptor([]byte(raw)); e == nil {
			t.Fatal("descriptor accepted", raw)
		}
	}
	if _, e := ParseDescriptor([]byte("\nhttps://media.example/movie.mp4?token=secret\n")); e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		time.Sleep(300 * time.Millisecond)
	}))
	defer server.Close()
	a, e := (Policy{}).Approve(context.Background(), server.URL+"/")
	if e != nil {
		t.Fatal(e)
	}
	c, e := New(context.Background(), server.URL+"/file", Policy{Approvals: []Approval{a}, ReadTimeout: 40 * time.Millisecond})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	resp, e := c.Open(context.Background(), "GET", "")
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	start := time.Now()
	_, e = io.ReadAll(resp.Body)
	if e == nil || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("body deadline missing %v %v", e, time.Since(start))
	}
	if strings.Contains(e.Error(), "token=secret") {
		t.Fatal("credential leaked")
	}
}

func TestManualRedirectsReturnEachHopWithoutContactingDestination(t *testing.T) {
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1) }))
	defer target.Close()
	for _, status := range []int{301, 302, 303, 307, 308} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, status) }))
		approval, err := (Policy{}).Approve(context.Background(), server.URL+"/")
		if err != nil {
			t.Fatal(err)
		}
		client, err := New(context.Background(), server.URL, Policy{Approvals: []Approval{approval}, ManualRedirects: true})
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Open(context.Background(), http.MethodGet, "")
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != status || response.Header.Get("Location") != target.URL {
			t.Fatalf("response=%v", response)
		}
		response.Body.Close()
		client.Close()
		server.Close()
	}
	if reached.Load() != 0 {
		t.Fatal("manual redirect followed an unadmitted destination")
	}
}
