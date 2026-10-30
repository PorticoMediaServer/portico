package remotemedia

import (
	"context"
	"net/netip"
	"testing"
)

func TestSavedEndpointApprovalIsExactOriginAndNeverNativeOrLibraryAuthority(t *testing.T) {
	ctx := context.Background()
	policy := Policy{}
	a, err := policy.PrepareEndpointApproval(ctx, "https://192.168.42.7:9443/bucket")
	if err != nil || a.Root != "https://192.168.42.7:9443/" || len(a.Addresses) != 1 {
		t.Fatal(a, err)
	}
	if err = policy.CheckEndpointApproval(ctx, "https://192.168.42.7:9443/other", a); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"http://192.168.42.7:9443/", "https://192.168.42.7:9444/", "https://192.168.42.8:9443/", "sftp://192.168.42.7:9443/"} {
		if err = policy.CheckEndpointApproval(ctx, target, a); err == nil {
			t.Fatal("approval crossed origin", target)
		}
	}
	a.Root = "smb://192.168.42.7:9443/"
	if err = policy.CheckEndpointApproval(ctx, "https://192.168.42.7:9443/", a); err == nil {
		t.Fatal("native approval covered HTTP")
	}
	policy.Approvals = []Approval{{Root: "https://192.168.42.7:9443/", Addresses: []string{"192.168.42.7"}}}
	if err = policy.CheckEndpointApproval(ctx, "https://192.168.42.7:9443/", Approval{}); err == nil {
		t.Fatal("library approval inferred")
	}
}

func TestSavedEndpointApprovalNeverListAndDNSChange(t *testing.T) {
	ctx := context.Background()
	for _, host := range []string{"127.0.0.1", "169.254.169.254", "169.254.1.2", "100.100.100.200", "192.0.2.1", "198.18.0.1", "[2001:db8::1]", "[::]", "[ff02::1]"} {
		if a, err := (Policy{}).PrepareEndpointApproval(ctx, "http://"+host+"/"); err == nil {
			t.Fatal("forbidden endpoint prepared", host, a)
		}
	}
	for _, host := range []string{"10.0.0.1", "172.16.0.1", "192.168.42.7", "100.64.0.1", "[fd00::1]", "8.8.8.8"} {
		p := Policy{}
		a, err := p.PrepareEndpointApproval(ctx, "http://"+host+"/")
		if err != nil {
			t.Fatal(host, err)
		}
		if err = p.CheckEndpointApproval(ctx, "http://"+host+"/", a); err != nil {
			t.Fatal(host, err)
		}
	}
	resolver := &nativePolicyResolver{ips: []netip.Addr{netip.MustParseAddr("192.168.42.7")}}
	p := Policy{Resolver: resolver}
	a, err := p.PrepareEndpointApproval(ctx, "http://NAS.example:8080/path")
	if err != nil || a.Root != "http://nas.example:8080/" {
		t.Fatal(a, err)
	}
	resolver.ips = []netip.Addr{netip.MustParseAddr("192.168.42.8")}
	if err = p.CheckEndpointApproval(ctx, "http://nas.example:8080/", a); err == nil {
		t.Fatal("DNS change refreshed saved addresses")
	}
}

func TestSavedEndpointProxyAdmitsPublicWithoutInferringPrivateApproval(t *testing.T) {
	ctx := context.Background()
	resolver := &nativePolicyResolver{ips: []netip.Addr{netip.MustParseAddr("192.168.42.7")}}
	p := Policy{Resolver: resolver, SourceEndpoints: true}
	approval, err := p.PrepareEndpointApproval(ctx, "https://nas.example/")
	if err != nil {
		t.Fatal(err)
	}
	p.Approvals = []Approval{approval}
	if _, _, err = p.admit(ctx, "https://nas.example/object"); err != nil {
		t.Fatal(err)
	}
	resolver.ips = []netip.Addr{netip.MustParseAddr("8.8.8.8")}
	if _, _, err = p.admit(ctx, "https://nas.example/object"); err != nil {
		t.Fatal("public resolution required private approval", err)
	}
	resolver.ips = []netip.Addr{netip.MustParseAddr("192.168.42.8")}
	if _, _, err = p.admit(ctx, "https://nas.example/object"); err == nil {
		t.Fatal("new private resolution reused approval")
	}
	if _, _, err = p.admit(ctx, "https://other.example/object"); err == nil {
		t.Fatal("approval crossed source origin")
	}
	resolver.ips = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	p.Approvals = []Approval{{Root: "https://nas.example:443/", Addresses: []string{"127.0.0.1"}}}
	if _, _, err = p.admit(ctx, "https://nas.example/object"); err == nil {
		t.Fatal("forbidden address admitted even when saved")
	}
}
