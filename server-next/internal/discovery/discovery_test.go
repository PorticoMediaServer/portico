package discovery

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"
)

func query(names ...string) []byte {
	p := make([]byte, 12)
	binary.BigEndian.PutUint16(p[4:], uint16(len(names)))
	for _, n := range names {
		p = append(p, name(n)...)
		p = append(p, 0, 12, 0, 1)
	}
	return p
}

// An app at home that asks for Portico servers is answered with the address, port, server id
// and fingerprint, and never the server's name; a question about something else is ignored.
func TestAnnouncement(t *testing.T) {
	r := Record{ServerID: "srv_Wl4E3ID18LNZFgw_Gjc3R0liIOV67Gd2vD54Ubw6OAY", Fingerprint: "Yw3NKWbEM2aRElRIu7JbT_QSpJxzLbLIq8G4WBvXEN0", Port: 32500}
	if !asked(query("_portico._tcp.local."), r) || !asked(query("_printer._tcp.local.", "portico-yw3nkwbe.local."), r) {
		t.Fatal("a question for this server was not recognised")
	}
	if asked(query("_printer._tcp.local."), r) || asked(answer(r, netip.MustParseAddr("192.168.1.20"), ttl), r) {
		t.Fatal("a question for something else, or another answer, was taken as a question for this server")
	}
	a := answer(r, netip.MustParseAddr("192.168.1.20"), ttl)
	for _, want := range [][]byte{[]byte("id=" + r.ServerID), []byte("fp=" + r.Fingerprint), {192, 168, 1, 20}, {0x7e, 0xf4}} {
		if !bytes.Contains(a, want) {
			t.Errorf("the answer lacks %q", want)
		}
	}
	if count := binary.BigEndian.Uint16(a[6:]); count != 4 {
		t.Errorf("the answer has %d records, want 4", count)
	}
	// The answer must read back as the names it says.
	at := 12
	for range 4 {
		_, end, ok := readName(a, at)
		if !ok {
			t.Fatal("the answer holds a name that cannot be read")
		}
		at = end + 8
		at += 2 + int(binary.BigEndian.Uint16(a[at:]))
	}
	if at != len(a) {
		t.Fatalf("the answer's records end at %d of %d bytes", at, len(a))
	}
}
