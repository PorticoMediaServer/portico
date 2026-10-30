package identity_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/authoritygate"
	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

type ownerCaptureFixture struct {
	db              *sql.DB
	store           *networking.SQLiteStore
	runner          *networking.AuthorityRunner
	intent          networking.Intent
	bearer          string
	verifier        string
	s               *identity.Service
	p               identity.Principal
	key             ed25519.PrivateKey
	claim           claimweb.Request
	start           claimweb.OwnerStart
	original, proof claimweb.Signed
}

func ownerCaptureSigned(t *testing.T, key ed25519.PrivateKey, value any) claimweb.Signed {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, raw))}
}

// This fixture uses the canonical fresh baseline, genuine Browser Setup and
// move confirmation, actual PreparePurpose, and complete Ed25519 proofs. It
// does not claim Hosted, protected-key signing, transport or browser coverage.
func newOwnerCaptureFixture(t *testing.T, verifiers ...networking.ApprovalVerifier) ownerCaptureFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server, err := networking.ServerIdentity(pub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO networking_server_identities(server_id,public_key,key_incarnation,created_at) VALUES(?,?,?,?);INSERT INTO networking_claim_identity(singleton,server_id,public_key,reset_generation) VALUES(1,?,?,0);INSERT INTO configuration(key,value) VALUES('id',?)`, server, []byte(pub), "capture-test-key", time.Now().UTC().Format(time.RFC3339Nano), server, []byte(pub), server); err != nil {
		t.Fatal(err)
	}
	s, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	const installation = "d297a1dc-3adc-4b1c-a7f2-fbd4640f69e8"
	ctx, err = identity.WithIssuingDevice(ctx, identity.DeviceRegistration{InstallationID: installation, Name: "Owner browser", Platform: "web", App: "portico"}, "127.0.0.1:1234")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := s.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	setup, err := s.SetupWithOptions(ctx, identity.SetupOptions{SetupToken: secret, AuthMode: "local", Username: "owner", Password: "Local-password1!", Name: "Capture server"}, true)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.AuthenticateContext(ctx, setup.Envelope.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	move, err := s.PreviewAccountMove(ctx, setup.Envelope.AccessToken, identity.MovePreviewRequest{To: identity.SignInPortico})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmPorticoMove(ctx, setup.Envelope.AccessToken, identity.MoveConfirmRequest{MoveID: move.ID, ExpectedRevision: move.Revision}); err != nil {
		t.Fatal(err)
	}
	cipher, err := networking.OpenClaimCipher(ctx, db, root)
	if err != nil {
		t.Fatal(err)
	}
	verify := networking.ApprovalVerifier(func(context.Context, []byte) (networking.ApprovalFacts, error) {
		return networking.ApprovalFacts{}, networking.ErrInvalid
	})
	if len(verifiers) != 0 {
		verify = verifiers[0]
	}
	store, err := networking.NewSQLiteStore(db, cipher, networking.GuardLocalOwner, networking.GuardInstalledClaim, verify, "https://hosted.example")
	if err != nil {
		t.Fatal(err)
	}
	op := identity.Token()
	gate, err := authoritygate.NewAuthorityGate(strings.Repeat("a", 64), false)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := networking.NewAuthorityRunner(func(ctx context.Context) (networking.LifecycleLease, error) { return gate.Acquire(ctx) })
	if err != nil {
		t.Fatal(err)
	}
	var intent networking.Intent
	if err = runner.Do(ctx, func(ctx context.Context) error {
		var err error
		intent, err = store.PreparePurpose(ctx, networking.Binding{OperationID: op, ServerID: server, AccountID: networking.UnboundAccount, PublicKey: pub}, networking.LocalOwner{AccountID: p.AccountID, ProfileID: p.ProfileID, Epoch: int64(p.Epoch)}, networking.ClaimPurpose{Purpose: "switch"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claim := claimweb.Request{Kind: "portico.claim.web.v2", Audience: "https://hosted.example", OperationID: op, ServerID: server, PublicKey: base64.RawURLEncoding.EncodeToString(pub), Name: "Capture server", Address: "https://server.example", Purpose: "switch", IssuedAt: now, ExpiresAt: now.Add(10 * time.Minute)}
	original := ownerCaptureSigned(t, key, claim)
	raw, _ := base64.RawURLEncoding.DecodeString(original.Payload)
	hash := sha256.Sum256(raw)
	verifier := identity.Token()
	pkce := sha256.Sum256([]byte(verifier))
	state := sha256.Sum256([]byte(identity.Token()))
	start := claimweb.OwnerStart{Kind: claimweb.OwnerStartKind, Audience: claim.Audience, ClaimHash: base64.RawURLEncoding.EncodeToString(hash[:]), OperationID: op, ServerID: server, PublicKey: claim.PublicKey, Address: claim.Address, Purpose: "switch", State: base64.RawURLEncoding.EncodeToString(state[:]), CodeChallenge: base64.RawURLEncoding.EncodeToString(pkce[:]), InstallationID: installation, ServerChallenge: identity.Token(), IssuedAt: now, ExpiresAt: now.Add(4 * time.Minute)}
	return ownerCaptureFixture{db: db, store: store, runner: runner, intent: intent, bearer: setup.Envelope.AccessToken, verifier: verifier, s: s, p: p, key: key, claim: claim, start: start, original: original, proof: ownerCaptureSigned(t, key, start)}
}

func (f ownerCaptureFixture) capture(ctx context.Context, tx *sql.Tx) error {
	return f.s.CaptureOwnerClaimTx(ctx, tx, f.p, f.original, f.proof, f.claim.Audience)
}

func TestOwnerCaptureFreezesFullProofsAndBindsOnlyActualTransaction(t *testing.T) {
	f := newOwnerCaptureFixture(t)
	ctx := context.Background()
	err := dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if err := f.capture(ctx, tx); err != nil {
			return err
		}
		if err := f.capture(ctx, tx); err != nil {
			t.Fatal("exact capture retry", err)
		}
		changed := f.start
		newState := sha256.Sum256([]byte(identity.Token()))
		changed.State = base64.RawURLEncoding.EncodeToString(newState[:])
		if err := f.s.CaptureOwnerClaimTx(ctx, tx, f.p, f.original, ownerCaptureSigned(t, f.key, changed), f.claim.Audience); !errors.Is(err, identity.ErrConflict) {
			t.Fatal("changed signed state replaced capture", err)
		}
		if err := f.s.BindOwnerClaimTransactionTx(ctx, tx, f.p, f.claim.OperationID, "actual_hosted_transaction"); err != nil {
			return err
		}
		if err := f.s.BindOwnerClaimTransactionTx(ctx, tx, f.p, f.claim.OperationID, "actual_hosted_transaction"); err != nil {
			t.Fatal("exact transaction replay", err)
		}
		if err := f.s.BindOwnerClaimTransactionTx(ctx, tx, f.p, f.claim.OperationID, "different_transaction"); !errors.Is(err, identity.ErrConflict) {
			t.Fatal("different transaction replaced capture", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var phase, state, transaction string
	var challenge, link int
	if err = f.db.QueryRow(`SELECT phase,state_hash,transaction_id,(SELECT count(*) FROM hosted_identity_challenges),(SELECT count(*) FROM account_portico_links) FROM identity_owner_completions WHERE operation_id=?`, f.claim.OperationID).Scan(&phase, &state, &transaction, &challenge, &link); err != nil || phase != "prepared" || state != identity.Digest(f.start.State) || state == f.start.State || transaction != "actual_hosted_transaction" || challenge != 1 || link != 0 {
		t.Fatalf("capture projection phase=%s challenge=%d links=%d: %v", phase, challenge, link, err)
	}
	if kind, err := f.s.SignInKind(ctx); err != nil || kind != identity.SignInLocal {
		t.Fatal("capture changed kind", err)
	}
}

func TestOwnerCaptureRechecksLiveFamilyMoveAndKeyBeforeAnyChallenge(t *testing.T) {
	f := newOwnerCaptureFixture(t)
	ctx := context.Background()
	for name, mutation := range map[string]string{
		"family revoked":         `UPDATE authorization_session_families SET revoked=1`,
		"token retired":          `UPDATE authorization_family_tokens SET retired=1`,
		"reset-only family":      `INSERT INTO reset_file_sessions(family_id,kind) SELECT id,'password' FROM authorization_session_families`,
		"epoch changed":          `UPDATE accounts SET epoch=epoch+1`,
		"key generation changed": `UPDATE networking_claim_identity SET reset_generation=reset_generation+1`,
		"confirmation withdrawn": `UPDATE identity_moves SET owner_confirmed=0`,
		"claim withdrawn":        `UPDATE networking_claim_intents SET stage='cancelled'`,
	} {
		t.Run(name, func(t *testing.T) {
			tx, err := f.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = tx.ExecContext(ctx, mutation); err != nil {
				t.Fatal(err)
			}
			if err = f.capture(ctx, tx); err == nil {
				t.Fatal("changed authority captured")
			}
			var count int
			if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM identity_owner_completions)+(SELECT count(*) FROM hosted_identity_challenges)`).Scan(&count); err != nil || count != 0 {
				t.Fatal("refusal left challenge or proof", count, err)
			}
		})
	}
}

func TestOwnerCaptureChallengeAndProofRollbackTogether(t *testing.T) {
	f := newOwnerCaptureFixture(t)
	ctx := context.Background()
	abort := errors.New("caller did not persist full pending")
	err := dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if err := f.capture(ctx, tx); err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	var count int
	if err = f.db.QueryRow(`SELECT (SELECT count(*) FROM identity_owner_completions)+(SELECT count(*) FROM hosted_identity_challenges)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed caller left challenge or proof", count, err)
	}
}
