package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func logoutHTTPFixture(t *testing.T) (Dependencies, identity.Envelope) {
	t.Helper()
	state := t.TempDir()
	db, e := persistence.Open(filepath.Join(state, "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	id, e := identity.New(db, state)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`); e != nil {
		t.Fatal(e)
	}
	session, e := id.Issue("account", "profile", "local", "owner", 1)
	if e != nil {
		t.Fatal(e)
	}
	control, e := hosted.New(db, id, "", "", "")
	if e != nil {
		t.Fatal(e)
	}
	return Dependencies{DB: db, Identity: id, Hosted: control, Catalog: catalog.New(db), Origins: []string{"http://127.0.0.1:11111"}}, session
}
func logoutHTTPRequest(h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func rotateLogoutFixture(t *testing.T, d Dependencies, token string) identity.AuthorizationEnvelope {
	t.Helper()
	v, e := d.Identity.RenewAuthorization(context.Background(), token, identity.RenewalBinding{ControllerID: "controller", ControllerEpoch: "epoch", RenewalRequestID: "request"}, func(_ context.Context, _ *sql.Tx, p identity.Principal, b identity.RenewalBinding) (time.Time, error) {
		if p.AccountID != "account" || p.ProfileID != "profile" || b.ControllerID != "controller" || b.ControllerEpoch != "epoch" {
			return time.Time{}, identity.ErrUnauthorized
		}
		return time.Time{}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestSessionFamilyHTTPLogoutAuthenticGenerations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"current", "retired", "expired-current", "expired-retired", "already-revoked"} {
		t.Run(kind, func(t *testing.T) {
			d, first := logoutHTTPFixture(t)
			h := New(d)
			other, e := d.Identity.Issue("account", "profile", "local", "owner", 1)
			if e != nil {
				t.Fatal(e)
			}
			presented, current := first.AccessToken, first.AccessToken
			if strings.Contains(kind, "retired") {
				next := rotateLogoutFixture(t, d, first.AccessToken)
				current = next.AccessToken
			}
			// A synthetic legacy row proves existing cleanup; no playback API, asset read,
			// encoder, media file, process or listener is created by this test.
			c := catalogtest.New(t, d.DB)
			library := c.Library("library", "Fixture", "movie", "/never-read-fixture")
			item := c.Movie(library, "/never-read-fixture/item.mp4", "Fixture", 2020)
			c.Drain()
			if _, e = d.DB.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,sequence,state,grant_hash,grant_token,expires_at,request_id,duration,mode) VALUES('legacy',?,'account','profile',?,?,1,0,'playing','synthetic-hash','synthetic-grant',?,'synthetic-request',10,'direct')`, identity.Digest(first.AccessToken), item.ID, item.Token, first.ExpiresAt); e != nil {
				t.Fatal(e)
			}
			if strings.Contains(kind, "expired") {
				if _, e = d.DB.Exec(`UPDATE authorization_family_tokens SET expires_at='2000-01-01T00:00:00Z' WHERE token_hash=?`, identity.Digest(presented)); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "already-revoked" {
				if e = d.Identity.LogoutToken(context.Background(), presented); e != nil {
					t.Fatal(e)
				}
			}
			if kind != "current" {
				if w := logoutHTTPRequest(h, "GET", "/v1/me", presented); w.Code != 401 {
					t.Fatal("noncurrent ordinary HTTP authority accepted", w.Code)
				}
			}
			w := logoutHTTPRequest(h, "DELETE", "/v1/sessions/current", presented)
			if w.Code != 204 || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("authentic family logout failed", w.Code)
			}
			if w = logoutHTTPRequest(h, "GET", "/v1/me", current); w.Code != 401 {
				t.Fatal("current descendant bearer survived", w.Code)
			}
			if w = logoutHTTPRequest(h, "GET", "/v1/me", other.AccessToken); w.Code != 200 {
				t.Fatal("independent login affected", w.Code)
			}
			var state string
			if e = d.DB.QueryRow(`SELECT state FROM playback_sessions WHERE id='legacy'`).Scan(&state); e != nil || state != "stopped" {
				t.Fatal("legacy cleanup missing", e)
			}
			if w = logoutHTTPRequest(h, "DELETE", "/v1/sessions/current", presented); w.Code != 204 {
				t.Fatal("authentic retry not idempotent", w.Code)
			}
		})
	}
}

type logoutUnreadBody struct{}

func (logoutUnreadBody) Read([]byte) (int, error) { panic("logout read an unsupported request body") }
func (logoutUnreadBody) Close() error             { return nil }
func TestSessionFamilyHTTPLogoutRequestSafety(t *testing.T) {
	d, session := logoutHTTPFixture(t)
	h := New(d)
	cases := []struct {
		name   string
		status int
		change func(*http.Request)
	}{
		{"missing", 401, func(r *http.Request) { r.Header.Del("Authorization") }},
		{"unknown", 401, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+identity.Token()) }},
		{"duplicate", 401, func(r *http.Request) { r.Header.Add("Authorization", "Bearer "+session.AccessToken) }},
		{"folded", 401, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+session.AccessToken+", Bearer "+session.AccessToken)
		}},
		{"oversize", 401, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 2049)) }},
		{"prefix", 401, func(r *http.Request) { r.Header.Set("Authorization", "Basic "+session.AccessToken) }},
		{"query", 400, func(r *http.Request) { r.URL.RawQuery = "ignored=1" }},
		{"empty-query", 400, func(r *http.Request) { r.URL.ForceQuery = true }},
		{"body", 400, func(r *http.Request) { r.ContentLength = 1; r.Body = logoutUnreadBody{} }},
		{"unknown-body", 400, func(r *http.Request) { r.ContentLength = -1; r.Body = logoutUnreadBody{} }},
		{"chunked", 400, func(r *http.Request) { r.TransferEncoding = []string{"chunked"}; r.Body = logoutUnreadBody{} }},
		// Another origin may call with a token; without one it is refused before authentication.
		{"origin", 403, func(r *http.Request) {
			r.Header.Set("Origin", "http://127.0.0.1:11112")
			r.Header.Del("Authorization")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("DELETE", "/v1/sessions/current", nil)
			r.Header.Set("Authorization", "Bearer "+session.AccessToken)
			c.change(r)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != c.status {
				t.Fatal("unexpected request disposition", w.Code)
			}
			if strings.Contains(w.Body.String(), session.AccessToken) {
				t.Fatal("response disclosed proof")
			}
			if _, e := d.Identity.Authenticate(session.AccessToken); e != nil {
				t.Fatal("invalid request mutated family", e)
			}
		})
	}
	if w := logoutHTTPRequest(h, "POST", "/v1/sessions/current", session.AccessToken); w.Code != 405 {
		t.Fatal("wrong method accepted", w.Code)
	}
	r := httptest.NewRequest("DELETE", "/v1/sessions/current", nil)
	r.Header.Set("Authorization", "Bearer "+session.AccessToken)
	r.Header.Set("Origin", "http://127.0.0.1:11111")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:11111" {
		t.Fatal("allowed browser origin failed", w.Code)
	}
}
func TestSessionFamilyHTTPLogoutContextAndFailureAreSafe(t *testing.T) {
	d, session := logoutHTTPFixture(t)
	h := New(d)

	// Hold the dedicated writer gate. Reader-pool saturation must no longer block
	// logout, but the request deadline must still bound waiting for a writer.
	var e error
	held, e := dbwork.Begin(context.Background(), d.DB, dbwork.ClassInteractive)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Rollback()

	// Deterministic: the request is cancelled only once the logout is queued
	// behind the held writer (a fixed 20 ms deadline under load could expire
	// earlier, during authentication, which proves nothing about the wait).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("DELETE", "/v1/sessions/current", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+session.AccessToken)
	w := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() { defer close(finished); h.ServeHTTP(w, r) }()
	fence := dbwork.ClassSecurityFence.String()
	baseline := dbwork.WriteGate().Waiting()[fence]
	queued := func() bool { return dbwork.WriteGate().Waiting()[fence] > baseline }
	for deadline := time.Now().Add(30 * time.Second); !queued(); time.Sleep(time.Millisecond) {
		select {
		case <-finished:
			t.Fatalf("logout finished without waiting for the held writer: %d", w.Code)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("logout never queued behind the held writer")
		}
	}
	cancelled := time.Now()
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("request cancellation did not bound logout: still waiting 5 s after the cancel")
	}
	_ = held.Rollback()
	if w.Code != 503 || time.Since(cancelled) > time.Second || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("request cancellation did not bound logout: %d after %s, Retry-After %q", w.Code, time.Since(cancelled), w.Header().Get("Retry-After"))
	}
	if _, e := d.Identity.Authenticate(session.AccessToken); e != nil {
		t.Fatal("cancelled request revoked family", e)
	}
	d.Identity.OnFamilyRevokedTx = func(context.Context, *sql.Tx, identity.FamilyState) error {
		return errors.New("PRIVATE-LOGOUT-CANARY internal database text")
	}
	w = logoutHTTPRequest(h, "DELETE", "/v1/sessions/current", session.AccessToken)
	if w.Code != 503 || strings.Contains(w.Body.String(), "PRIVATE-LOGOUT-CANARY") || strings.Contains(w.Body.String(), "database") {
		t.Fatal("unsafe logout failure response", w.Code)
	}
	if _, e = d.Identity.Authenticate(session.AccessToken); e != nil {
		t.Fatal("failed descendant callback did not rollback", e)
	}
	d.Identity.OnFamilyRevokedTx = nil
	if w = logoutHTTPRequest(h, "DELETE", "/v1/sessions/current", session.AccessToken); w.Code != 204 {
		t.Fatal("retry after safe failure failed", w.Code)
	}
}
