package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/netip"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/publicaddr"
)

func TestPublicSystemNamesOnlyTheAvailablePorticoHTTPSOrigin(t *testing.T) {
	s := newTestServer(t)
	onMainProfile(t, s)
	found := make(chan struct{})
	finder := publicaddr.WithServices(s.log, []publicaddr.Service{{Name: "one", Timeout: time.Second}, {Name: "two", Timeout: time.Second}},
		func(context.Context, publicaddr.Service) (netip.Addr, error) {
			return netip.MustParseAddr("198.51.100.7"), nil
		})
	finder.OnChange = func(string) { close(found) }
	s.public = finder
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); finder.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	finder.Set(true)
	select {
	case <-found:
	case <-time.After(3 * time.Second):
		t.Fatal("the stand-ins did not supply the public address")
	}
	s.access.mu.Lock()
	s.access.listenPort = 32500
	s.access.mu.Unlock()
	settings, _ := s.access.current()
	settings.RemoteAccess, settings.PublicPort = "on", 32510
	s.access.set(settings, true)
	check := func(want string) {
		t.Helper()
		// Signed out and plain HTTP: the page needs this before it can offer the secure address.
		w := reach(s, "GET", "/v1/system", "", "127.0.0.1", "untrusted.example:32500", false, nil)
		if w.Code != 200 {
			t.Fatalf("system answered %d", w.Code)
		}
		var answer map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
			t.Fatal(err)
		}
		raw, present := answer["porticoOrigin"]
		if want == "" {
			if present {
				t.Fatalf("unavailable origin was published: %s", raw)
			}
			return
		}
		var got string
		if err := json.Unmarshal(raw, &got); err != nil || got != want {
			t.Fatalf("origin %q, want %q: %v", got, want, err)
		}
	}
	check("")
	s.SetPorticoZone("abc123.portico.direct", func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &tls.Certificate{}, nil })
	check("https://198-51-100-7.abc123.portico.direct:32510")
	settings.RemoteAccess = "off"
	s.access.set(settings, true)
	check("")
	settings.RemoteAccess, settings.PublicPort = "on", 443
	s.access.set(settings, true)
	check("https://198-51-100-7.abc123.portico.direct")
	s.SetPorticoZone("", nil)
	check("")
}
