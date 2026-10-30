package connectivity

import (
	"context"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func TestPolicyProjectionFillsEmptyListsAndCarriesTheSettingsRevision(t *testing.T) {
	document := operations.SettingsDocument{Revision: 7, Effective: operations.Settings{RemoteSignInPolicy: "owner-only", RemoteBitrateLimitKbps: 6000}}
	policy := ProjectPolicy(document)
	if policy.Revision != 7 || policy.RemoteSignIn != "owner-only" || policy.RemoteBitrateLimitKbps != 6000 {
		t.Fatalf("projection: %+v", policy)
	}
	if policy.LANNetworks == nil || policy.AccessURLs == nil {
		t.Fatal("a nil list would serialise as null rather than an empty array")
	}
}

func TestAddressClassificationPrefersTheConfiguredLANList(t *testing.T) {
	lan := parseNetworks([]string{"203.0.113.0/24"})
	for _, c := range []struct {
		address, want string
	}{
		{"127.0.0.1", "public"},
		{"192.168.1.20", "public"},
		{"203.0.113.9", "lan"}, // configured, so it is LAN even though it is routable
		{"198.51.100.7", "public"},
		{"fe80::1", "public"},
		{"100.64.1.1", "public"},
	} {
		if got := classify(netip.MustParseAddr(c.address), lan); got != c.want {
			t.Fatalf("classify(%s)=%s want %s", c.address, got, c.want)
		}
	}
}

func TestStatusWarnsWhenPolicyAndHostDisagree(t *testing.T) {
	reporter := Reporter{
		Now: func() time.Time { return time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC) },
		Certificates: func(context.Context) (networking.CertificateStatus, error) {
			return networking.CertificateStatus{Configured: true, State: "pending", TLSReady: false}, nil
		},
	}
	status := reporter.Report(context.Background(), Policy{RemoteSignIn: "allow", LANDiscoveryEnabled: true})
	joined := strings.Join(status.Warnings, ",")
	if !strings.Contains(joined, "lan_discovery_unsupported_on_this_host") {
		t.Fatalf("discovery asked for but never started must warn: %v", status.Warnings)
	}
	if status.TLS.State != "pending" || status.TLS.Configured != true {
		t.Fatalf("TLS: %+v", status.TLS)
	}
	if status.Interfaces == nil || status.Addresses == nil || status.Warnings == nil {
		t.Fatal("lists must serialise as arrays")
	}
	// A reporter with no certificate seam still produces a report.
	plain := Reporter{}.Report(context.Background(), Policy{RemoteSignIn: "off"})
	if plain.TLS.Configured {
		t.Fatalf("TLS without a manager: %+v", plain.TLS)
	}
}

func TestAccessURLsEnterTheAddressList(t *testing.T) {
	status := Reporter{}.Report(context.Background(), Policy{AccessURLs: []string{"https://media.example.com:8443"}})
	found := false
	for _, address := range status.Addresses {
		if address.Source == "accessUrl" && address.Address == "media.example.com" && address.Family == "hostname" {
			found = true
		}
	}
	if !found {
		t.Fatalf("addresses: %+v", status.Addresses)
	}
}

func TestOwnerSettingControlsSingleDiscovery(t *testing.T) {
	a := NewAdvertiser(AdvertiserOptions{})
	started := make(chan struct{}, 2)
	stopped := make(chan struct{}, 2)
	a.Apply(true)
	a.Bind(context.Background(), func(ctx context.Context) { started <- struct{}{}; <-ctx.Done(); stopped <- struct{}{} })
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("not started")
	}
	a.Apply(true)
	select {
	case <-started:
		t.Fatal("duplicate advertiser")
	default:
	}
	a.Apply(false)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("not stopped")
	}
	if a.Status(false).State != "stopped" {
		t.Fatal("incorrect state")
	}
}

// A nil Advertiser is what a build or a host without discovery looks like; the
// status report must still say something truthful rather than panic.
func TestNilAdvertiserReportsUnavailable(t *testing.T) {
	var a *Advertiser
	status := a.Status(true)
	if status.Supported || status.State != "unavailable" || status.Service != ServiceType {
		t.Fatalf("status: %+v", status)
	}
	a.Apply(true)
	a.Close()
}

func TestInterfaceObservationIsPortableAndBounded(t *testing.T) {
	// net.Interfaces is available on every target; a host that refuses it yields
	// an empty list rather than an error, which is the contract the status report
	// depends on.
	for _, item := range observeInterfaces() {
		if item.Name == "" {
			t.Fatal("an interface without a name")
		}
		if item.Addresses == nil {
			t.Fatal("addresses must serialise as an array")
		}
		for _, raw := range item.Addresses {
			if _, err := netip.ParsePrefix(raw); err != nil {
				t.Fatalf("address %q is not a prefix: %v", raw, err)
			}
		}
	}
}

// A52: a slow stop does not block Status (an HTTP handler), a panicking run is
// restarted under supervision, and the process context stops it.
func TestDiscoveryRunIsSupervisedAndStopsWithoutBlockingStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	a := NewAdvertiser(AdvertiserOptions{})
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	runs := make(chan struct{}, 4)
	var calls atomic.Int32
	a.Apply(true)
	a.Bind(parent, func(ctx context.Context) {
		runs <- struct{}{}
		if calls.Add(1) == 1 {
			panic("first run fails")
		}
		<-ctx.Done()
		time.Sleep(300 * time.Millisecond)
	})
	for range 2 {
		select {
		case <-runs:
		case <-time.After(5 * time.Second):
			t.Fatal("panicked run was not restarted")
		}
	}
	stopped := make(chan struct{})
	go func() { a.Apply(false); close(stopped) }()
	time.Sleep(50 * time.Millisecond)
	statusDone := make(chan struct{})
	go func() { a.Status(true); close(statusDone) }()
	select {
	case <-statusDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Status blocked behind a stopping advertiser")
	}
	<-stopped
	a.Apply(true)
	select {
	case <-runs:
	case <-time.After(5 * time.Second):
		t.Fatal("not restarted after enable")
	}
	cancelParent()
	deadline := time.After(2 * time.Second)
	for a.Status(true).State == "advertising" {
		select {
		case <-deadline:
			t.Fatal("process context did not stop discovery")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestIPv6AccessOriginsKeepAddressAndFamily(t *testing.T) {
	out := Reporter{}.Report(context.Background(), Policy{AdvertiseURL: "http://[2001:db8::9]", LANNetworks: []string{"2001:db8::/64"}})
	for _, addr := range out.Addresses {
		if addr.Source == "accessUrl" {
			if addr.Address != "2001:db8::9" || addr.Family != "ipv6" || addr.Class != "lan" {
				t.Fatalf("%+v", addr)
			}
			return
		}
	}
	t.Fatal("external address absent")
}
