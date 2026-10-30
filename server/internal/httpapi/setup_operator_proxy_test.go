package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Real operator file import is the sole proxy trust authority before accounts
// exist. No owner/token/setting-row fixture is inserted to make HTTPS work.
func TestOperatorProxySetupUsesPersistentRealTransportConfiguration(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	path := filepath.Join(root, "portico.toml")
	if err = os.WriteFile(path, []byte("[remote]\ntrusted_proxies=['127.0.0.1/32']\nextra_addresses=['https://demo.getportico.tv:8443']\n"), 0600); err != nil {
		t.Fatal(err)
	}
	console := operations.New(db)
	if err = console.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	control, err := hosted.New(db, id, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	dependencies := func(store *operations.Store) Dependencies {
		return Dependencies{DB: db, Identity: id, Console: store, Catalog: catalog.New(db), Hosted: control, SetupHosts: []string{"demo.getportico.tv:8443"}, connectionPolicy: &connectionPolicyCache{}}
	}
	d := dependencies(console)
	h := New(d)
	request := func(method, path string, body any) *http.Request {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "http://demo.getportico.tv:8443"+path, bytes.NewReader(raw))
		r.RemoteAddr = "127.0.0.1:5678"
		r.Header.Set("Origin", "https://demo.getportico.tv:8443")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-Host", "demo.getportico.tv:8443")
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", "192.168.1.9")
		return r
	}
	assertPolicy := func(live Dependencies) {
		t.Helper()
		r := request("POST", "/v1/setup/browser", map[string]any{})
		if !live.setupRequestAllowed(r) || live.ownerSwitchWebOrigin(r) != "https://demo.getportico.tv:8443" || !live.secureTransport(r) {
			t.Fatal("real imported proxy configuration did not establish exact HTTPS origin")
		}
		if !live.requestIsRemote(r) || live.privateSetupPeer(r) {
			t.Fatal("forwarded remote client inherited the loopback proxy's locality")
		}
		actual, ok := networkpolicy.Client(r, live.trustedProxyPrefixes(ctx))
		if !ok || actual.String() != "192.168.1.9" {
			t.Fatal("proxy hid the actual client")
		}
		direct := request("POST", "/v1/setup/browser", map[string]any{})
		direct.RemoteAddr = "192.168.1.9:5678"
		direct.Header.Del("X-Forwarded-For")
		if live.setupPeerSubject(r) != live.setupPeerSubject(direct) {
			t.Fatal("proxy changed the remote client's rate-limit subject")
		}
	}
	assertPolicy(d)
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&count); err != nil || count != 0 {
		t.Fatal("transport configuration created an account", err)
	}
	for _, name := range []string{"wrong origin", "wrong port", "wrong authority", "untrusted peer", "duplicate proto", "malformed Forwarded", "conflicting forwarding"} {
		t.Run(name, func(t *testing.T) {
			r := request("POST", "/v1/setup/browser", map[string]any{})
			switch name {
			case "wrong origin":
				r.Header.Set("Origin", "https://attacker.example")
			case "wrong port":
				r.Host = "demo.getportico.tv:443"
				r.Header.Set("X-Forwarded-Host", "demo.getportico.tv:443")
				r.Header.Set("Origin", "https://demo.getportico.tv")
			case "wrong authority":
				r.Host = "attacker.example:8443"
				r.Header.Set("X-Forwarded-Host", "attacker.example:8443")
				r.Header.Set("Origin", "https://attacker.example:8443")
			case "untrusted peer":
				r.RemoteAddr = "198.51.100.12:1234"
			case "duplicate proto":
				r.Header.Add("X-Forwarded-Proto", "http")
			case "malformed Forwarded":
				r.Header.Del("X-Forwarded-Proto")
				r.Header.Set("Forwarded", `for="unterminated;proto=https`)
			case "conflicting forwarding":
				r.Header.Set("Forwarded", "for=198.51.100.9;proto=http;host=attacker.example:8443")
			}
			if name == "wrong origin" || name == "wrong port" || name == "wrong authority" {
				if d.setupRequestAllowed(r) {
					t.Fatal("unapproved browser authority admitted")
				}
				return // A valid trusted TLS scheme is not the independent authority allow-list.
			}
			if d.ownerSwitchWebOrigin(r) != "" {
				t.Fatal("invalid transport granted a secure setup claim origin")
			}
		})
	}
	call := func(body any) *httptest.ResponseRecorder {
		r := request("POST", "/v1/setup", body)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	invalid := map[string]any{"requestId": "33333333-3333-4333-8333-333333333333", "setupToken": "wrong-proof", "username": "owner", "password": "A-long-local-password", "name": "Actual test server", "authMode": "local", "interactive": true}
	if w := call(invalid); w.Code < 400 {
		t.Fatal("transport trust substituted for setup proof")
	}
	if err = db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&count); err != nil || count != 0 {
		t.Fatal("bad proof created an owner", err)
	}
	// Before anyone owns the server, a public client behind the proxy can't start setup (S1).
	public := request("POST", "/v1/setup/browser", map[string]any{})
	public.Header.Set("X-Forwarded-For", "198.51.100.9")
	if w := httptest.NewRecorder(); func() int { h.ServeHTTP(w, public); return w.Code }() != http.StatusForbidden {
		t.Fatal("a public client behind the proxy started setup")
	}
	bootstrap := httptest.NewRecorder()
	h.ServeHTTP(bootstrap, request("POST", "/v1/setup/browser", map[string]any{}))
	if bootstrap.Code != 200 {
		t.Fatalf("bootstrap status %d", bootstrap.Code)
	}
	var issued BrowserSetupResponse
	if err = json.Unmarshal(bootstrap.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	invalid["setupToken"] = issued.SetupToken
	if w := call(invalid); w.Code != 201 {
		t.Fatalf("real setup status %d", w.Code)
	}
	if err = db.QueryRow(`SELECT count(*) FROM direct_memberships WHERE role='owner'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("setup did not create exactly one actual owner", err)
	}
	// The file and relational settings, not a temporary setup-only trust rule,
	// supply the identical policy when a completed server starts again.
	restarted := operations.New(db)
	if err = restarted.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	doc, err := restarted.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc.Effective.TrustedProxies, []string{"127.0.0.1/32"}) || !reflect.DeepEqual(doc.Effective.AccessURLs, []string{"https://demo.getportico.tv:8443"}) {
		t.Fatal("completed setup/restart lost actual transport settings")
	}
	assertPolicy(dependencies(restarted))
}
