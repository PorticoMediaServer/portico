package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func privateResetCode(t *testing.T, location identity.OwnerResetLocation) string {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join(location.FileDirectory, location.FileName))
	if e != nil {
		t.Fatal(e)
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) != 6 || len(lines[2]) != 43 {
		t.Fatal("invalid private reset file")
	}
	return lines[2]
}
func TestResetFileHTTPProducerContractsAndAuthority(t *testing.T) {
	responses := map[string]json.RawMessage{}
	for _, mode := range []string{"local", "portico"} {
		t.Run(mode, func(t *testing.T) {
			f := administrationFixtureNew(t)
			if mode == "portico" {
				if _, e := f.deps.DB.Exec(`UPDATE accounts SET password_hash=X'';UPDATE identity_state SET sign_in_kind='portico';INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES('owner','verified-owner')`); e != nil {
					t.Fatal(e)
				}
			}
			call := func(path, body string) *httptest.ResponseRecorder {
				return recoveryRequest(f, remotePeer, "POST", path, "", body)
			}
			first := call("/v1/auth/owner-reset/request", `{"username":"owner"}`)
			if first.Code != 200 || first.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("request %d", first.Code)
			}
			assertSpecResponse(t, "POST", "/v1/auth/owner-reset/request", first)
			var location identity.OwnerResetLocation
			if e := json.Unmarshal(first.Body.Bytes(), &location); e != nil {
				t.Fatal(e)
			}
			code := privateResetCode(t, location)
			unknown := call("/v1/auth/owner-reset/request", `{"username":"missing"}`)
			if unknown.Code != 200 || strings.Contains(unknown.Body.String(), code) || strings.Contains(unknown.Body.String(), "missing") || strings.Contains(unknown.Body.String(), "owner\"") {
				t.Fatal("public reset request disclosed account/code")
			}
			for _, path := range []string{"/v1/setup/remote-recovery", "/v1/direct/password-help"} {
				if w := call(path, `{}`); w.Code >= 200 && w.Code < 300 {
					t.Fatal("retired route reachable", path)
				}
			}
			body, _ := json.Marshal(identity.OwnerResetVerify{Code: code})
			verified := call("/v1/auth/owner-reset/verify", string(body))
			if verified.Code != 200 {
				t.Fatalf("verify %d %s", verified.Code, verified.Body.String())
			}
			assertSpecResponse(t, "POST", "/v1/auth/owner-reset/verify", verified)
			var out identity.DirectSignIn
			if e := json.Unmarshal(verified.Body.Bytes(), &out); e != nil {
				t.Fatal(e)
			}
			if out.Session == nil || out.Session.RefreshToken != "" || out.ResetFileOwnerSession != (mode == "portico") || out.PasswordChangeRequired != (mode == "local") {
				t.Fatal("incorrect reset authority")
			}
			if mode == "local" {
				if w := recoveryRequest(f, remotePeer, "GET", "/v1/direct/members", out.Session.AccessToken, ""); w.Code >= 200 && w.Code < 300 {
					t.Fatal("must_change session managed members")
				}
			} else {
				if w := recoveryRequest(f, remotePeer, "GET", "/v1/direct/members", out.Session.AccessToken, ""); w.Code != 200 {
					t.Fatalf("reset-file owner session not genuine owner %d %s", w.Code, w.Body.String())
				}
			}
			if w := call("/v1/auth/owner-reset/verify", string(body)); w.Code != 401 {
				t.Fatal("replay admitted", w.Code)
			}
			if w := recoveryRequest(f, remotePeer, "GET", "/v1/direct/members", f.owner, ""); w.Code != 401 {
				t.Fatal("old owner session not revoked", w.Code)
			}
			responses[mode] = append(json.RawMessage(nil), verified.Body.Bytes()...)
			responses["location"] = append(json.RawMessage(nil), first.Body.Bytes()...)
		})
	}
	if path := os.Getenv("PORTICO_TEST_RESET_RESPONSES"); path != "" {
		raw, e := json.Marshal(responses)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
}
func TestMemberResetHTTPRejectsPasswordAndNonAdministrativeAuthorities(t *testing.T) {
	f := administrationFixtureNew(t)
	for _, q := range []struct{ token, target string }{{f.member, "member"}, {f.admin, "owner"}, {f.admin, "admin"}, {"", "member"}} {
		w := f.call("POST", "/v1/direct/members/"+q.target+"/reset-code", q.token, `{"expectedRevision":1}`)
		if w.Code != 401 && w.Code != 403 {
			t.Fatalf("invalid reset authority status %d", w.Code)
		}
	}
	if w := f.call("PATCH", "/v1/direct/members/member", f.owner, `{"expectedRevision":1,"password":"Owner-chosen-password1!","allowedLibraries":[]}`); w.Code != 400 {
		t.Fatalf("PATCH accepted password: %d", w.Code)
	}
	issued := f.call("POST", "/v1/direct/members/member/reset-code", f.admin, `{"expectedRevision":1}`)
	if issued.Code != 200 {
		t.Fatalf("admin member reset %d %s", issued.Code, issued.Body.String())
	}
	assertSpecResponse(t, "POST", "/v1/direct/members/{id}/reset-code", issued)
	var code identity.MemberResetCode
	if e := json.Unmarshal(issued.Body.Bytes(), &code); e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(identity.MemberResetVerify{Username: "member", Code: code.Code})
	redeemed := f.call("POST", "/v1/direct/password/reset", "", string(body))
	if redeemed.Code != 200 {
		t.Fatalf("member redemption %d %s", redeemed.Code, redeemed.Body.String())
	}
	assertSpecResponse(t, "POST", "/v1/direct/password/reset", redeemed)
	if w := f.call("POST", "/v1/direct/password/reset", "", string(body)); w.Code != 401 {
		t.Fatal("member replay admitted", w.Code)
	}
	if _, e := f.deps.Identity.DirectMe(context.Background(), f.member); e == nil {
		t.Fatal("old member session survived")
	}
}
