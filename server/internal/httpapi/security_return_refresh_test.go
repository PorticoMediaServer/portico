package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/authoritygate"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	trust "github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

const securityReturnOrigin = "https://server.example:32500"

// securityReturnServer is an installed Portico Account server, composed as the
// server composes it, with a fake Hosted that answers only the registration
// lookup the prepare route makes. The owner is signed in with Portico.
type securityReturnServer struct {
	db           *sql.DB
	id           *identity.Service
	handler      http.Handler
	installation string
	session      identity.Envelope
}

func newSecurityReturnServer(t *testing.T) *securityReturnServer {
	t.Helper()
	ctx := context.Background()
	state := t.TempDir()
	if e := os.Chmod(state, 0700); e != nil {
		t.Fatal(e)
	}
	db, e := persistence.Open(storagepaths.Path(state, "server.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if e = persistence.VerifyNetworkingClaims(ctx, db); e != nil {
		t.Fatal(e)
	}
	gate, e := authoritygate.NewAuthorityGate(strings.Repeat("a", 64), false)
	if e != nil {
		t.Fatal(e)
	}
	runner, e := networking.NewAuthorityRunner(func(ctx context.Context) (networking.LifecycleLease, error) { return gate.Acquire(ctx) })
	if e != nil {
		t.Fatal(e)
	}
	keys, e := networking.OpenCurrentKeys(ctx, db, state, runner)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { keys.Close() })
	id, e := identity.New(db, state)
	if e != nil {
		t.Fatal(e)
	}
	token, e := os.ReadFile(storagepaths.Path(state, "setup-token"))
	if e != nil {
		t.Fatal(e)
	}
	owner, e := id.Setup(string(token), "owner", "long-test-password", "Server")
	if e != nil {
		t.Fatal(e)
	}
	// A Portico Account server: the owner is a linked Portico Account member.
	if _, e = db.Exec(`UPDATE accounts SET password_hash=X'';UPDATE identity_state SET sign_in_kind='portico';INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES(?,?)`, owner.Viewer.AccountID, "hosted-owner"); e != nil {
		t.Fatal(e)
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	service, e := hosted.New(db, id, "https://hosted.example", base64.RawURLEncoding.EncodeToString(pub), trust.KeyID(pub))
	if e != nil {
		t.Fatal(e)
	}
	verifier, e := networking.NewApprovalVerifier("https://hosted.example", trust.KeyID(pub), pub)
	if e != nil {
		t.Fatal(e)
	}
	cipher, e := networking.OpenClaimCipher(ctx, db, state)
	if e != nil {
		t.Fatal(e)
	}
	store, e := networking.NewSQLiteStore(db, cipher, networking.GuardLocalOwner, networking.GuardInstalledClaim, verifier, "https://hosted.example")
	if e != nil {
		t.Fatal(e)
	}
	transport, e := networking.NewHTTPTransport("https://hosted.example", store)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(transport.CloseIdleConnections)
	service.UseCurrentClaims(store, runner, transport)
	var public []byte
	if e = db.QueryRow(`SELECT public_key FROM networking_claim_identity`).Scan(&public); e != nil {
		t.Fatal(e)
	}
	// Install the claim through the store's own transitions, approved by a
	// genuinely signed Hosted approval.
	var installed networking.Intent
	e = runner.Do(ctx, func(ctx context.Context) error {
		v, e := store.Prepare(ctx, networking.Binding{OperationID: "operation", ServerID: id.ID(), AccountID: "account", PublicKey: public}, networking.LocalOwner{AccountID: owner.Viewer.AccountID, ProfileID: owner.Viewer.ProfileID, Epoch: 1})
		if e != nil {
			return e
		}
		now := time.Now().UTC()
		raw, _ := json.Marshal(struct {
			Kind                  string `json:"kind"`
			Version               string `json:"version"`
			Audience              string `json:"audience"`
			OperationID           string `json:"operationId"`
			ServerID              string `json:"serverId"`
			AccountID             string `json:"accountId"`
			PublicKey             string `json:"publicKey"`
			LocalGeneration       string `json:"localGeneration"`
			ApprovalRevision      string `json:"approvalRevision"`
			Purpose               string `json:"purpose"`
			CurrentOwnerAccountID string `json:"currentOwnerAccountId"`
			PreviousOwnerRole     string `json:"previousOwnerRole"`
			ReturnRegistrationID  string `json:"returnRegistrationId"`
			IssuedAt              string `json:"issuedAt"`
			ExpiresAt             string `json:"expiresAt"`
		}{"portico.claim.approval", "2", "https://hosted.example", v.OperationID, v.ServerID, v.AccountID, base64.RawURLEncoding.EncodeToString(public), "0", "1", "setup", "", "", "", now.Format(time.RFC3339Nano), now.Add(4 * time.Minute).Format(time.RFC3339Nano)})
		cert, e := trust.Certify(key, pub, "pin1", "documents", now.Add(-time.Hour), now.Add(120*24*time.Hour), 1, nil)
		if e != nil {
			return e
		}
		proof, _ := json.Marshal(trust.Envelope{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, raw)), KeyID: "pin1", Certificate: cert})
		if v, e = store.Approve(ctx, v, proof); e != nil {
			return e
		}
		if v, e = store.BeginFinalizing(ctx, v); e != nil {
			return e
		}
		commit := networking.Commit{OperationID: v.OperationID, ServerID: v.ServerID, ClaimGeneration: "1", CredentialGeneration: "1"}
		if v, e = store.Committed(ctx, v, commit); e != nil {
			return e
		}
		secret, _ := networking.NewSecret([]byte(strings.Repeat("s", 43)))
		defer secret.Clear()
		if v, e = store.Install(ctx, v, networking.Result{Commit: commit, Credential: secret}); e != nil {
			return e
		}
		installed, e = store.Acknowledged(ctx, v)
		return e
	})
	if e != nil {
		t.Fatal("install", e)
	}
	var generation int64
	if e = db.QueryRow(`SELECT generation FROM networking_remote_state WHERE singleton=1`).Scan(&generation); e != nil {
		t.Fatal(e)
	}
	if generation < 1 {
		if _, e = db.Exec(`UPDATE networking_remote_state SET generation=1 WHERE singleton=1`); e != nil {
			t.Fatal(e)
		}
		generation = 1
	}
	// The fake Hosted answers only the registration lookup, with the
	// registration this installed server derives for the origin.
	transport.UseRoundTripper(securityRoundTrip(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/return-registration") {
			t.Errorf("unexpected Hosted call %s", r.URL.Path)
			return nil, io.EOF
		}
		body, _ := json.Marshal(hosted.ReturnRegistration{ID: serverreturn.RegistrationID(installed.ServerID, installed.OperationID, 1, 1, generation, securityReturnOrigin), Origin: securityReturnOrigin})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	}))
	s := &securityReturnServer{id: id, installation: identity.Token()}
	s.session = s.signIn(t)
	s.handler = New(Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Hosted: service, RouteIdentity: networking.NewRouteIdentityHandler(keys, runner, nil, nil)})
	s.db = db
	return s
}

type securityRoundTrip func(*http.Request) (*http.Response, error)

func (f securityRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func (s *securityReturnServer) signIn(t *testing.T) identity.Envelope {
	t.Helper()
	ctx, e := identity.WithIssuingDevice(context.Background(), identity.DeviceRegistration{InstallationID: s.installation, Name: "Owner browser", Platform: "web", App: "portico"}, "127.0.0.1:1")
	if e != nil {
		t.Fatal(e)
	}
	signed, e := s.id.PorticoSignIn(ctx, identity.PorticoIdentity{AccountID: "hosted-owner", Username: "owner"})
	if e != nil || signed.Session == nil {
		t.Fatal("Portico sign-in", e)
	}
	return *signed.Session
}

func (s *securityReturnServer) refresh(t *testing.T) {
	t.Helper()
	next, e := s.id.RefreshSession(context.Background(), s.session.RefreshToken, s.installation, identity.Token())
	if e != nil || next.AccessToken == s.session.AccessToken {
		t.Fatal("refresh", e)
	}
	s.session = next
}

func (s *securityReturnServer) call(t *testing.T, token, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, securityReturnOrigin+path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", securityReturnOrigin)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, r)
	return w
}

func (s *securityReturnServer) prepare(t *testing.T) serverreturn.SecuritySigned {
	t.Helper()
	w := s.call(t, s.session.AccessToken, "/v1/direct/security-return/prepare", map[string]string{
		"purpose": "password_set_or_change", "target": "", "origin": securityReturnOrigin,
		"openingId": "opening-" + identity.Token()[:16], "taskId": "task-" + identity.Token()[:16], "operationId": "operation-" + identity.Token()[:16],
		"providerIdentityId": "provider-identity", "providerRevision": "1", "nonce": identity.Token(),
		"expiresAt": time.Now().UTC().Add(4 * time.Minute).Format(time.RFC3339Nano),
	})
	var signed serverreturn.SecuritySigned
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &signed) != nil || signed.Payload == "" {
		t.Fatalf("prepare %d %s", w.Code, w.Body.String())
	}
	return signed
}

func (s *securityReturnServer) confirm(t *testing.T, token string, c serverreturn.SecuritySigned) *httptest.ResponseRecorder {
	t.Helper()
	return s.call(t, token, "/v1/direct/security-return/confirm", map[string]any{"context": c, "transactionId": "transaction-" + strings.Repeat("t", 20), "code": "scr_" + strings.Repeat("c", 43)})
}

// A routine refresh while the person is away at the provider keeps the
// Security return: it is bound to the sign-in (the session family), never to
// the token that started it.
func TestSecurityReturnCarriesOnAcrossARefresh(t *testing.T) {
	s := newSecurityReturnServer(t)
	opened := s.prepare(t)
	s.refresh(t)
	w := s.confirm(t, s.session.AccessToken, opened)
	var confirmation serverreturn.SecuritySigned
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &confirmation) != nil || confirmation.Payload == "" {
		t.Fatalf("confirm after a refresh %d %s", w.Code, w.Body.String())
	}
	// And another refresh before the page checks the confirmed return.
	s.refresh(t)
	if w = s.call(t, s.session.AccessToken, "/v1/direct/security-return/check", map[string]any{"context": opened}); w.Code != http.StatusNoContent {
		t.Fatalf("check after a refresh %d %s", w.Code, w.Body.String())
	}
}

// What ends the sign-in still ends the return.
func TestSecurityReturnRefusesAnEndedOrDifferentSignIn(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, s *securityReturnServer) string{
		"revoked family": func(t *testing.T, s *securityReturnServer) string {
			if _, e := s.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, s.session.SessionFamilyID); e != nil {
				t.Fatal(e)
			}
			return s.session.AccessToken
		},
		"epoch bump": func(t *testing.T, s *securityReturnServer) string {
			if _, e := s.db.Exec(`UPDATE accounts SET epoch=epoch+1 WHERE id IN(SELECT account_id FROM account_portico_links WHERE hosted_account_id='hosted-owner')`); e != nil {
				t.Fatal(e)
			}
			return s.session.AccessToken
		},
		"another family": func(t *testing.T, s *securityReturnServer) string {
			other := s.signIn(t)
			if other.SessionFamilyID == s.session.SessionFamilyID {
				t.Fatal("second sign-in reused the family")
			}
			return other.AccessToken
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSecurityReturnServer(t)
			opened := s.prepare(t)
			token := change(t, s)
			if w := s.confirm(t, token, opened); w.Code == 200 {
				t.Fatalf("%s confirmed the return", name)
			}
			if w := s.call(t, token, "/v1/direct/security-return/check", map[string]any{"context": opened}); w.Code == http.StatusNoContent {
				t.Fatalf("%s kept the return", name)
			}
		})
	}
}
