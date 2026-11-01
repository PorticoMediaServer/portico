package httpapi

import (
	"bytes"
	"context"
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
	"github.com/PorticoMediaServer/portico/server/internal/remotegate"
)

// O-169 R5b through the real wiring: setup's Remote access and Remote
// security reach the stored settings and, at once, the remote gate.
func TestSetupRemoteChoicesReachSettingsAndGate(t *testing.T) {
	for _, c := range []struct {
		name             string
		body             map[string]any
		access, security string
	}{
		{"local, Private only and Standard", map[string]any{"authMode": "local", "username": "owner", "password": "Local-password1!", "remoteAccess": "private", "remoteSecurity": "standard"}, "private", "standard"},
		{"local, defaults", map[string]any{"authMode": "local", "username": "owner", "password": "Local-password1!"}, "off", "enhanced"},
		{"Portico Account, defaults", map[string]any{"authMode": "hosted"}, "on", "enhanced"},
	} {
		t.Run(c.name, func(t *testing.T) {
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
			console := operations.New(db)
			gate := remotegate.New()
			h := New(Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Hosted: host, Console: console, RemoteGate: gate,
				Origins: []string{"http://127.0.0.1:32500"}})
			const origin = "http://127.0.0.1:32500"
			call := func(method, path, token string, body any) *httptest.ResponseRecorder {
				var raw []byte
				if body != nil {
					raw, _ = json.Marshal(body)
				}
				r := httptest.NewRequest(method, origin+path, bytes.NewReader(raw))
				r.RemoteAddr = "127.0.0.1:5000"
				r.Header.Set("Origin", origin)
				if body != nil {
					r.Header.Set("Content-Type", "application/json")
				}
				if token != "" {
					r.Header.Set("Authorization", "Bearer "+token)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			if p := gate.Policy(); p == nil || p.Access() != remotegate.AccessOn || p.Security() != remotegate.SecurityEnhanced {
				t.Fatalf("gate before setup: %+v", p)
			}
			w := call("POST", "/v1/setup/browser", "", map[string]any{})
			var token struct {
				SetupToken string `json:"setupToken"`
			}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &token) != nil {
				t.Fatalf("setup token: %d %s", w.Code, w.Body)
			}
			body := map[string]any{"requestId": "44444444-4444-4444-8444-444444444444", "setupToken": token.SetupToken, "name": "Home", "interactive": true}
			for k, v := range c.body {
				body[k] = v
			}
			w = call("POST", "/v1/setup", "", body)
			if w.Code != http.StatusCreated {
				t.Fatalf("setup: %d %s", w.Code, w.Body)
			}
			// The gate took the choices straight away (no settings save happened).
			if p := gate.Policy(); p.Access() != c.access || p.Security() != c.security {
				t.Fatalf("gate after setup: %s/%s, want %s/%s", p.Access(), p.Security(), c.access, c.security)
			}
			// The stored settings say the same; through the owner API where there is
			// an owner (local), else from the store (a Portico Account owner comes later).
			var access, security string
			if c.body["authMode"] == "local" {
				var out struct {
					AccessToken string `json:"accessToken"`
				}
				_ = json.Unmarshal(w.Body.Bytes(), &out)
				w = call("GET", "/v1/admin/console/settings", out.AccessToken, nil)
				var doc struct {
					Data struct {
						Effective struct {
							RemoteAccess   string `json:"remoteAccess"`
							RemoteSecurity string `json:"remoteSecurity"`
						} `json:"effective"`
					} `json:"data"`
				}
				if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &doc) != nil {
					t.Fatalf("settings: %d %s", w.Code, w.Body)
				}
				access, security = doc.Data.Effective.RemoteAccess, doc.Data.Effective.RemoteSecurity
			} else {
				doc, err := console.Settings(context.Background(), operations.AllowServerScope)
				if err != nil {
					t.Fatal(err)
				}
				access, security = doc.Effective.RemoteAccess, doc.Effective.RemoteSecurity
			}
			if access != c.access || security != c.security {
				t.Fatalf("stored settings: %s/%s, want %s/%s", access, security, c.access, c.security)
			}
		})
	}
	// An unknown choice is refused before anything is written.
	t.Run("unknown choice", func(t *testing.T) {
		root := t.TempDir()
		db, _ := persistence.Open(filepath.Join(root, "db"))
		defer db.Close()
		id, _ := identity.New(db, root)
		operations.WireSetupRemote(id)
		host, _ := hosted.New(db, id, "", "", "")
		h := New(Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Hosted: host, Console: operations.New(db), Origins: []string{"http://127.0.0.1:32500"}})
		post := func(path string, body any) *httptest.ResponseRecorder {
			raw, _ := json.Marshal(body)
			r := httptest.NewRequest("POST", "http://127.0.0.1:32500"+path, bytes.NewReader(raw))
			r.RemoteAddr = "127.0.0.1:5000"
			r.Header.Set("Origin", "http://127.0.0.1:32500")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			return w
		}
		var token struct {
			SetupToken string `json:"setupToken"`
		}
		_ = json.Unmarshal(post("/v1/setup/browser", map[string]any{}).Body.Bytes(), &token)
		if w := post("/v1/setup", map[string]any{"requestId": "55555555-5555-4555-8555-555555555555", "setupToken": token.SetupToken, "name": "Home", "authMode": "hosted", "remoteAccess": "sometimes"}); w.Code != http.StatusBadRequest {
			t.Fatalf("unknown remote access: %d %s", w.Code, w.Body)
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM onboarding_state_v1`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("setup state written: %d %v", n, err)
		}
	})
}
