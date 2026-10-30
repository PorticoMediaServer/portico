package networking

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/authoritygate"
	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

type ownerPendingRoundTrip func(*http.Request) (*http.Response, error)

func (fn ownerPendingRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

// Real fresh Server stores, keys, setup, family, move and HTTPTransport meet at
// this boundary. The response peer is controlled; this is not a Hosted DB pair.
func TestOwnerWebPendingFollowsAtomicCaptureAndFencesLateRevocation(t *testing.T) {
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
	const installation = "f89bfbc4-cd90-4cda-a925-a123bb4a8b8a"
	ctx, err = identity.WithIssuingDevice(ctx, identity.DeviceRegistration{InstallationID: installation, Name: "Owner browser", Platform: "web", App: "portico"}, "127.0.0.1:1234")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := id.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	setup, err := id.SetupWithOptions(ctx, identity.SetupOptions{SetupToken: secret, AuthMode: "local", Username: "owner", Password: "Local-password1!", Name: "Owner pending server"}, true)
	if err != nil {
		t.Fatal(err)
	}
	p, err := id.AuthenticateContext(ctx, setup.Envelope.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	move, err := id.PreviewAccountMove(ctx, setup.Envelope.AccessToken, identity.MovePreviewRequest{To: identity.SignInPortico})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = id.ConfirmPorticoMove(ctx, setup.Envelope.AccessToken, identity.MoveConfirmRequest{MoveID: move.ID, ExpectedRevision: move.Revision}); err != nil {
		t.Fatal(err)
	}
	cipher, err := OpenClaimCipher(ctx, db, root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteStore(db, cipher, GuardLocalOwner, GuardInstalledClaim, func(context.Context, []byte) (ApprovalFacts, error) { return ApprovalFacts{}, ErrInvalid }, "https://hosted.example")
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
	guard := func(ctx context.Context, tx *sql.Tx) error { return id.CheckClaimRequestOwnerTx(ctx, tx, p) }
	var v Intent
	err = runner.Do(ctx, func(ctx context.Context) error {
		ctx, err := guardedClaimRequest(ctx, store, guard)
		if err != nil {
			return err
		}
		var public []byte
		if err = db.QueryRowContext(ctx, `SELECT public_key FROM networking_claim_identity WHERE singleton=1`).Scan(&public); err != nil {
			return err
		}
		v, err = store.PreparePurpose(ctx, Binding{OperationID: identity.Token(), ServerID: id.ID(), AccountID: UnboundAccount, PublicKey: public}, LocalOwner{AccountID: p.AccountID, ProfileID: p.ProfileID, Epoch: int64(p.Epoch)}, ClaimPurpose{Purpose: "switch"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	authority := &OwnerStartAuthority{ExpiresAt: p.ExpiresAt, CaptureTx: func(ctx context.Context, tx *sql.Tx, audience string, original, start claimweb.Signed) error {
		return id.CaptureOwnerClaimTx(ctx, tx, p, original, start, audience)
	}, CaptureWithCountTx: func(ctx context.Context, tx *sql.Tx, audience string, original, start claimweb.Signed, sign OwnerSwitchCountSign) (*claimweb.Signed, error) {
		return id.CaptureOwnerClaimWithCountTx(ctx, tx, p, original, start, audience, identity.OwnerSwitchCountSigner(sign))
	}, BindTx: func(ctx context.Context, tx *sql.Tx, op, transaction string) error {
		return id.BindOwnerClaimTransactionTx(ctx, tx, p, op, transaction)
	}}
	h.authorize = func(r *http.Request) (ClaimRequestOwner, error) {
		if r.Header.Get("Authorization") != "Bearer "+setup.Envelope.AccessToken || r.Header.Get("Origin") != "https://server.example" {
			return ClaimRequestOwner{}, ErrClaimOwnerRequired
		}
		current, err := id.AuthenticateContext(r.Context(), setup.Envelope.AccessToken)
		if err != nil || current.AccountID != p.AccountID || current.ProfileID != p.ProfileID || current.Hash != p.Hash {
			return ClaimRequestOwner{}, ErrClaimOwnerRequired
		}
		return ClaimRequestOwner{Owner: LocalOwner{AccountID: p.AccountID, ProfileID: p.ProfileID, Epoch: int64(p.Epoch)}, Guard: guard, WebOrigin: "https://server.example", OwnerStart: authority}, nil
	}
	stateBytes, pkce := sha256.Sum256([]byte(identity.Token())), sha256.Sum256([]byte(identity.Token()))
	state, challenge := base64.RawURLEncoding.EncodeToString(stateBytes[:]), base64.RawURLEncoding.EncodeToString(pkce[:])
	calls, revoke := 0, false
	var first claimweb.PendingRequest
	transport.client = &http.Client{Transport: ownerPendingRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.String() != "https://hosted.example/v1/server-claims/pending" || r.Header.Get("Authorization") != "" || len(r.Cookies()) != 0 {
			t.Fatal("unexpected pending request authority")
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		var request claimweb.PendingRequest
		if err = json.Unmarshal(raw, &request); err != nil || request.OwnerStart == nil {
			t.Fatal("full owner proof absent", err)
		}
		claim, start, err := claimweb.VerifyOwnerStart(request.Original(), *request.OwnerStart, "https://hosted.example", time.Now())
		if err != nil || start.State != state || start.CodeChallenge != challenge || start.InstallationID != installation {
			t.Fatal("actual signed pending binding", err)
		}
		if request.OwnerSwitchCount == nil {
			t.Fatal("actual protected signer omitted captured count")
		}
		fact, err := claimweb.VerifyOwnerSwitchCount(request.Original(), *request.OwnerStart, *request.OwnerSwitchCount, "https://hosted.example", time.Now())
		if err != nil || fact.OtherPeopleCount != 0 || fact.PreviewID != move.ID || fact.PreviewRevision != move.Revision {
			t.Fatal("actual signed count lost its preview binding", err)
		}
		var count int
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM identity_owner_completions c JOIN hosted_identity_challenges h ON h.challenge_hash=c.server_challenge_hash AND h.installation_id=c.installation_id WHERE c.operation_id=? AND c.phase='prepared' AND c.claim_hash=?`, v.OperationID, start.ClaimHash).Scan(&count); err != nil || count != 1 {
			t.Fatal("pending preceded full durable capture", count, err)
		}
		if calls == 1 {
			first = request
		} else if request.Payload != first.Payload || request.Signature != first.Signature || *request.OwnerStart != *first.OwnerStart || *request.OwnerSwitchCount != *first.OwnerSwitchCount {
			t.Fatal("retry refreshed signed start or original request")
		}
		if revoke {
			if _, err = db.ExecContext(ctx, `UPDATE authorization_session_families SET revoked=1 WHERE id=?`, setup.Envelope.SessionFamilyID); err != nil {
				return nil, err
			}
		}
		body, _ := json.Marshal(claimweb.Pending{Purpose: "switch", Code: "ACTUAL_PENDING_CODE", OwnerTransactionID: "actual_owner_transaction", ServerID: claim.ServerID, Name: claim.Name, Address: claim.Address, RequestedAt: claim.IssuedAt, ExpiresAt: claim.ExpiresAt, Status: "pending"})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}
	begin := func() (out localWebClaim, err error) {
		err = runner.Do(ctx, func(ctx context.Context) error {
			ctx, err := guardedClaimRequest(ctx, store, guard)
			if err != nil {
				return err
			}
			out, err = h.beginOwnerWebClaim(ctx, v, "https://server.example", state, challenge, installation, authority)
			return err
		})
		return
	}
	// Exercise the actual public decoder, installation header and response. The
	// approval watcher is independently covered; do not start a background wait
	// against this bounded pending-only transport fixture.
	h.webWatches.Store(v.OperationID, &webClaimWatch{})
	t.Cleanup(func() { h.webWatches.Delete(v.OperationID) })
	serve := func(installationHeader string) *httptest.ResponseRecorder {
		body, err := json.Marshal(ClaimActionRequest{Expected: ClaimExpected{OperationID: v.OperationID, LocalGeneration: v.LocalGeneration, Revision: v.Revision}, OwnerStart: &ClaimOwnerStartRequest{State: state, CodeChallenge: challenge, InstallationID: installation}})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "https://server.example/v1/networking/claim/web-approval", bytes.NewReader(body)).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+setup.Envelope.AccessToken)
		r.Header.Set("Origin", "https://server.example")
		if installationHeader != "" {
			r.Header.Set("X-Portico-Installation-Id", installationHeader)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := serve(""); w.Code != http.StatusBadRequest || calls != 0 {
		t.Fatal("missing installation initiated private pending", w.Code, calls)
	}
	w := serve(installation)
	var status ClaimStatus
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &status) != nil || status.OwnerContinuation == nil || status.OwnerContinuation.Claim == nil || status.OwnerContinuation.Start == nil {
		t.Fatal("served owner initiation", w.Code)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("ownerSwitchCount")) || bytes.Contains(w.Body.Bytes(), []byte("otherPeopleCount")) {
		t.Fatal("private count appeared in public initiation")
	}
	expires, err := time.Parse(time.RFC3339Nano, status.OwnerContinuation.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	out := localWebClaim{Signed: *status.OwnerContinuation.Claim, OwnerStart: status.OwnerContinuation.Start, OwnerTransactionID: status.OwnerContinuation.TransactionID, URL: status.ApprovalURL, Code: status.ClaimCode, ExpiresAt: expires}
	if err != nil || out.OwnerStart == nil || out.OwnerTransactionID != "actual_owner_transaction" || calls != 1 {
		t.Fatal("initiation", err, calls)
	}
	retry, err := begin()
	if err != nil || retry.Signed != out.Signed || *retry.OwnerStart != *out.OwnerStart || retry.ExpiresAt != out.ExpiresAt || calls != 2 {
		t.Fatal("immutable initiation retry", err, calls)
	}
	err = runner.Do(ctx, func(ctx context.Context) error {
		status, err := h.snapshot(ctx)
		raw, _ := json.Marshal(status)
		if bytes.Contains(raw, []byte(state)) || bytes.Contains(raw, []byte(out.OwnerStart.Payload)) {
			t.Fatal("status disclosed signed private pending")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	revoke = true
	if late, err := begin(); err == nil || late.OwnerStart != nil || late.URL != "" || calls != 3 {
		t.Fatal("late revoked family received handoff", err, calls)
	}
	if kind, err := id.SignInKind(ctx); err != nil || kind != identity.SignInLocal {
		t.Fatal("pending changed server kind", err)
	}
	var links int
	if err = db.QueryRow(`SELECT count(*) FROM account_portico_links`).Scan(&links); err != nil || links != 0 {
		t.Fatal("pending invented owner linkage", err)
	}
	// A crash/failed pending response need not leave any watch/request row.
	// The typed capture still expires through the real deny-only recovery path.
	if _, err = db.Exec(`DELETE FROM networking_web_claims; UPDATE identity_owner_completions SET expires_ms=created_ms+1`); err != nil {
		t.Fatal(err)
	}
	retired := make(chan struct{})
	if err = store.SetTerminalRevoker(func(ctx context.Context, tx *sql.Tx, intent Intent, _ string) error {
		if err := id.RetireOwnerCompletionForClaimTx(ctx, tx, intent.OperationID); err != nil {
			return err
		}
		close(retired)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	recovery, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		h.RunWebClaimRecovery(recovery, func(string) ClaimRequestGuard { return nil })
	}()
	defer func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(2 * time.Second):
			t.Error("owned recovery did not stop")
		}
	}()
	select {
	case <-retired:
	case <-time.After(3 * time.Second):
		t.Fatal("typed expired capture without watch was stranded")
	}
	// The revoker runs inside the cancellation writer. Its signal is earlier
	// than durable publication; a WAL reader can still see the old snapshot.
	// Queue a writer barrier so the assertions observe commit or rollback,
	// without polling, sleeping, or treating the hook as a commit receipt.
	barrier, err := dbwork.Begin(ctx, db, dbwork.ClassSecurityFence)
	if err != nil {
		t.Fatal(err)
	}
	defer barrier.Rollback()
	var phase, moveStage string
	if err = barrier.Tx().QueryRow(`SELECT c.phase,m.stage FROM identity_owner_completions c JOIN identity_moves m ON m.id=c.move_id`).Scan(&phase, &moveStage); err != nil || phase != "cancelled" || moveStage != "cancelled" || calls != 3 {
		t.Fatal("no-watch expiry retained move or contacted Hosted", err, phase, moveStage, calls)
	}
}
