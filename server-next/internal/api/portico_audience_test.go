package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/trust"
)

func TestReturnedSignInNoteWorksOnlyAtItsOwnHTTPSPageOnce(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	settings, _ := p.s.access.current()
	settings.OwnAddress = OwnAddress{Enabled: true, Domain: "media.example"}
	settings.RemoteSecurity = "standard"
	settings.PublicPort = 8443
	p.s.access.set(settings, true)
	audience := "https://media.example:8443"
	challenge := p.challenge()
	note := p.fake.Sign(trust.Identity{Kind: trust.IdentityKind, AccountID: accountJustin.ID, Username: accountJustin.Username,
		ServerID: p.s.id, Challenge: challenge, Nonce: "n", IssuedAt: p.clock.Now(), ExpiresAt: p.clock.Now().Add(5 * time.Minute), Audience: audience, Pictures: []trust.Picture{}})
	identity, _ := json.Marshal(note)
	body, _ := json.Marshal(PorticoExchange{Challenge: challenge, Identity: identity, Device: device(23)})
	exchange := func(address, origin string) int {
		t.Helper()
		r := httptest.NewRequest("POST", address+"/v1/auth/portico/exchange", bytes.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		p.s.ServeHTTP(w, r)
		return w.Code
	}
	for _, attempt := range [][2]string{{audience, ""}, {audience, "https://web.getportico.tv"}, {audience, "https://media.example.evil:8443"}, {"https://media.example:8444", audience}, {"https://other.example:8443", audience}, {"http://media.example:8443", audience}} {
		if status := exchange(attempt[0], attempt[1]); status < 400 {
			t.Fatalf("a note escaped its page at %q from %q: %d", attempt[0], attempt[1], status)
		}
	}
	if status := exchange(audience, audience); status != 200 {
		t.Fatalf("own page: %d", status)
	}
	if status := exchange(audience, audience); status < 400 {
		t.Fatal("returned note worked twice")
	}
	// A normal note remains usable by the hosted app, without the server-page origin requirement.
	if code, _, _ := p.signIn(accountJustin, "", 24); code != 200 {
		t.Fatalf("normal note: %d", code)
	}
}

func TestReturnedSignInAudienceMustBeAnAddressTheServerServes(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s))
	settings, _ := s.access.current()
	settings.OwnAddress = OwnAddress{Enabled: true, Domain: "media.example"}
	settings.RemoteSecurity = "standard"
	s.access.set(settings, true)
	r := httptest.NewRequest("POST", "https://foreign.example/v1/auth/portico/exchange", nil)
	r.Header.Set("Origin", "https://foreign.example")
	if s.porticoAudienceAllowed(r, "https://foreign.example") {
		t.Fatal("a request Host was treated as a configured served address")
	}
	r = httptest.NewRequest("POST", "http://foreign.example/v1/auth/portico/exchange", nil)
	r.Header.Set("Origin", "https://media.example")
	r.Header.Set("X-Forwarded-Host", "media.example")
	r.Header.Set("X-Forwarded-Proto", "https")
	if s.porticoAudienceAllowed(r, "https://media.example") {
		t.Fatal("an untrusted forwarding header vouched for HTTPS")
	}
	r.RemoteAddr = "127.0.0.1:1234"
	if w := call(t, s, "PUT", "/v1/admin/settings/network", owner.AccessToken, NetworkSettingsEdit{TrustedProxies: []string{"127.0.0.1/32"}}, nil); w.Code != 200 {
		t.Fatalf("trusting the local test proxy: %d", w.Code)
	}
	if !s.porticoAudienceAllowed(r, "https://media.example") {
		t.Fatal("the trusted proxy's real HTTPS host was refused")
	}
}
