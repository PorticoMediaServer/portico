package networking

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"net/netip"
	"testing"
	"time"
)

func protocolMapping() Mapping {
	return Mapping{Protocol: "pcp", Client: "192.168.1.20", Gateway: "192.168.1.1", InternalPort: 32500, ExternalPort: 4443, Nonce: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 12)), Epoch: 900}
}
func TestP09PCPExactNoncePortProtocolAndFiniteLease(t *testing.T) {
	m := protocolMapping()
	q, e := pcpRequest(m, 3600)
	if e != nil || len(q) != 60 || q[36] != 6 || binary.BigEndian.Uint16(q[40:42]) != 32500 {
		t.Fatal("invalid PCP request", e)
	}
	b := append([]byte(nil), q...)
	b[1] = 129
	binary.BigEndian.PutUint32(b[8:12], 2)
	ip := netip.MustParseAddr("8.8.4.4").As16()
	copy(b[44:60], ip[:])
	out, e := parsePCPResponse(m, b, 3600)
	if e != nil || out.EpochGeneration != 1 || out.ExternalAddress != "8.8.4.4" || out.State != "mapped" || !out.RenewAt.Before(out.ExpiresAt) {
		t.Fatal(out, e)
	}
	for _, at := range []int{24, 36, 40} {
		bad := append([]byte(nil), b...)
		bad[at] ^= 1
		if _, e = parsePCPResponse(m, bad, 3600); e == nil {
			t.Fatalf("accepted mismatched field %d", at)
		}
	}
	bad := append([]byte(nil), b...)
	binary.BigEndian.PutUint32(bad[4:8], 0xffffffff)
	if _, e = parsePCPResponse(m, bad, 3600); e == nil {
		t.Fatal("accepted unbounded lease")
	}
	del, e := pcpRequest(m, 0)
	if e != nil || binary.BigEndian.Uint32(del[4:8]) != 0 || !bytes.Equal(del[24:36], q[24:36]) {
		t.Fatal("delete lost exact nonce")
	}
}
func TestP09NATPMPNeverUsesAllPortsDeletion(t *testing.T) {
	m := protocolMapping()
	q := natpmpRequest(m, 0)
	if binary.BigEndian.Uint16(q[4:6]) != 32500 || binary.BigEndian.Uint16(q[6:8]) != 0 || binary.BigEndian.Uint32(q[8:12]) != 0 {
		t.Fatal("unsafe NAT-PMP delete")
	}
	b := make([]byte, 16)
	b[1] = 130
	binary.BigEndian.PutUint16(b[8:10], 32500)
	binary.BigEndian.PutUint16(b[10:12], 4443)
	binary.BigEndian.PutUint32(b[12:16], 3600)
	if _, e := parseNATPMPMapping(m, b, 3600); e != nil {
		t.Fatal(e)
	}
	b[8] ^= 1
	if _, e := parseNATPMPMapping(m, b, 3600); e == nil {
		t.Fatal("foreign internal port accepted")
	}
}
func TestP09GatewayURLCannotEscapeSelectedRouter(t *testing.T) {
	if _, e := gatewayURL("http://192.168.1.1:1900/control", "192.168.1.1"); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{"http://127.0.0.1/control", "http://169.254.169.254/control", "http://attacker.example/control", "http://u:p@192.168.1.1/control", "file:///etc/passwd", "http://192.168.1.1/control#fragment"} {
		if _, e := gatewayURL(raw, "192.168.1.1"); e == nil {
			t.Fatal("unsafe gateway URL", raw)
		}
	}
}
func TestP09NATAndDefaultPortDiagnostics(t *testing.T) {
	for _, raw := range []string{"100.64.0.1", "10.0.0.1", "169.254.169.254", "198.51.100.1", "2001:db8::1"} {
		if publicAddress(netip.MustParseAddr(raw)) {
			t.Fatal("not a usable public address", raw)
		}
	}
	if routeURL(netip.MustParseAddr("fd00::1"), 80, "http") != "http://[fd00::1]" || routeURL(netip.MustParseAddr("8.8.8.8"), 443, "https") != "https://8.8.8.8" {
		t.Fatal("noncanonical default port")
	}
	m, e := grantedMapping(protocolMapping(), 3600, 901)
	if e != nil || time.Until(m.ExpiresAt) > time.Hour+time.Second {
		t.Fatal("invalid expiry", e)
	}
}
func TestP09DNSCompressionAndMTUBounds(t *testing.T) {
	for _, b := range [][]byte{{0xc0, 0}, {0xc0}, {64, 1, 2}, {0xc0, 0xff}} {
		if _, _, ok := dnsReadName(b, 0); ok {
			t.Fatalf("accepted malformed name %x", b)
		}
	}
	b := dnsName("_portico._tcp.local.")
	name, n, ok := dnsReadName(b, 0)
	if !ok || n != len(b) || name != discoveryService {
		t.Fatal(name, n, ok)
	}
	s := discoverySet{instance: "Media._portico._tcp.local.", host: "portico-test.local.", records: []discoveryRecord{{name: discoveryService, kind: 12, data: dnsName("Media._portico._tcp.local.")}}}
	packet := s.packet(120, false)
	if len(packet) > 1472 || binary.BigEndian.Uint16(packet[6:8]) != 1 {
		t.Fatal("invalid mDNS packet")
	}
	if s.requested([]byte{1, 2}) {
		t.Fatal("accepted truncated DNS query")
	}
}
