package networking

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"
	"time"
)

type wanMapper struct {
	address string
	err     error
	calls   int
}

func (m *wanMapper) Prepare(_ context.Context, in Mapping) (Mapping, error) {
	m.calls++
	in.ExternalAddress = m.address
	return in, m.err
}
func (m *wanMapper) Map(_ context.Context, in Mapping, _ uint32) (Mapping, error) {
	return in, errors.New("the observer must never create a mapping")
}

func stunReply(transaction []byte, kind uint16, ip [4]byte) []byte {
	b := make([]byte, 32)
	binary.BigEndian.PutUint16(b[0:], 0x0101)
	binary.BigEndian.PutUint16(b[2:], 12)
	binary.BigEndian.PutUint32(b[4:], stunMagicCookie)
	copy(b[8:20], transaction)
	binary.BigEndian.PutUint16(b[20:], kind)
	binary.BigEndian.PutUint16(b[22:], 8)
	b[25] = 0x01
	if kind == 0x0020 {
		var cookie [4]byte
		binary.BigEndian.PutUint32(cookie[:], stunMagicCookie)
		for i := range ip {
			ip[i] ^= cookie[i]
		}
	}
	copy(b[28:], ip[:])
	return b
}

func TestParseSTUNAddress(t *testing.T) {
	transaction := []byte("abcdefghijkl")
	want := netip.MustParseAddr("156.57.135.50")
	for _, kind := range []uint16{0x0020, 0x0001} {
		got, e := parseSTUNAddress(stunReply(transaction, kind, want.As4()), transaction)
		if e != nil || got != want {
			t.Fatal(kind, got, e)
		}
	}
	if _, e := parseSTUNAddress(stunReply([]byte("zzzzzzzzzzzz"), 0x0020, want.As4()), transaction); e == nil {
		t.Fatal("a reply to another transaction was accepted")
	}
	short := stunReply(transaction, 0x0020, want.As4())
	binary.BigEndian.PutUint16(short[22:], 200)
	if _, e := parseSTUNAddress(short, transaction); e == nil {
		t.Fatal("an attribute longer than the message was accepted")
	}
}

func TestWANObserverPrefersTheRouterAndLimitsOutsideLookups(t *testing.T) {
	topo := Topology{Gateway: "192.168.2.1", LocalAddress: "192.168.2.100"}
	now := time.Unix(1_800_000_000, 0)
	lookups, fetches := 0, 0
	mapper := &wanMapper{address: "156.57.135.50"}
	o := &WANObserver{mapper: mapper, stun: []string{"a", "b"}, http: []string{"c"}, now: func() time.Time { return now },
		lookup: func(context.Context, string) (netip.Addr, error) {
			lookups++
			return netip.MustParseAddr("8.8.4.4"), nil
		},
		fetch: func(context.Context, string) (netip.Addr, error) {
			fetches++
			return netip.MustParseAddr("9.9.9.9"), nil
		}}
	if ip, source := o.Observe(context.Background(), topo, 32500, false); ip.String() != "156.57.135.50" || source != "gateway" || lookups != 0 {
		t.Fatal(ip, source, lookups)
	}
	// A private "external" address is CGNAT or double NAT; an outside observer decides.
	mapper.address = "100.64.0.9"
	if ip, source := o.Observe(context.Background(), topo, 32500, false); ip.String() != "8.8.4.4" || source != "stun" || lookups != 1 || fetches != 0 {
		t.Fatal(ip, source, lookups, fetches)
	}
	now = now.Add(time.Minute)
	if ip, source := o.Observe(context.Background(), topo, 32500, false); ip.String() != "8.8.4.4" || source != "cached" || lookups != 1 {
		t.Fatal("outside services were asked again inside the interval", ip, source, lookups)
	}
	if _, source := o.Observe(context.Background(), topo, 32500, true); source != "stun" || lookups != 2 {
		t.Fatal("a topology change must ask at once", source, lookups)
	}
	// STUN blocked: fall back to HTTPS; everything failing keeps the last good answer.
	o.lookup = func(context.Context, string) (netip.Addr, error) { return netip.Addr{}, errors.New("blocked") }
	if ip, source := o.Observe(context.Background(), topo, 32500, true); ip.String() != "9.9.9.9" || source != "http" {
		t.Fatal(ip, source)
	}
	o.fetch = func(context.Context, string) (netip.Addr, error) { return netip.Addr{}, errors.New("down") }
	// Everything failing keeps the last good answer, and says so: "unavailable" is an attempt
	// that learned nothing, which is not the same as the rate limit's "cached" memory. Only
	// the first is a pass on which a family could be called absent, and neither is evidence
	// that the address itself has gone.
	if ip, source := o.Observe(context.Background(), topo, 32500, true); ip.String() != "9.9.9.9" || source != "unavailable" {
		t.Fatal(ip, source)
	}
	if !WANSourceAttempted("unavailable") || WANSourceFresh("unavailable") || WANSourceAttempted("cached") || !WANSourceFresh("stun") || !WANSourceFresh("gateway") || !WANSourceFresh("http") || WANSourceAttempted("") {
		t.Fatal("a source was classified wrongly")
	}
}
