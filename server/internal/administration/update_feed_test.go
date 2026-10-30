package administration

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCachedUpdatesChecksInBackgroundAndReportsFailureAsUnavailable(t *testing.T) {
	s, _, _ := newService(t)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	calls := 0
	s.Fetch = func(ctx context.Context, rawURL string) ([]byte, error) {
		calls++
		started <- struct{}{}
		select {
		case <-release:
			return nil, errors.New("feed offline")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	current := UpdateBuild{Version: "1.2.0"}
	first := s.CachedUpdates(context.Background(), current, "stable", "https://example.invalid/feed")
	if first.State != "unavailable" || !first.FeedConfigured || first.CheckedAt != "" {
		t.Fatalf("first read blocked or misreported feed: %+v", first)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background fetch never started")
	}
	second := s.CachedUpdates(context.Background(), current, "stable", "https://example.invalid/feed")
	if second.State != "unavailable" || calls != 1 {
		t.Fatalf("request repeated in-flight fetch: %+v, calls=%d", second, calls)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		result := s.CachedUpdates(context.Background(), current, "stable", "https://example.invalid/feed")
		if result.CheckedAt != "" {
			if result.State != "unavailable" || result.Status != "unavailable" || !result.FeedConfigured || calls != 1 {
				t.Fatalf("failed configured check: %+v, calls=%d", result, calls)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("background result was not cached")
}

func TestFetchReleaseFeedBoundsTheResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"channels":{"stable":[{"version":"1.2.0"}]}}`))
	}))
	defer server.Close()
	data, err := FetchReleaseFeed(context.Background(), server.URL)
	if err != nil || len(data) == 0 {
		t.Fatalf("configured feed: %q %v", data, err)
	}
	if _, err := FetchReleaseFeed(context.Background(), "file:///etc/passwd"); !errors.Is(err, ErrInput) {
		t.Fatalf("non-HTTP feed accepted: %v", err)
	}
}

func TestReleaseFeedCannotReachMetadataOrEscapeConfiguredOrigin(t *testing.T) {
	if _, err := FetchReleaseFeed(context.Background(), "http://169.254.169.254/latest/meta-data"); !errors.Is(err, remotemedia.ErrPolicy) {
		t.Fatalf("metadata service accepted: %v", err)
	}
	reached := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.Write([]byte("secret")) }))
	defer destination.Close()
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer feed.Close()
	if _, err := FetchReleaseFeed(context.Background(), feed.URL); !errors.Is(err, remotemedia.ErrPolicy) {
		t.Fatalf("redirect escaped: %v", err)
	}
	if reached {
		t.Fatal("unapproved redirect destination contacted")
	}
}

const officialFeed = "https://github.com/PorticoMediaServer/portico-server/releases/latest/download/portico-update-manifest.json"
const taggedFeed = "https://github.com/PorticoMediaServer/portico-server/releases/download/v1.2.3/portico-update-manifest.json"
const assetFeed = "https://release-assets.githubusercontent.com/github-production-release-asset/123/12345678-1234-1234-1234-123456789abc?sig=fixture"

func TestReleaseFeedRedirectAuthority(t *testing.T) {
	parse := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	for _, tc := range []struct {
		name, initial, current, next string
		allow                        bool
	}{
		{"latest to tag", officialFeed, officialFeed, taggedFeed, true},
		{"tag to asset", officialFeed, taggedFeed, assetFeed, true},
		{"direct asset redirect", officialFeed, officialFeed, assetFeed, true},
		{"explicit tagged feed", taggedFeed, taggedFeed, assetFeed, true},
		{"default HTTPS port", officialFeed, officialFeed, strings.Replace(assetFeed, ".com/", ".com:443/", 1), true},
		{"custom same origin", "https://feed.test/a", "https://feed.test/a", "https://feed.test/b", true},
		{"custom port normalization", "https://feed.test/a", "https://feed.test/a", "https://feed.test:443/b", true},
		{"private same origin", "http://127.0.0.1/a", "http://127.0.0.1/a", "http://127.0.0.1/b", true},
		{"custom cross origin", "https://feed.test/a", "https://feed.test/a", assetFeed, false},
		{"other repository", strings.Replace(officialFeed, "PorticoMediaServer", "attacker", 1), officialFeed, assetFeed, false},
		{"other manifest", strings.Replace(officialFeed, "portico-update-manifest", "other", 1), officialFeed, assetFeed, false},
		{"other GitHub path", officialFeed, officialFeed, "https://github.com/login", false},
		{"other release repo", officialFeed, officialFeed, strings.Replace(taggedFeed, "portico-server", "other", 1), false},
		{"asset onward", officialFeed, assetFeed, "https://example.com/feed", false},
		{"asset same host onward", officialFeed, assetFeed, assetFeed, false},
		{"downgrade", officialFeed, taggedFeed, strings.Replace(assetFeed, "https:", "http:", 1), false},
		{"custom downgrade", "https://feed.test/a", "https://feed.test/a", "http://feed.test/b", false},
		{"credentials", officialFeed, taggedFeed, strings.Replace(assetFeed, "https://", "https://user:secret@", 1), false},
		{"fragment", officialFeed, taggedFeed, assetFeed + "#fragment", false},
		{"nonstandard port", officialFeed, taggedFeed, strings.Replace(assetFeed, ".com/", ".com:444/", 1), false},
		{"lookalike", officialFeed, taggedFeed, strings.Replace(assetFeed, ".com/", ".com.attacker.test/", 1), false},
		{"asset subdomain", officialFeed, taggedFeed, strings.Replace(assetFeed, "//release-assets", "//evil.release-assets", 1), false},
		{"arbitrary asset path", officialFeed, taggedFeed, "https://release-assets.githubusercontent.com/feed.json", false},
		{"metadata", officialFeed, taggedFeed, "http://169.254.169.254/latest/meta-data", false},
		{"loopback", officialFeed, taggedFeed, "https://127.0.0.1/feed", false},
		{"encoded path", officialFeed, taggedFeed, strings.Replace(assetFeed, "github-production", "%67ithub-production", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := feedRedirectAllowed(parse(tc.initial), parse(tc.current), parse(tc.next)); got != tc.allow {
				t.Fatalf("allowed=%v, want %v", got, tc.allow)
			}
		})
	}
}

type feedResolver func(context.Context, string, string) ([]netip.Addr, error)

func (f feedResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f(ctx, network, host)
}

// Exercise actual multi-hop fetching without contacting GitHub. Only TLS dialing
// is routed to a disposable HTTP responder; policy DNS admission still sees the
// requested names and classifies their fixture answers.
func TestOfficialReleaseFeedFetchRedirectChainAndDNSFences(t *testing.T) {
	for _, address := range []string{"8.8.8.8", "127.0.0.1", "10.0.0.1", "169.254.169.254", "100.100.100.200", "fd00:ec2::254"} {
		t.Run(address, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != "" {
					t.Error("credentials forwarded")
				}
				switch r.URL.Path {
				case "/PorticoMediaServer/portico-server/releases/latest/download/portico-update-manifest.json":
					w.Header().Set("Set-Cookie", "secret=fixture")
					http.Redirect(w, r, taggedFeed, http.StatusFound)
				case "/PorticoMediaServer/portico-server/releases/download/v1.2.3/portico-update-manifest.json":
					http.Redirect(w, r, assetFeed, http.StatusFound)
				default:
					w.Write([]byte(`{"channels":{"stable":[{"version":"1.2.3"}]}}`))
				}
			}))
			defer server.Close()
			original := http.DefaultTransport
			transport := original.(*http.Transport).Clone()
			transport.DialTLSContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = original; transport.CloseIdleConnections() }()
			policy := remotemedia.Policy{Resolver: feedResolver(func(_ context.Context, _, host string) ([]netip.Addr, error) {
				ip := "8.8.8.8"
				if host == "release-assets.githubusercontent.com" {
					ip = address
				}
				return []netip.Addr{netip.MustParseAddr(ip)}, nil
			})}
			data, err := fetchReleaseFeed(context.Background(), officialFeed, policy)
			if address == "8.8.8.8" {
				if err != nil || !strings.Contains(string(data), "1.2.3") || hits.Load() != 3 {
					t.Fatalf("data=%s hits=%v err=%v", data, hits.Load(), err)
				}
			} else if !errors.Is(err, remotemedia.ErrPolicy) || hits.Load() != 2 {
				t.Fatalf("asset DNS escaped: data=%s hits=%v err=%v", data, hits.Load(), err)
			}
		})
	}
}

func TestCustomReleaseFeedSameOriginRedirectBoundsAndLoops(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/feed", http.StatusTemporaryRedirect)
		case "/feed":
			w.Write([]byte(`{"channels":{}}`))
		case "/large":
			w.Write(make([]byte, (1<<20)+1))
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/credentials":
			http.Redirect(w, r, "http://user:secret@"+r.Host+"/feed", http.StatusFound)
		case "/metadata":
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
		}
	}))
	defer server.Close()
	if raw, err := FetchReleaseFeed(context.Background(), server.URL+"/redirect"); err != nil || string(raw) != `{"channels":{}}` {
		t.Fatalf("same-origin feed=%s err=%v", raw, err)
	}
	for _, path := range []string{"/large", "/loop", "/credentials", "/metadata"} {
		if _, err := FetchReleaseFeed(context.Background(), server.URL+path); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
}
