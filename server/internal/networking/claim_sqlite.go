package networking

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"
)

var ErrOwnerCollision = errors.New("owner change target already exists locally")
var ErrClaimMoveRequired = errors.New("Local sign-in requires a confirmed move to Portico Account")

type LocalOwner struct {
	SetupOperationID string
	AccountID        string
	ProfileID        string
	Epoch            int64
}
type OwnerIntentGuard func(context.Context, *sql.Tx, LocalOwner) error

// InstalledAuthorityGuard checks current cached server authority under this
// transaction, including known credential revocation/account deletion/ownership
// changes. It must not depend on the consenting local owner's password epoch or
// login session and must not perform network I/O. Hosted independently validates
// the server credential on every outbound use; this is not proof of live reachability.
type InstalledAuthorityGuard func(context.Context, *sql.Tx, Intent) error

// ApprovalVerifier must authenticate the exact Hosted-signed bytes against
// configured trust and return only this immutable projection. It performs no
// network I/O. Caller JSON is never accepted as verified approval facts.
type ApprovalFacts struct {
	TargetUsername string
	ClaimPurpose
	Binding
	// TransitionMode is the signed closed mode from a v3 approval. Empty on a v2
	// approval, which is ordinary switch by definition.
	TransitionMode string
	Revision       int64
	IssuedAt       time.Time
	ExpiresAt      time.Time
}
type ApprovalVerifier func(context.Context, []byte) (ApprovalFacts, error)
type SQLiteStore struct {
	db             *sql.DB
	cipher         *ClaimCipher
	ownerGuard     OwnerIntentGuard
	installedGuard InstalledAuthorityGuard
	denied         TerminalClaimRevoker
	installed      func(context.Context, *sql.Tx, Intent) error
	claimChanged   func()
	verifyApproval ApprovalVerifier
	audience       string
	now            func() time.Time
}

func NewSQLiteStore(db *sql.DB, cipher *ClaimCipher, owner OwnerIntentGuard, installed InstalledAuthorityGuard, verifier ApprovalVerifier, audience string) (*SQLiteStore, error) {
	if db == nil || cipher == nil || owner == nil || installed == nil || verifier == nil || !validAudience(audience) {
		return nil, ErrInvalid
	}
	return &SQLiteStore{db: db, cipher: cipher, ownerGuard: owner, installedGuard: installed, verifyApproval: verifier, audience: audience, now: time.Now}, nil
}

type identityRow struct {
	server                     string
	key                        []byte
	generation                 int64
	active, installed, pending sql.NullString
}
type storedIntent struct {
	Intent
	owner          LocalOwner
	approvalIssued time.Time
	ownerReserved  bool
}

const intentColumns = `operation_id,server_id,account_id,public_key,local_generation,revision,stage,owner_id,owner_profile_id,owner_epoch,approval_revision,approval_issued_at,approval_expires_at,claim_generation,credential_generation,installation_acknowledged,setup_operation_id,purpose,current_owner_account_id,previous_owner_role,return_registration_id,approved_username,owner_change_reserved`

type rowScanner interface{ Scan(...any) error }

func scanIntent(row rowScanner) (storedIntent, error) {
	var v storedIntent
	var issued, expires string
	e := row.Scan(&v.OperationID, &v.ServerID, &v.AccountID, &v.PublicKey, &v.LocalGeneration, &v.Revision, &v.Stage, &v.owner.AccountID, &v.owner.ProfileID, &v.owner.Epoch, &v.ApprovalRevision, &issued, &expires, &v.ClaimGeneration, &v.CredentialGeneration, &v.InstallationAcknowledged, &v.owner.SetupOperationID, &v.Purpose, &v.CurrentOwnerAccountID, &v.PreviousOwnerRole, &v.ReturnRegistrationID, &v.ReservationUsername, &v.ownerReserved)
	if errors.Is(e, sql.ErrNoRows) {
		return v, ErrStale
	}
	if e != nil {
		return v, e
	}
	if issued != "" {
		v.approvalIssued, e = time.Parse(time.RFC3339Nano, issued)
		if e != nil {
			return v, ErrInvalid
		}
	}
	if expires != "" {
		v.ApprovalExpiresAt, e = time.Parse(time.RFC3339Nano, expires)
		if e != nil {
			return v, ErrInvalid
		}
	}
	if !validOwner(v.owner) {
		return v, ErrInvalid
	}
	return v, validateIntent(v.Intent, v.OperationID)
}
func validOwner(v LocalOwner) bool {
	if v.SetupOperationID != "" {
		return validID(v.SetupOperationID) && v.AccountID == "" && v.ProfileID == "" && v.Epoch > 0
	}
	return validID(v.AccountID) && validID(v.ProfileID) && v.Epoch >= 0
}
func (s *SQLiteStore) tx(ctx context.Context) (*dbwork.Write, error) {
	if _, e := claimAuthority(ctx); e != nil {
		return nil, e
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if e != nil {
		return nil, e
	}
	tx := gated.Tx()
	// Acquire SQLite's writer reservation BEFORE reading mutable intent state.
	if _, e = tx.ExecContext(ctx, `UPDATE networking_claim_identity SET reset_generation=reset_generation WHERE singleton=1`); e != nil {
		gated.Rollback()
		return nil, e
	}
	if e = guardClaimRequestTx(ctx, tx); e != nil {
		gated.Rollback()
		return nil, e
	}
	return gated, nil
}
func readIdentity(ctx context.Context, tx *sql.Tx) (identityRow, error) {
	var v identityRow
	e := tx.QueryRowContext(ctx, `SELECT server_id,public_key,reset_generation,active_operation_id,installed_operation_id,pending_operation_id FROM networking_claim_identity WHERE singleton=1`).Scan(&v.server, &v.key, &v.generation, &v.active, &v.installed, &v.pending)
	if errors.Is(e, sql.ErrNoRows) {
		return v, ErrStale
	}
	if e != nil {
		return v, e
	}
	if !validID(v.server) || len(v.key) != ed25519.PublicKeySize || v.generation < 0 {
		return v, ErrInvalid
	}
	return v, nil
}
func loadIntentTx(ctx context.Context, tx *sql.Tx, id string) (storedIntent, error) {
	return scanIntent(tx.QueryRowContext(ctx, `SELECT `+intentColumns+` FROM networking_claim_intents WHERE operation_id=?`, id))
}
func (s *SQLiteStore) Load(ctx context.Context, id string) (Intent, error) {
	if !validID(id) {
		return Intent{}, ErrInvalid
	}
	v, e := scanIntent(s.db.QueryRowContext(ctx, `SELECT `+intentColumns+` FROM networking_claim_intents WHERE operation_id=?`, id))
	return v.Intent, e
}
func (s *SQLiteStore) finish(ctx context.Context, gatedArg *dbwork.Write, v Intent, checkApproval bool) (Intent, error) {
	tx := gatedArg.Tx()
	if e := ctx.Err(); e != nil {
		return Intent{}, e
	}
	if checkApproval && !s.now().Before(v.ApprovalExpiresAt) {
		return Intent{}, ErrApprovalRequired
	}
	if e := commitClaimTx(ctx, func() error {
		if e := guardClaimRequestTx(ctx, tx); e != nil {
			return e
		}
		if checkApproval && !s.now().Before(v.ApprovalExpiresAt) {
			return ErrApprovalRequired
		}
		return gatedArg.Commit()
	}); e != nil {
		return Intent{}, e
	}
	return v, nil
}
func (s *SQLiteStore) positive(ctx context.Context, tx *sql.Tx, expected Intent) (storedIntent, error) {
	id, e := readIdentity(ctx, tx)
	if e != nil {
		return storedIntent{}, e
	}
	v, e := loadIntentTx(ctx, tx, expected.OperationID)
	if e != nil {
		return v, e
	}
	if expected.ClaimPurpose != v.ClaimPurpose || !sameBinding(expected.Binding, v.Binding) || expected.Revision != v.Revision || expected.Stage != v.Stage || expected.ApprovalRevision != v.ApprovalRevision || expected.ReservationUsername != v.ReservationUsername || !expected.ApprovalExpiresAt.Equal(v.ApprovalExpiresAt) || expected.ClaimGeneration != v.ClaimGeneration || expected.CredentialGeneration != v.CredentialGeneration || v.Stage == CancelPending || v.Stage == Cancelled || id.server != v.ServerID || !bytes.Equal(id.key, v.PublicKey) || id.generation != v.LocalGeneration || ((!id.active.Valid || id.active.String != v.OperationID) && (!id.pending.Valid || id.pending.String != v.OperationID)) {
		return v, ErrStale
	}
	if id.pending.Valid && id.pending.String == v.OperationID && (v.Stage == Prepared || v.Stage == Approved) && !(v.Purpose == "switch" && v.CurrentOwnerAccountID == "") {
		if !id.installed.Valid || !id.active.Valid || id.installed.String != id.active.String {
			return v, ErrStale
		}
		old, e := loadIntentTx(ctx, tx, id.installed.String)
		if e != nil {
			return v, e
		}
		if old.AccountID != v.CurrentOwnerAccountID {
			return v, ErrStale
		}
		if e = s.installedGuard(ctx, tx, old.Intent); e != nil {
			return v, e
		}
	}
	if (v.Purpose == "change-owner" || v.Purpose == "switch") && v.Stage != Prepared && v.Stage != Installed && (!v.ownerReserved || !claimUsername.MatchString(v.ReservationUsername)) {
		return v, ErrStale
	}
	if v.Stage == Installed {
		if !id.installed.Valid || id.installed.String != v.OperationID {
			return v, ErrStale
		}
		e = s.installedGuard(ctx, tx, v.Intent)
	} else {
		// Owner consent is required through Install, not as a lifetime login
		// lease on an already installed server credential.
		e = s.ownerGuard(ctx, tx, v.owner)
	}
	if e != nil {
		return v, e
	}
	return v, nil
}
func (s *SQLiteStore) Current(ctx context.Context, expected Intent) error {
	gated, e := s.tx(ctx)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	_, e = s.positive(ctx, tx, expected)
	if e != nil {
		return e
	}
	return ctx.Err()
}

// EstablishIdentity is explicit fresh-state setup. Mismatch never adopts a key.
func (s *SQLiteStore) EstablishIdentity(ctx context.Context, server string, key []byte) error {
	if !validID(server) || len(key) != 32 {
		return ErrInvalid
	}
	gated2, e := s.tx(ctx)
	if e != nil {
		return e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	id, e := readIdentity(ctx, tx)
	if errors.Is(e, ErrStale) {
		_, e = tx.ExecContext(ctx, `INSERT INTO networking_claim_identity(singleton,server_id,public_key,reset_generation) VALUES(1,?,?,0)`, server, key)
	} else if e == nil && (id.server != server || !bytes.Equal(id.key, key)) {
		return ErrStale
	}
	if e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	return commitClaimDatabaseTx(ctx, gated2)
}

// Prepare receives verified local-owner intent from root's authenticated setup
// transaction and invokes the mandatory guard again inside its own mutation.
func (s *SQLiteStore) Prepare(ctx context.Context, b Binding, owner LocalOwner) (Intent, error) {
	return s.PreparePurpose(ctx, b, owner, ClaimPurpose{Purpose: "setup"})
}
func (s *SQLiteStore) PreparePurpose(ctx context.Context, b Binding, owner LocalOwner, purpose ClaimPurpose) (Intent, error) {
	if !purpose.Valid() {
		return Intent{}, ErrInvalid
	}
	if !validBinding(b) || !validOwner(owner) {
		return Intent{}, ErrInvalid
	}
	gated3, e := s.tx(ctx)
	if e != nil {
		return Intent{}, e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	// Setup cannot enroll an already Local server. Its owner must use the
	// confirmed switch flow, even when a move preview already exists. Refuse
	// before creating (or replaying) an intent that HTTP transport cannot send.
	if purpose.Purpose == "setup" {
		var local bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_state WHERE singleton=1 AND sign_in_kind='local')`).Scan(&local); e != nil {
			return Intent{}, e
		}
		if local {
			return Intent{}, ErrClaimMoveRequired
		}
	}
	id, e := readIdentity(ctx, tx)
	if e != nil {
		return Intent{}, e
	}
	old, e := loadIntentTx(ctx, tx, b.OperationID)
	if e == nil {
		if !sameBinding(old.Binding, b) || old.owner != owner || old.ClaimPurpose != purpose {
			return Intent{}, ErrStale
		}
		if e = s.ownerGuard(ctx, tx, owner); e != nil {
			return Intent{}, e
		}
		return old.Intent, nil
	}
	if !errors.Is(e, ErrStale) {
		return Intent{}, e
	}
	localSwitch := false
	if purpose.Purpose == "switch" && purpose.CurrentOwnerAccountID == "" {
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_state k JOIN identity_moves m ON m.owner_id=? WHERE k.singleton=1 AND k.sign_in_kind='local' AND m.to_kind='portico' AND m.stage='preview' AND m.owner_confirmed=1)`, owner.AccountID).Scan(&localSwitch); e != nil {
			return Intent{}, e
		}
		if !localSwitch || id.active.Valid || id.pending.Valid || owner.SetupOperationID != "" {
			return Intent{}, ErrStale
		}
	}
	if purpose.Purpose != "setup" && !localSwitch {
		if !id.installed.Valid || !id.active.Valid || id.active.String != id.installed.String || owner.SetupOperationID != "" {
			return Intent{}, ErrStale
		}
		var current string
		if e = tx.QueryRowContext(ctx, `SELECT hosted_account_id FROM account_portico_links WHERE account_id=?`, owner.AccountID).Scan(&current); e != nil || current != purpose.CurrentOwnerAccountID {
			return Intent{}, ErrStale
		}
		if id.pending.Valid {
			return Intent{}, ErrStale
		}
		previous, e := loadIntentTx(ctx, tx, id.installed.String)
		if e != nil {
			return Intent{}, e
		}
		if previous.AccountID != purpose.CurrentOwnerAccountID {
			return Intent{}, ErrStale
		}
		if e = s.installedGuard(ctx, tx, previous.Intent); e != nil {
			return Intent{}, e
		}
	} else if purpose.Purpose == "setup" && (id.active.Valid || id.installed.Valid || id.pending.Valid) {
		return Intent{}, ErrStale
	}

	if id.server != b.ServerID || !bytes.Equal(id.key, b.PublicKey) || id.generation != b.LocalGeneration {
		return Intent{}, ErrStale
	}

	if e = s.ownerGuard(ctx, tx, owner); e != nil {
		return Intent{}, e
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	_, e = tx.ExecContext(ctx, `INSERT INTO networking_claim_intents(operation_id,server_id,account_id,public_key,local_generation,revision,stage,owner_id,owner_profile_id,owner_epoch,setup_operation_id,created_at,updated_at,purpose,current_owner_account_id,previous_owner_role,return_registration_id) VALUES(?,?,?,?,?,1,'prepared',?,?,?,?,?,?,?,?,?,?)`, b.OperationID, b.ServerID, b.AccountID, b.PublicKey, b.LocalGeneration, owner.AccountID, owner.ProfileID, owner.Epoch, owner.SetupOperationID, now, now, purpose.Purpose, purpose.CurrentOwnerAccountID, purpose.PreviousOwnerRole, purpose.ReturnRegistrationID)
	if e != nil {
		return Intent{}, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE networking_claim_identity SET active_operation_id=CASE WHEN ?='setup' THEN ? ELSE active_operation_id END,pending_operation_id=CASE WHEN ?<>'setup' THEN ? ELSE NULL END WHERE singleton=1`, purpose.Purpose, b.OperationID, purpose.Purpose, b.OperationID); e != nil {
		return Intent{}, e
	}
	v, e := loadIntentTx(ctx, tx, b.OperationID)
	if e != nil {
		return Intent{}, e
	}
	return s.finish(ctx, gated3, v.Intent, false)
}
func (s *SQLiteStore) Approve(ctx context.Context, expected Intent, proof []byte) (Intent, error) {
	if len(proof) < 1 || len(proof) > 16384 {
		return Intent{}, ErrInvalid
	}
	facts, e := s.verifyApproval(ctx, append([]byte(nil), proof...))
	if e != nil {
		return Intent{}, e
	}

	if facts.ClaimPurpose != expected.ClaimPurpose {
		return Intent{}, ErrApprovalRequired
	}
	adopt := expected.AccountID == UnboundAccount && validID(facts.AccountID) && facts.AccountID != UnboundAccount
	if adopt {
		// The web approval binds the approver's account; everything else in the
		// binding must still match exactly.
		bound := facts.Binding
		bound.AccountID = UnboundAccount
		if !sameBinding(bound, expected.Binding) {
			return Intent{}, ErrApprovalRequired
		}
	}
	check := expected
	if adopt {
		check.AccountID = facts.AccountID
	}
	if !sameBinding(facts.Binding, check.Binding) || facts.Revision < 1 || !facts.IssuedAt.Before(facts.ExpiresAt) || facts.ExpiresAt.Sub(facts.IssuedAt) > 5*time.Minute || facts.IssuedAt.After(s.now().Add(maxClockSkew)) || !s.now().Before(facts.ExpiresAt) {
		return Intent{}, ErrApprovalRequired
	}
	gated4, e := s.tx(ctx)
	if e != nil {
		return Intent{}, e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	if e = guardClaimRequestTx(ctx, tx); e != nil {
		return Intent{}, e
	}
	if _, e = s.positive(ctx, tx, expected); e != nil {
		return Intent{}, e
	}
	// A switch approval must carry the same signed mode the stored start bound to
	// this claim; legacy v1 start / v2 approval are both ordinary switch.
	if facts.Purpose == "switch" {
		var startPayload []byte
		if e = tx.QueryRowContext(ctx, `SELECT start_payload FROM identity_owner_completions WHERE operation_id=?`, expected.OperationID).Scan(&startPayload); e != nil {
			return Intent{}, ErrApprovalRequired
		}
		var start claimweb.OwnerStart
		if json.Unmarshal(startPayload, &start) != nil || start.TransitionMode != facts.TransitionMode {
			return Intent{}, ErrApprovalRequired
		}
	}
	if facts.Purpose == "change-owner" || facts.Purpose == "switch" {
		if facts.AccountID == expected.CurrentOwnerAccountID {
			return Intent{}, ErrApprovalRequired
		}
		if !claimUsername.MatchString(facts.TargetUsername) {
			return Intent{}, ErrApprovalRequired
		}
		exclude := ""
		if facts.Purpose == "switch" {
			if e = tx.QueryRowContext(ctx, `SELECT owner_id FROM networking_claim_intents WHERE operation_id=?`, expected.OperationID).Scan(&exclude); e != nil {
				return Intent{}, e
			}
		}
		var collision bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_portico_links WHERE hosted_account_id=? AND account_id<>?) OR EXISTS(SELECT 1 FROM accounts WHERE username=? COLLATE NOCASE AND id<>?)`, facts.AccountID, exclude, facts.TargetUsername, exclude).Scan(&collision); e != nil {
			return Intent{}, e
		}
		if collision {
			return Intent{}, ErrOwnerCollision
		}
	}
	if adopt {
		// Only a claim still waiting for its first approval can be bound, in the
		// same transaction as the approval, so it is never rebound.
		result, e := tx.ExecContext(ctx, `UPDATE networking_claim_intents SET account_id=? WHERE operation_id=? AND account_id=? AND stage='prepared' AND approval_revision=0 AND revision=?`, facts.AccountID, expected.OperationID, UnboundAccount, expected.Revision)
		if e != nil {
			return Intent{}, e
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return Intent{}, ErrStale
		}
		expected.AccountID = facts.AccountID
	}
	v, e := s.positive(ctx, tx, expected)
	if e != nil {
		return Intent{}, e
	}
	if v.Stage == Installed {
		return Intent{}, ErrStale
	}
	if facts.Revision < v.ApprovalRevision {
		return Intent{}, ErrStale
	}
	if facts.Revision == v.ApprovalRevision {
		if !facts.IssuedAt.Equal(v.approvalIssued) || !facts.ExpiresAt.Equal(v.ApprovalExpiresAt) || facts.TargetUsername != v.ReservationUsername {
			return Intent{}, ErrStale
		}
		return s.finish(ctx, gated4, v.Intent, true)
	}
	if v.Revision == math.MaxInt64 {
		return Intent{}, ErrStale
	}
	stage := v.Stage
	if stage == Prepared {
		stage = Approved
	}
	_, e = tx.ExecContext(ctx, `UPDATE networking_claim_intents SET revision=revision+1,stage=?,approval_revision=?,approval_issued_at=?,approval_expires_at=?,updated_at=?,approved_username=?,owner_change_reserved=? WHERE operation_id=?`, stage, facts.Revision, facts.IssuedAt.UTC().Format(time.RFC3339Nano), facts.ExpiresAt.UTC().Format(time.RFC3339Nano), s.now().UTC().Format(time.RFC3339Nano), facts.TargetUsername, facts.Purpose == "change-owner" || facts.Purpose == "switch", v.OperationID)
	if e != nil {
		return Intent{}, e
	}
	next, e := loadIntentTx(ctx, tx, v.OperationID)
	if e != nil {
		return Intent{}, e
	}
	return s.finish(ctx, gated4, next.Intent, true)
}
func (s *SQLiteStore) BeginFinalizing(ctx context.Context, expected Intent) (Intent, error) {
	return s.advance(ctx, expected, Approved, Finalizing, Commit{})
}
func (s *SQLiteStore) Committed(ctx context.Context, expected Intent, result Commit) (Intent, error) {
	if e := validateCommit(expected, result); e != nil {
		return Intent{}, e
	}
	return s.advance(ctx, expected, Finalizing, Retrieving, result)
}
func (s *SQLiteStore) advance(ctx context.Context, expected Intent, from, to Stage, result Commit) (Intent, error) {
	gated5, e := s.tx(ctx)
	if e != nil {
		return Intent{}, e
	}
	tx := gated5.Tx()
	defer gated5.Rollback()
	v, e := s.positive(ctx, tx, expected)
	if e != nil {
		return Intent{}, e
	}
	if v.Stage != from || v.Revision == math.MaxInt64 {
		return Intent{}, ErrStale
	}
	claim, credential := v.ClaimGeneration, v.CredentialGeneration
	if to == Retrieving {
		claim, credential = result.ClaimGeneration, result.CredentialGeneration
	}
	_, e = tx.ExecContext(ctx, `UPDATE networking_claim_intents SET revision=revision+1,stage=?,claim_generation=?,credential_generation=?,updated_at=? WHERE operation_id=?`, to, claim, credential, s.now().UTC().Format(time.RFC3339Nano), v.OperationID)
	if e != nil {
		return Intent{}, e
	}
	next, e := loadIntentTx(ctx, tx, v.OperationID)
	if e != nil {
		return Intent{}, e
	}
	return s.finish(ctx, gated5, next.Intent, true)
}
func (s *SQLiteStore) Install(ctx context.Context, expected Intent, result Result) (Intent, error) {
	if e := validateCommit(expected, result.Commit); e != nil {
		return Intent{}, e
	}
	if result.Credential == nil {
		return Intent{}, ErrInvalid
	}
	gated6, e := s.tx(ctx)
	if e != nil {
		return Intent{}, e
	}
	tx := gated6.Tx()
	defer gated6.Rollback()
	v, e := s.positive(ctx, tx, expected)
	if e != nil {
		return Intent{}, e
	}
	if v.Stage != Retrieving || v.Revision == math.MaxInt64 {
		return Intent{}, ErrStale
	}
	identity, e := readIdentity(ctx, tx)
	if e != nil {
		return Intent{}, e
	}
	if v.Purpose == "switch" && v.CurrentOwnerAccountID == "" {
		if !identity.pending.Valid || identity.pending.String != v.OperationID || identity.active.Valid || identity.installed.Valid {
			return Intent{}, ErrStale
		}
	} else if v.Purpose != "setup" {
		if !identity.pending.Valid || identity.pending.String != v.OperationID || !identity.installed.Valid || !identity.active.Valid || identity.active.String != identity.installed.String {
			return Intent{}, ErrStale
		}
		old, e := loadIntentTx(ctx, tx, identity.installed.String)
		if e != nil {
			return Intent{}, e
		}
		if old.Stage != Installed || old.AccountID != v.CurrentOwnerAccountID || old.ServerID != v.ServerID || old.LocalGeneration != v.LocalGeneration {
			return Intent{}, ErrStale
		}
		// Hosted's actual result credential proves the exact new generations. Never
		// restore the predecessor after this successful replacement publication.
		if _, e = tx.ExecContext(ctx, `UPDATE networking_claim_intents SET stage='cancelled',revision=revision+1 WHERE operation_id=?`, old.OperationID); e != nil {
			return Intent{}, e
		}
	} else if identity.installed.Valid {
		return Intent{}, ErrStale
	}
	nonce, ciphertext, e := s.cipher.seal(v.Intent, result.Credential)
	if e != nil {
		return Intent{}, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO networking_claim_credentials(operation_id,server_id,local_generation,claim_generation,credential_generation,nonce,ciphertext,installed_at) VALUES(?,?,?,?,?,?,?,?)`, v.OperationID, v.ServerID, v.LocalGeneration, v.ClaimGeneration, v.CredentialGeneration, nonce, ciphertext, s.now().UTC().Format(time.RFC3339Nano))
	if e != nil {
		return Intent{}, e
	}
	verified, e := s.readCredentialTx(ctx, tx, v.Intent)
	if e != nil {
		return Intent{}, e
	}
	defer verified.Clear()
	match := false
	e = result.Credential.Use(func(a []byte) error {
		return verified.Use(func(b []byte) error { match = bytes.Equal(a, b); return nil })
	})
	if e != nil || !match {
		return Intent{}, ErrUnavailable
	}
	_, e = tx.ExecContext(ctx, `UPDATE networking_claim_identity SET active_operation_id=?,installed_operation_id=?,pending_operation_id=NULL WHERE singleton=1`, v.OperationID, v.OperationID)
	if e != nil {
		return Intent{}, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE networking_claim_intents SET stage='installed',revision=revision+1,updated_at=? WHERE operation_id=?`, s.now().UTC().Format(time.RFC3339Nano), v.OperationID)
	if e != nil {
		return Intent{}, e
	}
	next, e := loadIntentTx(ctx, tx, v.OperationID)
	if e != nil {
		return Intent{}, e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM networking_claim_cancellations WHERE operation_id=?`, v.OperationID); e != nil {
		return Intent{}, e
	}
	if s.installed != nil {
		if e = s.installed(ctx, tx, next.Intent); e != nil {
			return Intent{}, e
		}
	}
	resultIntent, e := s.finish(ctx, gated6, next.Intent, true)
	if e == nil && s.claimChanged != nil {
		s.claimChanged()
	}
	return resultIntent, e
}
func (s *SQLiteStore) Acknowledged(ctx context.Context, expected Intent) (Intent, error) {
	gated7, e := s.tx(ctx)
	if e != nil {
		return Intent{}, e
	}
	tx := gated7.Tx()
	defer gated7.Rollback()
	v, e := s.positive(ctx, tx, expected)
	if e != nil {
		return Intent{}, e
	}
	if v.Stage != Installed {
		return Intent{}, ErrStale
	}
	if v.InstallationAcknowledged {
		return s.finish(ctx, gated7, v.Intent, false)
	}
	if v.Revision == math.MaxInt64 {
		return Intent{}, ErrStale
	}
	secret, e := s.readCredentialTx(ctx, tx, v.Intent)
	if e != nil {
		return Intent{}, e
	}
	secret.Clear()
	if _, e = tx.ExecContext(ctx, `UPDATE networking_claim_intents SET installation_acknowledged=1,revision=revision+1,updated_at=? WHERE operation_id=?`, s.now().UTC().Format(time.RFC3339Nano), v.OperationID); e != nil {
		return Intent{}, e
	}
	next, e := loadIntentTx(ctx, tx, v.OperationID)
	if e != nil {
		return Intent{}, e
	}
	resultIntent, e := s.finish(ctx, gated7, next.Intent, false)
	if e == nil && s.claimChanged != nil {
		s.claimChanged()
	}
	return resultIntent, e
}
func (s *SQLiteStore) readCredentialTx(ctx context.Context, tx *sql.Tx, v Intent) (*Secret, error) {
	var server, claim, credential string
	var generation int64
	var nonce, encrypted []byte
	e := tx.QueryRowContext(ctx, `SELECT server_id,local_generation,claim_generation,credential_generation,nonce,ciphertext FROM networking_claim_credentials WHERE operation_id=?`, v.OperationID).Scan(&server, &generation, &claim, &credential, &nonce, &encrypted)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrStale
	}
	if e != nil {
		return nil, e
	}
	if server != v.ServerID || generation != v.LocalGeneration || claim != v.ClaimGeneration || credential != v.CredentialGeneration {
		return nil, ErrStale
	}
	return s.cipher.open(v, nonce, encrypted)
}

// InstalledCredential is a root-internal accessor. It is not a browser method.
// The returned scoped Secret must be cleared after bounded server transport use.
func (s *SQLiteStore) InstalledCredential(ctx context.Context, expected Intent) (*Secret, error) {
	gated8, e := s.tx(ctx)
	if e != nil {
		return nil, e
	}
	tx := gated8.Tx()
	defer gated8.Rollback()
	v, e := s.positive(ctx, tx, expected)
	if e != nil {
		return nil, e
	}
	if v.Stage != Installed {
		return nil, ErrStale
	}
	secret, e := s.readCredentialTx(ctx, tx, v.Intent)
	if e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		secret.Clear()
		return nil, e
	}
	return secret, nil
}
func (s *SQLiteStore) BeginCancel(ctx context.Context, b Binding, item Cancellation) (Intent, Cancellation, error) {
	if !sameBinding(b, item.Binding) {
		return Intent{}, Cancellation{}, ErrInvalid
	}
	if e := validateCancellation(s.audience, item); e != nil {
		return Intent{}, Cancellation{}, e
	}
	gated9, e := s.tx(ctx)
	if e != nil {
		return Intent{}, Cancellation{}, e
	}
	tx := gated9.Tx()
	defer gated9.Rollback()
	id, e := readIdentity(ctx, tx)
	if e != nil {
		return Intent{}, Cancellation{}, e
	}
	v, e := loadIntentTx(ctx, tx, b.OperationID)
	if e != nil {
		return Intent{}, Cancellation{}, e
	}
	if !sameBinding(v.Binding, b) {
		return Intent{}, Cancellation{}, ErrStale
	}
	if v.Stage == CancelPending || v.Stage == Cancelled {
		old, e := s.cancellationTx(ctx, tx, v.Intent)
		return v.Intent, old, e
	}
	firstSwitch := v.Purpose == "switch" && v.CurrentOwnerAccountID == "" && id.pending.Valid && id.pending.String == b.OperationID && !id.active.Valid && !id.installed.Valid
	if id.server != b.ServerID || !bytes.Equal(id.key, b.PublicKey) || id.generation != b.LocalGeneration || (!firstSwitch && (!id.active.Valid || id.active.String != b.OperationID)) {
		return Intent{}, Cancellation{}, ErrStale
	}
	if item.ClaimGeneration != "" && item.ClaimGeneration != v.ClaimGeneration {
		return Intent{}, Cancellation{}, ErrStale
	}
	if v.Revision == math.MaxInt64 || id.generation == math.MaxInt64 {
		return Intent{}, Cancellation{}, ErrStale
	}
	// Deny-only cancellation remains possible after owner/session revocation.
	// Signature+immutable operation binding grant only this exact cleanup action.
	mode := "intent"
	if item.ClaimGeneration != "" {
		mode = "committed"
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	_, e = tx.ExecContext(ctx, `INSERT INTO networking_claim_cancellations(operation_id,request_id,mode,claim_generation,payload,signature,state,created_at) VALUES(?,?,?,?,?,?,'pending',?) ON CONFLICT(operation_id) DO NOTHING`, v.OperationID, item.RequestID, mode, item.ClaimGeneration, item.Proof.Payload, item.Proof.Signature, now)
	if e != nil {
		return Intent{}, Cancellation{}, e
	}
	if id.pending.Valid && id.pending.String == v.OperationID && id.installed.Valid {
		old, e := loadIntentTx(ctx, tx, id.installed.String)
		if e != nil {
			return Intent{}, Cancellation{}, e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE networking_claim_intents SET stage='cancelled',revision=revision+1 WHERE operation_id=?`, old.OperationID); e != nil {
			return Intent{}, Cancellation{}, e
		}
		if s.denied != nil {
			if e = s.denied(ctx, tx, old.Intent, "unclaimed"); e != nil {
				return Intent{}, Cancellation{}, e
			}
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE networking_claim_identity SET reset_generation=reset_generation+1,installed_operation_id=NULL,active_operation_id=?,pending_operation_id=NULL WHERE singleton=1`, v.OperationID); e != nil {
		return Intent{}, Cancellation{}, e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM networking_claim_credentials WHERE operation_id=?`, v.OperationID); e != nil {
		return Intent{}, Cancellation{}, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE networking_claim_intents SET stage='cancel_pending',revision=revision+1,updated_at=? WHERE operation_id=?`, now, v.OperationID); e != nil {
		return Intent{}, Cancellation{}, e
	}
	next, e := loadIntentTx(ctx, tx, v.OperationID)
	if e != nil {
		return Intent{}, Cancellation{}, e
	}
	saved, e := s.cancellationTx(ctx, tx, next.Intent)
	if e != nil {
		return Intent{}, Cancellation{}, e
	}
	if s.denied != nil {
		if e = s.denied(ctx, tx, v.Intent, "unclaimed"); e != nil {
			return Intent{}, Cancellation{}, e
		}
	}
	result, e := s.finish(ctx, gated9, next.Intent, false)
	return result, saved, e
}
func (s *SQLiteStore) cancellationTx(ctx context.Context, tx *sql.Tx, v Intent) (Cancellation, error) {
	item := Cancellation{Binding: v.Binding}
	var mode, state string
	e := tx.QueryRowContext(ctx, `SELECT request_id,mode,claim_generation,payload,signature,state FROM networking_claim_cancellations WHERE operation_id=?`, v.OperationID).Scan(&item.RequestID, &mode, &item.ClaimGeneration, &item.Proof.Payload, &item.Proof.Signature, &state)
	if e != nil {
		return item, e
	}
	expectedMode := "intent"
	if item.ClaimGeneration != "" {
		expectedMode = "committed"
	}
	if mode != expectedMode || ((v.Stage == CancelPending && state != "pending") || (v.Stage == Cancelled && state != "acknowledged")) {
		return item, ErrInvalid
	}
	return item, validateCancellation(s.audience, item)
}
func (s *SQLiteStore) PendingCancellation(ctx context.Context, expected Intent) (Cancellation, error) {
	gated10, e := s.tx(ctx)
	if e != nil {
		return Cancellation{}, e
	}
	tx := gated10.Tx()
	defer gated10.Rollback()
	v, e := loadIntentTx(ctx, tx, expected.OperationID)
	if e != nil {
		return Cancellation{}, e
	}
	if !sameBinding(v.Binding, expected.Binding) || v.Revision != expected.Revision || v.Stage != CancelPending {
		return Cancellation{}, ErrStale
	}
	return s.cancellationTx(ctx, tx, v.Intent)
}
func (s *SQLiteStore) CancelAcknowledged(ctx context.Context, expected Intent, requestID string) (Intent, error) {
	gated11, e := s.tx(ctx)
	if e != nil {
		return Intent{}, e
	}
	tx := gated11.Tx()
	defer gated11.Rollback()
	id, e := readIdentity(ctx, tx)
	if e != nil {
		return Intent{}, e
	}
	v, e := loadIntentTx(ctx, tx, expected.OperationID)
	if e != nil {
		return Intent{}, e
	}
	if !sameBinding(v.Binding, expected.Binding) || v.Revision != expected.Revision || v.Stage != CancelPending || v.Revision == math.MaxInt64 || !id.active.Valid || id.active.String != v.OperationID || id.generation != v.LocalGeneration+1 {
		return Intent{}, ErrStale
	}
	item, e := s.cancellationTx(ctx, tx, v.Intent)
	if e != nil {
		return Intent{}, e
	}
	if item.RequestID != requestID {
		return Intent{}, ErrStale
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	if _, e = tx.ExecContext(ctx, `UPDATE networking_claim_cancellations SET state='acknowledged',acknowledged_at=? WHERE operation_id=?`, now, v.OperationID); e != nil {
		return Intent{}, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE networking_claim_intents SET stage='cancelled',owner_change_reserved=0,revision=revision+1,updated_at=? WHERE operation_id=?`, now, v.OperationID); e != nil {
		return Intent{}, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE networking_claim_identity SET active_operation_id=NULL WHERE singleton=1`); e != nil {
		return Intent{}, e
	}
	next, e := loadIntentTx(ctx, tx, v.OperationID)
	if e != nil {
		return Intent{}, e
	}
	return s.finish(ctx, gated11, next.Intent, false)
}

// SetTerminalRevoker is startup-only and must be set before the handler opens.
func (s *SQLiteStore) SetTerminalRevoker(revoke TerminalClaimRevoker) error {
	if revoke == nil {
		return ErrInvalid
	}
	s.denied = revoke
	return nil
}

// SetInstallationHooks is startup-only. Admission readiness commits with the
// credential; changed runs only after a successful install or acknowledgment.
func (s *SQLiteStore) SetInstallationHooks(ready func(context.Context, *sql.Tx, Intent) error, changed func()) error {
	if ready == nil || changed == nil {
		return ErrInvalid
	}
	s.installed, s.claimChanged = ready, changed
	return nil
}

// WithdrawReplacement is a deny-only local withdrawal before any finalization
// can have started. The independent old installed tuple is never changed. A
// finalizing/retrieving ambiguity must use the exact remote cancellation path.
func (s *SQLiteStore) WithdrawReplacement(ctx context.Context, expected Intent) (Intent, bool, error) {
	gated, e := s.tx(ctx)
	if e != nil {
		return Intent{}, false, e
	}
	defer gated.Rollback()
	tx := gated.Tx()
	id, e := readIdentity(ctx, tx)
	if e != nil {
		return Intent{}, false, e
	}
	if !id.pending.Valid || id.pending.String != expected.OperationID {
		return Intent{}, false, nil
	}
	v, e := loadIntentTx(ctx, tx, expected.OperationID)
	if e != nil {
		return Intent{}, false, e
	}
	if !sameBinding(v.Binding, expected.Binding) || v.ClaimPurpose != expected.ClaimPurpose || v.Revision != expected.Revision {
		return Intent{}, true, ErrStale
	}
	if v.Stage != Prepared && v.Stage != Approved {
		return Intent{}, false, nil
	}
	firstSwitch := v.Purpose == "switch" && v.CurrentOwnerAccountID == "" && !id.installed.Valid && !id.active.Valid
	if !firstSwitch && (!id.installed.Valid || !id.active.Valid || id.active.String != id.installed.String) {
		return Intent{}, true, ErrStale
	}
	var captured bool
	if firstSwitch {
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_owner_completions WHERE operation_id=? AND phase='prepared')`, v.OperationID).Scan(&captured); e != nil {
			return Intent{}, true, e
		}
		if captured && s.denied == nil {
			return Intent{}, true, ErrUnavailable
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE networking_claim_intents SET stage='cancelled',revision=revision+1,owner_change_reserved=0 WHERE operation_id=?`, v.OperationID); e != nil {
		return Intent{}, true, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE networking_claim_identity SET pending_operation_id=NULL WHERE singleton=1 AND pending_operation_id=?`, v.OperationID); e != nil {
		return Intent{}, true, e
	}
	next, e := loadIntentTx(ctx, tx, v.OperationID)
	if e != nil {
		return Intent{}, true, e
	}
	if captured {
		if e = s.denied(ctx, tx, v.Intent, "unclaimed"); e != nil {
			return Intent{}, true, e
		}
		// Only this pending, never-installed operation was withdrawn. Its hook
		// just retired the captured family/challenge. The root lifecycle still
		// fences the denial commit; that ended family cannot authorize it again.
		if e = commitClaimTx(ctx, gated.Commit); e != nil {
			return Intent{}, true, e
		}
		return next.Intent, true, nil
	}
	out, e := s.finish(ctx, gated, next.Intent, false)
	return out, true, e
}
