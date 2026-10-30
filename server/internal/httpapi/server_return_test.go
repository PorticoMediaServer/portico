package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strings"
	"testing"
)

func TestRegisteredReturnServicesUnavailableIsPublicRetryableRefusal(t *testing.T) {
	w := httptest.NewRecorder()
	failure(w, hosted.ErrServicesUnavailable)
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"code":"portico_services_unavailable"`) || !strings.Contains(w.Body.String(), `"retry":"same_request"`) {
		t.Fatalf("offline refusal %d %s", w.Code, w.Body)
	}
	assertSpecResponse(t, "POST", "/v1/auth/portico-return/exchange", w)
}

func TestRegisteredReturnPreparationRejectsUnboundOriginsAndLocal(t *testing.T) {
	f := administrationFixtureNew(t)
	if _, e := f.deps.DB.Exec(`UPDATE accounts SET password_hash=X'';UPDATE identity_state SET sign_in_kind='portico'`); e != nil {
		t.Fatal(e)
	}
	for _, q := range []struct{ origin, nonce, installation string }{
		{"https://evil.example", identity.Token(), identity.Token()},
		{"http://server.example:32500", identity.Token(), identity.Token()},
		{"https://server.example:32500/path", identity.Token(), identity.Token()},
		{"https://server.example:32500", "bad", identity.Token()},
		{"https://server.example:32500", identity.Token(), ""},
	} {
		raw, _ := json.Marshal(porticoReturnPrepareRequest{Origin: q.origin, Nonce: q.nonce})
		r := httptest.NewRequest("POST", "https://server.example:32500/v1/auth/portico-return/prepare", strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Portico-Installation-Id", q.installation)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("unbound prepare %d %s", w.Code, w.Body)
		}
		assertSpecResponse(t, "POST", "/v1/auth/portico-return/prepare", w)
	}
	if _, e := f.deps.DB.Exec(`UPDATE identity_state SET sign_in_kind='local'`); e != nil {
		t.Fatal(e)
	}
	w := f.call("POST", "/v1/auth/portico-return/prepare", "", `{"origin":"https://server.example:32500","nonce":"`+identity.Token()+`"}`)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "sign_in_kind_local") {
		t.Fatalf("Local prepare %d %s", w.Code, w.Body)
	}
	var challenges int
	if e := f.deps.DB.QueryRow(`SELECT count(*) FROM hosted_identity_challenges`).Scan(&challenges); e != nil || challenges != 0 {
		t.Fatal("failed prepare created challenges", e)
	}
}

func TestRegisteredReturnPreparationUsesDeviceInstallationGrammar(t *testing.T) {
	f := administrationFixtureNew(t)
	if _, e := f.deps.DB.Exec(`UPDATE accounts SET password_hash=X'';UPDATE identity_state SET sign_in_kind='portico'`); e != nil {
		t.Fatal(e)
	}
	const uuid = "33333333-3333-4333-8333-333333333333"
	for _, q := range []struct {
		name, origin, nonce, installation string
		status                            int
	}{
		{"existing-uuid", "https://server.example:32500", identity.Token(), uuid, 401},
		{"existing-random-token", "https://server.example:32500", identity.Token(), identity.Token(), 401},
		{"short-installation", "https://server.example:32500", identity.Token(), strings.Repeat("a", 31), 400},
		{"long-installation", "https://server.example:32500", identity.Token(), strings.Repeat("a", 129), 400},
		{"installation-path", "https://server.example:32500", identity.Token(), uuid + "/", 400},
		{"installation-padding", "https://server.example:32500", identity.Token(), uuid + "=", 400},
		{"nonce-uuid", "https://server.example:32500", uuid, uuid, 400},
		{"nonce-padding", "https://server.example:32500", identity.Token() + "=", uuid, 400},
		{"nonce-noncanonical", "https://server.example:32500", strings.Repeat("a", 43), uuid, 400},
		{"different-host", "https://evil.example", identity.Token(), uuid, 400},
		{"insecure-origin", "http://server.example:32500", identity.Token(), uuid, 400},
	} {
		t.Run(q.name, func(t *testing.T) {
			raw, _ := json.Marshal(porticoReturnPrepareRequest{Origin: q.origin, Nonce: q.nonce})
			r := httptest.NewRequest("POST", "https://server.example:32500/v1/auth/portico-return/prepare", strings.NewReader(string(raw)))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Portico-Installation-Id", q.installation)
			w := httptest.NewRecorder()
			f.handler.ServeHTTP(w, r)
			// Valid input reaches the existing unavailable-signer refusal. This fixture
			// deliberately has no registered return authority and must not mint a proof.
			if w.Code != q.status {
				t.Fatalf("prepare status %d, want %d: %s", w.Code, q.status, w.Body.String())
			}
			assertSpecResponse(t, "POST", "/v1/auth/portico-return/prepare", w)
		})
	}
	var challenges int
	if e := f.deps.DB.QueryRow(`SELECT count(*) FROM hosted_identity_challenges`).Scan(&challenges); e != nil || challenges != 0 {
		t.Fatal("unavailable preparation created challenges", e)
	}
}

func TestRegisteredReturnHTTPAuthorityAndPrivateFailures(t *testing.T) {
	f := administrationFixtureNew(t)
	const code = "src_private-disposable-fixture-code-01234567890123456789"
	for _, kind := range []string{"missing-installation", "mismatched-installation", "untrusted-setup-host"} {
		t.Run(kind, func(t *testing.T) {
			body := map[string]any{"code": code, "transactionId": "srt_fixture", "verifier": strings.Repeat("v", 43), "registrationId": "ret_fixture", "serverChallenge": strings.Repeat("c", 32), "installationId": strings.Repeat("i", 43), "purpose": "setup", "requestId": "33333333-3333-4333-8333-333333333333", "setupToken": "private-setup-fixture", "expectedRevision": "0"}
			raw, _ := json.Marshal(body)
			request := httptest.NewRequest("POST", "http://localhost:32500/v1/auth/portico-return/exchange", strings.NewReader(string(raw)))
			request.RemoteAddr = "192.168.1.20:1000"
			request.Header.Set("Content-Type", "application/json")
			if kind == "mismatched-installation" {
				request.Header.Set("X-Portico-Installation-Id", strings.Repeat("j", 43))
			}
			if kind == "untrusted-setup-host" {
				request.Host = "rebinding.attacker.test:32500"
				request.Header.Set("Origin", "http://rebinding.attacker.test:32500")
				request.Header.Set("X-Portico-Installation-Id", strings.Repeat("i", 43))
			}
			response := httptest.NewRecorder()
			f.handler.ServeHTTP(response, request)
			if response.Code >= 200 && response.Code < 300 || response.Code >= 500 {
				t.Fatalf("authority failure status %d", response.Code)
			}
			if strings.Contains(response.Body.String(), code) || strings.Contains(response.Body.String(), "private-setup-fixture") {
				t.Fatal("private failure disclosed continuation")
			}
			assertSpecResponse(t, "POST", "/v1/auth/portico-return/exchange", response)
			var owners int
			if e := f.deps.DB.QueryRow(`SELECT count(*) FROM direct_memberships WHERE role='owner' AND disabled=0`).Scan(&owners); e != nil || owners != 1 {
				t.Fatal("failed return changed accounts", e)
			}
		})
	}
}
