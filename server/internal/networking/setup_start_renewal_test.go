package networking

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/authoritygate"
	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	hostedtrust "github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The producer branch of a setup-start renewal, against a stub Hosted pending endpoint.
// A slow certificate outlived the five-minute start of an INSTALLED setup: the same
// setup token asks for a fresh start, the server re-signs the same claim with new time
// bounds plus this browser's new start, sends both to Hosted, and binds the answer.
func TestSetupStartRenewalProducerAgainstStubHosted(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	gate, err := authoritygate.NewAuthorityGate(strings.Repeat("a", 64), false)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewAuthorityRunner(func(ctx context.Context) (LifecycleLease, error) { return gate.Acquire(ctx) })
	if err != nil {
		t.Fatal(err)
	}
	keys, err := OpenCurrentKeys(ctx, db, root, runner)
	if err != nil {
		t.Fatal(err)
	}
	defer keys.Close()
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	operations.WireSetupRemote(id)
	secret, err := id.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := id.SetupWithOptions(ctx, identity.SetupOptions{RequestID: "82a879b3-abf5-48c6-bba0-02ffd762f99c", SetupToken: secret, Name: "Setup server", AuthMode: "hosted"}, true); err != nil || out.Pending == nil {
		t.Fatal("pending hosted setup", err)
	}
	pending, proof, err := id.SetupClaimAuthority(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	hostedPub, hostedKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	verify, err := NewApprovalVerifier("https://hosted.example", hostedtrust.KeyID(hostedPub), hostedPub)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := OpenClaimCipher(ctx, db, root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteStore(db, cipher, GuardLocalOwner, GuardInstalledClaim, verify, "https://hosted.example")
	if err != nil {
		t.Fatal(err)
	}
	transport, err := NewHTTPTransport("https://hosted.example", store)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewClaimHandler(store, runner, transport, keys, func(*http.Request) (ClaimRequestOwner, error) { return ClaimRequestOwner{}, ErrClaimOwnerRequired }, id.Name)
	if err != nil {
		t.Fatal(err)
	}
	var public []byte
	if err = db.QueryRowContext(ctx, `SELECT public_key FROM networking_claim_identity WHERE singleton=1`).Scan(&public); err != nil {
		t.Fatal(err)
	}
	var v Intent
	if err = runner.Do(ctx, func(ctx context.Context) error {
		var e error
		v, e = store.PreparePurpose(ctx, Binding{OperationID: identity.Token(), ServerID: id.ID(), AccountID: UnboundAccount, PublicKey: public}, LocalOwner{SetupOperationID: pending.RequestID, Epoch: pending.Revision}, ClaimPurpose{Purpose: "setup"})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	authority := &SetupStartAuthority{RequestID: pending.RequestID, Revision: pending.Revision,
		CaptureTx: func(ctx context.Context, tx *sql.Tx, audience string, original, start claimweb.Signed) error {
			return id.CaptureSetupClaimTx(ctx, tx, pending, proof, original, start, audience)
		},
		RenewTx: func(ctx context.Context, tx *sql.Tx, audience string, original, start claimweb.Signed) error {
			return id.RenewSetupClaimTx(ctx, tx, pending, proof, original, start, audience)
		},
		BindTx: func(ctx context.Context, tx *sql.Tx, operation, transaction string) error {
			return id.BindSetupClaimTransactionTx(ctx, tx, pending, proof, operation, transaction)
		}}
	browser := func() (string, string, string) {
		state, pkce := sha256.Sum256([]byte(identity.Token())), sha256.Sum256([]byte(identity.Token()))
		return base64.RawURLEncoding.EncodeToString(state[:]), base64.RawURLEncoding.EncodeToString(pkce[:]), identity.Token()
	}

	// The stub Hosted pending endpoint records every request and answers its transaction.
	type seen struct {
		claim claimweb.Request
		start claimweb.SetupStart
		raw   claimweb.PendingRequest
	}
	var requests []seen
	answer := "first_setup_transaction"
	refuse := false
	transport.client = &http.Client{Transport: ownerPendingRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.String() != "https://hosted.example/v1/server-claims/pending" || r.Header.Get("Authorization") != "" {
			t.Fatal("unexpected Hosted request")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		var request claimweb.PendingRequest
		if err = json.Unmarshal(body, &request); err != nil || request.SetupStart == nil {
			t.Fatal("the setup start travels with its claim", err)
		}
		claim, start, err := claimweb.VerifySetupStart(request.Original(), *request.SetupStart, "https://hosted.example", time.Now())
		if err != nil {
			t.Fatal("Hosted receives a verifiable signed pair", err)
		}
		requests = append(requests, seen{claim, start, request})
		if refuse {
			return &http.Response{StatusCode: 409, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"claim_stale","message":"stale","retryable":false}}`))}, nil
		}
		out, _ := json.Marshal(claimweb.Pending{Purpose: "setup", Code: "claim_" + strings.Repeat("K", 43), SetupTransactionID: answer, ServerID: claim.ServerID, Name: claim.Name, Address: claim.Address, RequestedAt: claim.IssuedAt, ExpiresAt: start.ExpiresAt, Status: "pending"})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(out))}, nil
	})}
	// The real handler runs the producer under the setup token's own guarded request.
	guard := func(ctx context.Context, tx *sql.Tx) error {
		return id.CheckSetupClaimTx(ctx, tx, pending.RequestID, pending.Revision, proof)
	}
	begin := func(v Intent, state, challenge, installation string) (out localWebClaim, err error) {
		err = runner.Do(ctx, func(ctx context.Context) error {
			guarded, err := guardedClaimRequest(ctx, store, guard)
			if err != nil {
				return err
			}
			out, err = h.beginSetupWebClaim(guarded, v, "http://localhost:32500", state, challenge, installation, authority)
			return err
		})
		return
	}
	reload := func() (out Intent) {
		t.Helper()
		if err := runner.Do(ctx, func(ctx context.Context) error {
			g, err := h.store.tx(ctx)
			if err != nil {
				return err
			}
			defer g.Rollback()
			stored, err := loadIntentTx(ctx, g.Tx(), v.OperationID)
			out = stored.Intent
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}

	s1, c1, i1 := browser()
	if _, err = begin(v, s1, c1, i1); err != nil {
		t.Fatal("original start", err)
	}
	// Install through a real signed approval while the original start is live.
	now := time.Now().UTC()
	raw := []byte(fmt.Sprintf(`{"kind":"portico.claim.approval","version":"2","audience":"https://hosted.example","operationId":%q,"serverId":%q,"accountId":"account-approved","publicKey":%q,"localGeneration":"0","approvalRevision":"1","purpose":"setup","currentOwnerAccountId":"","previousOwnerRole":"","returnRegistrationId":"","issuedAt":%q,"expiresAt":%q}`,
		v.OperationID, v.ServerID, base64.RawURLEncoding.EncodeToString(public), now.Format(time.RFC3339Nano), now.Add(4*time.Minute).Format(time.RFC3339Nano)))
	cert, err := hostedtrust.Certify(hostedKey, hostedPub, "setup-documents", "documents", now.Add(-time.Hour), now.Add(time.Hour), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	approval, _ := json.Marshal(hostedtrust.Envelope{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(hostedKey, raw)), KeyID: "setup-documents", Certificate: cert})
	if err = runner.Do(ctx, func(ctx context.Context) error {
		x, e := store.Approve(ctx, v, approval)
		if e != nil {
			return e
		}
		if x, e = store.BeginFinalizing(ctx, x); e != nil {
			return e
		}
		commit := Commit{OperationID: x.OperationID, ServerID: x.ServerID, ClaimGeneration: "1", CredentialGeneration: "1"}
		if x, e = store.Committed(ctx, x, commit); e != nil {
			return e
		}
		credential, e := NewSecret([]byte(strings.Repeat("setup-result-fixture-", 3)))
		if e != nil {
			return e
		}
		defer credential.Clear()
		if x, e = store.Install(ctx, x, Result{Commit: commit, Credential: credential}); e != nil {
			return e
		}
		_, e = store.Acknowledged(ctx, x)
		return e
	}); err != nil {
		t.Fatal("signed approval and install", err)
	}
	installed := reload()
	if installed.Stage != Installed {
		t.Fatal("claim installed", installed.Stage)
	}
	s2, c2, i2 := browser()
	// Still live: another browser binding cannot replace the start.
	if _, err = begin(installed, s2, c2, i2); err == nil {
		t.Fatal("a live start was replaced")
	}
	// The certificate took longer than the start: it expires unused.
	if _, err = db.Exec(`UPDATE identity_setup_completions SET created_ms=created_ms-2400000,expires_ms=expires_ms-2400000`); err != nil {
		t.Fatal(err)
	}
	var oldChallenge string
	if err = db.QueryRow(`SELECT server_challenge_hash FROM identity_setup_completions`).Scan(&oldChallenge); err != nil {
		t.Fatal(err)
	}
	// Contabo, 29 Oct: Hosted refused the first renewal (409 claim_stale). The server had
	// already stored that renewed start, prepared and unbound, and it then lapsed too.
	refuse = true
	sx, cx, ix := browser()
	_, refused := begin(installed, sx, cx, ix)
	if !errors.Is(refused, ErrStale) {
		t.Fatal("the Hosted refusal is stale", refused)
	}
	// The refusal now explains itself in the server log: its step and class, no values.
	var logged []string
	h.failures = newFailureLog(func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })
	w := httptest.NewRecorder()
	h.failure(w, httptest.NewRequest("POST", "/v1/networking/claim/web-approval", nil), refused)
	if w.Code != 409 || len(logged) != 1 || !strings.Contains(logged[0], "answered 409: step hosted pending, identity stale") || strings.Contains(logged[0], sx) {
		t.Fatal("the refusal reason is logged", w.Code, logged)
	}
	var refusedPhase, refusedTransaction, refusedAttempt string
	if err = db.QueryRow(`SELECT phase,transaction_id,attempt_id FROM identity_setup_completions`).Scan(&refusedPhase, &refusedTransaction, &refusedAttempt); err != nil || refusedPhase != "prepared" || refusedTransaction != "" || refusedAttempt != "" {
		t.Fatal("the refused renewal left its prepared, unbound start", refusedPhase, refusedTransaction, refusedAttempt, err)
	}
	if _, err = db.Exec(`UPDATE identity_setup_completions SET created_ms=created_ms-2400000,expires_ms=expires_ms-2400000`); err != nil {
		t.Fatal(err)
	}
	refuse = false
	// The retry renews that lapsed renewal again, with this browser's fresh bindings.
	answer = "renewed_setup_transaction"
	before := len(requests)
	out, err := begin(installed, s2, c2, i2)
	if err != nil {
		t.Fatal("renewal", err)
	}
	if len(requests) != before+1 {
		t.Fatal("the renewal asks Hosted once")
	}
	first, renewed := requests[0], requests[len(requests)-1]
	// Request shape: the same claim but for its time bounds, and this browser's new start.
	a, b := first.claim, renewed.claim
	if !b.IssuedAt.After(a.IssuedAt) {
		t.Fatal("the renewed claim is newer")
	}
	a.IssuedAt, a.ExpiresAt, b.IssuedAt, b.ExpiresAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatal("a renewal changed the approved claim")
	}
	if renewed.start.State != s2 || renewed.start.CodeChallenge != c2 || renewed.start.InstallationID != i2 || renewed.start.ServerChallenge == first.start.ServerChallenge ||
		renewed.start.SetupRequestID != pending.RequestID || renewed.start.SetupRevision != pending.Revision || renewed.start.Address != "http://localhost:32500" {
		t.Fatal("the renewed start carries this browser's bindings for the same setup")
	}
	// Response handling: the answer is bound, live, and the old challenge is spent.
	var transaction, phase, challenge string
	var expires int64
	if err = db.QueryRow(`SELECT transaction_id,phase,expires_ms,server_challenge_hash FROM identity_setup_completions`).Scan(&transaction, &phase, &expires, &challenge); err != nil {
		t.Fatal(err)
	}
	if out.SetupTransactionID != answer || transaction != answer || expires <= time.Now().UnixMilli() || challenge == oldChallenge || out.SetupStart == nil || out.Code == "" {
		t.Fatal("the renewed transaction is bound and live", transaction, phase)
	}
	var stale int
	if err = db.QueryRow(`SELECT count(*) FROM hosted_identity_challenges WHERE challenge_hash=?`, oldChallenge).Scan(&stale); err != nil || stale != 0 {
		t.Fatal("the old server challenge was spent", err)
	}
	// A retry of the same renewal replays its own stored pair; another browser can't.
	if _, err = begin(installed, s2, c2, i2); err != nil {
		t.Fatal("same-browser retry", err)
	}
	if last := requests[len(requests)-1]; last.raw.Payload != renewed.raw.Payload || *last.raw.SetupStart != *renewed.raw.SetupStart {
		t.Fatal("a retry re-signed instead of replaying")
	}
	s3, c3, i3 := browser()
	if _, err = begin(installed, s3, c3, i3); err == nil {
		t.Fatal("another browser took over a live renewed start")
	}
	// Defence in depth: the renewal signer itself refuses a live start, whatever its caller decided.
	unbound := installed.Binding
	unbound.AccountID = UnboundAccount
	if err = runner.Do(ctx, func(ctx context.Context) error {
		guarded, err := guardedClaimRequest(ctx, store, guard)
		if err != nil {
			return err
		}
		_, err = keys.SignRenewedSetupStart(guarded, unbound, renewed.raw.Original(), renewed.start)
		return err
	}); err == nil {
		t.Fatal("the renewal signer signed for a live start")
	}
	// A bad Hosted answer binds nothing.
	answer = ""
	if _, err = db.Exec(`UPDATE identity_setup_completions SET created_ms=created_ms-2400000,expires_ms=expires_ms-2400000`); err != nil {
		t.Fatal(err)
	}
	s4, c4, i4 := browser()
	if _, err = begin(installed, s4, c4, i4); err == nil {
		t.Fatal("an invalid Hosted answer was accepted")
	}
	if err = db.QueryRow(`SELECT transaction_id FROM identity_setup_completions`).Scan(&transaction); err != nil || transaction != "" {
		t.Fatal("no transaction is bound from an invalid answer", transaction, err)
	}
}
