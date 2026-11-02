package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Start over is offered to exactly whoever may start setup: a local peer at a setup
// address, same origin. A public peer is refused, and once the server is set up start
// over refuses as a start does. (The installed-claim path is in networking's
// TestSetupStartOverAfterAnInstalledClaimLapsed.)
func TestSetupStartOverAdmission(t *testing.T) {
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
	h := New(Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Hosted: host})
	call := func(address, peer, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", address+path, bytes.NewReader(raw))
		r.RemoteAddr = peer
		r.Header.Set("Origin", address)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	token := func(w *httptest.ResponseRecorder) string {
		t.Helper()
		var out struct {
			Token string `json:"setupToken"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Token) != 43 {
			t.Fatalf("start over: %d %s", w.Code, w.Body)
		}
		return out.Token
	}
	// A public peer is refused, by name.
	if w := call("http://192.168.1.50:32500", "203.0.113.9:5000", "/v1/setup/start-over", map[string]any{}); w.Code != http.StatusForbidden || !bytes.Contains(w.Body.Bytes(), []byte(`"setup_source_not_local"`)) {
		t.Fatalf("a public peer started over: %d %s", w.Code, w.Body)
	}
	// A LAN peer at the server's LAN address may; with nothing started it's a plain start.
	first := token(call("http://192.168.1.50:32500", "192.168.1.20:5000", "/v1/setup/start-over", map[string]any{}))
	// A Portico Account setup that's under way (started, never approved) ends, and the
	// earlier setup token no longer works.
	if w := call("http://192.168.1.50:32500", "192.168.1.20:5000", "/v1/setup", map[string]any{"requestId": "44444444-4444-4444-8444-444444444444", "setupToken": first, "name": "My media", "authMode": "hosted", "interactive": true}); w.Code/100 != 2 {
		t.Fatalf("hosted setup: %d %s", w.Code, w.Body)
	}
	second := token(call("http://127.0.0.1:32500", "127.0.0.1:5000", "/v1/setup/start-over", map[string]any{}))
	if second == first {
		t.Fatal("start over kept the old setup token")
	}
	if _, _, err := id.SetupClaimAuthority(t.Context(), first); err == nil {
		t.Fatal("the old setup token still has authority")
	}
	// Setup completes locally; start over then refuses and changes nothing.
	if w := call("http://127.0.0.1:32500", "127.0.0.1:5000", "/v1/setup", map[string]any{"requestId": "55555555-5555-4555-8555-555555555555", "setupToken": second, "username": "owner", "password": "A-long-local-password", "name": "My media", "authMode": "local", "interactive": true}); w.Code != 201 {
		t.Fatalf("local setup: %d %s", w.Code, w.Body)
	}
	if w := call("http://127.0.0.1:32500", "127.0.0.1:5000", "/v1/setup/start-over", map[string]any{}); w.Code != http.StatusConflict || !bytes.Contains(w.Body.Bytes(), []byte(`"setup_already_complete"`)) {
		t.Fatalf("start over on a set-up server: %d %s", w.Code, w.Body)
	}
}
