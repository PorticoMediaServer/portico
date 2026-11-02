package httpapi

import (
	"bytes"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestBrowserSetupLocalOriginAndSingleUse(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	operations.WireSetupRemote(id)
	host, err := hosted.New(db, id, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	h := New(Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Hosted: host, SetupHosts: []string{"demo.example"}, Origins: []string{"http://127.0.0.1:19412", "https://web.getportico.tv"}})
	call := func(address, origin, peer, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", address+path, bytes.NewReader(raw))
		r.RemoteAddr = peer
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	// Before anyone owns it, setup is open to this machine and the local network
	// (PLEX-SETUP-NOTES S1), at a literal/local address or an independently configured
	// authority; cross-origin API permission never authorizes setup.
	for _, tc := range []struct{ name, address, origin, peer string }{
		{"LAN peer", "http://127.0.0.1:32500", "http://127.0.0.1:32500", "192.168.1.2:5000"},
		{"Tailscale peer", "https://demo.example", "https://demo.example", "100.101.102.103:5000"},
		{"IPv6 ULA peer", "http://127.0.0.1:32500", "http://127.0.0.1:32500", "[fd00::5]:5000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := call(tc.address, tc.origin, tc.peer, "/v1/setup/browser", map[string]any{}); w.Code != http.StatusOK {
				t.Fatalf("got status %d", w.Code)
			}
		})
	}
	// A public-internet source is refused, honestly and by name, whatever its scheme:
	// from outside, setup needs the setup code from the server's log (Justin, 1 Nov).
	for _, peer := range []string{"198.51.100.8:5000", "[2001:db8::8]:5000"} {
		w := call("https://demo.example", "https://demo.example", peer, "/v1/setup/browser", map[string]any{})
		if w.Code != http.StatusForbidden || !bytes.Contains(w.Body.Bytes(), []byte(`"setup_code_required"`)) {
			t.Fatalf("a public source %s started setup: %d %s", peer, w.Code, w.Body.String())
		}
	}
	// A forwarded header from an untrusted peer can't make a public source local.
	spoof := httptest.NewRequest("POST", "https://demo.example/v1/setup/browser", bytes.NewReader([]byte(`{}`)))
	spoof.RemoteAddr = "198.51.100.8:5000"
	spoof.Header.Set("Origin", "https://demo.example")
	spoof.Header.Set("Content-Type", "application/json")
	spoof.Header.Set("X-Forwarded-For", "192.168.1.2")
	if w := httptest.NewRecorder(); func() int { h.ServeHTTP(w, spoof); return w.Code }() != http.StatusForbidden {
		t.Fatal("a spoofed forwarded address made a public source local")
	}
	// Behind a reverse proxy nobody could trust yet, every visitor looks like the proxy's LAN
	// address: forwarding headers from an untrusted local peer mean "not local" (S1).
	for _, header := range []string{"X-Forwarded-For", "Forwarded", "X-Real-Ip", "Via"} {
		proxied := httptest.NewRequest("POST", "http://127.0.0.1:32500/v1/setup/browser", bytes.NewReader([]byte(`{}`)))
		proxied.RemoteAddr = "192.168.1.5:5000"
		proxied.Header.Set("Origin", "http://127.0.0.1:32500")
		proxied.Header.Set("Content-Type", "application/json")
		proxied.Header.Set(header, "198.51.100.8")
		if w := httptest.NewRecorder(); func() int { h.ServeHTTP(w, proxied); return w.Code }() != http.StatusForbidden {
			t.Fatalf("a proxied public visitor (%s) started setup", header)
		}
	}
	for _, tc := range []struct{ name, address, origin, peer string }{
		{"hosted origin", "http://127.0.0.1:32500", "https://web.getportico.tv", "127.0.0.1:5000"},
		{"cross-site origin", "http://127.0.0.1:32500", "http://evil.example", "127.0.0.1:5000"},
		{"missing origin", "http://127.0.0.1:32500", "", "127.0.0.1:5000"},
		{"unconfigured local origin", "http://127.0.0.1:32500", "http://localhost:9999", "127.0.0.1:5000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := call(tc.address, tc.origin, tc.peer, "/v1/setup/browser", map[string]any{}); w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
				t.Fatalf("got status %d", w.Code)
			}
		})
	}
	w := call("http://127.0.0.1:32500", "http://127.0.0.1:32500", "127.0.0.1:5000", "/v1/setup/browser", map[string]any{})
	if w.Code != 200 {
		t.Fatalf("bootstrap %d", w.Code)
	}
	var approval struct {
		ServerID string `json:"serverId"`
		Token    string `json:"setupToken"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &approval); err != nil || approval.ServerID != id.ID() || len(approval.Token) != 43 {
		t.Fatal("invalid bootstrap")
	}
	w = call("http://127.0.0.1:32500", "http://127.0.0.1:32500", "127.0.0.1:5000", "/v1/setup", map[string]any{"requestId": "33333333-3333-4333-8333-333333333333", "setupToken": approval.Token, "username": "owner", "password": "A-long-local-password", "name": "My media", "authMode": "local", "interactive": true})
	if w.Code != 201 {
		t.Fatalf("setup %d", w.Code)
	}
	var session identity.Envelope
	if err = json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:32500/v1/setup/state", nil)
	r.Header.Set("Authorization", "Bearer "+session.AccessToken)
	r.RemoteAddr = "127.0.0.1:5000"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var state SetupStateResponse
	if err = json.Unmarshal(w.Body.Bytes(), &state); err != nil || w.Code != 200 || state.Status != "complete" || state.Setup != nil || state.RetryAfterSeconds != 0 {
		t.Fatalf("a local server is complete once its owner exists, and an owner's token reveals nothing more: %d %s", w.Code, w.Body.String())
	}
	w = call("http://127.0.0.1:32500", "http://127.0.0.1:32500", "127.0.0.1:5000", "/v1/setup/browser", map[string]any{})
	if w.Code != 409 {
		t.Fatalf("initialized server exposes bootstrap: %d", w.Code)
	}
}

func TestAppSetupWithoutOriginAndNeverFromABrowser(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	operations.WireSetupRemote(id)
	host, err := hosted.New(db, id, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	h := New(Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Hosted: host, Origins: []string{"https://web.getportico.tv"}})
	call := func(path string, headers map[string]string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "http://192.168.1.20:32500"+path, bytes.NewReader(raw))
		r.RemoteAddr = "192.168.1.30:5000"
		r.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	// A browser always names its origin on a POST: even this server's own page is refused here
	// (it has its own route), and so are other sites, a fetch that says it is a browser's, and a
	// body that is not JSON.
	for name, headers := range map[string]map[string]string{
		"own origin":     {"Origin": "http://192.168.1.20:32500"},
		"another site":   {"Origin": "http://evil.example"},
		"hosted origin":  {"Origin": "https://web.getportico.tv"},
		"browser fetch":  {"Sec-Fetch-Mode": "cors"},
		"not JSON":       {"Content-Type": "text/plain"},
		"cross-site tag": {"Sec-Fetch-Site": "cross-site"},
	} {
		if w := call("/v1/setup/app", headers, map[string]any{}); w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden && w.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("%s: got status %d", name, w.Code)
		}
	}
	// An app reaches the server at a local address, never through a name that rebinds to it.
	rebound := httptest.NewRequest("POST", "http://media.example.com/v1/setup/app", bytes.NewReader([]byte(`{}`)))
	rebound.RemoteAddr = "192.168.1.30:5000"
	rebound.Header.Set("Content-Type", "application/json")
	if w := httptest.NewRecorder(); func() int { h.ServeHTTP(w, rebound); return w.Code }() == http.StatusOK {
		t.Fatal("an app setup start through an unconfigured name was answered")
	}
	w := call("/v1/setup/app", nil, map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("app setup start %d: %s", w.Code, w.Body.String())
	}
	var start struct {
		ServerID string `json:"serverId"`
		Token    string `json:"setupToken"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &start); err != nil || start.ServerID != id.ID() || len(start.Token) != 43 {
		t.Fatal("invalid app setup start")
	}
	w = call("/v1/setup", nil, map[string]any{"requestId": "44444444-4444-4444-8444-444444444444", "setupToken": start.Token, "username": "owner", "password": "A-long-local-password", "name": "Living room", "authMode": "local", "interactive": true})
	if w.Code != 201 {
		t.Fatalf("setup %d: %s", w.Code, w.Body.String())
	}
	if w = call("/v1/setup/app", nil, map[string]any{}); w.Code != 409 {
		t.Fatalf("a set-up server still hands out its setup secret: %d", w.Code)
	}
}
