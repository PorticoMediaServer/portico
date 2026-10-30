package networking_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/authoritygate"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	trust "github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/httpapi"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
)

type ownerPairReply struct {
	Status                             int
	Headers                            http.Header
	Body                               json.RawMessage
	BodyEmpty                          bool
	FinalizeRequests, ExchangeRequests int
}
type ownerPairBridge struct {
	t                                     *testing.T
	cmd                                   *exec.Cmd
	in                                    io.WriteCloser
	out                                   *bufio.Scanner
	mu                                    sync.Mutex
	afterHTTP                             func(*http.Request, ownerPairReply) error
	approved                              chan struct{}
	Root, AccountID, OwnerToken, Audience string
}

// The private producer is an actual checked Hosted test executable. Every
// ephemeral credential and raw response stays in these pipes, even on failure.
func newOwnerPairBridge(t *testing.T) *ownerPairBridge {
	t.Helper()
	binary := os.Getenv("PORTICO_OWNER_CONTINUATION_BINARY")
	if binary == "" {
		t.Skip("checked Hosted executable and isolated PostgreSQL required for actual pair")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestOwnerContinuationActualProducerBridge$", "-test.count=1")
	cmd.Env = append(os.Environ(), "PORTICO_OWNER_CONTINUATION_BRIDGE=1")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal("private producer input unavailable")
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal("private producer output unavailable")
	}
	// No child diagnostic bytes are copied to logs: a failing producer may
	// include a raw response in its own test diagnostics.
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		t.Fatal("private producer failed to start")
	}
	b := &ownerPairBridge{t: t, cmd: cmd, in: in, out: bufio.NewScanner(out), approved: make(chan struct{})}
	b.out.Buffer(make([]byte, 4096), 256<<10)
	t.Cleanup(func() {
		b.mu.Lock()
		_ = json.NewEncoder(b.in).Encode(map[string]string{"Action": "stop"})
		_ = b.in.Close()
		b.mu.Unlock()
		if b.cmd.Wait() != nil {
			t.Error("private producer failed or did not stop cleanly")
		}
	})
	var hello struct{ Root, AccountID, OwnerToken, Audience string }
	b.read(&hello)
	b.Root, b.AccountID, b.OwnerToken, b.Audience = hello.Root, hello.AccountID, hello.OwnerToken, hello.Audience
	root, err := base64.RawURLEncoding.Strict().DecodeString(b.Root)
	if err != nil || len(root) != 32 || len(b.OwnerToken) != 43 || b.AccountID == "" || b.Audience != "https://hosted.example" {
		t.Fatal("invalid private producer initialization")
	}
	return b
}
func (b *ownerPairBridge) read(out any) {
	b.t.Helper()
	for b.out.Scan() {
		line := b.out.Text()
		if strings.HasPrefix(line, "PORTICO_OWNER_JSON:") {
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "PORTICO_OWNER_JSON:")), out) != nil {
				b.t.Fatal("invalid private producer reply")
			}
			return
		}
	}
	b.t.Fatal("private producer stopped before its reply")
}
func (b *ownerPairBridge) call(command any) ownerPairReply {
	b.t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if json.NewEncoder(b.in).Encode(command) != nil {
		b.t.Fatal("private producer command failed")
	}
	var out ownerPairReply
	b.read(&out)
	return out
}
func (b *ownerPairBridge) http(method, path, token string, body any) ownerPairReply {
	b.t.Helper()
	return b.call(map[string]any{"Action": "http", "Method": method, "Path": path, "Token": token, "Body": body})
}
func (b *ownerPairBridge) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.Host != "hosted.example" {
		return nil, errors.New("unexpected pair destination")
	}
	if r.URL.Path == "/v1/server-claims/events" {
		// Delay only this bounded network request until the real HTTP approval
		// commits. The serial private driver cannot process approval behind an
		// open event wait. No approval/status/claim bytes are fabricated here.
		select {
		case <-b.approved:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	var raw []byte
	if r.Body != nil {
		var err error
		raw, err = io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			return nil, err
		}
	}
	defer clear(raw)
	if len(raw) > 0 && !json.Valid(raw) {
		return nil, errors.New("invalid pair HTTP request")
	}
	out := b.call(map[string]any{"Action": "http", "Method": r.Method, "Path": r.URL.RequestURI(), "Headers": r.Header, "Body": json.RawMessage(raw)})
	if b.afterHTTP != nil {
		if err := b.afterHTTP(r, out); err != nil {
			clear(out.Body)
			return nil, err
		}
	}
	payload := out.Body
	if out.BodyEmpty {
		payload = nil
	}
	return &http.Response{StatusCode: out.Status, Header: out.Headers, Body: io.NopCloser(bytes.NewReader(payload)), ContentLength: int64(len(payload)), Request: r}, nil
}

var ownerPairDeniedReads = []struct{ id, path string }{
	{"get_direct_account", "/v1/direct"},
	{"get_sign_in_settings", "/v1/admin/sign-in"},
	{"get_admin_access_members", "/v1/admin/access/members"},
}

// A missing route proves nothing about a narrowed family. Keep the actual
// authority probes tied to the production registry even when the PG pair is
// deliberately not enabled in the ordinary package run.
func TestOwnerPairRefusalProbesUseRegisteredAccountRoutes(t *testing.T) {
	registry, err := httpapi.FoundationRegistry(httpapi.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	for _, probe := range ownerPairDeniedReads {
		found := false
		for _, route := range registry.Definitions() {
			if route.ID == probe.id && route.Method == http.MethodGet && route.Path == probe.path {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("owner refusal probe is not a registered account GET: %s", probe.id)
		}
	}
}

func TestOwnerPairLateACKUsesCanonicalTransportOrder(t *testing.T) {
	// Exercise the same nested body encoding used by the private pipe command.
	// The bytes come from the real public transport DTO, not a second verifier.
	commit := networking.Commit{OperationID: "operation", ServerID: "server", ClaimGeneration: "1", CredentialGeneration: "2"}
	raw, err := json.Marshal(map[string]any{"Action": "http", "Body": networking.OwnerPairCommitRequestForTest(commit)})
	if err != nil {
		t.Fatal("canonical ACK command encoding failed")
	}
	var command struct{ Body json.RawMessage }
	if json.Unmarshal(raw, &command) != nil || string(command.Body) != `{"operationId":"operation","serverId":"server","claimGeneration":"1","credentialGeneration":"2"}` {
		t.Fatal("nested ACK lost canonical producer field order")
	}
}

func TestActualOwnerContinuationInstallsAndSpendsOneGenuineHostedGrant(t *testing.T) {
	runActualOwnerContinuation(t, "success")
}

func TestActualOwnerContinuationPreinstallDenialReconcilesGenuineHostedCommit(t *testing.T) {
	for _, mode := range []string{"expiry-after-finalize", "signout-after-finalize", "lost-finalize-after-signout", "lost-result-after-signout"} {
		t.Run(mode, func(t *testing.T) { runActualOwnerContinuation(t, mode) })
	}
}

func TestActualOwnerContinuationLostExchangeUsesFreshOrdinaryAdmission(t *testing.T) {
	runActualOwnerContinuation(t, "lost-owner-exchange")
}

func runActualOwnerContinuation(t *testing.T, mode string) {
	b := newOwnerPairBridge(t)
	initial := b.call(map[string]string{"Action": "snapshot"})
	var empty struct {
		Server      any            `json:"server"`
		OwnerStarts []any          `json:"ownerStarts"`
		Operations  []any          `json:"operations"`
		Counts      map[string]int `json:"counts"`
	}
	if initial.Status != 200 || json.Unmarshal(initial.Body, &empty) != nil || empty.Server != nil || len(empty.OwnerStarts) != 0 || len(empty.Operations) != 0 || len(empty.Counts) != 6 {
		t.Fatal("pair did not start with an empty claim/registration namespace")
	}
	for _, count := range empty.Counts {
		if count != 0 {
			t.Fatal("pair started with invented claim/member/route state")
		}
	}
	ctx := context.Background()
	state := t.TempDir()
	db, err := persistence.Open(filepath.Join(state, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	gate, err := authoritygate.NewAuthorityGate(strings.Repeat("a", 64), false)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := networking.NewAuthorityRunner(func(ctx context.Context) (networking.LifecycleLease, error) { return gate.Acquire(ctx) })
	if err != nil {
		t.Fatal(err)
	}
	keys, err := networking.OpenCurrentKeys(ctx, db, state, runner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keys.Close() })
	id, err := identity.New(db, state)
	if err != nil {
		t.Fatal(err)
	}
	const installation = "f89bfbc4-cd90-4cda-a925-a123bb4a8b8a"
	issuing, err := identity.WithIssuingDevice(ctx, identity.DeviceRegistration{InstallationID: installation, Name: "Owner browser", Platform: "web", App: "portico"}, "127.0.0.1:1234")
	if err != nil {
		t.Fatal(err)
	}
	setupToken, err := id.BrowserSetupToken(issuing)
	if err != nil {
		t.Fatal(err)
	}
	setup, err := id.SetupWithOptions(issuing, identity.SetupOptions{SetupToken: setupToken, AuthMode: "local", Username: "owner", Password: "Pair-" + identity.Token() + "9!", Name: "Genuine paired server"}, true)
	if err != nil {
		t.Fatal(err)
	}
	root, err := base64.RawURLEncoding.DecodeString(b.Root)
	if err != nil {
		t.Fatal("private root unavailable")
	}
	control, err := hosted.New(db, id, b.Audience, b.Root, trust.KeyID(root))
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := networking.NewApprovalVerifier(b.Audience, trust.KeyID(root), root, control.ValidateTrust)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := networking.OpenClaimCipher(ctx, db, state)
	if err != nil {
		t.Fatal(err)
	}
	store, err := networking.NewSQLiteStore(db, cipher, networking.GuardLocalOwner, networking.GuardInstalledClaim, verifier, b.Audience)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := networking.NewHTTPTransport(b.Audience, store)
	if err != nil {
		t.Fatal(err)
	}
	transport.UseRoundTripper(b)
	if err = store.SetTerminalRevoker(control.RevokeClaimTx); err != nil {
		t.Fatal(err)
	}
	if err = control.UseCurrentClaims(store, runner, transport); err != nil {
		t.Fatal(err)
	}
	if err = control.ConfigureIdentityDepartures(keys); err != nil {
		t.Fatal(err)
	}
	id.PrepareIdentityDeparture = control.PrepareIdentityDeparture
	claims, err := networking.NewClaimHandler(store, runner, transport, keys, httpapi.NetworkingClaimAuthorizer(id, nil), id.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err = claims.ConfigureCertificates(ctx, state, networking.CertificateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = claims.ConfigureRemote(ctx, "0.0.0.0:32500", control.NetworkChanged); err != nil {
		t.Fatal(err)
	}
	api := httpapi.New(httpapi.Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Hosted: control, Networking: claims, RouteIdentity: networking.NewRouteIdentityHandler(keys, runner, claims.Certificates(), nil), Console: operations.New(db), Access: httpapi.AccessArea{Access: access.New(db)}, Origins: []string{"https://server.example"}})
	requestAt := func(origin, token, method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if body != nil {
			var e error
			raw, e = json.Marshal(body)
			if e != nil {
				t.Fatal("actual local request encoding failed")
			}
		}
		defer clear(raw)
		r := httptest.NewRequest(method, origin+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		r.Header.Set("X-Portico-Installation-Id", installation)
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		return requestAt("https://server.example", setup.AccessToken, method, path, body)
	}
	read := func(w *httptest.ResponseRecorder, out any) {
		t.Helper()
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), out) != nil {
			t.Fatalf("actual local handler failed: status=%d", w.Code)
		}
	}
	var move identity.AccountMove
	read(request("POST", "/v1/admin/sign-in/move/preview", identity.MovePreviewRequest{To: identity.SignInPortico}), &move)
	if w := request("POST", "/v1/admin/sign-in/move/confirm", identity.MoveConfirmRequest{MoveID: move.ID, ExpectedRevision: move.Revision}); w.Code != 200 {
		t.Fatalf("actual move confirmation status=%d", w.Code)
	}
	var status networking.ClaimStatus
	read(request("GET", "/v1/networking/claim", nil), &status)
	read(request("POST", "/v1/networking/claim/prepare", networking.ClaimPrepareRequest{Purpose: "switch", Expected: status.Identity}), &status)
	if status.Operation == nil {
		t.Fatal("actual claim operation absent")
	}
	assertInitiatingRetired := func() {
		t.Helper()
		var revoked bool
		if e := db.QueryRow(`SELECT revoked FROM authorization_session_families WHERE id=?`, setup.SessionFamilyID).Scan(&revoked); e != nil || !revoked {
			t.Fatal("initiating family was not durably retired")
		}
		if _, e := id.AuthenticateContext(ctx, setup.AccessToken); e == nil {
			t.Fatal("retired initiating family retained generic authority")
		}
	}
	operation := status.Operation.OperationID
	var faulted atomic.Bool
	faultDone := make(chan struct{})
	var faultMu sync.Mutex
	var armedPayload, armedSignature, cancelReceipt []byte
	var lateIntent networking.Intent
	var lateResult networking.Result
	t.Cleanup(func() {
		if lateResult.Credential != nil {
			lateResult.Credential.Clear()
		}
		clear(armedPayload)
		clear(armedSignature)
		clear(cancelReceipt)
	})
	preinstallDenial := mode != "success" && mode != "lost-owner-exchange"
	if mode != "success" {
		// Install the hook before web approval starts a background watch. It
		// observes an actual handler reply; it never fabricates approval,
		// commit, result, credential or cancellation response bytes.
		b.afterHTTP = func(r *http.Request, out ownerPairReply) error {
			if mode == "lost-owner-exchange" {
				if r.URL.Path == "/v1/server-returns/exchange" && out.Status == 200 && faulted.CompareAndSwap(false, true) {
					defer close(faultDone)
					return io.ErrUnexpectedEOF // Actual grant issued, response lost; never retained/replayed.
				}
				return nil
			}
			if r.URL.Path == "/v1/server-claims/cancel" && out.Status == 200 {
				faultMu.Lock()
				cancelReceipt = append([]byte(nil), out.Body...)
				faultMu.Unlock()
			}
			target := "/v1/server-claims/finalize"
			if mode == "lost-result-after-signout" {
				target = "/v1/server-claims/result"
			}
			if r.URL.Path != target || out.Status != 200 || !faulted.CompareAndSwap(false, true) {
				return nil
			}
			defer close(faultDone)
			if err := db.QueryRow(`SELECT payload,signature FROM networking_claim_cancellations WHERE operation_id=? AND mode='intent' AND claim_generation='' AND state='pending'`, operation).Scan(&armedPayload, &armedSignature); err != nil {
				t.Fatal("positive request preceded durable original-key cancellation")
			}
			if mode == "lost-result-after-signout" {
				var result struct {
					OperationID          string `json:"operationId"`
					ServerID             string `json:"serverId"`
					ClaimGeneration      string `json:"claimGeneration"`
					CredentialGeneration string `json:"credentialGeneration"`
					ServerCredential     string `json:"serverCredential"`
				}
				if json.Unmarshal(out.Body, &result) != nil || result.OperationID != operation {
					t.Fatal("actual result capture failed")
				}
				lateIntent, err = store.Load(r.Context(), operation)
				if err != nil {
					t.Fatal("actual retrieving intent unavailable")
				}
				secret, e := networking.NewSecret([]byte(result.ServerCredential))
				result.ServerCredential = ""
				if e != nil {
					t.Fatal("actual result credential unavailable")
				}
				lateResult = networking.Result{Commit: networking.Commit{OperationID: result.OperationID, ServerID: result.ServerID, ClaimGeneration: result.ClaimGeneration, CredentialGeneration: result.CredentialGeneration}, Credential: secret}
			}
			if mode == "expiry-after-finalize" {
				// A controlled local deadline withdrawal between the genuine
				// central commit and local install. Original signed bytes and
				// Hosted time/authority remain untouched; no five-minute sleep.
				if e := dbwork.WithWriteTx(ctx, db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
					_, e := tx.ExecContext(ctx, `UPDATE identity_owner_completions SET expires_ms=created_ms+1 WHERE operation_id=? AND phase='prepared'`, operation)
					return e
				}); e != nil {
					t.Fatal("local deadline withdrawal failed")
				}
			} else if w := request("DELETE", "/v1/sessions/current", nil); w.Code != http.StatusNoContent {
				t.Fatalf("actual initiating family signout status=%d", w.Code)
			}
			if strings.HasPrefix(mode, "lost-") {
				return io.ErrUnexpectedEOF
			}
			return nil
		}
	}
	stateBytes, verifierBytes := sha256.Sum256([]byte(identity.Token())), sha256.Sum256([]byte(identity.Token()))
	stateValue, pkce := base64.RawURLEncoding.EncodeToString(stateBytes[:]), base64.RawURLEncoding.EncodeToString(verifierBytes[:])
	challenge := sha256.Sum256([]byte(pkce))
	read(request("POST", "/v1/networking/claim/web-approval", networking.ClaimActionRequest{Expected: *status.Operation, OwnerStart: &networking.ClaimOwnerStartRequest{State: stateValue, CodeChallenge: base64.RawURLEncoding.EncodeToString(challenge[:]), InstallationID: installation}}), &status)
	if status.OwnerContinuation == nil || status.OwnerContinuation.Claim == nil || status.OwnerContinuation.Start == nil || status.ClaimCode == "" {
		t.Fatal("actual full pre-handoff capture absent")
	}
	_, start, err := claimweb.VerifyOwnerStart(*status.OwnerContinuation.Claim, *status.OwnerContinuation.Start, b.Audience, time.Now())
	if err != nil {
		t.Fatal("actual initiating proofs invalid")
	}
	transaction := status.OwnerContinuation.TransactionID
	approved := b.http("POST", "/v1/server-claims/approve-code", b.OwnerToken, map[string]string{"code": status.ClaimCode})
	if approved.Status != 200 {
		t.Fatalf("actual approval status=%d", approved.Status)
	}
	close(b.approved)
	awaited := request("POST", "/v1/networking/claim/await-approval", map[string]any{"expected": status.Operation})
	if preinstallDenial {
		if !faulted.Load() {
			t.Fatal("denial did not cross actual Hosted commit/result boundary")
		}
		select {
		case <-faultDone:
		case <-time.After(2 * time.Second):
			t.Fatal("controlled denial did not finish")
		}
		var stage string
		if err := db.QueryRow(`SELECT stage FROM networking_claim_intents WHERE operation_id=?`, operation).Scan(&stage); err != nil {
			t.Fatal("local claim phase unavailable")
		}
		if stage == "installed" {
			t.Fatal("late revoked/expired continuation installed")
		}
		// Recreate the store over its existing protected state, as after a
		// process restart. Recovery may use only the already armed denial.
		freshCipher, e := networking.OpenClaimCipher(ctx, db, state)
		if e != nil {
			t.Fatal("saved cancellation cipher could not reopen")
		}
		restarted, e := networking.NewSQLiteStore(db, freshCipher, networking.GuardLocalOwner, networking.GuardInstalledClaim, verifier, b.Audience)
		if e != nil {
			t.Fatal("saved claim store could not reopen")
		}
		if e = restarted.SetTerminalRevoker(control.RevokeClaimTx); e != nil {
			t.Fatal("actual denial callback unavailable")
		}
		if stage != "cancelled" {
			coordinator, e := networking.NewCoordinator(operation, b.Audience, restarted, transport, keys)
			if e != nil {
				t.Fatal("original cancellation coordinator unavailable")
			}
			if e = runner.Do(ctx, func(ctx context.Context) error { _, e := coordinator.ReconcilePreinstallCancellation(ctx); return e }); e != nil {
				t.Fatal("original-key committed cancellation failed")
			}
		}
		var phase, kind, cancellationState string
		var installedCount int
		var payload, signature []byte
		if e = db.QueryRow(`SELECT i.stage,c.phase,s.sign_in_kind,x.state,x.payload,x.signature,(SELECT count(*) FROM networking_claim_credentials) FROM networking_claim_intents i JOIN identity_owner_completions c ON c.operation_id=i.operation_id JOIN identity_state s JOIN networking_claim_cancellations x ON x.operation_id=i.operation_id WHERE i.operation_id=?`, operation).Scan(&stage, &phase, &kind, &cancellationState, &payload, &signature, &installedCount); e != nil || stage != "cancelled" || phase != "cancelled" || kind != "local" || cancellationState != "acknowledged" || installedCount != 0 || !bytes.Equal(payload, armedPayload) || !bytes.Equal(signature, armedSignature) {
			t.Fatal("reconciliation lost original proof, local authority or cancellation CAS")
		}
		assertInitiatingRetired()
		if lateResult.Credential != nil {
			if e = runner.Do(ctx, func(ctx context.Context) error { _, e := restarted.Install(ctx, lateIntent, lateResult); return e }); e == nil {
				t.Fatal("actual late result installed after cancellation")
			}
			var lateAck ownerPairReply
			if e = lateResult.Credential.Use(func(token []byte) error {
				lateAck = b.http("POST", "/v1/server-claims/ack", string(token), networking.OwnerPairCommitRequestForTest(lateResult.Commit))
				return nil
			}); e != nil {
				t.Fatal("late ACK proof unavailable")
			}
			var refused struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if lateAck.Status != http.StatusConflict || json.Unmarshal(lateAck.Body, &refused) != nil || refused.Error.Code != "claim_stale" {
				t.Fatalf("cancelled original credential ACK status=%d", lateAck.Status)
			}
		}
		replay := b.http("POST", "/v1/server-claims/cancel", "", map[string]string{"payload": base64.RawURLEncoding.EncodeToString(payload), "signature": base64.RawURLEncoding.EncodeToString(signature)})
		faultMu.Lock()
		receipt := append([]byte(nil), cancelReceipt...)
		faultMu.Unlock()
		if replay.Status != 200 || len(receipt) == 0 || !bytes.Equal(receipt, replay.Body) {
			t.Fatal("exact original-key cancellation replay changed receipt")
		}
		snapshot := b.call(map[string]string{"Action": "snapshot", "ServerID": id.ID()})
		var central struct {
			Server *struct {
				Revoked bool `json:"revoked"`
			} `json:"server"`
			OwnerStarts []struct {
				Phase string `json:"phase"`
			} `json:"ownerStarts"`
			Counts map[string]int `json:"counts"`
		}
		if snapshot.Status != 200 || snapshot.FinalizeRequests != 1 || snapshot.ExchangeRequests != 0 || json.Unmarshal(snapshot.Body, &central) != nil || central.Server == nil || !central.Server.Revoked || len(central.OwnerStarts) != 1 || central.OwnerStarts[0].Phase != "cancelled" || central.Counts["cancellations"] != 1 {
			t.Fatal("committed denial left live central claim or repeated positive work")
		}
		return
	}
	read(awaited, &status)
	if status.State != "installed" || !status.InstallationAcknowledged || status.Operation == nil {
		t.Fatal("actual approval did not install and ACK")
	}
	var stillLive bool
	var grantKind string
	if err = db.QueryRow(`SELECT NOT f.revoked,g.grant_kind FROM authorization_session_families f JOIN authorization_family_grants g ON g.family_id=f.id WHERE f.id=?`, setup.SessionFamilyID).Scan(&stillLive, &grantKind); err != nil || !stillLive || grantKind != identity.GrantOwnerClaimCompletion {
		t.Fatal("installed owner continuation was not a live narrowed family")
	}
	for _, probe := range ownerPairDeniedReads {
		if w := request("GET", probe.path, nil); w.Code != 401 && w.Code != 403 {
			t.Fatalf("narrow continuation reached unrelated authority: route=%s status=%d", probe.id, w.Code)
		}
	}
	origin, generation := networking.OwnerPairHTTPSForTest(t, claims)
	var installed networking.Intent
	if err = runner.Do(ctx, func(ctx context.Context) error {
		var e error
		installed, e = store.InstalledIntent(ctx)
		if e != nil {
			return e
		}
		secret, e := store.InstalledCredential(ctx, installed)
		if e != nil {
			return e
		}
		defer secret.Clear()
		return secret.Use(func(token []byte) error {
			out := b.call(map[string]any{"Action": "verified-route-fact", "ServerID": id.ID(), "Origin": origin, "Generation": generation, "Token": string(token)})
			if out.Status != 204 && out.Status != 200 {
				return errors.New("actual installed route fact refused")
			}
			return nil
		})
	}); err != nil {
		t.Fatal("installed route fact failed", err)
	}
	read(request("POST", "/v1/networking/claim/continue", networking.ClaimActionRequest{Expected: *status.Operation}), &status)
	if status.OwnerContinuation == nil || status.OwnerContinuation.ReturnRegistration == nil {
		t.Fatal("actual installed resolver registration absent")
	}
	registration := *status.OwnerContinuation.ReturnRegistration
	completed := b.http("POST", "/v1/server-returns/"+url.PathEscape(transaction)+"/complete", b.OwnerToken, map[string]any{})
	var callback struct {
		Location string `json:"location"`
	}
	if completed.Status != 200 || json.Unmarshal(completed.Body, &callback) != nil {
		t.Fatalf("actual owner completion status=%d", completed.Status)
	}
	u, err := url.Parse(callback.Location)
	if err != nil || u.Scheme != "https" || u.Host != "server.example" || u.Path != "/auth/portico-return" || u.RawQuery != "" || u.Fragment != "transaction="+transaction {
		t.Fatal("owner callback was not transaction-only on original origin")
	}
	q := serverreturn.OwnerExchange{TransactionID: transaction, State: stateValue, Verifier: pkce, RegistrationID: registration.ID, InstallationID: installation, ServerChallenge: start.ServerChallenge, Purpose: "switch"}
	var admitted identity.DirectSignIn
	answer := request("POST", "/v1/auth/portico-return/exchange", q)
	if mode == "lost-owner-exchange" {
		if !faulted.Load() {
			t.Fatal("lost response did not cross actual owner grant issuance")
		}
		<-faultDone
		// Both existing route error classes describe an unavailable transport;
		// neither may carry an admitted session or reopen the spent attempt.
		var failed struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
			Session json.RawMessage `json:"session"`
		}
		if (answer.Code != http.StatusInternalServerError && answer.Code != http.StatusServiceUnavailable) || json.Unmarshal(answer.Body.Bytes(), &failed) != nil || (failed.Error.Code != "internal" && failed.Error.Code != "portico_services_unavailable") || failed.Session != nil {
			t.Fatalf("lost owner response status=%d", answer.Code)
		}
		var phase, kind, claim, linked string
		var consumed int64
		if err = db.QueryRow(`SELECT c.phase,c.consumed_ms,s.sign_in_kind,i.stage,l.hosted_account_id FROM identity_owner_completions c JOIN identity_state s JOIN networking_claim_intents i ON i.operation_id=c.operation_id JOIN account_portico_links l ON l.account_id=c.owner_id WHERE c.operation_id=?`, operation).Scan(&phase, &consumed, &kind, &claim, &linked); err != nil || phase != "superseded" || consumed <= 0 || kind != "portico" || claim != "installed" || linked != b.AccountID {
			t.Fatal("lost exchange did not leave a terminal attempt and installed bound owner")
		}
		assertInitiatingRetired()
		if replay := request("POST", "/v1/auth/portico-return/exchange", q); replay.Code != 401 && replay.Code != 403 {
			t.Fatalf("terminal owner replay status=%d", replay.Code)
		}
		// Fresh ordinary identity admission on the actual controlled HTTPS
		// origin. No old bearer, owner assertion, second claim or replay cache.
		nonceBytes := sha256.Sum256([]byte(identity.Token()))
		nonce := base64.RawURLEncoding.EncodeToString(nonceBytes[:])
		var challenge struct {
			Challenge string `json:"challenge"`
			ExpiresAt string `json:"expiresAt"`
			Proof     struct {
				Payload   string `json:"payload"`
				Signature string `json:"signature"`
			} `json:"proof"`
		}
		read(requestAt(origin, "", "POST", "/v1/direct/portico-challenge", map[string]string{"nonce": nonce}), &challenge)
		payload, pe := base64.RawURLEncoding.Strict().DecodeString(challenge.Proof.Payload)
		signature, se := base64.RawURLEncoding.Strict().DecodeString(challenge.Proof.Signature)
		var proof networking.ChallengeProof
		if pe != nil || se != nil || !ed25519.Verify(installed.PublicKey, payload, signature) || json.Unmarshal(payload, &proof) != nil || proof.ServerID != id.ID() || proof.Challenge != challenge.Challenge || proof.Nonce != nonce || proof.InstallationID != installation || !proof.ExpiresAt.After(time.Now()) {
			t.Fatal("fresh ordinary challenge did not prove the installed server")
		}
		assertion := b.http("POST", "/v1/servers/"+url.PathEscape(id.ID())+"/identity", b.OwnerToken, map[string]string{"challenge": challenge.Challenge})
		var signed hosted.Signed
		if assertion.Status != 200 || json.Unmarshal(assertion.Body, &signed) != nil {
			t.Fatalf("fresh ordinary identity status=%d", assertion.Status)
		}
		read(requestAt(origin, "", "POST", "/v1/direct/sign-in", map[string]any{"porticoIdentity": signed}), &admitted)
		if admitted.Session == nil || admitted.Account.ID != setup.Viewer.AccountID || admitted.Account.HostedAccountID != b.AccountID || admitted.ServerID != id.ID() || admitted.Session.Viewer.Role != "owner" || len(admitted.Profiles) != 1 || admitted.Profiles[0].ID != setup.Viewer.ProfileID {
			t.Fatal("fresh identity did not admit the same already-bound owner and profile")
		}
		if w := requestAt(origin, admitted.Session.AccessToken, "GET", "/v1/me", nil); w.Code != 200 {
			t.Fatalf("fresh ordinary owner session status=%d", w.Code)
		}
		snapshot := b.call(map[string]string{"Action": "snapshot", "ServerID": id.ID()})
		if snapshot.Status != 200 || snapshot.FinalizeRequests != 1 || snapshot.ExchangeRequests != 1 {
			t.Fatal("ordinary recovery repeated claim or consumed owner exchange")
		}
		var terminal string
		var spentAt, completedAt int64
		if err = db.QueryRow(`SELECT phase,consumed_ms,completed_ms FROM identity_owner_completions WHERE operation_id=?`, operation).Scan(&terminal, &spentAt, &completedAt); err != nil || terminal != "superseded" || spentAt != consumed || completedAt != 0 {
			t.Fatal("ordinary identity resurrected the terminal owner attempt")
		}
		return
	}
	read(answer, &admitted)
	if admitted.Session == nil || admitted.Account.ID != setup.Viewer.AccountID || admitted.ServerID != id.ID() || admitted.Account.HostedAccountID != b.AccountID {
		t.Fatal("actual owner grant changed the captured account/server")
	}
	assertInitiatingRetired()
	if _, err = id.AuthenticateContext(ctx, admitted.Session.AccessToken); err != nil {
		t.Fatal("actual completed owner grant unusable", err)
	}
	if w := request("POST", "/v1/auth/portico-return/exchange", q); w.Code == 200 {
		t.Fatal("consumed owner exchange replay admitted")
	}
	snapshot := b.call(map[string]string{"Action": "snapshot", "ServerID": id.ID()})
	if snapshot.Status != 200 || snapshot.FinalizeRequests != 1 || snapshot.ExchangeRequests != 1 {
		t.Fatal("owner pair repeated central claim or exchange")
	}
	var phase string
	var spent sql.NullInt64
	if err = db.QueryRow(`SELECT phase,consumed_ms FROM identity_owner_completions WHERE operation_id=?`, installed.OperationID).Scan(&phase, &spent); err != nil || phase != "completed" || !spent.Valid || spent.Int64 <= 0 {
		t.Fatal("local final spend was not durable", err)
	}
}
