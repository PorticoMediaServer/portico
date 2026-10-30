package networking

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"strconv"
	"testing"
	"time"
)

func handlerFixture(t *testing.T) (*sqlFixture, *ClaimHandler, *modelTransport) {
	t.Helper()
	f := fixtureCurrentSQL(t)
	f.now = time.Now().UTC()
	verifier, e := NewApprovalVerifier("https://hosted.example", trust.KeyID(f.hostedKey.Public().(ed25519.PublicKey)), f.hostedKey.Public().(ed25519.PublicKey))
	if e != nil {
		t.Fatal(e)
	}
	f.store.verifyApproval = verifier
	f.store.installedGuard = GuardInstalledClaim
	if _, e = f.db.Exec(`CREATE TABLE request_family(active INTEGER);INSERT INTO request_family VALUES(1)`); e != nil {
		t.Fatal(e)
	}
	_, runner := lifecycleRunner(t)
	transport := &modelTransport{now: f.now}
	authorize := func(r *http.Request) (ClaimRequestOwner, error) {
		if r.Header.Get("Authorization") != "Bearer current" || r.Header.Get("Origin") != "https://web.example" {
			return ClaimRequestOwner{}, ErrClaimOwnerRequired
		}
		return ClaimRequestOwner{Owner: f.owner, Guard: func(ctx context.Context, tx *sql.Tx) error {
			var active int
			e := tx.QueryRowContext(ctx, `SELECT active FROM request_family`).Scan(&active)
			if e != nil {
				return e
			}
			if active != 1 {
				return ErrClaimOwnerRequired
			}
			return nil
		}}, nil
	}
	h, e := NewClaimHandler(f.store, runner, transport, modelSigner{key: f.localKey}, authorize, func() string { return "Test server" })
	if e != nil {
		t.Fatal(e)
	}
	return f, h, transport
}
func handlerCall(t *testing.T, h *ClaimHandler, method, path string, body any) (ClaimStatus, int) {
	t.Helper()
	var raw []byte
	if body != nil {
		var e error
		raw, e = json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Origin", "https://web.example")
	r.Header.Set("Authorization", "Bearer current")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var result ClaimStatus
	if w.Code == 200 {
		if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
			t.Fatal(e)
		}
	} else {
		t.Log(w.Body.String())
	}
	return result, w.Code
}

func TestFirstOwnerSwitchPreparedSnapshotUsesPendingOperation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, h, tr := handlerFixture(t)
	if _, err := f.db.Exec(`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local'); INSERT INTO identity_moves(id,owner_id,owner_revision,from_kind,to_kind,start_over,stage,revision,people,devices,profiles,server_name,created_ms,owner_confirmed) VALUES('owner-switch-preview','local_owner',1,'local','portico',0,'preview',1,1,1,1,'Fixture',1,1)`); err != nil {
		t.Fatal(err)
	}
	original := h.authorize
	h.authorize = func(r *http.Request) (ClaimRequestOwner, error) {
		actor, err := original(r)
		actor.WebOrigin = "https://web.example"
		return actor, err
	}
	before, code := handlerCall(t, h, http.MethodGet, "/v1/networking/claim", nil)
	if code != 200 || before.State != "unconfigured" {
		t.Fatal("initial state", code, before)
	}
	prepared, code := handlerCall(t, h, http.MethodPost, "/v1/networking/claim/prepare", ClaimPrepareRequest{Expected: before.Identity, Purpose: "switch"})
	if code != 200 || prepared.State != string(Prepared) || prepared.Operation == nil {
		t.Fatal("first owner switch was hidden by empty installed pointer", code, prepared)
	}
	again, code := handlerCall(t, h, http.MethodGet, "/v1/networking/claim", nil)
	if code != 200 || again.Operation == nil || *again.Operation != *prepared.Operation || again.State != string(Prepared) {
		t.Fatal("pending operation was not resumable", code, again)
	}
	var active, installed sql.NullString
	if err := f.db.QueryRow(`SELECT active_operation_id,installed_operation_id FROM networking_claim_identity WHERE singleton=1`).Scan(&active, &installed); err != nil || active.Valid || installed.Valid {
		t.Fatal("snapshot invented an installed claim", active.Valid, installed.Valid, err)
	}
	if tr.finalizes != 0 || tr.retrieves != 0 {
		t.Fatal("read/prepare called remote claim commit")
	}
}
func handlerPrepare(t *testing.T, f *sqlFixture, h *ClaimHandler) ClaimStatus {
	t.Helper()
	s, code := handlerCall(t, h, "GET", "/v1/networking/claim", nil)
	if code != 200 || s.State != "unconfigured" {
		t.Fatal("initial", code, s)
	}
	body := struct {
		AccountID string                `json:"accountId"`
		Expected  ClaimIdentityExpected `json:"expected"`
	}{f.binding.AccountID, s.Identity}
	s, code = handlerCall(t, h, "POST", "/v1/networking/claim/prepare", body)
	if code != 200 || s.State != "prepared" || s.ApprovalRequest == nil {
		t.Fatal("prepare", code, s)
	}
	return s
}
func handlerApprove(t *testing.T, f *sqlFixture, h *ClaimHandler, s ClaimStatus) ClaimStatus {
	t.Helper()
	a := s.ApprovalRequest
	w := approvalWire{Kind: "portico.claim.approval", Version: "2", Audience: "https://hosted.example", OperationID: a.OperationID, ServerID: a.ServerID, AccountID: f.binding.AccountID, PublicKey: a.PublicKey, LocalGeneration: a.LocalGeneration, ApprovalRevision: 1, Purpose: "setup", IssuedAt: f.now.Format(time.RFC3339Nano), ExpiresAt: f.now.Add(5 * time.Minute).Format(time.RFC3339Nano)}
	raw, _ := json.Marshal(w)
	body := struct {
		Expected         ClaimExpected `json:"expected"`
		ApprovalEnvelope string        `json:"approvalEnvelope,omitempty"`
	}{*s.Operation, string(signedApprovalFixture(string(raw), f.hostedKey))}
	s, code := handlerCall(t, h, "POST", "/v1/networking/claim/approve", body)
	if code != 200 || s.State != "approved" {
		t.Fatal("approve", code, s)
	}
	return s
}
func TestClaimHandlerOwnerJourney(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, h, tr := handlerFixture(t)
	s := handlerApprove(t, f, h, handlerPrepare(t, f, h))
	first := *s.Operation
	for _, want := range []string{"retrieving", "installed", "installed"} {
		body := struct {
			Expected ClaimExpected `json:"expected"`
		}{*s.Operation}
		var code int
		s, code = handlerCall(t, h, "POST", "/v1/networking/claim/continue", body)
		if code != 200 || s.State != want {
			t.Fatal("continue", code, s)
		}
	}
	if !s.InstallationAcknowledged || tr.finalizes != 1 || tr.retrieves != 1 {
		t.Fatal("not acknowledged")
	}
	if _, code := handlerCall(t, h, "POST", "/v1/networking/claim/continue", struct {
		Expected ClaimExpected `json:"expected"`
	}{first}); code != 409 {
		t.Fatal("stale accepted", code)
	}
	s, code := handlerCall(t, h, "POST", "/v1/networking/claim/cancel", struct {
		Expected ClaimExpected `json:"expected"`
	}{*s.Operation})
	if code != 200 || s.State != "unconfigured" || tr.cancels != 1 {
		t.Fatal("cancel", code, s)
	}
	var keys int
	if e := f.db.QueryRow(`SELECT count(*) FROM networking_server_identities`).Scan(&keys); e != nil || keys != 1 {
		t.Fatal("erased identity")
	}
}
func TestClaimHandlerSessionRevokedDuringRemote(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, h, tr := handlerFixture(t)
	s := handlerApprove(t, f, h, handlerPrepare(t, f, h))
	tr.finalizeHook = func() error { _, e := f.db.Exec(`UPDATE request_family SET active=0`); return e }
	if _, code := handlerCall(t, h, "POST", "/v1/networking/claim/continue", struct {
		Expected ClaimExpected `json:"expected"`
	}{*s.Operation}); code != 401 {
		t.Fatal("late revoked published", code)
	}
	v, e := f.store.Load(context.Background(), s.Operation.OperationID)
	if e != nil || v.Stage != Finalizing {
		t.Fatal("advanced", e, v.Stage)
	}
}
func TestClaimHandlerMandatoryAdmissionAndCommitGuard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, h, _ := handlerFixture(t)
	r := httptest.NewRequest("GET", "/v1/networking/claim", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("missing owner")
	}
	previous := h.authorize
	h.authorize = func(r *http.Request) (ClaimRequestOwner, error) { a, e := previous(r); a.Guard = nil; return a, e }
	if _, code := handlerCall(t, h, "GET", "/v1/networking/claim", nil); code != 401 {
		t.Fatal("missing guard")
	}
	h.authorize = previous
	s, code := handlerCall(t, h, "GET", "/v1/networking/claim", nil)
	if code != 200 {
		t.Fatal(code)
	}
	h.authorize = func(r *http.Request) (ClaimRequestOwner, error) {
		a, e := previous(r)
		base := a.Guard
		a.Guard = func(ctx context.Context, tx *sql.Tx) error {
			if e := base(ctx, tx); e != nil {
				return e
			}
			var count int
			if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM networking_claim_intents`).Scan(&count); e != nil {
				return e
			}
			if count > 0 {
				return ErrClaimOwnerRequired
			}
			return nil
		}
		return a, e
	}
	body := struct {
		AccountID string                `json:"accountId"`
		Expected  ClaimIdentityExpected `json:"expected"`
	}{f.binding.AccountID, s.Identity}
	if _, code = handlerCall(t, h, "POST", "/v1/networking/claim/prepare", body); code != 401 {
		t.Fatal("commit guard", code)
	}
	var count int
	if e := f.db.QueryRow(`SELECT count(*) FROM networking_claim_intents`).Scan(&count); e != nil || count != 0 {
		t.Fatal("denied write committed")
	}
}

// The web console's Connect prepares without an account (the approver on
// web.getportico.tv becomes the owner) and sends exactly {"expected":{...}}.
// The canonical decoder must accept that body; it answered 400
// invalid_request before, so no claim could start from the web (demo, 24 Sep).
func TestClaimHandlerPreparesWithoutAnAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, h, _ := handlerFixture(t)
	s, code := handlerCall(t, h, "GET", "/v1/networking/claim", nil)
	if code != 200 || s.State != "unconfigured" {
		t.Fatal("initial", code, s)
	}
	// Byte for byte what the console sends: the identity object as the server gave it.
	body := json.RawMessage(`{"expected":{"serverId":"` + s.Identity.ServerID + `","localGeneration":"` + strconv.FormatInt(s.Identity.LocalGeneration, 10) + `"}}`)
	s, code = handlerCall(t, h, "POST", "/v1/networking/claim/prepare", body)
	if code != 200 || s.State != "prepared" || s.AccountID != UnboundAccount {
		t.Fatalf("unbound prepare: %d %+v", code, s)
	}
	// An explicit empty accountId is not canonical and stays refused.
	_, h2, _ := handlerFixture(t)
	s2, _ := handlerCall(t, h2, "GET", "/v1/networking/claim", nil)
	raw := json.RawMessage(`{"accountId":"","expected":{"serverId":"` + s2.Identity.ServerID + `","localGeneration":"` + strconv.FormatInt(s2.Identity.LocalGeneration, 10) + `"}}`)
	if _, code = handlerCall(t, h2, "POST", "/v1/networking/claim/prepare", raw); code != 400 {
		t.Fatal("empty accountId accepted", code)
	}
}

// The web-approval request of an account-less claim is signed with
// accountId "" while its binding holds UnboundAccount; the signer must accept
// exactly that pairing (it refused it, so web-approval answered 400 too), and
// still refuse any other account.
func TestUnboundWebClaimRequestIsSigned(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, h, _ := handlerFixture(t)
	s, _ := handlerCall(t, h, "GET", "/v1/networking/claim", nil)
	body := json.RawMessage(`{"expected":{"serverId":"` + s.Identity.ServerID + `","localGeneration":"` + strconv.FormatInt(s.Identity.LocalGeneration, 10) + `"}}`)
	if s, _ = handlerCall(t, h, "POST", "/v1/networking/claim/prepare", body); s.Operation == nil {
		t.Fatal("prepare")
	}
	var incarnation string
	if err := f.db.QueryRow(`SELECT key_incarnation FROM networking_server_identities`).Scan(&incarnation); err != nil {
		t.Fatal(err)
	}
	keyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(keyDir, incarnation+".ed25519"), f.localKey, 0600); err != nil {
		t.Fatal(err)
	}
	keyRoot, err := os.OpenRoot(keyDir)
	if err != nil {
		t.Fatal(err)
	}
	defer keyRoot.Close()
	keys := &ProtectedKeys{db: f.db, root: keyRoot}
	sign := func(account string) error {
		return h.runner.Do(context.Background(), func(ctx context.Context) error {
			v, e := h.expected(ctx, *s.Operation)
			if e != nil {
				return e
			}
			if v.AccountID != UnboundAccount {
				t.Fatalf("binding account %q", v.AccountID)
			}
			raw, _ := json.Marshal(map[string]any{"purpose": "setup", "currentOwnerAccountId": "", "previousOwnerRole": "", "returnRegistrationId": "", "kind": "portico.claim.web.v2", "operationId": v.OperationID, "serverId": v.ServerID, "accountId": account, "publicKey": base64.RawURLEncoding.EncodeToString(v.PublicKey), "localGeneration": strconv.FormatInt(v.LocalGeneration, 10)})
			_, e = keys.Sign(ctx, v.Binding, raw)
			return e
		})
	}
	if e := sign(""); e != nil {
		t.Fatal("unbound web claim request not signed:", e)
	}
	for _, other := range []string{UnboundAccount, "acct_someone"} {
		if e := sign(other); !errors.Is(e, ErrInvalid) {
			t.Fatal("signed for account", other, e)
		}
	}
}
