package remotemedia

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestTransitionAddressesStayBlockedAcrossEveryApprovalRoute(t *testing.T) {
	ctx := context.Background()
	for _, address := range []string{
		"64:ff9b::7f00:1", "64:ff9b::a9fe:a9fe", "64:ff9b:1::a00:1", "64:ff9b:1:ffff:ffff:ffff:ffff:ffff",
		"2002:7f00:1::", "2002:a9fe:a9fe::1", "2001:0:4136:e378:8000:63bf:3fff:fdd2",
	} {
		t.Run(address, func(t *testing.T) {
			ip := netip.MustParseAddr(address)
			resolver := resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) { return []netip.Addr{ip}, nil })
			for _, target := range []string{"https://[" + address + "]/", "https://transition.example/"} {
				policy := Policy{Resolver: resolver}
				if _, err := policy.Approve(ctx, target); !errors.Is(err, ErrPolicy) {
					t.Fatal("transition address gained an owner approval", err)
				}
				policy.Approvals = []Approval{{Root: target, Addresses: []string{address}}}
				for _, source := range []bool{false, true} {
					policy.SourceEndpoints = source
					client, err := New(ctx, target, policy)
					if client != nil {
						client.Close()
					}
					if !errors.Is(err, ErrPolicy) {
						t.Fatal("saved approval bypassed a transition block", source, err)
					}
				}
			}
			for _, scheme := range []string{"sftp", "smb"} {
				endpoint := NativeEndpoint{Scheme: scheme, Host: "transition.example", Port: 2222}
				root, err := endpoint.Root()
				if err != nil {
					t.Fatal(err)
				}
				if _, err = (Policy{Resolver: resolver}).CheckNativeEndpoint(ctx, endpoint, Approval{Root: root}); !errors.Is(err, ErrPolicy) {
					t.Fatal("native policy accepted transition destination", err)
				}
			}
		})
	}
	for _, address := range []string{"8.8.8.8", "::ffff:8.8.8.8", "2606:4700:4700::1111"} {
		if !public(netip.MustParseAddr(address)) {
			t.Fatal("ordinary public address was refused", address)
		}
	}
}
