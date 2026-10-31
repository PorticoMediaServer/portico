package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

const setupStateRequest = "44444444-4444-4444-8444-444444444444"

func setupStateFixture(t *testing.T) (http.Handler, *identity.Service, func(peer, origin, token string) (int, SetupStateResponse, string)) {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	host, err := hosted.New(db, id, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	h := New(Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Hosted: host, Origins: []string{"https://web.getportico.tv"}})
	read := func(peer, origin, token string) (int, SetupStateResponse, string) {
		r := httptest.NewRequest("GET", "http://127.0.0.1:32500/v1/setup/state", nil)
		r.RemoteAddr = peer
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var out SetupStateResponse
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code, out, w.Body.String()
	}
	return h, id, read
}

func TestSetupStateTellsAnyoneOnlyTheCoarseStatus(t *testing.T) {
	_, id, read := setupStateFixture(t)
	ctx := context.Background()
	if code, out, body := read("198.51.100.8:5000", "", ""); code != 200 || out.Status != "ready" || out.Setup != nil || out.RetryAfterSeconds != 0 {
		t.Fatalf("open server: %d %s", code, body)
	}
	token, err := id.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// On an open server a token proves nothing and changes nothing.
	if code, out, body := read("127.0.0.1:5000", "", token); code != 200 || out.Status != "ready" || out.Setup != nil {
		t.Fatalf("open server with its bootstrap token: %d %s", code, body)
	}
	if _, err = id.SetupWithOptions(ctx, identity.SetupOptions{RequestID: setupStateRequest, SetupToken: token, Name: "Family server", AuthMode: "hosted"}, true); err != nil {
		t.Fatal(err)
	}
	code, out, body := read("198.51.100.8:5000", "", "")
	if code != 200 || out.Status != "in_progress" || out.Setup != nil || out.RetryAfterSeconds < 1700 || out.RetryAfterSeconds > 1800 {
		t.Fatalf("started elsewhere: %d %s", code, body)
	}
	if strings.Contains(body, setupStateRequest) {
		t.Fatal("the coarse answer named the setup request")
	}
}

func TestSetupStateShowsItsOwnSetupOnlyToItsToken(t *testing.T) {
	_, id, read := setupStateFixture(t)
	ctx := context.Background()
	token, err := id.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = id.SetupWithOptions(ctx, identity.SetupOptions{RequestID: setupStateRequest, SetupToken: token, Name: "Family server", AuthMode: "hosted"}, true); err != nil {
		t.Fatal(err)
	}
	code, out, body := read("192.168.1.2:5000", "", token)
	if code != 200 || out.Status != "in_progress" || out.Setup == nil || out.Setup.Variant != "started" || out.Setup.RequestID != setupStateRequest || out.Setup.Revision != 1 {
		t.Fatalf("own setup: %d %s", code, body)
	}
	expires, err := time.Parse(time.RFC3339Nano, out.Setup.ExpiresAt)
	if err != nil || time.Until(expires) < 29*time.Minute || time.Until(expires) > 30*time.Minute {
		t.Fatalf("expiresAt %q", out.Setup.ExpiresAt)
	}
	// The same page's own origin is first party; a token from another site's page is refused.
	if code, _, body = read("127.0.0.1:5000", "http://127.0.0.1:32500", token); code != 200 {
		t.Fatalf("same origin: %d %s", code, body)
	}
	for _, origin := range []string{"https://web.getportico.tv", "http://evil.example", "http://127.0.0.1:9999"} {
		if code, _, body = read("127.0.0.1:5000", origin, token); code != http.StatusUnauthorized && code != http.StatusForbidden {
			t.Fatalf("token from %s: %d %s", origin, code, body)
		}
	}
	// A token is only accepted from where setup is: this machine or the local network.
	if code, _, body = read("198.51.100.8:5000", "", token); code != http.StatusForbidden || !strings.Contains(body, `"setup_source_not_local"`) {
		t.Fatalf("public source with token: %d %s", code, body)
	}
	// Another token, or a malformed header, is refused.
	other := strings.Repeat("A", 43)
	if code, _, body = read("127.0.0.1:5000", "", other); code != http.StatusUnauthorized {
		t.Fatalf("other token: %d %s", code, body)
	}
	if code, _, body = read("127.0.0.1:5000", "", "a b"); code != http.StatusUnauthorized {
		t.Fatalf("malformed token: %d %s", code, body)
	}
}

func TestSetupStateRateLimitsRefusedTokensOnly(t *testing.T) {
	_, id, read := setupStateFixture(t)
	ctx := context.Background()
	token, err := id.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = id.SetupWithOptions(ctx, identity.SetupOptions{RequestID: setupStateRequest, SetupToken: token, Name: "Family server", AuthMode: "hosted"}, true); err != nil {
		t.Fatal(err)
	}
	// Polling with the right token is never limited.
	for range 40 {
		if code, _, body := read("127.0.0.1:5000", "", token); code != 200 {
			t.Fatalf("own poll: %d %s", code, body)
		}
	}
	wrong := strings.Repeat("B", 43)
	limited := false
	for range 25 {
		code, _, _ := read("127.0.0.1:5000", "", wrong)
		if code == http.StatusTooManyRequests {
			limited = true
			break
		}
		if code != http.StatusUnauthorized {
			t.Fatalf("wrong token: %d", code)
		}
	}
	if !limited {
		t.Fatal("guessing tokens was never limited")
	}
}

func TestSetupStateOfACompletedServerIsCompleteWhoeverAsks(t *testing.T) {
	h, id, read := setupStateFixture(t)
	token, err := id.BrowserSetupToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"requestId": setupStateRequest, "setupToken": token, "username": "owner", "password": "A-long-local-password", "name": "My media", "authMode": "local", "interactive": true})
	r := httptest.NewRequest("POST", "http://127.0.0.1:32500/v1/setup", bytes.NewReader(raw))
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("Origin", "http://127.0.0.1:32500")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("local setup: %d %s", w.Code, w.Body.String())
	}
	for _, bearer := range []string{"", token, strings.Repeat("C", 43)} {
		if code, out, body := read("198.51.100.8:5000", "", ""); code != 200 || out.Status != "complete" || out.Setup != nil {
			t.Fatalf("completed (%q): %d %s", bearer, code, body)
		}
		if code, out, body := read("127.0.0.1:5000", "", bearer); code != 200 || out.Status != "complete" || out.Setup != nil {
			t.Fatalf("completed with token %q: %d %s", bearer, code, body)
		}
	}
}
