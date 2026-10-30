package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"testing"
)

// The Node regression sends the exported helper's and real generated routeBody
// bytes here. The actual production router, strict decoder and SQL writer run.
func TestRepairPublicHTTPBridge(t *testing.T) {
	if os.Getenv("PORTICO_REPAIR_PUBLIC_BRIDGE") != "1" {
		t.Skip("real client/handler protocol driver")
	}
	root := t.TempDir()
	db, e := persistence.Open(storagepaths.Path(root, "server.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	id, e := identity.New(db, root)
	if e != nil {
		t.Fatal(e)
	}
	control, e := hosted.New(db, id, "", "", "")
	if e != nil {
		t.Fatal(e)
	}
	handler := New(Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Hosted: control})
	token, e := id.BrowserSetupToken(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	emit := func(v any) { raw, _ := json.Marshal(v); fmt.Println("PORTICO_REPAIR_JSON:" + string(raw)) }
	emit(map[string]any{"setupToken": token})
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 128<<10)
	for scanner.Scan() {
		var q struct {
			Path, Method, Token string
			Body                json.RawMessage
		}
		if json.Unmarshal(scanner.Bytes(), &q) != nil {
			t.Fatal("bad client request")
		}
		if q.Path == "stop" {
			return
		}
		r := httptest.NewRequest(q.Method, "http://127.0.0.1:32500"+q.Path, bytes.NewReader(q.Body))
		r.RemoteAddr = "127.0.0.1:5000"
		r.Header.Set("Content-Type", "application/json")
		if q.Token != "" {
			r.Header.Set("Authorization", "Bearer "+q.Token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var out any
		if json.Unmarshal(w.Body.Bytes(), &out) != nil {
			out = map[string]any{}
		}
		emit(map[string]any{"status": w.Code, "body": out})
	}
	if e = scanner.Err(); e != nil {
		t.Fatal(e)
	}
}
func TestRepairCapabilitiesModeHTTP(t *testing.T) {
	for _, mode := range []string{"local", "hosted", "pending", "pending-configured", "fresh"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			db, e := persistence.Open(filepath.Join(root, "db"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			id, e := identity.New(db, root)
			if e != nil {
				t.Fatal(e)
			}
			if mode != "fresh" {
				secret, _ := id.BrowserSetupToken(context.Background())
				q := identity.SetupOptions{RequestID: "33333333-3333-4333-8333-333333333333", SetupToken: secret, Name: "Fixture"}
				if strings.HasPrefix(mode, "pending") {
					q.AuthMode = "hosted"
				} else {
					q.AuthMode = "local"
					q.Username = "owner"
					q.Password = "Local-long-password"
				}
				if _, e = id.SetupWithOptions(context.Background(), q, true); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "hosted" {
				if _, e = db.Exec(`UPDATE accounts SET password_hash=X'';UPDATE identity_state SET sign_in_kind='portico';UPDATE onboarding_state_v1 SET auth_mode='hosted';INSERT INTO account_portico_links(account_id,hosted_account_id) SELECT id,'fixture-portico-owner' FROM accounts`); e != nil {
					t.Fatal(e)
				}
			}
			control, e := hosted.New(db, id, "", "", "")
			if mode == "pending-configured" {
				public := make([]byte, 32)
				control, e = hosted.New(db, id, "https://hosted.example", base64.RawURLEncoding.EncodeToString(public), trust.KeyID(public))
			}

			if e != nil {
				t.Fatal(e)
			}
			h := New(Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Hosted: control})
			r := httptest.NewRequest("GET", "http://127.0.0.1:32500/v1/auth/capabilities", nil)
			r.RemoteAddr = "127.0.0.1:5000"
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			var caps identity.AuthCapabilities
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &caps) != nil {
				t.Fatal("actual capabilities route")
			}
			hasPassword, hasPortico := false, false
			for _, m := range caps.Methods {
				if m == "password" {
					hasPassword = true
				}
				if m == "hosted" {
					hasPortico = true
				}
			}
			// Canonical selected kind describes the sign-in family; an unrelated
			// Hosted configuration or claim observation cannot replace it. An
			// unfinished Portico setup still has no usable member sign-in method.
			if hasPassword != (mode == "local") || hasPortico != (mode == "hosted") || caps.HostedAttach != (mode != "local") || caps.ServerName == "" || caps.SetupRequired != (mode == "fresh" || strings.HasPrefix(mode, "pending")) {
				t.Fatal("method/mode/setup facts")
			}
			if mode == "local" || mode == "hosted" {
				_, e = id.DirectLogin(context.Background(), "owner", "Local-long-password")
				if (e == nil) != (mode == "local") {
					t.Fatal("advertised password disagrees with actual authority")
				}
				if mode == "hosted" && !errors.Is(e, identity.ErrSignInKindPortico) {
					t.Fatal("Portico password refusal lost its kind fence", e)
				}
			}
			if strings.Contains(w.Body.String(), "fixture-portico-owner") || strings.Contains(w.Body.String(), "owner@") {
				t.Fatal("public owner identity leaked")
			}
		})
	}
}

// Invoked by the genuine cross-repo journey while its disposable completed
// claim database is still alive. No synthetic caller or ready predicate.
func TestRepairCapabilitiesExistingClaimHTTP(t *testing.T) {
	root := os.Getenv("PORTICO_REPAIR_COMPLETED_STATE")
	if root == "" {
		t.Skip("genuine completed claim driver")
	}
	db, e := persistence.Open(storagepaths.Path(root, "server.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	id, e := identity.New(db, root)
	if e != nil {
		t.Fatal(e)
	}
	id.SetupClaimReadyTx = networking.SetupClaimInstalledTx
	public, e := base64.RawURLEncoding.DecodeString(os.Getenv("PORTICO_REPAIR_PUBLIC_ROOT"))
	if e != nil {
		t.Fatal(e)
	}
	control, e := hosted.New(db, id, "https://hosted.example", base64.RawURLEncoding.EncodeToString(public), trust.KeyID(public))
	if e != nil {
		t.Fatal(e)
	}
	h := New(Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Hosted: control})
	r := httptest.NewRequest("GET", "http://127.0.0.1:32500/v1/auth/capabilities", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out identity.AuthCapabilities
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.HostedAttach || out.SetupRequired {
		t.Fatal("genuine completed account-mode capability response")
	}
	hasHosted := false
	for _, method := range out.Methods {
		if method == "password" {
			t.Fatal("password fallback advertised")
		}
		hasHosted = hasHosted || method == "hosted"
	}
	if !hasHosted {
		t.Fatal("genuine account sign-in omitted")
	}
}
