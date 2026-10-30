package identity_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/authoritygate"
	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	hostedtrust "github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
)

type setupContinuationFixture struct {
	db                          *sql.DB
	s                           *identity.Service
	store                       *networking.SQLiteStore
	runner                      *networking.AuthorityRunner
	pending                     identity.PendingAccountSetup
	proofHash, secret, verifier string
	claim                       claimweb.Request
	start                       claimweb.SetupStart
	original, proof             claimweb.Signed
	intent                      networking.Intent
	key, hostedKey              ed25519.PrivateKey
}

func newSetupContinuationFixture(t *testing.T) setupContinuationFixture {
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
	if _, err = db.Exec(`INSERT INTO networking_server_identities(server_id,public_key,key_incarnation,created_at) VALUES(?,?,?,?);INSERT INTO networking_claim_identity(singleton,server_id,public_key,reset_generation) VALUES(1,?,?,0);INSERT INTO configuration(key,value) VALUES('id',?)`, server, []byte(pub), "setup-test-key", time.Now().UTC().Format(time.RFC3339Nano), server, []byte(pub), server); err != nil {
		t.Fatal(err)
	}
	s, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := s.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.SetupWithOptions(ctx, identity.SetupOptions{RequestID: "82a879b3-abf5-48c6-bba0-02ffd762f99c", SetupToken: secret, Name: "Setup server", AuthMode: "hosted"}, true)
	if err != nil || out.Pending == nil {
		t.Fatal("actual pending setup", err)
	}
	pending, proofHash, err := s.SetupClaimAuthority(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	hostedPub, hostedKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	verify, err := networking.NewApprovalVerifier("https://hosted.example", hostedtrust.KeyID(hostedPub), hostedPub)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := networking.OpenClaimCipher(ctx, db, root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := networking.NewSQLiteStore(db, cipher, networking.GuardLocalOwner, networking.GuardInstalledClaim, verify, "https://hosted.example")
	if err != nil {
		t.Fatal(err)
	}
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
		var e error
		intent, e = store.PreparePurpose(ctx, networking.Binding{OperationID: identity.Token(), ServerID: server, AccountID: networking.UnboundAccount, PublicKey: pub}, networking.LocalOwner{SetupOperationID: pending.RequestID, Epoch: pending.Revision}, networking.ClaimPurpose{Purpose: "setup"})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claim := claimweb.Request{Kind: "portico.claim.web.v2", Audience: "https://hosted.example", OperationID: intent.OperationID, ServerID: server, PublicKey: base64.RawURLEncoding.EncodeToString(pub), Name: "Setup server", Address: "https://server.example", Purpose: "setup", IssuedAt: now, ExpiresAt: now.Add(10 * time.Minute)}
	original := ownerCaptureSigned(t, key, claim)
	raw, _ := base64.RawURLEncoding.DecodeString(original.Payload)
	hash := sha256.Sum256(raw)
	verifier := identity.Token()
	pkce := sha256.Sum256([]byte(verifier))
	start := claimweb.SetupStart{Kind: claimweb.SetupStartKind, Audience: claim.Audience, ClaimHash: base64.RawURLEncoding.EncodeToString(hash[:]), OperationID: intent.OperationID, ServerID: server, PublicKey: claim.PublicKey, Address: claim.Address, Purpose: "setup", SetupRequestID: pending.RequestID, SetupRevision: pending.Revision, State: identity.Token(), CodeChallenge: base64.RawURLEncoding.EncodeToString(pkce[:]), InstallationID: "d297a1dc-3adc-4b1c-a7f2-fbd4640f69e8", ServerChallenge: identity.Token(), IssuedAt: now, ExpiresAt: now.Add(4 * time.Minute)}
	s.SetupClaimReadyTx = networking.SetupClaimInstalledTx
	return setupContinuationFixture{db: db, s: s, store: store, runner: runner, pending: pending, proofHash: proofHash, secret: secret, verifier: verifier, claim: claim, start: start, original: original, proof: ownerCaptureSigned(t, key, start), intent: intent, key: key, hostedKey: hostedKey}
}
func (f setupContinuationFixture) write(fn func(*sql.Tx) error) error {
	return dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassSecurityFence, fn)
}
func (f setupContinuationFixture) capture(t *testing.T) {
	t.Helper()
	if err := f.write(func(tx *sql.Tx) error {
		if err := f.s.CaptureSetupClaimTx(context.Background(), tx, f.pending, f.proofHash, f.original, f.proof, f.claim.Audience); err != nil {
			return err
		}
		return f.s.BindSetupClaimTransactionTx(context.Background(), tx, f.pending, f.proofHash, f.intent.OperationID, "actual_setup_transaction")
	}); err != nil {
		t.Fatal(err)
	}
}
func (f setupContinuationFixture) install(t *testing.T) {
	t.Helper()
	now := time.Now().UTC()
	raw := []byte(fmt.Sprintf(`{"kind":"portico.claim.approval","version":"2","audience":"https://hosted.example","operationId":%q,"serverId":%q,"accountId":"account-approved","publicKey":%q,"localGeneration":"0","approvalRevision":"1","purpose":"setup","currentOwnerAccountId":"","previousOwnerRole":"","returnRegistrationId":"","issuedAt":%q,"expiresAt":%q}`, f.intent.OperationID, f.intent.ServerID, f.claim.PublicKey, now.Format(time.RFC3339Nano), now.Add(4*time.Minute).Format(time.RFC3339Nano)))
	pub := f.hostedKey.Public().(ed25519.PublicKey)
	cert, err := hostedtrust.Certify(f.hostedKey, pub, "setup-documents", "documents", now.Add(-time.Hour), now.Add(time.Hour), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	proof, _ := json.Marshal(hostedtrust.Envelope{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.hostedKey, raw)), KeyID: "setup-documents", Certificate: cert})
	if err = f.runner.Do(context.Background(), func(ctx context.Context) error {
		v, e := f.store.Approve(ctx, f.intent, proof)
		if e != nil {
			return e
		}
		v, e = f.store.BeginFinalizing(ctx, v)
		if e != nil {
			return e
		}
		commit := networking.Commit{OperationID: v.OperationID, ServerID: v.ServerID, ClaimGeneration: "1", CredentialGeneration: "1"}
		v, e = f.store.Committed(ctx, v, commit)
		if e != nil {
			return e
		}
		secret, e := networking.NewSecret([]byte(strings.Repeat("setup-result-fixture-", 3)))
		if e != nil {
			return e
		}
		defer secret.Clear()
		v, e = f.store.Install(ctx, v, networking.Result{Commit: commit, Credential: secret})
		if e != nil {
			return e
		}
		_, e = f.store.Acknowledged(ctx, v)
		return e
	}); err != nil {
		t.Fatal("real signed approval/install", err)
	}
}
// installed marks the captured setup installed, as the claim's install does (O-163): no
// certificate, HTTPS route or return registration is involved.
func (f setupContinuationFixture) installed(t *testing.T) serverreturn.SetupExchange {
	t.Helper()
	if err := f.write(func(tx *sql.Tx) error {
		return f.s.MarkSetupInstalledTx(context.Background(), tx, f.pending, f.proofHash, f.intent.OperationID)
	}); err != nil {
		t.Fatal(err)
	}
	// Marking again is a no-op, not a conflict.
	if err := f.write(func(tx *sql.Tx) error {
		return f.s.MarkSetupInstalledTx(context.Background(), tx, f.pending, f.proofHash, f.intent.OperationID)
	}); err != nil {
		t.Fatal("re-marking an installed setup", err)
	}
	return serverreturn.SetupExchange{TransactionID: "actual_setup_transaction", State: f.start.State, Verifier: f.verifier, InstallationID: f.start.InstallationID, ServerChallenge: f.start.ServerChallenge, Purpose: "setup"}
}

func TestCapturedSetupReturnOriginRetainsFrozenAuthority(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mode := range []string{"current", "wrong proof", "wrong revision", "wrong operation", "wrong audience", "expired", "retired", "consumed", "signature changed"} {
		t.Run(mode, func(t *testing.T) {
			f := newSetupContinuationFixture(t)
			f.capture(t)
			f.install(t)
			pending, proof, operation, audience := f.pending, f.proofHash, f.intent.OperationID, f.claim.Audience
			var statement string
			switch mode {
			case "wrong proof":
				proof = identity.Token()
			case "wrong revision":
				pending.Revision++
			case "wrong operation":
				operation = identity.Token()
			case "wrong audience":
				audience = "https://other.example"
			case "expired":
				statement = `UPDATE identity_setup_completions SET created_ms=1,expires_ms=2`
			case "retired":
				statement = `UPDATE networking_claim_identity SET reset_generation=reset_generation+1`
			case "consumed":
				statement = `UPDATE identity_setup_completions SET phase='consumed',attempt_id='another-attempt'`
			case "signature changed":
				statement = `UPDATE identity_setup_completions SET start_signature=zeroblob(64)`
			}
			if statement != "" {
				if _, err := f.db.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			var origin string
			err := f.write(func(tx *sql.Tx) error {
				var err error
				origin, err = f.s.CapturedSetupReturnOriginTx(context.Background(), tx, pending, proof, operation, audience)
				return err
			})
			if mode == "current" {
				if err != nil || origin != f.claim.Address {
					t.Fatalf("frozen origin %q: %v", origin, err)
				}
			} else if err == nil || origin != "" {
				t.Fatal("retired or altered capture returned an origin")
			}
			var accounts int
			if err := f.db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&accounts); err != nil || accounts != 0 {
				t.Fatal("origin recovery admitted an account", err)
			}
		})
	}
}
func TestSetupContinuationFrozenCaptureAndCurrentAuthority(t *testing.T) {
	f := newSetupContinuationFixture(t)
	f.capture(t)
	f.capture(t)
	for _, change := range []func(*claimweb.SetupStart){func(v *claimweb.SetupStart) { v.SetupRevision++ }, func(v *claimweb.SetupStart) { v.State = identity.Token() }, func(v *claimweb.SetupStart) { v.SetupRequestID = "different" }} {
		v := f.start
		change(&v)
		if err := f.write(func(tx *sql.Tx) error {
			return f.s.CaptureSetupClaimTx(context.Background(), tx, f.pending, f.proofHash, f.original, ownerCaptureSigned(t, f.key, v), f.claim.Audience)
		}); err == nil {
			t.Fatal("replaced setup capture accepted")
		}
	}
	if err := f.write(func(tx *sql.Tx) error {
		return f.s.BindSetupClaimTransactionTx(context.Background(), tx, f.pending, f.proofHash, f.intent.OperationID, "replacement_transaction")
	}); err == nil {
		t.Fatal("transaction replaced")
	}
	if _, err := f.db.Exec(`UPDATE onboarding_state_v1 SET revision=revision+1`); err != nil {
		t.Fatal(err)
	}
	if err := f.write(func(tx *sql.Tx) error {
		return f.s.CheckSetupContinuationTx(context.Background(), tx, f.pending, f.proofHash, f.intent.OperationID, "")
	}); err == nil {
		t.Fatal("replaced setup retained authority")
	}
	var accounts int
	if err := f.db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&accounts); err != nil || accounts != 0 {
		t.Fatal("capture fabricated owner", err)
	}
}
func TestSetupContinuationInstalledExchangeCreatesOnlyMatchingOwner(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mode := range []string{"success", "wrong account", "wrong purpose", "replaced setup", "credential replaced", "registration named", "expired"} {
		t.Run(mode, func(t *testing.T) {
			f := newSetupContinuationFixture(t)
			f.capture(t)
			f.install(t)
			q := f.installed(t)
			ctx := context.Background()
			var operation, attempt string
			if err := f.write(func(tx *sql.Tx) error {
				var e error
				operation, attempt, e = f.s.BeginSetupReturnTx(ctx, tx, f.pending, f.proofHash, q)
				return e
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.write(func(tx *sql.Tx) error {
				_, _, e := f.s.BeginSetupReturnTx(ctx, tx, f.pending, f.proofHash, q)
				return e
			}); err == nil {
				t.Fatal("consumed attempt restarted")
			}
			who := identity.PorticoIdentity{AccountID: "account-approved", Username: "actual_setup_owner", DisplayName: "Actual setup owner", Consent: "verified-identity-fixture", SignerKeyID: "current-document-key", Return: &identity.PorticoReturnBinding{Purpose: "setup", InstallationID: q.InstallationID, RegistrationID: "", OperationID: operation, ClaimGeneration: 1, CredentialGeneration: 1, ExpiresAt: time.Now().Add(45 * time.Second)}}
			switch mode {
			case "wrong account":
				who.AccountID = "another-account"
			case "wrong purpose":
				who.Return.Purpose = "switch"
			case "replaced setup":
				f.db.Exec(`UPDATE onboarding_state_v1 SET revision=revision+1`)
			case "credential replaced":
				// The installed claim and credential generations still bind setup (O-163).
				f.db.Exec(`UPDATE networking_claim_intents SET credential_generation='2' WHERE operation_id=?`, operation)
			case "registration named":
				// Setup names no registration; an assertion naming one is not this setup's.
				who.Return.RegistrationID = "ret_" + strings.Repeat("r", 43)
			case "expired":
				f.db.Exec(`UPDATE identity_setup_completions SET expires_ms=created_ms+1`)
			}
			var out identity.DirectSignIn
			nonce := identity.Token()
			err := f.write(func(tx *sql.Tx) error {
				var e error
				out, e = f.s.CompleteSetupReturnTx(ctx, tx, f.pending, f.proofHash, operation, attempt, q, who, nonce)
				return e
			})
			if mode != "success" {
				if err == nil {
					t.Fatal("invalid setup admitted")
				}
				var accounts, nonces, challenge int
				if e := f.db.QueryRow(`SELECT (SELECT count(*) FROM accounts),(SELECT count(*) FROM hosted_identity_nonces),(SELECT count(*) FROM hosted_identity_challenges)`).Scan(&accounts, &nonces, &challenge); e != nil || accounts != 0 || nonces != 0 || challenge != 1 {
					t.Fatal("failed admission was not atomic", e)
				}
				return
			}
			if err != nil || out.Session == nil || out.Account.Role != identity.TierOwner || out.Account.HostedAccountID != who.AccountID || out.Account.Username != who.Username {
				t.Fatal("actual setup owner admission", err)
			}
			if _, _, err = f.s.SetupClaimAuthority(ctx, f.secret); err == nil {
				t.Fatal("completed setup retained authority")
			}
			if err = f.write(func(tx *sql.Tx) error {
				_, e := f.s.CompleteSetupReturnTx(ctx, tx, f.pending, f.proofHash, operation, attempt, q, who, nonce)
				return e
			}); err == nil {
				t.Fatal("setup completion replayed")
			}
		})
	}
}
