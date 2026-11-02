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
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/authoritygate"
	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	hostedtrust "github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
	"github.com/PorticoMediaServer/portico/server/internal/setupflow"
)

// Finding #2: Portico Services installed a setup claim, but the server's return ran out
// before the owner exchange. Starting over from the server withdraws the installed claim
// (the server's own deny-only cancellation, committed mode, acknowledged by Portico
// Services), ends the old setup so its return can never complete, and a fresh claim at
// another address then completes setup with an owner. Once set up, start over refuses.
type startOverHarness struct {
	db       *sql.DB
	id       *identity.Service
	h        *ClaimHandler
	offline  *bool
	cancels  func() []string
	install  func(token, request, address string) startOverAttempt
	exchange func(startOverAttempt) (identity.DirectSignIn, error)
	phase    func() setupflow.Phase
}

type startOverAttempt struct {
	pending  identity.PendingAccountSetup
	proof    string
	intent   Intent
	start    claimweb.SetupStart
	verifier string
}

func newStartOverHarness(t *testing.T) *startOverHarness {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
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
	t.Cleanup(func() { keys.Close() })
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	operations.WireSetupRemote(id)
	id.SetupClaimReadyTx = SetupClaimInstalledTx
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

	// Stub Portico Services: pending answers each start's transaction; cancel records the
	// deny-only assertion and acknowledges it (or fails while offline).
	type cancelled struct{ operation, mode, generation string }
	var starts []claimweb.SetupStart
	var cancels []cancelled
	offline := false
	transport.client = &http.Client{Transport: ownerPendingRoundTrip(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		reply := func(v any) (*http.Response, error) {
			out, _ := json.Marshal(v)
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(out))}, nil
		}
		switch {
		case r.URL.String() == "https://hosted.example/v1/server-claims/pending":
			var request claimweb.PendingRequest
			if err = json.Unmarshal(body, &request); err != nil || request.SetupStart == nil {
				t.Error("pending without a setup start", err)
				return nil, io.EOF
			}
			claim, start, err := claimweb.VerifySetupStart(request.Original(), *request.SetupStart, "https://hosted.example", time.Now())
			if err != nil {
				t.Error("unverifiable setup start", err)
				return nil, io.EOF
			}
			starts = append(starts, start)
			return reply(claimweb.Pending{Purpose: "setup", Code: "claim_" + strings.Repeat("K", 43), SetupTransactionID: fmt.Sprintf("setup_transaction_%d", len(starts)), ServerID: claim.ServerID, Name: claim.Name, Address: claim.Address, RequestedAt: claim.IssuedAt, ExpiresAt: start.ExpiresAt, Status: "pending"})
		case strings.HasSuffix(r.URL.Path, "/cancel"):
			if offline {
				return nil, errors.New("Portico Services offline")
			}
			var in struct {
				Payload string `json:"payload"`
			}
			if err = json.Unmarshal(body, &in); err != nil {
				return nil, err
			}
			raw, err := base64.RawURLEncoding.DecodeString(in.Payload)
			if err != nil {
				return nil, err
			}
			var wire struct {
				OperationID     string `json:"operationId"`
				RequestID       string `json:"requestId"`
				Mode            string `json:"mode"`
				ClaimGeneration string `json:"claimGeneration"`
			}
			if err = json.Unmarshal(raw, &wire); err != nil {
				return nil, err
			}
			cancels = append(cancels, cancelled{wire.OperationID, wire.Mode, wire.ClaimGeneration})
			return reply(map[string]string{"operationId": wire.OperationID, "requestId": wire.RequestID})
		}
		t.Errorf("unexpected Portico Services request %s", r.URL)
		return nil, io.EOF
	})}

	// One setup attempt, from start to an installed, acknowledged claim at address.
	setupInstalled := func(token, request, address string) startOverAttempt {
		t.Helper()
		if out, err := id.SetupWithOptions(ctx, identity.SetupOptions{RequestID: request, SetupToken: token, Name: "Setup server", AuthMode: "hosted"}, true); err != nil || out.Pending == nil {
			t.Fatal("pending hosted setup", err)
		}
		pending, proof, err := id.SetupClaimAuthority(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		var generation int64
		if err = db.QueryRowContext(ctx, `SELECT public_key,reset_generation FROM networking_claim_identity WHERE singleton=1`).Scan(&public, &generation); err != nil {
			t.Fatal(err)
		}
		var v Intent
		if err = runner.Do(ctx, func(ctx context.Context) error {
			var e error
			v, e = store.PreparePurpose(ctx, Binding{OperationID: identity.Token(), ServerID: id.ID(), AccountID: UnboundAccount, PublicKey: public, LocalGeneration: generation}, LocalOwner{SetupOperationID: pending.RequestID, Epoch: pending.Revision}, ClaimPurpose{Purpose: "setup"})
			return e
		}); err != nil {
			t.Fatal("prepare", err)
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
		guard := func(ctx context.Context, tx *sql.Tx) error {
			return id.CheckSetupClaimTx(ctx, tx, pending.RequestID, pending.Revision, proof)
		}
		verifier := identity.Token()
		challenge := sha256.Sum256([]byte(verifier))
		if err = runner.Do(ctx, func(ctx context.Context) error {
			guarded, err := guardedClaimRequest(ctx, store, guard)
			if err != nil {
				return err
			}
			_, err = h.beginSetupWebClaim(guarded, v, address, identity.Token(), base64.RawURLEncoding.EncodeToString(challenge[:]), identity.Token(), authority)
			return err
		}); err != nil {
			t.Fatal("setup start", err)
		}
		now := time.Now().UTC()
		raw := []byte(fmt.Sprintf(`{"kind":"portico.claim.approval","version":"2","audience":"https://hosted.example","operationId":%q,"serverId":%q,"accountId":"account-approved","publicKey":%q,"localGeneration":%q,"approvalRevision":"1","purpose":"setup","currentOwnerAccountId":"","previousOwnerRole":"","returnRegistrationId":"","issuedAt":%q,"expiresAt":%q}`,
			v.OperationID, v.ServerID, base64.RawURLEncoding.EncodeToString(public), fmt.Sprint(v.LocalGeneration), now.Format(time.RFC3339Nano), now.Add(4*time.Minute).Format(time.RFC3339Nano)))
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
			t.Fatal("approve and install", err)
		}
		if err = withWrite(ctx, db, func(tx *sql.Tx) error { return id.MarkSetupInstalledTx(ctx, tx, pending, proof, v.OperationID) }); err != nil {
			t.Fatal("mark installed", err)
		}
		return startOverAttempt{pending: pending, proof: proof, intent: v, start: starts[len(starts)-1], verifier: verifier}
	}
	phase := func() setupflow.Phase {
		t.Helper()
		state, err := setupflow.Derive(ctx, db, time.Now(), nil)
		if err != nil {
			t.Fatal("derive", err)
		}
		return state.Phase
	}
	exchange := func(a startOverAttempt) (identity.DirectSignIn, error) {
		q := serverreturn.SetupExchange{TransactionID: fmt.Sprintf("setup_transaction_%d", len(starts)), State: a.start.State, Verifier: a.verifier, InstallationID: a.start.InstallationID, ServerChallenge: a.start.ServerChallenge, Purpose: "setup"}
		var out identity.DirectSignIn
		err := withWrite(ctx, db, func(tx *sql.Tx) error {
			operation, attemptID, err := id.BeginSetupReturnTx(ctx, tx, a.pending, a.proof, q)
			if err != nil {
				return err
			}
			who := identity.PorticoIdentity{AccountID: "account-approved", Username: "actual_setup_owner", DisplayName: "Actual setup owner", Consent: "verified-identity-fixture", SignerKeyID: "current-document-key",
				Return: &identity.PorticoReturnBinding{Purpose: "setup", InstallationID: q.InstallationID, OperationID: operation, ClaimGeneration: 1, CredentialGeneration: 1, ExpiresAt: time.Now().Add(45 * time.Second)}}
			out, err = id.CompleteSetupReturnTx(ctx, tx, a.pending, a.proof, operation, attemptID, q, who, identity.Token())
			return err
		})
		return out, err
	}

	return &startOverHarness{db: db, id: id, h: h, offline: &offline, install: setupInstalled, exchange: exchange, phase: phase,
		cancels: func() []string {
			out := []string{}
			for _, c := range cancels {
				out = append(out, c.operation+" "+c.mode+" "+c.generation)
			}
			return out
		}}
}

func TestSetupStartOverAfterAnInstalledClaimLapsed(t *testing.T) {
	ctx := context.Background()
	x := newStartOverHarness(t)
	db, id, h, offline, setupInstalled, exchange, phase := x.db, x.id, x.h, x.offline, x.install, x.exchange, x.phase
	token, err := id.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first := setupInstalled(token, "82a879b3-abf5-48c6-bba0-02ffd762f99c", "http://localhost:32500")
	// The return ran out before the owner exchange.
	if _, err = db.Exec(`UPDATE identity_setup_completions SET created_ms=created_ms-2400000,expires_ms=expires_ms-2400000`); err != nil {
		t.Fatal(err)
	}
	if got := phase(); got != setupflow.InstalledLapsed {
		t.Fatal("lapsed installed setup", got)
	}

	// Start over while Portico Services is unreachable: the withdrawal waits in the outbox.
	*offline = true
	pending, err := h.RetireSetupClaim(ctx)
	if err != nil || !pending {
		t.Fatal("an offline withdrawal stays queued", pending, err)
	}
	var stage string
	if err = db.QueryRow(`SELECT stage FROM networking_claim_intents WHERE operation_id=?`, first.intent.OperationID).Scan(&stage); err != nil || stage != string(CancelPending) {
		t.Fatal("queued withdrawal", stage, err)
	}
	var credentials int
	if err = db.QueryRow(`SELECT count(*) FROM networking_claim_credentials WHERE operation_id=?`, first.intent.OperationID).Scan(&credentials); err != nil || credentials != 0 {
		t.Fatal("the withdrawn claim's credential is gone at once", credentials, err)
	}
	second, err := id.StartOverSetup(ctx)
	if err != nil || second == "" || second == token {
		t.Fatal("start over", err)
	}
	if got := phase(); got != setupflow.Open {
		t.Fatal("started over", got)
	}
	var challenges int
	if err = db.QueryRow(`SELECT count(*) FROM hosted_identity_challenges`).Scan(&challenges); err != nil || challenges != 0 {
		t.Fatal("the old setup's identity challenge outlived start over", challenges, err)
	}
	// The old attempt can never admit an owner.
	if _, err = exchange(first); err == nil {
		t.Fatal("the old setup's return completed after start over")
	}
	// Back online: the queued withdrawal is delivered, committed, for the installed claim.
	*offline = false
	if pending, err = h.RetireSetupClaim(ctx); err != nil || pending {
		t.Fatal("withdrawal delivered", pending, err)
	}
	if got := x.cancels(); len(got) != 1 || got[0] != first.intent.OperationID+" committed 1" {
		t.Fatal("Portico Services got one committed cancellation for the installed claim", got)
	}
	var active sql.NullString
	if err = db.QueryRow(`SELECT active_operation_id FROM networking_claim_identity WHERE singleton=1`).Scan(&active); err != nil || active.Valid {
		t.Fatal("no claim is active after the acknowledgement", active, err)
	}

	// A fresh claim from another address completes setup with an owner.
	fresh := setupInstalled(second, "5b6c7d8e-1f2a-4b3c-8d4e-5f6a7b8c9d0e", "http://192.168.1.50:32500")
	if fresh.start.Address != "http://192.168.1.50:32500" {
		t.Fatal("the fresh start is bound to the new address", fresh.start.Address)
	}
	out, err := exchange(fresh)
	if err != nil || out.Session == nil || out.Account.Role != identity.TierOwner {
		t.Fatal("owner exchange after start over", err)
	}
	if got := phase(); got != setupflow.Completed {
		t.Fatal("setup complete", got)
	}
	// Set up: start over refuses, as a start does, and touches nothing.
	if _, err = id.StartOverSetup(ctx); !errors.Is(err, identity.ErrConflict) {
		t.Fatal("start over on a set-up server", err)
	}
	if pending, err = h.RetireSetupClaim(ctx); err != nil || pending {
		t.Fatal("no claim to withdraw once set up", pending, err)
	}
	if got := x.cancels(); len(got) != 1 {
		t.Fatal("a set-up server's claim was cancelled", got)
	}
}

func withWrite(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	return dbwork.WithWriteTx(ctx, db, dbwork.ClassSecurityFence, fn)
}

// The lead's race: the owner exchange commits after start over read "no owner yet" but
// before its first write. The write re-checks under the same writer reservation the
// exchange takes, so start over refuses and the now-owned server keeps its installed claim.
func TestSetupStartOverLosesToAnOwnerExchangeBetweenReadAndWrite(t *testing.T) {
	ctx := context.Background()
	x := newStartOverHarness(t)
	token, err := x.id.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	live := x.install(token, "82a879b3-abf5-48c6-bba0-02ffd762f99c", "http://192.168.1.50:32500")
	if got := x.phase(); got != setupflow.Installed {
		t.Fatal("installed, owner exchange still open", got)
	}
	exchanged := false
	retireSetupClaimBeforeWrite = func() {
		out, err := x.exchange(live)
		exchanged = err == nil && out.Session != nil
	}
	t.Cleanup(func() { retireSetupClaimBeforeWrite = nil })
	pending, err := x.h.RetireSetupClaim(ctx)
	if !exchanged {
		t.Fatal("the seam did not complete the owner exchange")
	}
	if !errors.Is(err, ErrSetupComplete) || pending {
		t.Fatal("start over must lose to the owner exchange", pending, err)
	}
	if got := x.phase(); got != setupflow.Completed {
		t.Fatal("setup stays complete", got)
	}
	var stage string
	var credentials, queued int
	if err = x.db.QueryRow(`SELECT stage FROM networking_claim_intents WHERE operation_id=?`, live.intent.OperationID).Scan(&stage); err != nil || stage != string(Installed) {
		t.Fatal("the installed claim is intact", stage, err)
	}
	if err = x.db.QueryRow(`SELECT (SELECT count(*) FROM networking_claim_credentials WHERE operation_id=?),(SELECT count(*) FROM networking_claim_cancellations)`, live.intent.OperationID).Scan(&credentials, &queued); err != nil || credentials != 1 || queued != 0 {
		t.Fatal("no credential lost and no cancellation queued", credentials, queued, err)
	}
	if got := x.cancels(); len(got) != 0 {
		t.Fatal("Portico Services was asked to cancel an owned server's claim", got)
	}
	// And the identity half refuses on its own, in its own write.
	if _, err = x.id.StartOverSetup(ctx); !errors.Is(err, identity.ErrConflict) {
		t.Fatal("identity start over on a set-up server", err)
	}
}
