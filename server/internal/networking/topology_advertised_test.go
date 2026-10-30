package networking

import (
	"context"
	"fmt"
	"net"
	"testing"
)

func TestVirtualInterfacePrefixes(t *testing.T) {
	for _, name := range []string{"docker0", "br-1a2b3c", "br0", "veth9f2a", "virbr0", "vmnet8", "vboxnet0", "cni0", "flannel.1", "caliabc", "lxcbr0", "lxdbr0", "podman0", "zt0", "wg0", "tun0", "tap0", "utun3", "tailscale0"} {
		if !VirtualInterface(name) {
			t.Fatalf("VirtualInterface(%q)=false, want true", name)
		}
	}
	// Physical interface names remain eligible.
	for _, name := range []string{"eth0", "en0", "enp3s0", "wlan0", "lo"} {
		if VirtualInterface(name) {
			t.Fatalf("VirtualInterface(%q)=true, want false", name)
		}
	}
}

// stubTopology replaces the interface enumeration and the gateway probe for
// one test. The gateway probe returns local for the given gateway.
func stubTopology(t *testing.T, ifaces []ifaceAddrs, gateway, local string) {
	t.Helper()
	oldIfaces, oldDial := topologyInterfaces, topologyDialLocal
	topologyInterfaces = func() ([]ifaceAddrs, error) { return ifaces, nil }
	topologyDialLocal = func(g string) (string, error) {
		if g == gateway {
			return local, nil
		}
		return "", errTopologyStubNoRoute
	}
	t.Cleanup(func() { topologyInterfaces, topologyDialLocal = oldIfaces, oldDial })
}

var errTopologyStubNoRoute = errTopologyNoRoute{}

type errTopologyNoRoute struct{}

func (errTopologyNoRoute) Error() string { return "no route" }

func topoIface(name string, addrs ...string) ifaceAddrs {
	return ifaceAddrs{Name: name, Flags: net.FlagUp, Addrs: addrs}
}

func lanSet(t *testing.T, lan []string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, ip := range lan {
		out[ip] = true
	}
	return out
}

func TestAdvertisedInterfaceRestrictsLAN(t *testing.T) {
	stubTopology(t, []ifaceAddrs{
		topoIface("eth0", "192.168.1.20/24"),
		topoIface("eth1", "192.168.1.21/24"),
		topoIface("docker0", "172.17.0.1/16"),
	}, "192.168.1.1", "192.168.1.20")
	topo, e := observeTopology(context.Background(), "0.0.0.0:32500", "192.168.1.1", "eth0")
	if e != nil {
		t.Fatal(e)
	}
	got := lanSet(t, topo.LAN)
	if !got["192.168.1.20"] || got["192.168.1.21"] || got["172.17.0.1"] {
		t.Fatalf("advertised eth0 should leave only its addresses in LAN: %v", topo.LAN)
	}
	if topo.LocalAddress != "192.168.1.20" {
		t.Fatalf("local address: %q", topo.LocalAddress)
	}
	if topo.ErrorCode != "" {
		t.Fatalf("error code: %q", topo.ErrorCode)
	}
}

func TestAdvertisedInterfaceMissingFallsBack(t *testing.T) {
	stubTopology(t, []ifaceAddrs{
		topoIface("eth0", "192.168.1.20/24"),
		topoIface("eth1", "192.168.1.21/24"),
		topoIface("docker0", "172.17.0.1/16"),
	}, "192.168.1.1", "192.168.1.20")
	topo, e := observeTopology(context.Background(), "0.0.0.0:32500", "192.168.1.1", "eth99")
	if e != nil {
		t.Fatal(e)
	}
	got := lanSet(t, topo.LAN)
	if !got["192.168.1.20"] || !got["192.168.1.21"] {
		t.Fatalf("a missing interface falls back to automatic: %v", topo.LAN)
	}
	if got["172.17.0.1"] {
		t.Fatalf("automatic included container interface: %v", topo.LAN)
	}
	if topo.ErrorCode != "advertised_interface_missing" {
		t.Fatalf("error code: %q", topo.ErrorCode)
	}
}

func TestVirtualGatewayInterfaceIsExcluded(t *testing.T) {
	stubTopology(t, []ifaceAddrs{
		topoIface("eth0", "192.168.1.20/24"),
		topoIface("docker0", "172.17.0.1/16"),
		topoIface("veth9f2a", "10.5.0.2/16"),
	}, "172.17.0.1", "172.17.0.1")
	topo, e := observeTopology(context.Background(), "0.0.0.0:32500", "172.17.0.1", "")
	if e != nil {
		t.Fatal(e)
	}
	got := lanSet(t, topo.LAN)
	if got["172.17.0.1"] {
		t.Fatalf("virtual gateway interface included: %v", topo.LAN)
	}
	if !got["192.168.1.20"] {
		t.Fatalf("a physical interface stays: %v", topo.LAN)
	}
	if got["10.5.0.2"] {
		t.Fatalf("virtual interface included: %v", topo.LAN)
	}
	if topo.LocalAddress != "" {
		t.Fatalf("local address: %q", topo.LocalAddress)
	}
}

func TestAllPhysicalLANAddressesExcludeBridgesAndVPN(t *testing.T) {
	stubTopology(t, []ifaceAddrs{
		topoIface("eth0", "192.168.1.20/24", "192.168.1.21/24", "fd00::20/64", "fd00::21/64"),
		topoIface("eth1", "10.0.0.2/24"), topoIface("docker0", "172.17.0.1/16"), topoIface("br0", "172.18.0.1/16"), topoIface("tailscale0", "100.64.0.1/10", "fd7a:115c:a1e0::1/48"), topoIface("wg0", "10.9.0.1/24"),
	}, "192.168.1.1", "192.168.1.20")
	topo, err := observeTopology(context.Background(), "[::]:32500", "192.168.1.1", "")
	if err != nil {
		t.Fatal(err)
	}
	got := lanSet(t, topo.LAN)
	if len(got) != 5 {
		t.Fatalf("LAN addresses %+v", topo.LAN)
	}
	for _, ip := range []string{"192.168.1.20", "192.168.1.21", "fd00::20", "fd00::21", "10.0.0.2"} {
		if !got[ip] {
			t.Fatal("missing real LAN address", ip, topo.LAN)
		}
	}
}

func TestAutomaticLANRetainsMoreThanEightRealAddresses(t *testing.T) {
	addresses := []string{}
	for i := 1; i <= 20; i++ {
		addresses = append(addresses, fmt.Sprintf("10.0.0.%d/24", i))
	}
	stubTopology(t, []ifaceAddrs{topoIface("eth0", addresses...)}, "10.0.0.254", "10.0.0.1")
	topo, err := observeTopology(context.Background(), "[::]:32500", "10.0.0.254", "")
	if err != nil || len(topo.LAN) != 20 {
		t.Fatalf("LAN len=%d incomplete=%v %v", len(topo.LAN), topo.Incomplete, err)
	}
}
