package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func pinRecoveryRequest(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:11111"+path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAccountOnlySignInCanRecoverProtectedPIN(t *testing.T) {
	for _, primary := range []bool{true, false} {
		name := "multiple profiles"
		if primary {
			name = "protected primary"
		}
		t.Run(name, func(t *testing.T) {
			d, owner := completedLocalFoundationFixture(t)
			profile := owner.Viewer.ProfileID
			if !primary {
				account, err := d.Identity.CreateDirectProfile(t.Context(), owner.AccessToken, "Protected", "blue")
				if err != nil {
					t.Fatal(err)
				}
				for _, candidate := range account.Profiles {
					if !candidate.Primary {
						profile = candidate.ID
					}
				}
				if profile == owner.Viewer.ProfileID {
					t.Fatal("secondary profile was not created")
				}
			}
			if _, err := d.Identity.PINReset(t.Context(), owner.AccessToken, profile, "4821"); err != nil {
				t.Fatal(err)
			}
			h := New(d)
			w := pinRecoveryRequest(t, h, "POST", "/v1/direct/sign-in", "", map[string]string{"username": "owner", "password": "Local-password1!"})
			var signed identity.DirectSignIn
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &signed) != nil || signed.Session == nil || signed.Session.Viewer.Role != "account" {
				t.Fatalf("fresh account-only sign-in: status=%d", w.Code)
			}
			token := signed.Session.AccessToken
			w = pinRecoveryRequest(t, h, "GET", "/v1/direct/pin-recovery", token, nil)
			var methods identity.PINRecoveryMethods
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &methods) != nil || !methods.Password {
				t.Fatalf("account-only recovery descriptor: status=%d", w.Code)
			}
			if w := pinRecoveryRequest(t, h, "GET", "/v1/me", token, nil); w.Code != 401 {
				t.Fatalf("recovery grant became a viewer: status=%d", w.Code)
			}
			// Session management is a typed direct-account route that still
			// requires a selected viewer, unlike legacy profile creation.
			if w := pinRecoveryRequest(t, h, "GET", "/v1/direct/sessions", token, nil); w.Code != 401 {
				t.Fatalf("recovery widened another management route: status=%d", w.Code)
			}
			path := "/v1/direct/profiles/" + profile + "/pin-reset"
			if w := pinRecoveryRequest(t, h, "POST", path, "", map[string]string{"pin": ""}); w.Code != 401 {
				t.Fatalf("anonymous recovery: status=%d", w.Code)
			}
			if w := pinRecoveryRequest(t, h, "POST", path, token, map[string]string{"pin": ""}); w.Code != 200 {
				t.Fatalf("account-only PIN reset: status=%d", w.Code)
			}
			var protected bool
			if err := d.DB.QueryRow(`SELECT pin_hash IS NOT NULL FROM direct_profiles WHERE id=?`, profile).Scan(&protected); err != nil || protected {
				t.Fatalf("PIN was not cleared: protected=%v err=%v", protected, err)
			}
			if err := d.Identity.LogoutToken(t.Context(), token); err != nil {
				t.Fatal(err)
			}
			if w := pinRecoveryRequest(t, h, "GET", "/v1/direct/pin-recovery", token, nil); w.Code != 401 {
				t.Fatalf("retired recovery credential: status=%d", w.Code)
			}
		})
	}
}

func TestAccountPINRecoveryKeepsRequiredFactorGate(t *testing.T) {
	d, owner := completedLocalFoundationFixture(t)
	policy, err := d.Identity.ReadSignInPolicy(t.Context(), owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	policy.Values.RequireTwoStep = "administrators"
	if _, err = d.Identity.ChangeSignInPolicy(t.Context(), owner.AccessToken, identity.SignInPolicyPatch{ExpectedRevision: policy.Revision, Values: policy.Values}); err != nil {
		t.Fatal(err)
	}
	h := New(d)
	w := pinRecoveryRequest(t, h, "POST", "/v1/direct/sign-in", "", map[string]string{"username": "owner", "password": "Local-password1!"})
	var signed identity.DirectSignIn
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &signed) != nil || signed.Session == nil || signed.Session.Viewer.Role != "account" || !signed.TwoStepSetupRequired {
		t.Fatalf("required-factor sign-in: status=%d", w.Code)
	}
	for _, request := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/direct/pin-recovery", nil},
		{"POST", "/v1/direct/profiles/" + owner.Viewer.ProfileID + "/pin-reset", map[string]string{"pin": "1234"}},
	} {
		w := pinRecoveryRequest(t, h, request.method, request.path, signed.Session.AccessToken, request.body)
		if w.Code != 403 || !bytes.Contains(w.Body.Bytes(), []byte(`"code":"factor_setup_required"`)) {
			t.Fatalf("factor gate %s: status=%d", request.path, w.Code)
		}
	}
	var revision int64
	if err = d.DB.QueryRow(`SELECT pin_revision FROM direct_profiles WHERE id=?`, owner.Viewer.ProfileID).Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("factor-gated reset changed PIN revision: revision=%d err=%v", revision, err)
	}
}
