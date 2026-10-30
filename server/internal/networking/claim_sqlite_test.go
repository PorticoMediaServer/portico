package networking

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	_ "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

type sqlFixture struct {
	db                  *sql.DB
	path, dir           string
	store               *SQLiteStore
	cipher              *ClaimCipher
	binding             Binding
	owner               LocalOwner
	localKey, hostedKey ed25519.PrivateKey
	now                 time.Time
}

func openFixtureDB(t *testing.T, path string) *sql.DB {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	t.Helper()
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, e = db.Exec(`PRAGMA foreign_keys=ON;PRAGMA journal_mode=WAL;PRAGMA busy_timeout=1000;`); e != nil {
		t.Fatal(e)
	}
	return db
}
func fixtureSQL(t *testing.T) *sqlFixture {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "claims.sqlite")
	db := openFixtureDB(t, path)
	if e := installNetworkingClaimsForTest(t, fixtureClaimContext(t), db); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`CREATE TABLE fixture_owners(id TEXT PRIMARY KEY,profile TEXT,epoch INTEGER,allowed INTEGER);INSERT INTO fixture_owners VALUES('local_owner','local_profile',0,1);CREATE TABLE fixture_server_authority(server_id TEXT PRIMARY KEY,account_id TEXT,allowed INTEGER)`); e != nil {
		t.Fatal(e)
	}
	pub, localKey, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	serverID, e := ServerIdentity(pub)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO fixture_server_authority VALUES(?,'hosted_owner',1)`, serverID); e != nil {
		t.Fatal(e)
	}
	_, hostedKey, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	cipher, e := OpenClaimCipher(fixtureClaimContext(t), db, dir)
	if e != nil {
		t.Fatal(e)
	}
	f := &sqlFixture{db: db, path: path, dir: dir, cipher: cipher, binding: Binding{OperationID: "claim_one", ServerID: serverID, AccountID: "hosted_owner", PublicKey: pub, LocalGeneration: 0}, owner: LocalOwner{AccountID: "local_owner", ProfileID: "local_profile", Epoch: 0}, localKey: localKey, hostedKey: hostedKey, now: time.Date(2026, 9, 6, 1, 0, 0, 0, time.UTC)}
	f.store = f.storeFor(t, db, cipher)
	stageIdentityFixture(t, db, nil, DurableIdentity{ServerID: serverID, PublicKey: pub, KeyIncarnation: "key1"})
	if e = f.store.EstablishIdentity(fixtureClaimContext(t), f.binding.ServerID, pub); e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *sqlFixture) storeFor(t *testing.T, db *sql.DB, cipher *ClaimCipher) *SQLiteStore {
	t.Helper()
	guard := func(ctx context.Context, tx *sql.Tx, o LocalOwner) error {
		var profile string
		var epoch, allowed int
		e := tx.QueryRowContext(ctx, `SELECT profile,epoch,allowed FROM fixture_owners WHERE id=?`, o.AccountID).Scan(&profile, &epoch, &allowed)
		if e != nil || profile != o.ProfileID || int64(epoch) != o.Epoch || allowed != 1 {
			return ErrStale
		}
		return nil
	}
	installedGuard := func(ctx context.Context, tx *sql.Tx, v Intent) error {
		var account string
		var allowed int
		e := tx.QueryRowContext(ctx, `SELECT account_id,allowed FROM fixture_server_authority WHERE server_id=?`, v.ServerID).Scan(&account, &allowed)
		if e != nil || account != v.AccountID || allowed != 1 {
			return ErrStale
		}
		return nil
	}
	verifier := func(ctx context.Context, raw []byte) (ApprovalFacts, error) {
		var envelope struct {
			Payload   []byte
			Signature []byte
		}
		if e := ctx.Err(); e != nil {
			return ApprovalFacts{ClaimPurpose: ClaimPurpose{Purpose: "setup"}}, e
		}
		if json.Unmarshal(raw, &envelope) != nil || !ed25519.Verify(f.hostedKey.Public().(ed25519.PublicKey), envelope.Payload, envelope.Signature) {
			return ApprovalFacts{ClaimPurpose: ClaimPurpose{Purpose: "setup"}}, ErrInvalid
		}
		var facts ApprovalFacts
		if json.Unmarshal(envelope.Payload, &facts) != nil {
			return ApprovalFacts{ClaimPurpose: ClaimPurpose{Purpose: "setup"}}, ErrInvalid
		}
		return facts, nil
	}
	s, e := NewSQLiteStore(db, cipher, guard, installedGuard, verifier, "https://hosted.example")
	if e != nil {
		t.Fatal(e)
	}
	s.now = func() time.Time { return f.now }
	return s
}
func (f *sqlFixture) approval(t *testing.T, b Binding, revision int64, issued, expiry time.Time) []byte {
	t.Helper()
	raw, e := json.Marshal(ApprovalFacts{ClaimPurpose: ClaimPurpose{Purpose: "setup"}, Binding: b, Revision: revision, IssuedAt: issued, ExpiresAt: expiry})
	if e != nil {
		t.Fatal(e)
	}
	envelope, e := json.Marshal(struct {
		Payload   []byte
		Signature []byte
	}{raw, ed25519.Sign(f.hostedKey, raw)})
	if e != nil {
		t.Fatal(e)
	}
	return envelope
}
func (f *sqlFixture) retrieving(t *testing.T) Intent {
	t.Helper()
	ctx := fixtureClaimContext(t)
	v, e := f.store.Prepare(ctx, f.binding, f.owner)
	if e != nil {
		t.Fatal(e)
	}
	v, e = f.store.Approve(ctx, v, f.approval(t, f.binding, 1, f.now, f.now.Add(5*time.Minute)))
	if e != nil {
		t.Fatal(e)
	}
	v, e = f.store.BeginFinalizing(ctx, v)
	if e != nil {
		t.Fatal(e)
	}
	v, e = f.store.Committed(ctx, v, Commit{OperationID: v.OperationID, ServerID: v.ServerID, ClaimGeneration: "7", CredentialGeneration: "2"})
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func resultFor(t *testing.T, v Intent) Result {
	t.Helper()
	secret, e := NewSecret([]byte(strings.Repeat("synthetic_credential_", 3)))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(secret.Clear)
	return Result{Commit: Commit{OperationID: v.OperationID, ServerID: v.ServerID, ClaimGeneration: v.ClaimGeneration, CredentialGeneration: v.CredentialGeneration}, Credential: secret}
}
func (f *sqlFixture) cancel(t *testing.T, v Intent) Cancellation {
	t.Helper()
	c, e := NewCoordinator(v.OperationID, "https://hosted.example", f.store, &modelTransport{}, modelSigner{f.localKey})
	if e != nil {
		t.Fatal(e)
	}
	item, e := c.cancellation(fixtureClaimContext(t), v)
	if e != nil {
		t.Fatal(e)
	}
	return item
}

func TestClaimSQLiteInstallRestartAndSchema(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	if e := verifyNetworkingClaimsForTest(t, fixtureClaimContext(t), f.db); e != nil {
		t.Fatal(e)
	}
	v := f.retrieving(t)
	result := resultFor(t, v)
	installed, e := f.store.Install(fixtureClaimContext(t), v, result)
	if e != nil {
		t.Fatal(e)
	}
	db2 := openFixtureDB(t, f.path)
	key2, e := OpenClaimCipher(fixtureClaimContext(t), db2, f.dir)
	if e != nil {
		t.Fatal(e)
	}
	restarted := f.storeFor(t, db2, key2)
	loaded, e := restarted.Load(fixtureClaimContext(t), v.OperationID)
	if e != nil || loaded.Stage != Installed {
		t.Fatal("installation not durable", e)
	}
	secret, e := restarted.InstalledCredential(fixtureClaimContext(t), loaded)
	if e != nil {
		t.Fatal(e)
	}
	defer secret.Clear()
	match := false
	_ = secret.Use(func(a []byte) error {
		return result.Credential.Use(func(b []byte) error { match = bytes.Equal(a, b); return nil })
	})
	if !match {
		t.Fatal("wrong recovered credential")
	}
	ack, e := restarted.Acknowledged(fixtureClaimContext(t), installed)
	if e != nil || !ack.InstallationAcknowledged {
		t.Fatal("ack failed", e)
	}
	var ciphertext []byte
	if e = f.db.QueryRow(`SELECT ciphertext FROM networking_claim_credentials`).Scan(&ciphertext); e != nil {
		t.Fatal(e)
	}
	var nonce []byte
	if e = f.db.QueryRow(`SELECT nonce FROM networking_claim_credentials`).Scan(&nonce); e != nil {
		t.Fatal(e)
	}
	// Plain storage: the credential sits in the row with an empty nonce.
	if len(nonce) != 0 {
		t.Fatal("credential sealed")
	}
	if _, e = NewSecret(ciphertext); e != nil {
		t.Fatal("stored credential unreadable", e)
	}
}

func TestClaimSQLiteCancelWinsLateInstallAndRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	v := f.retrieving(t)
	item := f.cancel(t, v)
	cancelled, out, e := f.store.BeginCancel(fixtureClaimContext(t), v.Binding, item)
	if e != nil || cancelled.Stage != CancelPending {
		t.Fatal(e)
	}
	if _, e = f.store.Install(fixtureClaimContext(t), v, resultFor(t, v)); !errors.Is(e, ErrStale) {
		t.Fatal("late install bypassed cancellation", e)
	}
	db2 := openFixtureDB(t, f.path)
	cipher2, e := OpenClaimCipher(fixtureClaimContext(t), db2, f.dir)
	if e != nil {
		t.Fatal(e)
	}
	s2 := f.storeFor(t, db2, cipher2)
	loaded, e := s2.Load(fixtureClaimContext(t), v.OperationID)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := s2.PendingCancellation(fixtureClaimContext(t), loaded)
	if e != nil || replay.RequestID != out.RequestID || !bytes.Equal(replay.Proof.Payload, out.Proof.Payload) {
		t.Fatal("outbox not exact", e)
	}
	terminal, e := s2.CancelAcknowledged(fixtureClaimContext(t), loaded, replay.RequestID)
	if e != nil || terminal.Stage != Cancelled {
		t.Fatal(e)
	}
	old, e := s2.Prepare(fixtureClaimContext(t), f.binding, f.owner)
	if e != nil || old.Stage != Cancelled {
		t.Fatal("tombstone revived", e)
	}
}

func TestClaimSQLiteInstallThenCancelRevokesProtectedReference(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	v := f.retrieving(t)
	installed, e := f.store.Install(fixtureClaimContext(t), v, resultFor(t, v))
	if e != nil {
		t.Fatal(e)
	}
	db2 := openFixtureDB(t, f.path)
	s2 := f.storeFor(t, db2, f.cipher)
	pending, _, e := s2.BeginCancel(fixtureClaimContext(t), installed.Binding, f.cancel(t, installed))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.store.InstalledCredential(fixtureClaimContext(t), installed); !errors.Is(e, ErrStale) {
		t.Fatal("reset retained credential access", e)
	}
	var n int
	if e = f.db.QueryRow(`SELECT count(*) FROM networking_claim_credentials`).Scan(&n); e != nil || n != 0 {
		t.Fatal("cancel retained ciphertext", e)
	}
	if pending.LocalGeneration != 0 {
		t.Fatal("historical cancellation binding changed")
	}
	var generation int
	if e = f.db.QueryRow(`SELECT reset_generation FROM networking_claim_identity`).Scan(&generation); e != nil || generation != 1 {
		t.Fatal("local reset generation not fenced", e)
	}
}

func TestClaimSQLitePrecommitCancelAndSuccessorIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	v, e := f.store.Prepare(fixtureClaimContext(t), f.binding, f.owner)
	if e != nil {
		t.Fatal(e)
	}
	item := f.cancel(t, v)
	pending, _, e := f.store.BeginCancel(fixtureClaimContext(t), v.Binding, item)
	if e != nil {
		t.Fatal(e)
	}
	nextBinding := f.binding
	nextBinding.OperationID = "claim_two"
	nextBinding.LocalGeneration = 1
	if _, e = f.store.Prepare(fixtureClaimContext(t), nextBinding, f.owner); !errors.Is(e, ErrStale) {
		t.Fatal("successor admitted before cancellation ack")
	}
	if _, e = f.store.CancelAcknowledged(fixtureClaimContext(t), pending, item.RequestID); e != nil {
		t.Fatal(e)
	}
	next, e := f.store.Prepare(fixtureClaimContext(t), nextBinding, f.owner)
	if e != nil {
		t.Fatal(e)
	}
	old, _, e := f.store.BeginCancel(fixtureClaimContext(t), v.Binding, item)
	if e != nil || old.Stage != Cancelled {
		t.Fatal("old cancel replay failed")
	}
	if e = f.store.Current(fixtureClaimContext(t), next); e != nil {
		t.Fatal("old cancellation touched successor", e)
	}
}

func TestClaimSQLiteApprovalRevisionAndExpiry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	v, e := f.store.Prepare(fixtureClaimContext(t), f.binding, f.owner)
	if e != nil {
		t.Fatal(e)
	}
	proof := f.approval(t, f.binding, 1, f.now, f.now.Add(time.Minute))
	v, e = f.store.Approve(fixtureClaimContext(t), v, proof)
	if e != nil {
		t.Fatal(e)
	}
	same, e := f.store.Approve(fixtureClaimContext(t), v, proof)
	if e != nil || same.Revision != v.Revision {
		t.Fatal("exact approval was not idempotent")
	}
	if _, e = f.store.Approve(fixtureClaimContext(t), v, f.approval(t, f.binding, 1, f.now, f.now.Add(2*time.Minute))); !errors.Is(e, ErrStale) {
		t.Fatal("same revision extended approval")
	}
	f.now = f.now.Add(time.Minute)
	if _, e = f.store.BeginFinalizing(fixtureClaimContext(t), v); !errors.Is(e, ErrApprovalRequired) {
		t.Fatal("expired approval finalized", e)
	}
	loaded, e := f.store.Load(fixtureClaimContext(t), v.OperationID)
	if e != nil || loaded.Stage != Approved {
		t.Fatal("expired write did not roll back")
	}
	newProof := f.approval(t, f.binding, 2, f.now, f.now.Add(time.Minute))
	reapproved, e := f.store.Approve(fixtureClaimContext(t), loaded, newProof)
	if e != nil || reapproved.ApprovalRevision != 2 {
		t.Fatal("explicit reapproval failed", e)
	}
}

func TestClaimSQLiteGuardRollbackAndDenyOnlyCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	v := f.retrieving(t)
	if _, e := f.db.Exec(`UPDATE fixture_owners SET epoch=epoch+1`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.store.Install(fixtureClaimContext(t), v, resultFor(t, v)); !errors.Is(e, ErrStale) {
		t.Fatal("revoked owner installed", e)
	}
	var count int
	_ = f.db.QueryRow(`SELECT count(*) FROM networking_claim_credentials`).Scan(&count)
	if count != 0 {
		t.Fatal("failed guard persisted credential")
	}
	if _, _, e := f.store.BeginCancel(fixtureClaimContext(t), v.Binding, f.cancel(t, v)); e != nil {
		t.Fatal("owner revocation prevented deny-only cancellation", e)
	}
}

func TestClaimSQLiteOutboxFailureRollsBackFence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	v := f.retrieving(t)
	if _, e := f.db.Exec(`CREATE TRIGGER fixture_reject_cancel BEFORE INSERT ON networking_claim_cancellations BEGIN SELECT RAISE(ABORT,'fixture cancellation failure');END`); e != nil {
		t.Fatal(e)
	}
	if _, _, e := f.store.BeginCancel(fixtureClaimContext(t), v.Binding, f.cancel(t, v)); e == nil {
		t.Fatal("failed outbox committed")
	}
	loaded, e := f.store.Load(fixtureClaimContext(t), v.OperationID)
	if e != nil || loaded.Stage != Retrieving || loaded.Revision != v.Revision {
		t.Fatal("partial cancellation published")
	}
	var generation int
	_ = f.db.QueryRow(`SELECT reset_generation FROM networking_claim_identity`).Scan(&generation)
	if generation != 0 {
		t.Fatal("failed outbox advanced generation")
	}
}

func TestClaimSQLitePlaintextCredentialAndNoKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	v := f.retrieving(t)
	installed, e := f.store.Install(fixtureClaimContext(t), v, resultFor(t, v))
	if e != nil {
		t.Fatal(e)
	}
	var nonce, stored []byte
	if e = f.db.QueryRow(`SELECT nonce,ciphertext FROM networking_claim_credentials`).Scan(&nonce, &stored); e != nil {
		t.Fatal(e)
	}
	// Plain storage: an empty nonce and the secret as-is, no key file.
	if len(nonce) != 0 {
		t.Fatal("credential sealed")
	}
	if _, e = NewSecret(stored); e != nil {
		t.Fatal("stored credential unreadable", e)
	}
	if _, e = f.db.Exec(`UPDATE networking_claim_credentials SET ciphertext=zeroblob(length(ciphertext))`); e != nil {
		t.Fatal(e)
	}
	if _, e = f.store.InstalledCredential(fixtureClaimContext(t), installed); !errors.Is(e, ErrUnavailable) {
		t.Fatal("corrupt credential accepted", e)
	}
	if _, e = os.Stat(storagepaths.Path(f.dir, "networking-claim-credentials.key")); !os.IsNotExist(e) {
		t.Fatal("key file present")
	}
	if _, e = OpenClaimCipher(fixtureClaimContext(t), f.db, f.dir); e != nil {
		t.Fatal("opening without a key failed", e)
	}
}

func TestClaimSQLitePlaintextRoundTripAndTamper(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	v := f.retrieving(t)
	result := resultFor(t, v)
	nonce, ciphertext, e := f.cipher.seal(v, result.Credential)
	if e != nil || len(nonce) != 0 {
		t.Fatal("seal did not store plainly", e)
	}
	opened, e := f.cipher.open(v, nonce, ciphertext)
	if e != nil {
		t.Fatal(e)
	}
	defer opened.Clear()
	match := false
	if e = result.Credential.Use(func(a []byte) error {
		return opened.Use(func(b []byte) error { match = bytes.Equal(a, b); return nil })
	}); e != nil || !match {
		t.Fatal("plaintext round trip changed the credential", e)
	}
	tampered := append([]byte(nil), ciphertext...)
	tampered[0] = 0
	if _, e = f.cipher.open(v, nonce, tampered); !errors.Is(e, ErrUnavailable) {
		t.Fatal("tampered credential accepted", e)
	}
}

func TestClaimSQLiteCancellationWhileConnectionBlocked(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	v := f.retrieving(t)
	tx, e := f.db.BeginTx(fixtureClaimContext(t), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(fixtureClaimContext(t), 30*time.Millisecond)
	defer cancel()
	if _, e = f.store.BeginFinalizing(ctx, v); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("blocked operation ignored deadline", e)
	}
}

func TestClaimSQLiteGenerationOverflowAndNoKeyAdoption(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	if e := f.store.EstablishIdentity(fixtureClaimContext(t), f.binding.ServerID, make([]byte, 32)); !errors.Is(e, ErrStale) {
		t.Fatal("mismatched identity adopted")
	}
	v, e := f.store.Prepare(fixtureClaimContext(t), f.binding, f.owner)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec(`UPDATE networking_claim_identity SET reset_generation=9223372036854775807;UPDATE networking_claim_intents SET local_generation=9223372036854775807`); e != nil {
		t.Fatal(e)
	}
	v.LocalGeneration = 9223372036854775807
	item := f.cancel(t, v)
	if _, _, e = f.store.BeginCancel(fixtureClaimContext(t), v.Binding, item); !errors.Is(e, ErrStale) {
		t.Fatal("generation overflow accepted", e)
	}
}

func TestClaimSQLiteInstalledAuthorityIndependentOfOwnerEpoch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	v := f.retrieving(t)
	installed, e := f.store.Install(fixtureClaimContext(t), v, resultFor(t, v))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec(`UPDATE fixture_owners SET epoch=epoch+1`); e != nil {
		t.Fatal(e)
	}
	f.now = f.now.Add(10 * time.Minute) // Original approval also expires after installation.
	if e = f.store.Current(fixtureClaimContext(t), installed); e != nil {
		t.Fatal("installed authority depended on owner epoch", e)
	}
	secret, e := f.store.InstalledCredential(fixtureClaimContext(t), installed)
	if e != nil {
		t.Fatal(e)
	}
	secret.Clear()
	installed, e = f.store.Acknowledged(fixtureClaimContext(t), installed)
	if e != nil {
		t.Fatal("ack depended on original consent expiry", e)
	}
	if _, e = f.db.Exec(`UPDATE fixture_server_authority SET allowed=0`); e != nil {
		t.Fatal(e)
	}
	if e = f.store.Current(fixtureClaimContext(t), installed); !errors.Is(e, ErrStale) {
		t.Fatal("known server revocation allowed current", e)
	}
	if _, e = f.store.InstalledCredential(fixtureClaimContext(t), installed); !errors.Is(e, ErrStale) {
		t.Fatal("known server revocation exposed credential", e)
	}
	if _, e = f.store.Acknowledged(fixtureClaimContext(t), installed); !errors.Is(e, ErrStale) {
		t.Fatal("known server revocation acknowledged", e)
	}
	if _, _, e = f.store.BeginCancel(fixtureClaimContext(t), installed.Binding, f.cancel(t, installed)); e != nil {
		t.Fatal("revocation blocked deny-only cleanup", e)
	}
}

func TestClaimSQLiteConcurrentInstallThenCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	v := f.retrieving(t)
	result := resultFor(t, v)
	assertion := f.cancel(t, v)
	db2 := openFixtureDB(t, f.path)
	other := f.storeFor(t, db2, f.cipher)
	entered, release := make(chan struct{}), make(chan struct{})
	guard := f.store.ownerGuard
	f.store.ownerGuard = func(ctx context.Context, tx *sql.Tx, owner LocalOwner) error {
		if e := guard(ctx, tx, owner); e != nil {
			return e
		}
		close(entered) // Install has acquired the SQLite writer reservation.
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// Concurrent requests acquire separate leases from the same process gate.
	// Sharing one lease spuriously fences the second request when SQLite releases
	// its writer before the first lease's Commit callback has returned.
	_, runner := lifecycleRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	installed := make(chan error, 1)
	go func() {
		installed <- runner.Do(ctx, func(scoped context.Context) error {
			_, e := f.store.Install(scoped, v, result)
			return e
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("Install never acquired writer")
	}
	started := make(chan struct{})
	cancelled := make(chan error, 1)
	go func() {
		cancelled <- runner.Do(ctx, func(scoped context.Context) error {
			close(started)
			_, _, e := other.BeginCancel(scoped, v.Binding, assertion)
			return e
		})
	}()
	<-started
	close(release)
	select {
	case e := <-installed:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("Install did not finish")
	}
	select {
	case e := <-cancelled:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("Cancel did not finish")
	}
	loaded, e := other.Load(fixtureClaimContext(t), v.OperationID)
	if e != nil || loaded.Stage != CancelPending {
		t.Fatal("concurrent cancellation did not fence installation", e)
	}
	var count, generation int
	if e = db2.QueryRow(`SELECT count(*) FROM networking_claim_credentials`).Scan(&count); e != nil {
		t.Fatal(e)
	}
	if e = db2.QueryRow(`SELECT reset_generation FROM networking_claim_identity`).Scan(&generation); e != nil {
		t.Fatal(e)
	}
	if count != 0 || generation != 1 {
		t.Fatal("concurrent cancellation left credential or reset mismatch")
	}
}

func TestClaimSQLiteCancellationProofBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	v := f.retrieving(t)
	item := f.cancel(t, v)
	item.ClaimGeneration = "999"
	if _, _, e := f.store.BeginCancel(fixtureClaimContext(t), v.Binding, item); e == nil {
		t.Fatal("altered assertion projection accepted")
	}
	wrong := v
	wrong.ClaimGeneration = "999"
	signedWrong := f.cancel(t, wrong)
	if _, _, e := f.store.BeginCancel(fixtureClaimContext(t), v.Binding, signedWrong); !errors.Is(e, ErrStale) {
		t.Fatal("different committed generation accepted", e)
	}
	item = f.cancel(t, v)
	item.Proof.Signature[0] ^= 1
	if _, _, e := f.store.BeginCancel(fixtureClaimContext(t), v.Binding, item); e == nil {
		t.Fatal("invalid cancellation signature accepted")
	}
	loaded, e := f.store.Load(fixtureClaimContext(t), v.OperationID)
	if e != nil || loaded.Stage != Retrieving || loaded.Revision != v.Revision {
		t.Fatal("failed proof mutated intent", e)
	}
}

// Rows sealed under the old file key migrate to plaintext on open, and the
// key file goes away. Installed credentials are durable: without this pass
// every remote claim would need reinstalling at upgrade.
func TestClaimCredentialsMigrateToPlain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	v := f.retrieving(t)
	installed, e := f.store.Install(fixtureClaimContext(t), v, resultFor(t, v))
	if e != nil {
		t.Fatal(e)
	}
	var plain []byte
	if e = f.db.QueryRow(`SELECT ciphertext FROM networking_claim_credentials`).Scan(&plain); e != nil {
		t.Fatal(e)
	}
	key := make([]byte, 32)
	if _, e = rand.Read(key); e != nil {
		t.Fatal(e)
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		t.Fatal(e)
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		t.Fatal(e)
	}
	additional, e := aad(installed)
	if e != nil {
		t.Fatal(e)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		t.Fatal(e)
	}
	sealed := aead.Seal(nil, nonce, plain, additional)
	if _, e = f.db.Exec(`UPDATE networking_claim_credentials SET nonce=?,ciphertext=?`, nonce, sealed); e != nil {
		t.Fatal(e)
	}
	if e = writePathFixture(storagepaths.Path(f.dir, "networking-claim-credentials.key"), key, 0600); e != nil {
		t.Fatal(e)
	}
	if e = migrateClaimCredentials(fixtureClaimContext(t), f.db, f.dir); e != nil {
		t.Fatal("migration failed", e)
	}
	var after, nonceAfter []byte
	if e = f.db.QueryRow(`SELECT nonce,ciphertext FROM networking_claim_credentials`).Scan(&nonceAfter, &after); e != nil {
		t.Fatal(e)
	}
	if len(nonceAfter) != 0 || !bytes.Equal(after, plain) {
		t.Fatal("sealed credential not migrated to plaintext")
	}
	if _, e = os.Stat(storagepaths.Path(f.dir, "networking-claim-credentials.key")); !os.IsNotExist(e) {
		t.Fatal("key file left behind")
	}
	if _, e = f.store.InstalledCredential(fixtureClaimContext(t), installed); e != nil {
		t.Fatal("migrated credential unreadable", e)
	}
}

// installNetworkingClaimsForTest builds a bare fixture from the current baseline
// and checks the claim schema with the same verifier called during startup.
func installNetworkingClaimsForTest(t *testing.T, ctx context.Context, db *sql.DB) error {
	t.Helper()
	var existing int
	if e := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&existing); e != nil {
		return e
	}
	if existing == 0 {
		// Build the same fresh canonical schema atomically. Executing thousands
		// of DDL statements as individual autocommits made these on-disk test
		// fixtures spend their timeout in fsync before reaching an assertion.
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if _, e = tx.ExecContext(ctx, persistence.BaselineSQL()); e != nil {
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	return persistence.VerifyNetworkingClaims(ctx, db)
}

// installNetworkingMigration verifies that the current baseline already carries
// the networking tables that older fixture setup applied as separate migrations.
func installNetworkingMigration(t *testing.T, db *sql.DB) {
	t.Helper()
	if e := installNetworkingClaimsForTest(t, context.Background(), db); e != nil {
		t.Fatal(e)
	}
}

// verifyNetworkingClaimsForTest checks a baseline fixture with the same verifier
// called during startup.
func verifyNetworkingClaimsForTest(t *testing.T, ctx context.Context, db *sql.DB) error {
	t.Helper()
	return persistence.VerifyNetworkingClaims(ctx, db)
}

func TestClaimInstallationReadinessAndWakeCommitTogether(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	v := f.retrieving(t)
	failure := errors.New("admission unavailable")
	wakes := 0
	if err := f.store.SetInstallationHooks(func(ctx context.Context, tx *sql.Tx, intent Intent) error {
		if intent.Stage != Installed {
			t.Fatal("readiness before installation")
		}
		_, err := tx.ExecContext(ctx, `UPDATE fixture_owners SET allowed=2`)
		if err != nil {
			return err
		}
		return failure
	}, func() { wakes++ }); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Install(fixtureClaimContext(t), v, resultFor(t, v)); !errors.Is(err, failure) {
		t.Fatal("readiness failure was ignored", err)
	}
	var allowed, credentials int
	if err := f.db.QueryRow(`SELECT allowed,(SELECT count(*) FROM networking_claim_credentials) FROM fixture_owners`).Scan(&allowed, &credentials); err != nil {
		t.Fatal(err)
	}
	if allowed != 1 || credentials != 0 || wakes != 0 {
		t.Fatal("failed readiness partially committed", allowed, credentials, wakes)
	}
	f.store.installed = func(ctx context.Context, tx *sql.Tx, _ Intent) error {
		_, err := tx.ExecContext(ctx, `UPDATE fixture_owners SET allowed=2`)
		return err
	}
	f.store.claimChanged = func() {
		var stage Stage
		if err := f.db.QueryRow(`SELECT stage FROM networking_claim_intents`).Scan(&stage); err != nil || stage != Installed {
			t.Fatal("wake before commit", stage, err)
		}
		wakes++
	}
	installed, err := f.store.Install(fixtureClaimContext(t), v, resultFor(t, v))
	if err != nil {
		t.Fatal(err)
	}
	acknowledged, err := f.store.Acknowledged(fixtureClaimContext(t), installed)
	if err != nil || wakes != 2 {
		t.Fatal("install and acknowledgment did not wake control", wakes, err)
	}
	if _, err = f.store.Acknowledged(fixtureClaimContext(t), acknowledged); err != nil || wakes != 2 {
		t.Fatal("idempotent acknowledgment repeated wake", wakes, err)
	}
}
