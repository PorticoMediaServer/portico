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
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestOwnerRecoveryEmptyLaneSleepsUntilActualWake(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	synctest.Test(t, func(t *testing.T) {
		f, h, tr := handlerFixture(t)
		// Observe the same real SQLite file through the production instrumented
		// driver. The test clock advances production timers; SQL and recovery code
		// are real, with no replacement schema or query stub.
		observed, err := sql.Open(dbwork.DriverName, f.path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { observed.Close() })
		observed.SetMaxOpenConns(1)
		h.store.db = observed
		ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
		defer cancel()
		ctx, read := dbwork.Measure(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			h.RunWebClaimRecovery(ctx, func(string) ClaimRequestGuard { return nil })
		}()
		defer func() {
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("owner recovery did not stop")
			}
		}()
		// With no setup return resolver, one empty recovery pass reads saved
		// watches, expired owner captures, queued denials and the pending-work
		// predicate. Wait for that final predicate before measuring idle work.
		const emptyPassStatements = 4
		synctest.Wait()
		before := read().Statements
		if before != emptyPassStatements {
			t.Fatalf("empty recovery startup read %d statements, wanted %d", before, emptyPassStatements)
		}
		// Cross the original 30-second retry boundary with the production timer.
		time.Sleep(30 * time.Second)
		synctest.Wait()
		if after := read().Statements; after != before {
			t.Fatalf("empty recovery polled SQLite at its retry boundary: %d -> %d", before, after)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("empty recovery stopped without cancellation")
		default:
		}
		if after := read().Statements; after != before {
			t.Fatalf("empty recovery polled SQLite after its startup read: %d -> %d", before, after)
		}
		h.wakeClaimRecovery()
		synctest.Wait()
		if after := read().Statements; after != before+emptyPassStatements {
			t.Fatalf("recovery wake read %d statements, wanted %d", after, before+emptyPassStatements)
		}
		if tr.finalizes != 0 || tr.retrieves != 0 || tr.cancels != 0 {
			t.Fatal("empty recovery performed remote claim work")
		}
	})
}

// ownerRecoveryFixture keeps real owner/start authority local to these recovery
// vectors. The unrelated SQL fixtures and their guard tables stay unchanged.
type ownerRecoveryFixture struct {
	db               *sql.DB
	path, dir        string
	store            *SQLiteStore
	cipher           *ClaimCipher
	keys             *ProtectedKeys
	identity         *identity.Service
	principal        identity.Principal
	familyID         string
	runner           *AuthorityRunner
	owner            LocalOwner
	binding          Binding
	hostedKey        ed25519.PrivateKey
	now              time.Time
	ctx              context.Context
	requestGuard     ClaimRequestGuard
	installedChanges int
}

func newOwnerRecoveryFixture(t *testing.T) *ownerRecoveryFixture {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "server.sqlite")
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	gate, runner := lifecycleRunner(t)
	lease, err := gate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lease.Close)
	ctx := context.WithValue(lease.Context(), claimAuthorityKey{}, LifecycleLease(lease))
	keys, err := OpenCurrentKeys(ctx, db, root, runner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { keys.Close() })
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	const installation = "f89bfbc4-cd90-4cda-a925-a123bb4a8b8a"
	setupCtx, err := identity.WithIssuingDevice(ctx, identity.DeviceRegistration{InstallationID: installation, Name: "Owner browser", Platform: "web", App: "portico"}, "127.0.0.1:1234")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := id.BrowserSetupToken(setupCtx)
	if err != nil {
		t.Fatal(err)
	}
	setup, err := id.SetupWithOptions(setupCtx, identity.SetupOptions{SetupToken: secret, AuthMode: "local", Username: "owner", Password: "Local-password1!", Name: "Fixture"}, true)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := id.AuthenticateContext(setupCtx, setup.Envelope.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	move, err := id.PreviewAccountMove(setupCtx, setup.Envelope.AccessToken, identity.MovePreviewRequest{To: identity.SignInPortico})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = id.ConfirmPorticoMove(setupCtx, setup.Envelope.AccessToken, identity.MoveConfirmRequest{MoveID: move.ID, ExpectedRevision: move.Revision}); err != nil {
		t.Fatal(err)
	}
	_, hosted, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := OpenClaimCipher(ctx, db, root)
	if err != nil {
		t.Fatal(err)
	}
	var public []byte
	if err = db.QueryRowContext(ctx, `SELECT public_key FROM networking_claim_identity WHERE singleton=1`).Scan(&public); err != nil {
		t.Fatal(err)
	}
	f := &ownerRecoveryFixture{db: db, path: path, dir: root, keys: keys, identity: id, principal: principal, familyID: setup.Envelope.SessionFamilyID, runner: runner, cipher: cipher, hostedKey: hosted, now: time.Now().UTC(), owner: LocalOwner{AccountID: principal.AccountID, ProfileID: principal.ProfileID, Epoch: int64(principal.Epoch)}, binding: Binding{OperationID: identity.Token(), ServerID: id.ID(), AccountID: UnboundAccount, PublicKey: public}}
	f.requestGuard = func(ctx context.Context, tx *sql.Tx) error { return id.CheckClaimRequestOwnerTx(ctx, tx, principal) }
	f.store = f.storeFor(t, db, cipher)
	f.ctx, err = guardedClaimRequest(ctx, f.store, f.requestGuard)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *ownerRecoveryFixture) storeFor(t *testing.T, db *sql.DB, cipher *ClaimCipher) *SQLiteStore {
	t.Helper()
	verify := func(ctx context.Context, raw []byte) (ApprovalFacts, error) {
		if err := ctx.Err(); err != nil {
			return ApprovalFacts{}, err
		}
		var envelope struct{ Payload, Signature []byte }
		if json.Unmarshal(raw, &envelope) != nil || !ed25519.Verify(f.hostedKey.Public().(ed25519.PublicKey), envelope.Payload, envelope.Signature) {
			return ApprovalFacts{}, ErrInvalid
		}
		var facts ApprovalFacts
		if json.Unmarshal(envelope.Payload, &facts) != nil {
			return ApprovalFacts{}, ErrInvalid
		}
		return facts, nil
	}
	store, err := NewSQLiteStore(db, cipher, GuardLocalOwner, GuardInstalledClaim, verify, "https://hosted.example")
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return f.now }
	if err = store.SetInstallationHooks(func(ctx context.Context, tx *sql.Tx, v Intent) error {
		return f.identity.InstallOwnerClaimTx(ctx, tx, v.OperationID)
	}, func() { f.installedChanges++ }); err != nil {
		t.Fatal(err)
	}
	if err = store.SetTerminalRevoker(func(ctx context.Context, tx *sql.Tx, v Intent, _ string) error {
		return f.identity.RetireOwnerCompletionForClaimTx(ctx, tx, v.OperationID)
	}); err != nil {
		t.Fatal(err)
	}
	return store
}

func firstSwitchApproved(t *testing.T, f *ownerRecoveryFixture) Intent {
	t.Helper()
	ctx := f.ctx
	v, err := f.store.PreparePurpose(ctx, f.binding, f.owner, ClaimPurpose{Purpose: "switch"})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := NewHTTPTransport("https://hosted.example", f.store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	h, err := NewClaimHandler(f.store, f.runner, transport, f.keys, func(*http.Request) (ClaimRequestOwner, error) { return ClaimRequestOwner{}, ErrClaimOwnerRequired }, func() string { return "Fixture" })
	if err != nil {
		t.Fatal(err)
	}
	authority := &OwnerStartAuthority{CaptureTx: func(ctx context.Context, tx *sql.Tx, audience string, original, start claimweb.Signed) error {
		return f.identity.CaptureOwnerClaimTx(ctx, tx, f.principal, original, start, audience)
	}, BindTx: func(ctx context.Context, tx *sql.Tx, op, transaction string) error {
		return f.identity.BindOwnerClaimTransactionTx(ctx, tx, f.principal, op, transaction)
	}}
	stateBytes, pkce := sha256.Sum256([]byte(identity.Token())), sha256.Sum256([]byte(identity.Token()))
	state, challenge := base64.RawURLEncoding.EncodeToString(stateBytes[:]), base64.RawURLEncoding.EncodeToString(pkce[:])
	calls := 0
	var originalStart claimweb.OwnerStart
	transport.client.Transport = ownerPendingRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.String() != "https://hosted.example/v1/server-claims/pending" || r.Header.Get("Authorization") != "" || len(r.Cookies()) != 0 {
			t.Fatal("unexpected pending request authority")
		}
		var request claimweb.PendingRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.OwnerStart == nil {
			t.Fatal("full owner proof absent", err)
		}
		claim, start, err := claimweb.VerifyOwnerStart(request.Original(), *request.OwnerStart, "https://hosted.example", time.Now())
		if err != nil || claim.OperationID != v.OperationID || claim.ServerID != v.ServerID || start.State != state || start.CodeChallenge != challenge || start.InstallationID != "f89bfbc4-cd90-4cda-a925-a123bb4a8b8a" {
			t.Fatal("actual signed pending binding", err)
		}
		originalStart = start
		var stored []byte
		if err = f.db.QueryRowContext(ctx, `SELECT start_payload FROM identity_owner_completions WHERE operation_id=? AND phase='prepared'`, v.OperationID).Scan(&stored); err != nil {
			t.Fatal("pending preceded durable capture", err)
		}
		raw, err := base64.RawURLEncoding.DecodeString(request.OwnerStart.Payload)
		if err != nil || !bytes.Equal(stored, raw) {
			t.Fatal("stored start differs from original signed start", err)
		}
		body, err := json.Marshal(claimweb.Pending{Purpose: "switch", Code: "ACTUAL_PENDING_CODE", OwnerTransactionID: "actual_owner_transaction", ServerID: claim.ServerID, Name: claim.Name, Address: claim.Address, RequestedAt: claim.IssuedAt, ExpiresAt: claim.ExpiresAt, Status: "pending"})
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	out, err := h.beginOwnerWebClaim(ctx, v, "https://server.example", state, challenge, "f89bfbc4-cd90-4cda-a925-a123bb4a8b8a", authority)
	if err != nil || out.OwnerStart == nil || out.OwnerTransactionID != "actual_owner_transaction" || calls != 1 {
		t.Fatal("original owner start was not captured and bound", err, calls)
	}
	// The signed approval adopts a distinct Hosted account from this exact
	// unbound operation and the verified, durably stored original start mode.
	approvedBinding := v.Binding
	approvedBinding.AccountID = "account-approved"
	facts := ApprovalFacts{TargetUsername: "approved_owner", ClaimPurpose: v.ClaimPurpose, Binding: approvedBinding, Revision: 1, IssuedAt: f.now, ExpiresAt: f.now.Add(5 * time.Minute), TransitionMode: originalStart.TransitionMode}
	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := json.Marshal(struct{ Payload, Signature []byte }{raw, ed25519.Sign(f.hostedKey, raw)})
	if err != nil {
		t.Fatal(err)
	}
	v, err = f.store.Approve(ctx, v, proof)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *ownerRecoveryFixture) cancel(t *testing.T, v Intent) Cancellation {
	t.Helper()
	coordinator, err := NewCoordinator(v.OperationID, "https://hosted.example", f.store, &modelTransport{}, f.keys)
	if err != nil {
		t.Fatal(err)
	}
	item, err := coordinator.cancellation(f.ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func firstSwitchRetrieving(t *testing.T, f *ownerRecoveryFixture, v Intent) (Intent, Cancellation) {
	t.Helper()
	ctx := f.ctx
	item, err := f.store.ArmPreinstallCancellation(ctx, v, f.cancel(t, v))
	if err != nil {
		t.Fatal(err)
	}
	v, err = f.store.BeginFinalizing(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	v, err = f.store.Committed(ctx, v, Commit{OperationID: v.OperationID, ServerID: v.ServerID, ClaimGeneration: "7", CredentialGeneration: "2"})
	if err != nil {
		t.Fatal(err)
	}
	return v, item
}

func TestOwnerPreinstallCancellationSurvivesExpiredApprovalAndRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, failure := range []string{"expired", "revoked", "lost-finalize"} {
		t.Run(failure, func(t *testing.T) {
			f := newOwnerRecoveryFixture(t)
			ctx := f.ctx
			v := firstSwitchApproved(t, f)
			v, item := firstSwitchRetrieving(t, f, v)
			if failure == "lost-finalize" {
				// The original FINALIZE may have committed centrally even when no
				// generation was received locally. The armed proof stays intent-mode.
				if _, err := f.db.Exec(`UPDATE networking_claim_intents SET stage='finalizing',claim_generation='',credential_generation='' WHERE operation_id=?`, v.OperationID); err != nil {
					t.Fatal(err)
				}
				var err error
				v, err = f.store.Load(ctx, v.OperationID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if failure == "revoked" {
				if _, err := f.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, f.familyID); err != nil {
					t.Fatal(err)
				}
				// Establish the actual owner refusal while the approval is still
				// live; the original expiry advancement remains independent below.
				snapshot, err := dbwork.BeginSnapshot(ctx, f.db)
				if err != nil {
					t.Fatal(err)
				}
				revoked := f.requestGuard(ctx, snapshot.Tx())
				snapshot.Rollback()
				if revoked == nil {
					t.Fatal("revoked original owner retained approval authority")
				}
			}
			f.now = f.now.Add(6 * time.Minute)
			if failure != "lost-finalize" {
				if _, err := f.store.Install(ctx, v, resultFor(t, v)); err == nil {
					t.Fatal("late install accepted expired or revoked initiating authority")
				}
			}
			if _, err := f.db.Exec(`UPDATE identity_moves SET owner_confirmed=0,stage='cancelled'`); err != nil {
				t.Fatal(err)
			}
			// The dormant proof grants no Local egress before cancellation wins.
			envelope := proofEnvelope{Payload: base64.RawURLEncoding.EncodeToString(item.Proof.Payload), Signature: base64.RawURLEncoding.EncodeToString(item.Proof.Signature)}
			if err := f.store.checkServiceRequest(ctx, "/v1/server-claims/cancel", envelope); !errors.Is(err, ErrLocalServices) {
				t.Fatal("armed proof became network authority before cancellation", err)
			}
			queued, saved, err := f.store.QueuePreinstallCancellation(ctx, v.OperationID)
			if err != nil || queued.Stage != CancelPending || !bytes.Equal(saved.Proof.Payload, item.Proof.Payload) || !bytes.Equal(saved.Proof.Signature, item.Proof.Signature) {
				t.Fatal("original deny-only cancellation not queued", err)
			}
			db := openFixtureDB(t, f.path)
			cipher, err := OpenClaimCipher(ctx, db, f.dir)
			if err != nil {
				t.Fatal(err)
			}
			restarted := f.storeFor(t, db, cipher)
			again, exact, err := restarted.QueuePreinstallCancellation(ctx, v.OperationID)
			if err != nil || again.Revision != queued.Revision || !bytes.Equal(exact.Proof.Payload, item.Proof.Payload) || !bytes.Equal(exact.Proof.Signature, item.Proof.Signature) {
				t.Fatal("restart changed pending cancellation", err)
			}
			if err = restarted.Current(ctx, v); err == nil {
				t.Fatal("cancelled generation retained positive authority")
			}
			tr, err := NewHTTPTransport("https://hosted.example", restarted)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(tr.CloseIdleConnections)
			calls := 0
			tr.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/v1/server-claims/cancel" || r.Header.Get("Authorization") != "" {
					t.Fatal("reconciliation sent another request or a credential")
				}
				raw, _ := io.ReadAll(r.Body)
				var got proofEnvelope
				if json.Unmarshal(raw, &got) != nil || got != envelope {
					t.Fatal("reconciliation changed exact saved proof")
				}
				if calls == 1 {
					return nil, errors.New("simulated lost cancellation receipt")
				}
				body, _ := json.Marshal(map[string]string{"operationId": v.OperationID, "requestId": item.RequestID})
				return reply(200, string(body)), nil
			})
			coordinator, err := NewCoordinator(v.OperationID, "https://hosted.example", restarted, tr, f.keys)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = coordinator.ReconcilePreinstallCancellation(ctx); err == nil {
				t.Fatal("lost receipt reported success")
			}
			if done, err := coordinator.ReconcilePreinstallCancellation(ctx); err != nil || done.Stage != Cancelled || calls != 2 {
				t.Fatal("exact saved cancellation could not finish after lost receipt", err)
			}
			for _, path := range []string{"/v1/server-claims/cancel", "/v1/server-claims/nonce", "/v1/server-claims/finalize", "/v1/server-claims/result", "/v1/servers/checkin"} {
				if err := restarted.checkServiceRequest(ctx, path, envelope); !errors.Is(err, ErrLocalServices) {
					t.Fatal("retired outbox granted Local networking", path, err)
				}
			}
		})
	}
}

func TestOwnerPreinstallInstallWinRetiresCancellationAtomically(t *testing.T) {
	f := newOwnerRecoveryFixture(t)
	ctx := f.ctx
	v, item := firstSwitchRetrieving(t, f, firstSwitchApproved(t, f))
	installed, err := f.store.Install(ctx, v, resultFor(t, v))
	if err != nil || installed.Stage != Installed || f.installedChanges != 1 {
		t.Fatal(err)
	}
	if _, _, err = f.store.QueuePreinstallCancellation(ctx, v.OperationID); err == nil {
		t.Fatal("pre-install recovery cancelled a locally installed claim")
	}
	if allowed, err := f.store.queuedPreinstallCancellation(ctx, item.Proof.Payload, item.Proof.Signature); err != nil || allowed {
		t.Fatal("installed claim retained cancellation exception", err)
	}
}

func TestOwnerPreinstallCapturedDeadlineRefusesLateInstall(t *testing.T) {
	f := newOwnerRecoveryFixture(t)
	ctx := f.ctx
	v, item := firstSwitchRetrieving(t, f, firstSwitchApproved(t, f))
	deadline := f.now.Add(time.Minute)
	guarded, err := guardedClaimRequest(ctx, f.store, func(ctx context.Context, tx *sql.Tx) error {
		if !f.now.Before(deadline) {
			return ErrStale
		}
		return f.requestGuard(ctx, tx)
	})
	if err != nil {
		t.Fatal(err)
	}
	// Expire the captured initiating challenge before the approval itself.
	f.now = deadline
	if _, err = f.store.Install(guarded, v, resultFor(t, v)); err == nil {
		t.Fatal("local install accepted an expired captured challenge")
	}
	queued, exact, err := f.store.QueuePreinstallCancellation(ctx, v.OperationID)
	if err != nil || queued.Stage != CancelPending || !bytes.Equal(exact.Proof.Payload, item.Proof.Payload) {
		t.Fatal("expired captured guard stranded its saved cancellation", err)
	}
}

func TestOwnerPreinstallCancellationEnvelopeAndSuccessorFence(t *testing.T) {
	f := newOwnerRecoveryFixture(t)
	ctx := f.ctx
	v, item := firstSwitchRetrieving(t, f, firstSwitchApproved(t, f))
	if _, _, err := f.store.QueuePreinstallCancellation(ctx, v.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE identity_moves SET owner_confirmed=0,stage='cancelled'`); err != nil {
		t.Fatal(err)
	}
	exact := proofEnvelope{Payload: base64.RawURLEncoding.EncodeToString(item.Proof.Payload), Signature: base64.RawURLEncoding.EncodeToString(item.Proof.Signature)}
	for _, bad := range []proofEnvelope{{exact.Payload + "\n", exact.Signature}, {exact.Payload, exact.Signature + "\r\n"}, {exact.Payload + "=", exact.Signature}, {exact.Payload, base64.RawURLEncoding.EncodeToString(make([]byte, 64))}} {
		if err := f.store.checkServiceRequest(ctx, "/v1/server-claims/cancel", bad); err == nil {
			t.Fatal("noncanonical or substituted cancellation envelope accepted")
		}
	}
	if _, err := f.db.Exec(`UPDATE networking_claim_identity SET reset_generation=reset_generation+1,active_operation_id=NULL`); err != nil {
		t.Fatal(err)
	}
	if err := f.store.checkServiceRequest(ctx, "/v1/server-claims/cancel", exact); !errors.Is(err, ErrLocalServices) {
		t.Fatal("predecessor outbox survived successor generation", err)
	}
}

func TestOwnerPreinstallCoordinatorArmsBeforeFinalizeAndWorkerReconciles(t *testing.T) {
	f := newOwnerRecoveryFixture(t)
	ctx := f.ctx
	v := firstSwitchApproved(t, f)
	transport := &modelTransport{now: f.now}
	transport.finalizeHook = func() error {
		var count int
		if err := f.db.QueryRow(`SELECT count(*) FROM networking_claim_cancellations WHERE operation_id=? AND mode='intent' AND claim_generation='' AND state='pending'`, v.OperationID).Scan(&count); err != nil || count != 1 {
			t.Fatal("FINALIZE escaped before durable original-key cancellation", err)
		}
		// Central FINALIZE succeeded, but local Committed/Install can no longer
		// cross the captured deadline. Recovery must not need a new signature.
		f.now = f.now.Add(6 * time.Minute)
		return nil
	}
	coordinator, err := NewCoordinator(v.OperationID, "https://hosted.example", f.store, transport, f.keys)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.now = func() time.Time { return f.now }
	if _, err = coordinator.Step(ctx); !errors.Is(err, ErrApprovalRequired) || transport.finalizes != 1 {
		t.Fatal("late FINALIZE response crossed the local deadline", err)
	}
	_, runner := lifecycleRunner(t)
	tr, err := NewHTTPTransport("https://hosted.example", f.store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tr.CloseIdleConnections)
	calls := 0
	tr.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/v1/server-claims/cancel" {
			t.Fatal("recovery performed positive claim work", r.URL.Path)
		}
		var envelope proofEnvelope
		if json.NewDecoder(r.Body).Decode(&envelope) != nil {
			t.Fatal("missing stored cancellation envelope")
		}
		raw, _ := base64.RawURLEncoding.DecodeString(envelope.Payload)
		var wire cancelWire
		if json.Unmarshal(raw, &wire) != nil || wire.Mode != "intent" || wire.ClaimGeneration != "" {
			t.Fatal("recovery fabricated a central generation")
		}
		body, _ := json.Marshal(map[string]string{"operationId": v.OperationID, "requestId": wire.RequestID})
		return reply(200, string(body)), nil
	})
	h, err := NewClaimHandler(f.store, runner, tr, f.keys, func(*http.Request) (ClaimRequestOwner, error) { return ClaimRequestOwner{}, ErrClaimOwnerRequired }, func() string { return "Fixture" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE identity_moves SET owner_confirmed=0,stage='cancelled'`); err != nil {
		t.Fatal(err)
	}
	if err = h.reconcileWebClaim(context.Background(), v.OperationID, nil, false); err != nil || calls != 1 {
		t.Fatal("fresh lifecycle recovery could not deliver its stored denial", err)
	}
	loaded, err := f.store.Load(ctx, v.OperationID)
	if err != nil || loaded.Stage != Cancelled {
		t.Fatal("worker did not finish the durable cancellation", err)
	}
}
