package main

import (
	"net"
	"testing"
)

// ONB-02 item 2: the default bind is LAN-reachable. Setup still accepts only
// private peers and public traffic still requires TLS, so reachability does
// not widen trust (see privateSetupPeer and the DirectListener gate in run).
func TestDefaultBindIsLANReachable(t *testing.T) {
	if defaultBind != ":32500" {
		t.Fatalf("default bind is %q, want LAN-reachable :32500", defaultBind)
	}
	if v := env("PORTICO_BIND", defaultBind); v != defaultBind && env("PORTICO_BIND", "") == "" {
		t.Fatalf("env fallback did not apply: %q", v)
	}
}

// ONB-02 item 1: the setup block names a real private LAN address when one
// exists, and never names loopback, link-local or unspecified addresses.
func TestFirstPrivateLANAddressIsPrivateOrEmpty(t *testing.T) {
	addr := firstPrivateLANAddress()
	if addr == "" {
		t.Skip("no private LAN address on this machine")
	}
	ip := net.ParseIP(addr)
	if ip == nil || ip.To4() == nil {
		t.Fatalf("not an IPv4 address: %q", addr)
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || !ip.IsPrivate() {
		t.Fatalf("not a private LAN address: %q", addr)
	}
}

// ONB-02 item 4: a state directory this process created never warns about
// foreign schema objects; an existing directory with unknowns still warns,
// and an empty check never warns either way.
func TestFreshStateDirDowngradesForeignSchemaWarning(t *testing.T) {
	defer func() { stateDirFreshThisProcess = false }()
	stateDirFreshThisProcess = false
	if warnForeignSchema(nil) {
		t.Fatal("empty check warned")
	}
	if !warnForeignSchema([]string{"table networking_claim_authority"}) {
		t.Fatal("existing directory with unknowns did not warn")
	}
	stateDirFreshThisProcess = true
	if warnForeignSchema([]string{"table networking_claim_authority"}) {
		t.Fatal("fresh directory warned about its own objects")
	}
	if warnForeignSchema(nil) {
		t.Fatal("fresh empty check warned")
	}
}
