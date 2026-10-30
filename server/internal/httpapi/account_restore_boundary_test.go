package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A real password sign-in with more than one profile remains account-only.
// Neither restoration nor refresh selects a profile or authorizes /v1/me.
func TestAccountRestoreUsesAccountScopeUntilExplicitProfileSelection(t *testing.T) {
	d, owner := completedLocalFoundationFixture(t)
	h := New(d)
	const installation = "66a2cf74-34a1-49aa-984f-76547f54b76e"
	call := func(method, path, token string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if body != nil {
			var err error
			raw, err = json.Marshal(body)
			if err != nil {
				t.Fatal("test request encoding failed")
			}
		}
		defer clear(raw)
		r := httptest.NewRequest(method, "http://127.0.0.1:11111"+path, bytes.NewReader(raw))
		r.RemoteAddr = "127.0.0.1:42000"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Portico-Installation-Id", installation)
		r.Header.Set("X-Portico-Device-Platform", "web")
		r.Header.Set("X-Portico-App", "Portico Web")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/v1/direct/profiles", owner.AccessToken, map[string]string{"name": "Second profile", "art": "blue"}); w.Code != 201 {
		t.Fatalf("profile creation status=%d", w.Code)
	}
	w := call("POST", "/v1/direct/sign-in", "", map[string]string{"username": "owner", "password": "Local-password1!"})
	var signedIn identity.DirectSignIn
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &signedIn) != nil || signedIn.Session == nil || signedIn.Session.Viewer.Role != "account" || len(signedIn.Profiles) != 2 {
		t.Fatalf("password account admission status=%d", w.Code)
	}
	if len(w.Header().Values("Set-Cookie")) != 0 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("sign-in is not a cookie-based authority")
	}
	account := *signedIn.Session
	if w := call("GET", "/v1/direct", "", nil); w.Code != 401 {
		t.Fatalf("anonymous account snapshot status=%d", w.Code)
	}
	assertRestricted := func(token string) {
		t.Helper()
		for _, path := range []string{"/v1/me", "/v1/bootstrap"} {
			if w := call("GET", path, token, nil); w.Code != 401 {
				t.Fatalf("account-only viewer refusal path=%s status=%d", path, w.Code)
			}
		}
		w := call("GET", "/v1/direct", token, nil)
		var snapshot identity.DirectSnapshot
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &snapshot) != nil || snapshot.Account.ID != signedIn.Account.ID || snapshot.Account.PrimaryProfileID != account.Viewer.ProfileID || len(snapshot.Profiles) != 2 {
			t.Fatalf("live account snapshot status=%d", w.Code)
		}
	}
	assertRestricted(account.AccessToken)
	w = call("POST", "/v1/auth/refresh", "", map[string]string{"refreshToken": account.RefreshToken, "installationId": installation, "requestId": "restore-account-request"})
	var renewed identity.Envelope
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &renewed) != nil || renewed.Viewer.Role != "account" || renewed.Viewer.AccountID != account.Viewer.AccountID {
		t.Fatalf("account-only refresh status=%d", w.Code)
	}
	assertRestricted(renewed.AccessToken)
	w = call("POST", "/v1/direct/profiles/"+account.Viewer.ProfileID+"/select", renewed.AccessToken, map[string]string{"pin": "", "installationId": installation})
	var selection struct {
		Session identity.Envelope `json:"session"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &selection) != nil || selection.Session.Viewer.Role != "owner" {
		t.Fatalf("explicit profile selection status=%d", w.Code)
	}
	if w := call(http.MethodGet, "/v1/me", selection.Session.AccessToken, nil); w.Code != 200 {
		t.Fatalf("selected viewer status=%d", w.Code)
	}
	if err := d.Identity.LogoutToken(t.Context(), renewed.AccessToken); err != nil {
		t.Fatal("account family retirement failed")
	}
	if w := call("GET", "/v1/direct", renewed.AccessToken, nil); w.Code != 401 {
		t.Fatalf("retired account snapshot status=%d", w.Code)
	}
}
