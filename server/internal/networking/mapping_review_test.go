package networking

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

func reviewMapping() Mapping {
	return Mapping{Protocol: "pcp", Client: "192.168.1.20", Gateway: "192.168.1.1", InternalPort: 32500, ExternalPort: 4443, Nonce: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 12)), Epoch: 900, EpochObservedAt: time.Now(), MutationPending: true}
}
func reviewPCPReply(t *testing.T, m Mapping, lease, epoch uint32) []byte {
	t.Helper()
	b, e := pcpRequest(m, lease)
	if e != nil {
		t.Fatal(e)
	}
	b[1] = 129
	binary.BigEndian.PutUint32(b[8:12], epoch)
	ip := netip.MustParseAddr("8.8.4.4").As16()
	copy(b[44:60], ip[:])
	return b
}
func TestW2I02MappingEpochUsesElapsedTimeAndDetectsWrap(t *testing.T) {
	now := time.Now()
	for _, protocol := range []string{"pcp", "natpmp"} {
		m := reviewMapping()
		m.Protocol = protocol
		m.Epoch = 100
		m.EpochObservedAt = now.Add(-30 * time.Minute)
		if mappingEpochReset(m, 1900, now) {
			t.Fatal(protocol, "normal clock treated as reboot")
		}
		if !mappingEpochReset(m, 200, now) {
			t.Fatal(protocol, "larger post-reboot uptime accepted")
		}
		m.Epoch = 0xfffffffe
		m.EpochObservedAt = now.Add(-5 * time.Second)
		if !mappingEpochReset(m, 3, now) {
			t.Fatal(protocol, "wrap accepted")
		}
	}
	m := reviewMapping()
	m.Epoch = 100
	m.EpochObservedAt = now
	if mappingEpochReset(m, 99, now) {
		t.Fatal("PCP's permitted one-second regression rejected")
	}
	if !mappingEpochReset(m, 200, now) {
		t.Fatal("PCP excessive forward jump accepted")
	}
}
func TestW2I02PCPUnsupportedLeaseRetainsActualCleanupIdentity(t *testing.T) {
	m := reviewMapping()
	b := reviewPCPReply(t, m, 172800, 901)
	out, e := parsePCPResponse(m, b, 3600)
	if !errors.Is(e, errMappingResponse) || out.State != "cleanup_pending" || out.MutationPending || out.ExternalAddress != "8.8.4.4" || time.Until(out.ExpiresAt) < 47*time.Hour || out.Nonce != m.Nonce {
		t.Fatalf("lost lease evidence: %+v %v", out, e)
	}
	raw, e := json.Marshal(out)
	if e != nil {
		t.Fatal(e)
	}
	var persisted Mapping
	if e = json.Unmarshal(raw, &persisted); e != nil || persisted.ExpiresAt != out.ExpiresAt.UTC() { // time.Time's monotonic part is intentionally not persisted.
		if e != nil || !persisted.ExpiresAt.Equal(out.ExpiresAt) {
			t.Fatal("lease did not survive persistence", e)
		}
	}
	if b, e = pcpRequest(persisted, 0); e != nil || !bytes.Equal(b[24:36], reviewPCPReply(t, m, 3600, 901)[24:36]) || binary.BigEndian.Uint32(b[4:8]) != 0 {
		t.Fatal("cleanup ownership changed", e)
	}
}
func TestW2I02PCPOptionsAndCommonErrors(t *testing.T) {
	m := reviewMapping()
	b := reviewPCPReply(t, m, 3600, 901)
	if _, e := parsePCPResponse(m, append(append([]byte{}, b...), 128, 0, 0, 1, 7, 0, 0, 0), 3600); e != nil {
		t.Fatal("valid optional option rejected", e)
	}
	for _, tail := range [][]byte{{1, 0, 0, 0}, {128, 0, 0, 5, 1, 2, 3, 4}, {128, 0, 0}} {
		if _, e := parsePCPResponse(m, append(append([]byte{}, b...), tail...), 3600); e == nil {
			t.Fatal("invalid option accepted", tail)
		}
	}
	legacy := []byte{0, 129, 0, 1, 0, 0, 0, 0}
	if _, e := parsePCPResponse(m, legacy, 3600); !errors.Is(e, errMappingUnsupported) {
		t.Fatal("legacy negotiation not recognized", e)
	}
	denied := make([]byte, 24)
	denied[0] = 2
	denied[1] = 129
	denied[3] = 2
	out, e := parsePCPResponse(m, denied, 3600)
	if !errors.Is(e, errMappingDenied) || out.MutationPending != m.MutationPending || out.Nonce != m.Nonce {
		t.Fatal("unowned common error erased earlier intent")
	}
	for _, at := range []int{24, 36, 40} {
		bad := append([]byte{}, b...)
		bad[at] ^= 1
		if _, e := parsePCPResponse(m, bad, 3600); e == nil {
			t.Fatal("reply for another mapping accepted", at)
		}
	}
}
func TestW2I02SOAPOnlyExactActionAcknowledgesMutation(t *testing.T) {
	service := "urn:schemas-upnp-org:service:WANIPConnection:1"
	wrap := func(body string) []byte {
		return []byte(`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body>` + body + `</s:Body></s:Envelope>`)
	}
	response := `<u:DeletePortMappingResponse xmlns:u="` + service + `"/>`
	if _, e := parseUPnPResponse(wrap(response), 200, service, "DeletePortMapping"); e != nil {
		t.Fatal(e)
	}
	invalid := [][]byte{nil, []byte("<html>ok</html>"), wrap(""), wrap(`<u:AddPortMappingResponse xmlns:u="` + service + `"/>`), wrap(response + response), append(wrap(response), []byte("junk")...), wrap(`<u:DeletePortMappingResponse xmlns:u="foreign"/>`), wrap(`<u:DeletePortMappingResponse xmlns:u="` + service + `"><NewLeaseDuration>0</NewLeaseDuration><NewLeaseDuration>1</NewLeaseDuration></u:DeletePortMappingResponse>`)}
	for _, raw := range invalid {
		if _, e := parseUPnPResponse(raw, 200, service, "DeletePortMapping"); e == nil {
			t.Fatalf("false SOAP acknowledgement: %s", raw)
		}
	}
	if _, e := parseUPnPResponse(wrap(response), 500, service, "DeletePortMapping"); e == nil {
		t.Fatal("HTTP error acknowledged")
	}
	fault := `<s:Fault><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>714</errorCode></UPnPError></detail></s:Fault>`
	if _, e := parseUPnPResponse(wrap(fault), 500, service, "GetSpecificPortMappingEntry"); !errors.Is(e, errUPnPMissing) {
		t.Fatal("owned absent-mapping response rejected", e)
	}
}
func TestW2I02NATPMPExactTupleAndNoWildcardDeletion(t *testing.T) {
	m := reviewMapping()
	m.Protocol = "natpmp"
	b := make([]byte, 16)
	b[1] = 130
	binary.BigEndian.PutUint16(b[8:10], uint16(m.InternalPort))
	binary.BigEndian.PutUint16(b[10:12], 4443)
	binary.BigEndian.PutUint32(b[12:16], 3600)
	binary.BigEndian.PutUint32(b[4:8], 901)
	out, e := parseNATPMPMapping(m, b, 3600)
	if e != nil || out.MutationPending || out.State != "mapped" {
		t.Fatal(out, e)
	}
	for _, bad := range [][]byte{b[:15], append(append([]byte{}, b...), 0)} {
		if _, e = parseNATPMPMapping(m, bad, 3600); e == nil {
			t.Fatal("nonexact NAT-PMP response accepted")
		}
	}
	b[8] ^= 1
	if _, e = parseNATPMPMapping(m, b, 3600); e == nil {
		t.Fatal("foreign tuple accepted")
	}
	q := natpmpRequest(m, 0)
	if binary.BigEndian.Uint16(q[4:6]) == 0 || binary.BigEndian.Uint16(q[6:8]) != 0 || binary.BigEndian.Uint32(q[8:12]) != 0 {
		t.Fatal("wildcard cleanup")
	}
}
func TestW2I02GatewayDatagramIgnoresUnrelatedResponses(t *testing.T) {
	// Loopback-only fake, never a LAN gateway or provider operation.
	server, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5351})
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	done := make(chan error, 1)
	go func() {
		b := make([]byte, 128)
		_ = server.SetDeadline(time.Now().Add(time.Second))
		_, peer, e := server.ReadFromUDP(b)
		if e != nil {
			done <- e
			return
		}
		_, e = server.WriteToUDP([]byte("other"), peer)
		if e == nil {
			_, e = server.WriteToUDP([]byte("owned"), peer)
		}
		done <- e
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	m := Mapping{Protocol: "natpmp", Client: "127.0.0.1", Gateway: "127.0.0.1"}
	b, e := gatewayDatagram(ctx, m, []byte("request"), 5, func(b []byte) bool { return string(b) == "owned" })
	if e != nil || string(b) != "owned" {
		t.Fatal(string(b), e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
func TestW2I02PCPPreparationIsReadOnlyAndPreservesPendingOwner(t *testing.T) {
	server, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5351})
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	wire := make(chan []byte, 1)
	go func() {
		b := make([]byte, 128)
		_ = server.SetDeadline(time.Now().Add(time.Second))
		n, peer, e := server.ReadFromUDP(b)
		if e != nil {
			wire <- nil
			return
		}
		wire <- append([]byte{}, b[:n]...)
		response := make([]byte, 24)
		response[0] = 2
		response[1] = 128
		binary.BigEndian.PutUint32(response[8:12], 123)
		_, _ = server.WriteToUDP(response, peer)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	m := Mapping{Protocol: "pcp", Client: "127.0.0.1", Gateway: "127.0.0.1", Nonce: "same-owner"}
	out, e := pcpPrepare(ctx, m)
	if e != nil || out.Epoch != 123 || out.EpochObservedAt.IsZero() || out.Nonce != m.Nonce {
		t.Fatal(out, e)
	}
	b := <-wire
	if len(b) != 24 || b[1] != 0 || binary.BigEndian.Uint32(b[4:8]) != 0 {
		t.Fatal("preparation mutated gateway", b)
	}
	m.MutationPending = true
	cancel()
	out, e = pcpPrepare(ctx, m)
	if e != nil || out != m {
		t.Fatal("uncertain owner reprobed or replaced", out, e)
	}
}

func TestW2I02RetransmittedMAPDenialIsNotConclusiveAbsence(t *testing.T) {
	server, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5351})
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	done := make(chan error, 1)
	go func() {
		b := make([]byte, 128)
		_ = server.SetDeadline(time.Now().Add(2 * time.Second))
		_, _, e := server.ReadFromUDP(b)
		if e != nil {
			done <- e
			return
		} // First request might create a lease, but its reply is lost.
		_, peer, e := server.ReadFromUDP(b)
		if e != nil {
			done <- e
			return
		}
		reply := make([]byte, 16)
		reply[1] = 130
		binary.BigEndian.PutUint16(reply[2:4], 2)
		binary.BigEndian.PutUint16(reply[8:10], 32500)
		_, e = server.WriteToUDP(reply, peer)
		done <- e
	}()
	m := reviewMapping()
	m.Protocol = "natpmp"
	m.Client = "127.0.0.1"
	m.Gateway = "127.0.0.1"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, e := natpmpMap(ctx, m, 3600)
	if !errors.Is(e, errMappingResponse) || errors.Is(e, errMappingDenied) || !out.MutationPending {
		t.Fatal("later denial erased ambiguous lease", out, e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
