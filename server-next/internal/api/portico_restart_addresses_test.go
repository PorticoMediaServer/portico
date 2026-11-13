package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
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
