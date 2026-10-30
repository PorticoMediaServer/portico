package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
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
	// Any peer may start setup at a literal/local address or an independently
	// configured authority; cross-origin API permission never authorizes setup.
	for _, tc := range []struct{ name, address, origin, peer string }{
		{"LAN peer", "http://127.0.0.1:32500", "http://127.0.0.1:32500", "192.168.1.2:5000"},
		{"public peer", "https://demo.example", "https://demo.example", "198.51.100.8:5000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := call(tc.address, tc.origin, tc.peer, "/v1/setup/browser", map[string]any{}); w.Code != http.StatusOK {
				t.Fatalf("got status %d", w.Code)
			}
		})
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
	var state identity.OnboardingState
	if err = json.Unmarshal(w.Body.Bytes(), &state); err != nil || w.Code != 200 || !state.Ready || state.Phase != "ready" || state.AuthMode != "local" {
		t.Fatal("a local server is ready once its owner exists (no checklist), without a recovery-owner role")
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
