package remotemedia

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

type nativePolicyResolver struct {
	ips   []netip.Addr
	calls int
}

func (r *nativePolicyResolver) LookupNetIP(ctx context.Context, _, _ string) ([]netip.Addr, error) {
	r.calls++
	return append([]netip.Addr(nil), r.ips...), ctx.Err()
}

func TestNativeApprovalRecordsOnlyPermittedPrivateAddresses(t *testing.T) {
	ctx := context.Background()
	for _, scheme := range []string{"sftp", "smb"} {
		t.Run(scheme, func(t *testing.T) {
			resolver := &nativePolicyResolver{ips: []netip.Addr{
				netip.MustParseAddr("192.168.42.7"), netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("fd12::7"), netip.MustParseAddr("100.85.4.7"), netip.MustParseAddr("192.168.42.7"),
			}}
			policy := Policy{Resolver: resolver}
			endpoint := NativeEndpoint{Scheme: scheme, Host: "NAS.Example.", Port: 2222}
			approval, err := policy.PrepareNativeApproval(ctx, endpoint)
			if err != nil || approval.Root != scheme+"://nas.example:2222/" || strings.Join(approval.Addresses, ",") != "100.85.4.7,192.168.42.7,fd12::7" {
				t.Fatal("native candidate facts", approval, err)
			}
			checked, err := policy.CheckNativeEndpoint(ctx, endpoint, approval)
			if err != nil || len(checked) != len(resolver.ips) || resolver.calls != 2 {
				t.Fatal("native endpoint did not re-resolve all addresses", checked, err, resolver.calls)
			}
			resolver.ips = []netip.Addr{netip.MustParseAddr("192.168.42.8")}
			if _, err = policy.CheckNativeEndpoint(ctx, endpoint, approval); !errors.Is(err, ErrPolicy) {
				t.Fatal("DNS rebound to an unsaved private address", err)
			}
			resolver.ips = []netip.Addr{netip.MustParseAddr("8.8.8.8")}
			if _, err = policy.CheckNativeEndpoint(ctx, endpoint, approval); err != nil {
				t.Fatal("public address incorrectly needed a private grant", err)
			}
		})
	}
}

func TestNativeApprovalNeverCoversHardBlockedOrSpecialAddresses(t *testing.T) {
	for _, address := range []string{
		"127.0.0.1", "::1", "::ffff:127.0.0.1", "169.254.169.254", "169.254.1.2", "100.100.100.200", "fd00:ec2::254", "fe80::1", "fe80::1%eth0", "0.0.0.0", "::", "224.0.0.1", "ff02::1", "192.0.2.1", "198.51.100.1", "203.0.113.1", "198.18.1.1", "2001:db8::1", "3fff::1", "2001:2::1",
	} {
		t.Run(address, func(t *testing.T) {
			for _, scheme := range []string{"sftp", "smb"} {
				endpoint := NativeEndpoint{Scheme: scheme, Host: address, Port: 2222}
				if _, err := (Policy{}).PrepareNativeApproval(context.Background(), endpoint); !errors.Is(err, ErrPolicy) {
					t.Fatal("saved source could approve forbidden endpoint", scheme, err)
				}
				root, _ := endpoint.Root()
				if _, err := (Policy{}).CheckNativeEndpoint(context.Background(), endpoint, Approval{Root: root, Addresses: []string{address}}); !errors.Is(err, ErrPolicy) {
					t.Fatal("fabricated saved address bypassed hard block", scheme, err)
				}
			}
		})
	}
}

func TestNativeApprovalHasNoHTTPSAliasOrImplicitGrant(t *testing.T) {
	ctx := context.Background()
	resolver := &nativePolicyResolver{ips: []netip.Addr{netip.MustParseAddr("192.168.42.7")}}
	https := Approval{Root: "https://nas.example:2222/", Addresses: []string{"192.168.42.7"}}
	policy := Policy{Resolver: resolver, Approvals: []Approval{https}}
	for _, scheme := range []string{"sftp", "smb"} {
		endpoint := NativeEndpoint{Scheme: scheme, Host: "nas.example", Port: 2222}
		for _, saved := range []Approval{{}, https, {Root: "sftp://other.example:2222/", Addresses: https.Addresses}} {
			if _, err := policy.CheckNativeEndpoint(ctx, endpoint, saved); !errors.Is(err, ErrPolicy) {
				t.Fatal("missing or unrelated source approval granted native I/O", err)
			}
		}
		approval, err := policy.PrepareNativeApproval(ctx, endpoint)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = (Policy{Resolver: resolver, Approvals: []Approval{approval}}).admit(ctx, https.Root); !errors.Is(err, ErrPolicy) {
			t.Fatal("native approval granted HTTPS", err)
		}
		other := endpoint
		if scheme == "sftp" {
			other.Scheme = "smb"
		} else {
			other.Scheme = "sftp"
		}
		if _, err = policy.CheckNativeEndpoint(ctx, other, approval); !errors.Is(err, ErrPolicy) {
			t.Fatal("native protocols shared approval", err)
		}
	}
	public := NativeEndpoint{Scheme: "sftp", Host: "8.8.8.8", Port: 22}
	if _, err := policy.CheckNativeEndpoint(ctx, public, Approval{}); !errors.Is(err, ErrPolicy) {
		t.Fatal("old source without a saved approval was inferred", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := policy.PrepareNativeApproval(canceled, public); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled owner save observed fresh facts", err)
	}
	if _, err := policy.CheckNativeEndpoint(canceled, public, Approval{Root: "sftp://8.8.8.8:22/"}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled source admitted a connection", err)
	}
}
