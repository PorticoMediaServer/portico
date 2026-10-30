package remotemedia

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestSFTPProxyPinsTheResolvedNativeEndpointAndRechecksReconnects(t *testing.T) {
	ctx := context.Background()
	resolver := &nativePolicyResolver{ips: []netip.Addr{netip.MustParseAddr("192.168.42.7")}}
	policy := Policy{Resolver: resolver, Approvals: []Approval{{Root: "sftp://nas.example:22/", Addresses: []string{"192.168.42.7"}}}}
	proxy, err := StartSFTPPolicyProxy(ctx, policy, "private", "fixture", func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer closePolicyFixture(t, proxy)
	addresses, err := proxy.connectAddresses(ctx, "nas.example", "22")
	if err != nil || len(addresses) != 1 || addresses[0].String() != "192.168.42.7" || resolver.calls != 1 {
		t.Fatal("native connection did not receive the exact admitted numeric address", addresses, err, resolver.calls)
	}
	// The connection path consumes those addresses directly. A later tunnel
	// must perform a new lookup instead of reusing the original preflight.
	resolver.ips = []netip.Addr{netip.MustParseAddr("192.168.42.8")}
	if _, err = proxy.connectAddresses(ctx, "nas.example", "22"); !errors.Is(err, ErrPolicy) || resolver.calls != 2 {
		t.Fatal("reconnect used an unsaved private address", err, resolver.calls)
	}
	for _, endpoint := range [][2]string{{"other.example", "22"}, {"nas.example", "2222"}, {"127.0.0.1", "22"}, {"169.254.169.254", "22"}} {
		if _, err = proxy.connectAddresses(ctx, endpoint[0], endpoint[1]); !errors.Is(err, ErrPolicy) {
			t.Fatal("native tunnel escaped exact endpoint", endpoint, err)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "http://nas.example/", nil)
	request.Header.Set("Proxy-Authorization", proxy.authorization)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatal("SFTP approval admitted HTTP", response.Code)
	}
}

func TestSFTPProxyRejectsHTTPApprovalAndBlockedMixedDNS(t *testing.T) {
	ctx := context.Background()
	if proxy, err := StartSFTPPolicyProxy(ctx, Policy{Approvals: []Approval{{Root: "https://nas.example:22/", Addresses: []string{"192.168.42.7"}}}}, "private", "fixture", func(ctx context.Context) error { return ctx.Err() }); !errors.Is(err, ErrPolicy) || proxy != nil {
		if proxy != nil {
			closePolicyFixture(t, proxy)
		}
		t.Fatal("HTTPS approval transferred to native protocol", err)
	}
	resolver := &nativePolicyResolver{ips: []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("64:ff9b::a9fe:a9fe")}}
	proxy, err := StartSFTPPolicyProxy(ctx, Policy{Resolver: resolver, Approvals: []Approval{{Root: "sftp://nas.example:22/"}}}, "private", "fixture", func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer closePolicyFixture(t, proxy)
	if _, err = proxy.connectAddresses(ctx, "nas.example", "22"); !errors.Is(err, ErrPolicy) {
		t.Fatal("mixed public/transition answer reached native dial", err)
	}
}
