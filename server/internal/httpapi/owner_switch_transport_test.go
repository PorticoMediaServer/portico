package httpapi

import (
	"crypto/tls"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOwnerSwitchTransportRequiresActualHTTPSOrLiteralLocalhost(t *testing.T) {
	d := Dependencies{}
	for _, tc := range []struct {
		name, target, origin, peer string
		tls, want                  bool
	}{
		{"network HTTP", "http://media.example.com:32500/", "http://media.example.com:32500", "192.0.2.1:1234", false, false},
		{"forged origin", "http://media.example.com/", "https://media.example.com", "192.0.2.1:1234", false, false},
		{"absent origin is not app proof", "http://media.example.com/", "", "192.0.2.1:1234", false, false},
		{"literal localhost", "http://localhost:41327/", "http://localhost:41327", "127.0.0.1:1234", false, true},
		{"loopback numeric is not literal localhost", "http://127.0.0.1:41327/", "http://127.0.0.1:41327", "127.0.0.1:1234", false, false},
		{"IPv6 numeric is not literal localhost", "http://[::1]:41327/", "http://[::1]:41327", "[::1]:1234", false, false},
		{"localhost suffix", "http://evil.localhost:41327/", "http://evil.localhost:41327", "127.0.0.1:1234", false, false},
		{"localhost remote socket", "http://localhost:41327/", "http://localhost:41327", "192.0.2.1:1234", false, false},
		{"actual TLS", "https://media.example.com/", "https://media.example.com", "192.0.2.1:1234", true, true},
		{"TLS different origin", "https://media.example.com/", "https://other.example.com", "192.0.2.1:1234", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", tc.target, nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-Forwarded-Proto", "https") // Untrusted forwarding never grants HTTPS.
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			} else {
				r.TLS = nil
			}
			if got := d.ownerSwitchWebOrigin(r); (got != "") != tc.want || (tc.want && got != tc.origin) {
				t.Fatalf("transport prerequisite: got %q, want allowed=%v", got, tc.want)
			}
		})
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
