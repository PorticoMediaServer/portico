package httpapi

import (
	"bytes"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestBrowserSetupAuthorityIndependentOfHostAndOrigin(t *testing.T) {
	d := Dependencies{SetupHosts: []string{"media.example:8443"}, TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}
	for _, tc := range []struct {
		name, address, origin, peer, forwarded string
		allowed                                bool
	}{
		{"literal LAN", "http://192.168.1.20:32500", "http://192.168.1.20:32500", "192.168.1.50:8000", "", true},
		{"IPv6", "http://[fd00::20]:32500", "http://[fd00::20]:32500", "192.168.1.50:8000", "", true},
		{"localhost", "http://localhost:32500", "http://localhost:32500", "192.168.1.50:8000", "", true},
		{"rebinding", "http://rebind.attacker.test:32500", "http://rebind.attacker.test:32500", "192.168.1.50:8000", "", false},
		{"unknown local suffix", "http://attacker.local:32500", "http://attacker.local:32500", "192.168.1.50:8000", "", false},
		{"configured authority", "https://media.example:8443", "https://media.example:8443", "192.168.1.50:8000", "", true},
		{"different port", "https://media.example:443", "https://media.example:443", "192.168.1.50:8000", "", false},
		{"trusted proxy", "http://backend:32500", "https://media.example:8443", "127.0.0.1:8000", "for=192.168.1.50;proto=https;host=media.example:8443", true},
		{"untrusted forwarding", "http://rebind.attacker.test:32500", "http://rebind.attacker.test:32500", "192.168.1.50:8000", "for=192.168.1.50;proto=https;host=media.example:8443", false},
		{"trusted peer forged allowed host", "http://rebind.attacker.test:32500", "http://rebind.attacker.test:32500", "127.0.0.1:8000", "for=192.168.1.50;proto=https;host=media.example:8443", false},
		{"configured trailing dot", "https://media.example.:8443", "https://media.example.:8443", "192.168.1.50:8000", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", tc.address+"/v1/setup/browser", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", tc.origin)
			if tc.forwarded != "" {
				r.Header.Set("Forwarded", tc.forwarded)
			}
			if got := d.setupRequestAllowed(r); got != tc.allowed {
				t.Fatalf("allowed=%v, want %v", got, tc.allowed)
			}
		})
	}
	if machine, err := os.Hostname(); err == nil && machine != "" {
		r := httptest.NewRequest("POST", "http://"+machine+":32500/v1/setup/browser", nil)
		if !d.browserSetupAuthorityAllowed(r) {
			t.Fatal("the machine's independently obtained name must work")
		}
	}
}

func TestSetupHostsConfiguration(t *testing.T) {
	for _, raw := range []string{"https://media.example", "media.example/path", "media.example,", "*.example", "media.example:", "media.example:0", "media.example:65536"} {
		if _, err := ParseSetupHosts(raw); err == nil {
			t.Fatalf("invalid authority accepted: %q", raw)
		}
	}
	hosts, err := ParseSetupHosts(" Media.Example.:8443, nas.local:32500, [fd00::20]:32500 ")
	if err != nil || len(hosts) != 3 || hosts[0] != "media.example:8443" {
		t.Fatalf("hosts=%v err=%v", hosts, err)
	}
	for _, machine := range []string{"nas", "nas.local", "nas.local."} {
		if !setupMachineHostMatches("nas", machine) || !setupMachineHostMatches("nas.local", machine) || setupMachineHostMatches("attacker.local", machine) {
			t.Fatalf("machine aliases: %q", machine)
		}
	}
}

func TestConfiguredSetupAccessURLThroughPreservedProxyHost(t *testing.T) {
	f := administrationFixtureNew(t)
	d := f.deps
	d.connectionPolicy = &connectionPolicyCache{}
	d.connectionPolicy.setting.Store(&connectionSettingValue{accessURLs: []string{"https://media.example"}})
	r := httptest.NewRequest("POST", "http://media.example/v1/setup/browser", nil)
	r.RemoteAddr = "192.168.1.50:8000"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://media.example")
	if !d.setupRequestAllowed(r) {
		t.Fatal("configured external HTTPS authority was refused behind a preserving proxy")
	}
	r.Header.Set("Origin", "https://attacker.example")
	if d.setupRequestAllowed(r) {
		t.Fatal("configured authority allowed another origin")
	}
}

func TestRebindingCannotReadBootstrapOrWriteOrResumeOwner(t *testing.T) {
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
	control, err := hosted.New(db, id, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	h := New(Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Hosted: control})
	// Even possession of a token obtained by an authorized app must not bypass
	// the browser authority guard on the setup write or receipt replay routes.
	token, err := id.BrowserSetupToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/setup/browser", "/v1/setup", "/v1/setup/resume"} {
		body := `{"requestId":"33333333-3333-4333-8333-333333333333","setupToken":"` + token + `"}`
		if path == "/v1/setup/browser" {
			body = `{}`
		}
		r := httptest.NewRequest("POST", "http://rebind.attacker.test:32500"+path, bytes.NewBufferString(body))
		r.RemoteAddr = "192.168.1.50:8000"
		r.Header.Set("Origin", "http://rebind.attacker.test:32500")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if !id.SetupRequired() {
		t.Fatal("untrusted browser created an owner")
	}
}
