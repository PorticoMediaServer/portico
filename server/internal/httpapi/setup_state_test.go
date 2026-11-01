package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
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
	h, id, read, _ := setupStateFixtureDB(t)
	return h, id, read
}

func setupStateFixtureDB(t *testing.T) (http.Handler, *identity.Service, func(peer, origin, token string) (int, SetupStateResponse, string), *sql.DB) {
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
	operations.WireSetupRemote(id)
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
	return h, id, read, db
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

func setupPost(h http.Handler, path, origin, peer string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "http://127.0.0.1:32500"+path, bytes.NewReader(raw))
	r.RemoteAddr = peer
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// Back from an unapproved Portico Account setup; then local is a fresh choice. Before
// Back, the server refuses a local owner on that setup outright.
func TestSetupAbandonThenLocalAndNeverLocalOnAPorticoSetup(t *testing.T) {
	h, id, read := setupStateFixture(t)
	ctx := context.Background()
	token, err := id.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = id.SetupWithOptions(ctx, identity.SetupOptions{RequestID: setupStateRequest, SetupToken: token, Name: "Family server", AuthMode: "hosted"}, true); err != nil {
		t.Fatal(err)
	}
	const self = "http://127.0.0.1:32500"
	local := map[string]any{"requestId": setupStateRequest, "setupToken": token, "username": "owner", "password": "A-long-local-password", "name": "My media", "authMode": "local", "interactive": true}
	if w := setupPost(h, "/v1/setup", self, "127.0.0.1:5000", local); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"setup_portico_mode"`) {
		t.Fatalf("local owner on a Portico Account setup: %d %s", w.Code, w.Body.String())
	}
	delete(local, "requestId")
	if w := setupPost(h, "/v1/setup", self, "127.0.0.1:5000", local); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"setup_portico_mode"`) {
		t.Fatalf("local owner with a new request: %d %s", w.Code, w.Body.String())
	}

	back := map[string]any{"requestId": setupStateRequest, "revision": "1", "setupToken": token}
	for _, tc := range []struct{ origin, peer string }{{"https://web.getportico.tv", "127.0.0.1:5000"}, {"http://evil.example", "127.0.0.1:5000"}} {
		if w := setupPost(h, "/v1/setup/abandon", tc.origin, tc.peer, back); w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
			t.Fatalf("Back from %s: %d %s", tc.origin, w.Code, w.Body.String())
		}
	}
	if w := setupPost(h, "/v1/setup/abandon", "", "198.51.100.8:5000", back); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"setup_source_not_local"`) {
		t.Fatalf("Back from a public source: %d %s", w.Code, w.Body.String())
	}
	wrong := map[string]any{"requestId": setupStateRequest, "revision": "1", "setupToken": strings.Repeat("D", 43)}
	if w := setupPost(h, "/v1/setup/abandon", self, "127.0.0.1:5000", wrong); w.Code != http.StatusUnauthorized {
		t.Fatalf("Back with another token: %d %s", w.Code, w.Body.String())
	}
	for range 2 { // the second is a replay of a lost answer
		w := setupPost(h, "/v1/setup/abandon", self, "127.0.0.1:5000", back)
		var out SetupStateResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Status != "ready" || out.Setup != nil {
			t.Fatalf("Back: %d %s", w.Code, w.Body.String())
		}
	}
	if code, out, body := read("127.0.0.1:5000", "", ""); code != 200 || out.Status != "ready" {
		t.Fatalf("after Back: %d %s", code, body)
	}
	w := setupPost(h, "/v1/setup/browser", self, "127.0.0.1:5000", map[string]any{})
	var start BrowserSetupResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &start) != nil || start.SetupToken == token {
		t.Fatalf("fresh start after Back: %d %s", w.Code, w.Body.String())
	}
	local["setupToken"] = start.SetupToken
	if w = setupPost(h, "/v1/setup", self, "127.0.0.1:5000", local); w.Code != 201 {
		t.Fatalf("local after Back: %d %s", w.Code, w.Body.String())
	}
}

// Once Portico Services has approved, Back is refused and setup stays as it was.
func TestSetupAbandonAfterApprovalIsRefused(t *testing.T) {
	h, id, read, db := setupStateFixtureDB(t)
	ctx := context.Background()
	token, err := id.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = id.SetupWithOptions(ctx, identity.SetupOptions{RequestID: setupStateRequest, SetupToken: token, Name: "Family server", AuthMode: "hosted"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO networking_server_identities(server_id,public_key,key_incarnation,created_at) SELECT 'srv_Wl4E3ID18LNZFgw_Gjc3R0liIOV67Gd2vD54Ubw6OAY',X'000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f','setup-state-test','2026-10-31T00:00:00Z' WHERE NOT EXISTS(SELECT 1 FROM networking_server_identities);
 INSERT INTO networking_claim_intents(operation_id,server_id,account_id,public_key,local_generation,revision,stage,owner_id,owner_profile_id,owner_epoch,setup_operation_id,created_at,updated_at,purpose,approval_revision)
 SELECT 'op_setup',server_id,'acct_owner',public_key,0,1,'approved','','',1,?,'2026-10-31T00:00:00Z','2026-10-31T00:00:00Z','setup',1 FROM networking_server_identities LIMIT 1;
 INSERT INTO networking_claim_identity(singleton,server_id,public_key,reset_generation) SELECT 1,server_id,public_key,0 FROM networking_server_identities LIMIT 1 ON CONFLICT(singleton) DO NOTHING;
 UPDATE networking_claim_identity SET active_operation_id='op_setup' WHERE singleton=1`, setupStateRequest); err != nil {
		t.Fatal(err)
	}
	back := map[string]any{"requestId": setupStateRequest, "revision": "1", "setupToken": token}
	if w := setupPost(h, "/v1/setup/abandon", "http://127.0.0.1:32500", "127.0.0.1:5000", back); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"setup_state_changed"`) {
		t.Fatalf("Back after approval: %d %s", w.Code, w.Body.String())
	}
	if code, out, body := read("127.0.0.1:5000", "", token); code != 200 || out.Setup == nil || out.Setup.Variant != "approved" {
		t.Fatalf("approved setup after a refused Back: %d %s", code, body)
	}
}

// Try again applies only to the caller's own setup whose delivery stopped; anything
// else is refused without touching the claim.
func TestSetupRetryRefusesWhatIsNotItsOwnExhaustedSetup(t *testing.T) {
	h, id, _ := setupStateFixture(t)
	ctx := context.Background()
	token, err := id.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = id.SetupWithOptions(ctx, identity.SetupOptions{RequestID: setupStateRequest, SetupToken: token, Name: "Family server", AuthMode: "hosted"}, true); err != nil {
		t.Fatal(err)
	}
	const self = "http://127.0.0.1:32500"
	retry := map[string]any{"requestId": setupStateRequest, "revision": "1", "setupToken": token}
	// Started, not exhausted: nothing to try again.
	if w := setupPost(h, "/v1/setup/retry", self, "127.0.0.1:5000", retry); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"setup_state_changed"`) {
		t.Fatalf("retry while started: %d %s", w.Code, w.Body.String())
	}
	other := map[string]any{"requestId": setupStateRequest, "revision": "1", "setupToken": strings.Repeat("E", 43)}
	if w := setupPost(h, "/v1/setup/retry", self, "127.0.0.1:5000", other); w.Code != http.StatusUnauthorized {
		t.Fatalf("retry with another token: %d %s", w.Code, w.Body.String())
	}
	wrongRequest := map[string]any{"requestId": "55555555-5555-4555-8555-555555555555", "revision": "1", "setupToken": token}
	if w := setupPost(h, "/v1/setup/retry", self, "127.0.0.1:5000", wrongRequest); w.Code != http.StatusUnauthorized {
		t.Fatalf("retry naming another request: %d %s", w.Code, w.Body.String())
	}
	if w := setupPost(h, "/v1/setup/retry", "", "198.51.100.8:5000", retry); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"setup_source_not_local"`) {
		t.Fatalf("retry from a public source: %d %s", w.Code, w.Body.String())
	}
	if w := setupPost(h, "/v1/setup/retry", "https://web.getportico.tv", "127.0.0.1:5000", retry); w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Fatalf("retry from another site: %d %s", w.Code, w.Body.String())
	}
}

// Only the caller's own setup, started, awaiting approval or installed, may ask a
// Portico Account to approve it; anything else is refused before networking is asked.
func TestSetupApprovalRefusesWhatIsNotItsOwnLiveSetup(t *testing.T) {
	h, id, _, db := setupStateFixtureDB(t)
	ctx := context.Background()
	token, err := id.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = id.SetupWithOptions(ctx, identity.SetupOptions{RequestID: setupStateRequest, SetupToken: token, Name: "Family server", AuthMode: "hosted"}, true); err != nil {
		t.Fatal(err)
	}
	ask := func(origin, peer, bearer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:32500/v1/setup/approval", strings.NewReader(`{"setupStart":{"state":"s","codeChallenge":"c","installationId":"i"}}`))
		r.RemoteAddr = peer
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	const self = "http://127.0.0.1:32500"
	if w := ask(self, "127.0.0.1:5000", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d %s", w.Code, w.Body.String())
	}
	if w := ask(self, "127.0.0.1:5000", strings.Repeat("F", 43)); w.Code != http.StatusUnauthorized {
		t.Fatalf("another token: %d %s", w.Code, w.Body.String())
	}
	if w := ask("", "198.51.100.8:5000", token); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"setup_source_not_local"`) {
		t.Fatalf("public source: %d %s", w.Code, w.Body.String())
	}
	if w := ask("https://web.getportico.tv", "127.0.0.1:5000", token); w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Fatalf("another site: %d %s", w.Code, w.Body.String())
	}
	// Expired: Back and start again instead.
	if _, err = db.Exec(`UPDATE onboarding_state_v1 SET receipt_until=receipt_until-3600`); err != nil {
		t.Fatal(err)
	}
	if w := ask(self, "127.0.0.1:5000", token); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"setup_state_changed"`) {
		t.Fatalf("expired setup: %d %s", w.Code, w.Body.String())
	}
}
