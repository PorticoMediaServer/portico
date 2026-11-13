package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
	"github.com/PorticoMediaServer/portico/server-next/internal/publicaddr"
)

func TestRestartKeepsPublishedAddresses(t *testing.T) {
	p := newPorticoRig(t)
	addresses := []hosted.Address{{Address: "192.168.1.20", Port: 32500}, {Address: "203.0.113.7", Port: 40123}}
	p.s.link.published = func() []hosted.Address { return addresses }
	p.link(accountJustin)
	held := func() string {
		t.Helper()
		raw, err := json.Marshal(p.fake.Document(p.s.id)["addresses"])
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	want := held()
	sent := p.fake.Count("PUT", "/document")
	p.clock.Add(time.Hour)
	p.open()
	p.s.link.published = p.s.porticoPublished
	// The listener and public-address finder have not started yet. Restarting must
	// not replace the addresses Services already holds with this temporary empty list.
	p.run(documentGap)
	p.s.access.mu.Lock()
	p.s.access.networkStarted, p.s.access.listenPort = true, 32500
	p.s.access.mu.Unlock()
	p.run(addressGap)
	if got := held(); got != want || p.fake.Count("PUT", "/document") != sent {
		t.Fatalf("restart replaced published addresses: got %s, want %s", got, want)
	}
	// Discovery finishes with a changed address: it is still published normally,
	// without the owner re-saving any settings.
	addresses[1].Address = "203.0.113.8"
	p.s.link.published = func() []hosted.Address { return addresses }
	p.step()
	want = strings.Replace(want, "203.0.113.7", "203.0.113.8", 1)
	if got := held(); got != want || p.fake.Count("PUT", "/document") != sent+1 {
		t.Fatalf("discovered addresses were not published after restart: %s", got)
	}
}

func TestRestartWithdrawsPublicAddressAfterFailedDiscovery(t *testing.T) {
	p := newPorticoRig(t)
	p.s.link.published = func() []hosted.Address {
		return []hosted.Address{{Address: "192.168.1.20", Port: 32500}, {Address: "203.0.113.7", Port: 40123}}
	}
	p.link(accountJustin)
	sent := p.fake.Count("PUT", "/document")
	p.clock.Add(time.Hour)
	p.open()
	p.s.link.published = p.s.porticoPublished
	p.s.access.mu.Lock()
	p.s.access.networkStarted, p.s.access.listenPort = true, 32500
	p.s.access.mu.Unlock()

	asked := make(chan struct{}, 1)
	finder := publicaddr.WithServices(p.s.log, []publicaddr.Service{{Name: "blocked", Timeout: time.Second}},
		func(context.Context, publicaddr.Service) (netip.Addr, error) {
			select {
			case asked <- struct{}{}:
			default:
			}
			return netip.Addr{}, errors.New("address service unreachable")
		})
	p.s.public = finder
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	done := make(chan struct{})
	go func() { defer close(done); finder.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	finder.Set(true)
	select {
	case <-asked:
	case <-ctx.Done():
		t.Fatal("the fake address lookup did not start")
	}
	answer := finder.CheckNow(ctx)
	if answer.CheckedAt.IsZero() || answer.Address != "" || answer.Problem == "" {
		t.Fatalf("the failed lookup did not finish: %+v", answer)
	}

	p.run(addressGap)
	if p.fake.Count("PUT", "/document") != sent+1 {
		t.Fatal("a completed failed lookup left Services holding the old public address")
	}
	for _, raw := range p.fake.Document(p.s.id)["addresses"].([]any) {
		address := raw.(map[string]any)["address"].(string)
		if !netip.MustParseAddr(address).IsPrivate() {
			t.Fatalf("after failed discovery Services still publishes a public address: %s", address)
		}
	}
}
