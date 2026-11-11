package publicaddr

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"
)

// These tests never ask a real outside service: every finder here asks fakes.

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func fakes(answers map[string]string) *Finder {
	var services []Service
	for name := range answers {
		services = append(services, Service{Name: name, Timeout: time.Second})
	}
	return WithServices(quiet, services, func(_ context.Context, s Service) (netip.Addr, error) {
		if answers[s.Name] == "" {
			return netip.Addr{}, errors.New("no answer")
		}
		return netip.ParseAddr(answers[s.Name])
	})
}

// An address is believed when two services give it, not when one does; private and shared
// (CGNAT) answers count for nothing; and a round where nobody agrees keeps the last address
// and says why.
func TestTwoServicesMustAgree(t *testing.T) {
	ctx := t.Context()
	f := fakes(map[string]string{"A": "198.51.100.7", "B": "198.51.100.7", "C": "203.0.113.5", "D": ""})
	var told []string
	f.OnChange = func(a string) { told = append(told, a) }
	f.round(ctx)
	if a := f.Answer(); a.Address != "198.51.100.7" || len(a.AgreedBy) != 2 || a.Problem != "" {
		t.Fatalf("two agreeing: %+v", a)
	}
	f.services, f.ask = []Service{{Name: "A", Timeout: time.Second}, {Name: "B", Timeout: time.Second}, {Name: "C", Timeout: time.Second}},
		func(_ context.Context, s Service) (netip.Addr, error) {
			return netip.ParseAddr(map[string]string{"A": "192.168.1.1", "B": "100.64.0.9", "C": "203.0.113.5"}[s.Name])
		}
	f.round(ctx)
	if a := f.Answer(); a.Address != "198.51.100.7" || a.Problem == "" {
		t.Fatalf("nobody agreeing: %+v", a)
	}
	if len(told) != 1 {
		t.Fatalf("told of %q", told)
	}
}

// The real services are never asked from a test, whoever forgets to fake them.
func TestRealServicesRefusedInTests(t *testing.T) {
	f := New(quiet)
	f.round(t.Context())
	if a := f.Answer(); a.Problem != errInTest.Error() {
		t.Fatalf("a test reached the real services: %+v", a)
	}
}

// A STUN answer is read only when it answers this request.
func TestParseSTUN(t *testing.T) {
	tx := []byte("abcdefghijkl")
	b := make([]byte, 32)
	binary.BigEndian.PutUint16(b[0:], 0x0101)
	binary.BigEndian.PutUint16(b[2:], 12)
	binary.BigEndian.PutUint32(b[4:], stunCookie)
	copy(b[8:], tx)
	binary.BigEndian.PutUint16(b[20:], 0x0020)
	binary.BigEndian.PutUint16(b[22:], 8)
	b[25] = 0x01
	ip := [4]byte{198, 51, 100, 7}
	var cookie [4]byte
	binary.BigEndian.PutUint32(cookie[:], stunCookie)
	for i := range ip {
		b[28+i] = ip[i] ^ cookie[i]
	}
	if addr, err := parseSTUN(b, tx); err != nil || addr.String() != "198.51.100.7" {
		t.Fatalf("parsed %v %v", addr, err)
	}
	if _, err := parseSTUN(b, []byte("another req.")); err == nil {
		t.Fatal("an answer to another request was read")
	}
}
