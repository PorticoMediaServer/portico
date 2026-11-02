package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/remotegate"
)

// The lockout warning appears only when the address setup is reached at would be refused
// once setup is complete, and offers only the fixes that would let it in. A fix chosen in
// setup takes effect when setup completes.
func TestSetupRemoteCheckWarnsOnlyWhenRefusedAfterSetup(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	path := filepath.Join(root, "portico.toml")
	if err = os.WriteFile(path, []byte("[remote]\ntrusted_proxies=['10.9.9.9/32']\n"), 0600); err != nil {
		t.Fatal(err)
	}
	console := operations.New(db)
	if err = console.InitializeConfiguration(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	operations.WireSetupRemote(id)
	gate := remotegate.New()
	h := New(Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Console: console, RemoteGate: gate})
	token, err := id.BrowserSetupToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// An outside peer admitted by the setup code, at the server's public address.
	code, err := id.RotateSetupCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = id.AdmitSetupCode(t.Context(), code); err != nil {
		t.Fatal(err)
	}
	if err = id.AdmitSetupToken(t.Context(), token, "203.0.113.9", "http://203.0.113.5:32500"); err != nil {
		t.Fatal(err)
	}
	check := func(address, peer, access, security string, headers map[string]string) SetupRemoteCheck {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"setupToken": token, "remoteAccess": access, "remoteSecurity": security})
		r := httptest.NewRequest("POST", address+"/v1/setup/remote-check", bytes.NewReader(raw))
		r.RemoteAddr = peer
		r.Header.Set("Content-Type", "application/json")
		// From outside, the setup token a setup code issued is what admits the check.
		r.Header.Set("Authorization", "Bearer "+token)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var out SetupRemoteCheck
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("remote check: %d %s", w.Code, w.Body)
		}
		return out
	}
	want := func(name string, got SetupRemoteCheck, reason string, fixes ...string) {
		t.Helper()
		if fixes == nil {
			fixes = []string{}
		}
		if got.Refused != (reason != "") || got.Reason != reason || !reflect.DeepEqual(got.Fixes, fixes) {
			t.Errorf("%s: refused=%v reason=%q fixes=%v, want %q %v", name, got.Refused, got.Reason, got.Fixes, reason, fixes)
		}
	}
	want("LAN, plain HTTP", check("http://192.168.1.50:32500", "192.168.1.20:5000", "on", "enhanced", nil), "")
	want("outside, plain HTTP, Enhanced", check("http://203.0.113.5:32500", "203.0.113.9:5000", "on", "enhanced", nil), "http", "standard")
	want("outside, plain HTTP, Standard", check("http://203.0.113.5:32500", "203.0.113.9:5000", "on", "standard", nil), "")
	want("outside, remote access Off", check("http://203.0.113.5:32500", "203.0.113.9:5000", "off", "standard", nil), "access")
	// The same person through their reverse proxy at a name: the code admits that origin.
	if code, err = id.RotateSetupCode(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = id.AdmitSetupCode(t.Context(), code); err != nil {
		t.Fatal(err)
	}
	if err = id.AdmitSetupToken(t.Context(), token, "203.0.113.9", "https://media.example.com"); err != nil {
		t.Fatal(err)
	}
	proxied := map[string]string{"X-Forwarded-For": "203.0.113.9", "X-Forwarded-Proto": "https", "X-Forwarded-Host": "media.example.com"}
	named := check("http://media.example.com", "10.9.9.9:5000", "on", "enhanced", proxied)
	want("through a trusted proxy, an unlisted name", named, "name", "address", "standard")
	if named.Address != "https://media.example.com" {
		t.Fatalf("the address to keep allowing: %q", named.Address)
	}
	// Keep allowing this address, chosen in setup: once set up, the gate lets the name in.
	raw, _ := json.Marshal(map[string]any{"requestId": "88888888-8888-4888-8888-888888888888", "setupToken": token, "username": "owner", "password": "A-long-local-password", "name": "Den", "authMode": "local", "interactive": true, "remoteAccess": "on", "remoteSecurity": "enhanced", "allowAddress": named.Address})
	r := httptest.NewRequest("POST", "http://127.0.0.1:32500/v1/setup", bytes.NewReader(raw))
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("Origin", "http://127.0.0.1:32500")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("local setup with the address kept: %d %s", w.Code, w.Body)
	}
	if p := gate.Policy(); p == nil || p.Unconfigured() || !p.Listed("media.example.com") {
		t.Fatal("the kept address is listed once setup completes")
	}
}
