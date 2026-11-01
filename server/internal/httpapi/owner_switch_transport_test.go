package httpapi

import (
	"crypto/tls"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/remotegate"
)

// Setup, the setup claim's return and the owner switch share one plain-HTTP rule
// (Justin, 1 Nov): loopback and local-network addresses work, as Unraid and Docker
// installs are reached from another machine on the LAN; a public peer or a public bound
// address never does. Both predicates must agree on every case.
func TestLocalPlainHTTPIsOneRuleForSetupReturnAndOwnerSwitch(t *testing.T) {
	on := remotegate.New()
	on.Set(remotegate.NewPolicy(remotegate.Settings{Security: remotegate.SecurityEnhanced, Access: remotegate.AccessOn}))
	off := remotegate.New()
	off.Set(remotegate.NewPolicy(remotegate.Settings{Security: remotegate.SecurityEnhanced, Access: remotegate.AccessOff}))
	for _, tc := range []struct {
		name, target, origin, peer string
		gate                       *remotegate.Gate
		tls, want                  bool
	}{
		{"literal localhost", "http://localhost:41327/", "http://localhost:41327", "127.0.0.1:1234", nil, false, true},
		{"loopback IPv4", "http://127.0.0.1:41327/", "http://127.0.0.1:41327", "127.0.0.1:1234", nil, false, true},
		{"loopback IPv4 elsewhere in 127/8", "http://127.0.0.2:41327/", "http://127.0.0.2:41327", "127.0.0.2:1234", nil, false, true},
		{"loopback IPv6", "http://[::1]:41327/", "http://[::1]:41327", "[::1]:1234", nil, false, true},
		{"Unraid-style LAN peer", "http://192.168.1.50:32500/", "http://192.168.1.50:32500", "192.168.1.20:51515", nil, false, true},
		{"10/8 LAN", "http://10.0.0.5:32500/", "http://10.0.0.5:32500", "10.0.0.9:1234", nil, false, true},
		{"172.16/12 LAN", "http://172.20.0.5:32500/", "http://172.20.0.5:32500", "172.20.0.1:1234", nil, false, true},
		{"link-local", "http://169.254.10.5:32500/", "http://169.254.10.5:32500", "169.254.10.9:1234", nil, false, true},
		{"IPv6 ULA", "http://[fd00::5]:32500/", "http://[fd00::5]:32500", "[fd00::9]:1234", nil, false, true},
		{"tailnet while remote access is On", "http://100.100.1.1:32500/", "http://100.100.1.1:32500", "100.100.1.2:1234", on, false, true},
		{"tailnet while remote access is Off", "http://100.100.1.1:32500/", "http://100.100.1.1:32500", "100.100.1.2:1234", off, false, false},
		{"LAN address from a public peer", "http://192.168.1.50:32500/", "http://192.168.1.50:32500", "203.0.113.9:1234", nil, false, false},
		{"public bound address from a LAN peer", "http://203.0.113.5:32500/", "http://203.0.113.5:32500", "192.168.1.20:1234", nil, false, false},
		{"network HTTP name from a public peer", "http://media.example.com:32500/", "http://media.example.com:32500", "192.0.2.1:1234", nil, false, false},
		{"localhost from a public socket", "http://localhost:41327/", "http://localhost:41327", "192.0.2.1:1234", nil, false, false},
		{"rebinding name at loopback", "http://evil.localhost:41327/", "http://evil.localhost:41327", "127.0.0.1:1234", nil, false, false},
		{"forged origin", "http://192.168.1.50/", "http://192.168.1.51", "192.168.1.20:1234", nil, false, false},
		{"forged HTTPS origin", "http://media.example.com/", "https://media.example.com", "192.0.2.1:1234", nil, false, false},
		{"actual TLS", "https://127.0.0.1/", "https://127.0.0.1", "192.0.2.1:1234", nil, true, true},
		{"TLS different origin", "https://127.0.0.1/", "https://other.example.com", "192.0.2.1:1234", nil, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Dependencies{RemoteGate: tc.gate}
			r := httptest.NewRequest("POST", tc.target, nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("Origin", tc.origin)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			} else {
				r.TLS = nil
			}
			owner, setup := d.ownerSwitchWebOrigin(r), d.setupWebOrigin(r)
			if owner != setup {
				t.Fatalf("setup and owner switch disagree: setup %q, owner switch %q", setup, owner)
			}
			if (owner != "") != tc.want || (tc.want && owner != tc.origin) {
				t.Fatalf("got %q, want allowed=%v", owner, tc.want)
			}
		})
	}
}

// Untrusted forwarding never grants HTTPS, and a forwarded public client is not local.
func TestLocalPlainHTTPIgnoresUntrustedForwarding(t *testing.T) {
	d := Dependencies{}
	r := httptest.NewRequest("POST", "http://192.168.1.50:32500/", nil)
	r.RemoteAddr = "192.168.1.20:1234"
	r.Header.Set("Origin", "https://192.168.1.50:32500")
	r.Header.Set("X-Forwarded-Proto", "https")
	if got := d.ownerSwitchWebOrigin(r); got != "" {
		t.Fatalf("forwarded HTTPS from an untrusted peer: %q", got)
	}
	r = httptest.NewRequest("POST", "http://192.168.1.50:32500/", nil)
	r.RemoteAddr = "192.168.1.20:1234"
	r.Header.Set("Origin", "http://192.168.1.50:32500")
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := d.setupWebOrigin(r); got != "" {
		t.Fatalf("forwarding headers from an untrusted peer: %q", got)
	}
}

func TestNetworkHTTPPorticoSwitchAndStartOverRefuseBeforePreview(t *testing.T) {
	f := administrationFixtureNew(t)
	for _, body := range []string{`{"to":"portico","startOver":false}`, `{"to":"portico","startOver":true}`} {
		r := httptest.NewRequest("POST", "http://media.example.com/v1/admin/sign-in/move/preview", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+f.owner)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://media.example.com")
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("insecure owner action: %d %s", w.Code, w.Body)
		}
	}
	var moves, claims, challenges int
	if err := f.deps.DB.QueryRow(`SELECT (SELECT count(*) FROM identity_moves),(SELECT count(*) FROM networking_claim_intents),(SELECT count(*) FROM hosted_identity_challenges)`).Scan(&moves, &claims, &challenges); err != nil || moves != 0 || claims != 0 || challenges != 0 {
		t.Fatal("insecure refusal created preview/private claim/challenge state", moves, claims, challenges, err)
	}
}
