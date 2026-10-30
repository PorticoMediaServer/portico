package networking

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/dirsync"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

// ProtectedKeys is the current immutable key registry. It never imports a key
// from configuration, replaces a missing key, or deletes predecessor keys.
type ProtectedKeys struct {
	db   *sql.DB
	root *os.Root
}

func (k *ProtectedKeys) Close() error { return k.root.Close() }
func syncDirectory(path string) error { return dirsync.Sync(path) }

func privateDirectory(path string) error {
	if e := os.MkdirAll(path, 0700); e != nil {
		return e
	}
	info, e := os.Lstat(path)
	if e != nil || !info.IsDir() {
		return ErrUnavailable
	}
	return nil
}
func (k *ProtectedKeys) read(incarnation string) (ed25519.PrivateKey, error) {
	if !validID(incarnation) {
		return nil, ErrInvalid
	}
	// Every refusal below is ErrUnavailable to callers. The stage and the
	// system error travel beside it so a handler can log which one it was; no
	// path, name or content does.
	name := incarnation + ".ed25519"
	info, e := k.root.Lstat(name)
	switch {
	case e != nil:
		return nil, keyFileFailure("lstat", e)
	case !info.Mode().IsRegular():
		return nil, keyFileFailure("type", nil)
	case info.Size() != ed25519.PrivateKeySize:
		return nil, keyFileFailure("size", nil)
	}
	f, e := k.root.Open(name)
	if e != nil {
		return nil, keyFileFailure("open", e)
	}
	defer f.Close()
	actual, e := f.Stat()
	if e != nil {
		return nil, keyFileFailure("stat", e)
	}
	if !os.SameFile(info, actual) {
		return nil, keyFileFailure("changed", nil)
	}
	raw, e := io.ReadAll(io.LimitReader(f, 65))
	if e != nil || len(raw) != 64 {
		clear(raw)
		return nil, keyFileFailure("read", e)
	}
	key := ed25519.PrivateKey(raw)
	derived := ed25519.NewKeyFromSeed(key.Seed())
	defer clear(derived)
	if !bytes.Equal(derived, key) {
		clear(raw)
		return nil, keyFileFailure("content", nil)
	}
	return key, nil
}

// holdsIdentityTx reports whether the registry holds the signing key of the
// server identity the database records for applicationID.
func (k *ProtectedKeys) holdsIdentityTx(ctx context.Context, tx *sql.Tx, applicationID string) error {
	snapshot, e := CurrentIdentityTx(ctx, tx)
	if e != nil || snapshot.ServerID != applicationID {
		return ErrStale
	}
	key, e := k.read(snapshot.KeyIncarnation)
	if e != nil {
		return e
	}
	defer clear(key)
	if !bytes.Equal(key.Public().(ed25519.PublicKey), snapshot.PublicKey) {
		return ErrStale
	}
	return nil
}

// unmarkedStateTx refuses a database that records no application identity but
// already holds accounts, identities or legacy key material.
func unmarkedStateTx(ctx context.Context, tx *sql.Tx) error {
	var count int
	if e := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM accounts)+(SELECT count(*) FROM networking_server_identities)+(SELECT count(*) FROM configuration WHERE key IN('hosted_token','server_private_key'))`).Scan(&count); e != nil || count != 0 {
		return ErrStale
	}
	return nil
}

// VerifyStateKeys answers, without changing anything, whether OpenCurrentKeys
// would accept this database beside this state folder's key registry: the
// identity the database records must have its signing key there. It never
// creates the registry or a key, so it is safe to ask of a restored database
// before deciding to keep it.
func VerifyStateKeys(ctx context.Context, db *sql.DB, state string) error {
	if db == nil {
		return ErrInvalid
	}
	directory := storagepaths.Path(state, "networking-keys")
	gated, e := dbwork.BeginSnapshot(ctx, db)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var applicationID string
	e = tx.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key='id'`).Scan(&applicationID)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if applicationID == "" {
		if e = unmarkedStateTx(ctx, tx); e != nil {
			return e
		}
		entries, e := os.ReadDir(directory)
		if e != nil && !errors.Is(e, os.ErrNotExist) || len(entries) != 0 {
			return ErrUnavailable
		}
		return nil
	}
	root, e := os.OpenRoot(directory)
	if e != nil {
		return ErrUnavailable
	}
	defer root.Close()
	return (&ProtectedKeys{db, root}).holdsIdentityTx(ctx, tx, applicationID)
}

// OpenCurrentKeys initializes one canonical application/server identity before
// identity.New or Setup. Existing unmarked state is refused, never adopted.
// Startup-only: main opens these keys before identity and backup services.
func OpenCurrentKeys(ctx context.Context, db *sql.DB, state string, runner *AuthorityRunner) (*ProtectedKeys, error) {
	return openCurrentKeys(ctx, db, state, runner, freshKeySteps{syncDirectory, (*sql.Tx).Commit})
}

// freshKeySteps are the durability operations of fresh key creation; tests
// substitute them to exercise each failure point.
type freshKeySteps struct {
	syncDir func(string) error
	commit  func(*sql.Tx) error
}

func openCurrentKeys(ctx context.Context, db *sql.DB, state string, runner *AuthorityRunner, steps freshKeySteps) (*ProtectedKeys, error) {
	if db == nil || runner == nil {
		return nil, ErrInvalid
	}
	directory := storagepaths.Path(state, "networking-keys")
	if e := privateDirectory(directory); e != nil {
		return nil, e
	}
	root, e := os.OpenRoot(directory)
	if e != nil {
		return nil, e
	}
	keys := &ProtectedKeys{db, root}
	e = runner.Do(ctx, func(ctx context.Context) (result error) {
		gated, e := dbwork.Begin(ctx, db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
		if e != nil {
			return e
		}
		tx := gated.Tx()
		defer gated.Rollback()
		var applicationID string
		e = tx.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key='id'`).Scan(&applicationID)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if applicationID != "" {
			return keys.holdsIdentityTx(ctx, tx, applicationID)
		}
		if e = unmarkedStateTx(ctx, tx); e != nil {
			return e
		}
		entries, e := os.ReadDir(directory)
		if e != nil || len(entries) != 0 {
			return ErrUnavailable
		}
		public, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return e
		}
		defer clear(key)
		random := make([]byte, 32)
		if _, e = rand.Read(random); e != nil {
			return e
		}
		incarnation := hex.EncodeToString(random)
		f, e := root.OpenFile(incarnation+".ed25519", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		commitStarted := false
		defer func() {
			if result == nil {
				return
			}
			// Roll back before removing the file created by this attempt. Once the
			// commit has been attempted its outcome is ambiguous: SQLite may
			// recover a committed identity after a failed sync, so its key is
			// kept and the next start reports the unmarked directory instead.
			_ = gated.Rollback()
			if commitStarted {
				return
			}
			if err := root.Remove(incarnation + ".ed25519"); err != nil {
				result = errors.Join(result, err)
				return
			}
			result = errors.Join(result, steps.syncDir(directory))
		}()
		_, e = f.Write(key)
		if e == nil {
			e = f.Sync()
		}
		closed := f.Close()
		if e != nil {
			return e
		}
		if closed != nil {
			return closed
		}
		if e = steps.syncDir(directory); e != nil {
			return e
		}
		if e = steps.syncDir(state); e != nil {
			return e
		}
		reopened, e := keys.read(incarnation)
		if e != nil {
			return e
		}
		defer clear(reopened)
		if !bytes.Equal(reopened, key) {
			return ErrUnavailable
		}
		server, e := ServerIdentity(public)
		if e != nil {
			return e
		}
		if _, e = StageIdentityTx(ctx, tx, nil, DurableIdentity{server, public, incarnation}); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO configuration(key,value) VALUES('id',?)`, server); e != nil {
			return e
		}
		return commitClaimTx(ctx, func() error {
			commitStarted = true
			return steps.commit(tx)
		})
	})
	if e != nil {
		root.Close()
		return nil, e
	}
	return keys, nil
}
func (k *ProtectedKeys) Sign(ctx context.Context, b Binding, payload []byte) ([]byte, error) {
	if _, e := claimAuthority(ctx); e != nil {
		return nil, e
	}
	if !validBinding(b) || len(payload) < 1 || len(payload) > 16<<10 {
		return nil, ErrInvalid
	}
	var kind struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(payload, &kind) != nil {
		return nil, ErrInvalid
	}
	switch kind.Kind {
	case "portico.claim.nonce-request", "portico.claim.proof", "portico.claim.cancel":
	case "portico.claim.web.v2":
		var q struct {
			OperationID     string `json:"operationId"`
			ServerID        string `json:"serverId"`
			AccountID       string `json:"accountId"`
			PublicKey       string `json:"publicKey"`
			LocalGeneration int64  `json:"localGeneration,string"`
		}
		// A web claim prepared without an account carries accountId "" (the
		// approver becomes the owner); its binding holds UnboundAccount.
		account := b.AccountID
		if account == UnboundAccount {
			account = ""
		}
		if json.Unmarshal(payload, &q) != nil || q.OperationID != b.OperationID || q.ServerID != b.ServerID || q.AccountID != account || q.PublicKey != base64.RawURLEncoding.EncodeToString(b.PublicKey) || q.LocalGeneration != b.LocalGeneration {
			return nil, ErrInvalid
		}
	case "portico.server.leave":
		if !validDepartureSigningPayload(payload, b) {
			return nil, ErrInvalid
		}
	case "portico.server.endpoint.v1":
		if !validateEndpointSigningPayload(payload, b) {
			return nil, ErrInvalid
		}
	default:
		return nil, ErrInvalid
	}
	gated2, e := dbwork.BeginSnapshot(ctx, k.db)
	if e != nil {
		return nil, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if e = guardClaimRequestTx(ctx, tx); e != nil {
		return nil, e
	}
	id, active, installed, e := readCurrentIdentityTx(ctx, tx)
	if e != nil {
		return nil, e
	}
	var pending sql.NullString
	if e = tx.QueryRowContext(ctx, `SELECT pending_operation_id FROM networking_claim_identity WHERE singleton=1`).Scan(&pending); e != nil {
		return nil, e
	}
	if id.ServerID != b.ServerID || !bytes.Equal(id.PublicKey, b.PublicKey) || id.ResetGeneration != b.LocalGeneration || ((!active.Valid || active.String != b.OperationID) && (!pending.Valid || pending.String != b.OperationID) && (kind.Kind != "portico.server.leave" || !installed.Valid || installed.String != b.OperationID)) {
		return nil, ErrStale
	}
	if kind.Kind == "portico.server.leave" {
		var wire struct {
			ClaimGeneration      string `json:"claimGeneration"`
			CredentialGeneration string `json:"credentialGeneration"`
		}
		if json.Unmarshal(payload, &wire) != nil {
			return nil, ErrInvalid
		}
		current, e := loadIntentTx(ctx, tx, b.OperationID)
		if e != nil {
			return nil, e
		}
		if current.ClaimGeneration != wire.ClaimGeneration || current.CredentialGeneration != wire.CredentialGeneration {
			return nil, ErrStale
		}
	}
	if kind.Kind == "portico.claim.web.v2" {
		var purpose ClaimPurpose
		if json.Unmarshal(payload, &purpose) != nil || !purpose.Valid() {
			return nil, ErrInvalid
		}
		current, e := loadIntentTx(ctx, tx, b.OperationID)
		if e != nil {
			return nil, e
		}
		if current.ClaimPurpose != purpose {
			return nil, ErrStale
		}
	}
	if kind.Kind == "portico.server.endpoint.v1" {
		var proof endpointChallenge
		if json.Unmarshal(payload, &proof) != nil {
			return nil, ErrInvalid
		}
		current, e := loadIntentTx(ctx, tx, b.OperationID)
		if e != nil {
			return nil, e
		}
		if !current.InstallationAcknowledged || proof.ClaimGeneration != current.ClaimGeneration || proof.CredentialGeneration != current.CredentialGeneration {
			return nil, ErrStale
		}
		if e = GuardInstalledClaim(ctx, tx, current.Intent); e != nil {
			return nil, e
		}
	}
	key, e := k.read(id.KeyIncarnation)
	if e != nil {
		return nil, e
	}
	defer clear(key)
	if !bytes.Equal(key.Public().(ed25519.PublicKey), b.PublicKey) {
		return nil, ErrStale
	}
	if _, e = claimAuthority(ctx); e != nil {
		return nil, e
	}
	if e = guardClaimRequestTx(ctx, tx); e != nil {
		return nil, e
	}
	return ed25519.Sign(key, payload), nil
}
