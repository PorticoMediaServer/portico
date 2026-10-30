package identity

// P2-62 / P251 mutation lifetimes.
//
// Three separate lifetime owners exist, and they are conjunctive: the current
// membership, the profile, and the installation. This file owns the installation
// lifetime and the registration handshake that turns it into one mutation root
// for the signed-in owner. Receipts themselves stay in P2-51's one receipt
// store; a root is only the durable "who was signed in where" that a receipt is
// bound to. There is no second receipt, result or acknowledgement system here,
// and no generic mutation route.
//
// The installation row is a watermark, not a grant. It is created only by a real
// authenticated device registration (registerDeviceTx). A registration handshake
// for an installation this server has never enrolled fails; it never creates the
// row, because an unknown-root retry must not be able to mint a lifetime.
//
// Registration is a full same-writer CAS:
//
//	expectedRevision == current          -> commit current+1 (or replay the
//	                                       current registration when the nonce
//	                                       repeats)
//	expectedRevision+1 == current        -> replay with the recorded nonce;
//	                                       a different nonce is a conflict,
//	                                       because someone else committed that
//	                                       revision
//	anything else                        -> stale
//
// Replaying a registration whose root has since been deleted is refused instead
// of allocated again, so destroying a lifetime can never be undone by a retry.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
)

// The receipt store's own bounds for the same identifiers. A root id and a
// registration nonce are both 128 bits written as 22 characters, and an
// incarnation is 128 bits written as 32 lowercase hex characters.
const (
	MutationRootIDLength            = 22
	MutationIncarnationLength       = 32
	MutationRegistrationMax   int64 = 9007199254740991
)

var (
	// ErrMutationLifetimeInput is a caller-supplied value that is not a
	// well-formed lifetime record.
	ErrMutationLifetimeInput = requestinput.New("invalid mutation lifetime record")
	// ErrRegistrationStale means the client's expected revision is not the
	// installation's current one, so the handshake was not attempted against
	// the state it names.
	ErrRegistrationStale = errors.New("the installation registration revision is stale")
	// ErrRegistrationNonce means the installation already committed the
	// revision the client wanted under a different nonce.
	ErrRegistrationNonce = errors.New("the installation registered this revision with a different nonce")
	// ErrInstallationUnknown means this server enrolled no installation
	// lifetime for the secret. It is never created by a registration attempt.
	ErrInstallationUnknown = errors.New("the installation has no lifetime on this server")
	// ErrInstallationRetired means the lifetime was destroyed. A destroyed
	// lifetime is replaced by a new incarnation, never reset in place.
	ErrInstallationRetired = errors.New("the installation lifetime was destroyed")
	// ErrInstallationIncarnation means the client names a different lifetime
	// than the one this installation secret carries.
	ErrInstallationIncarnation = errors.New("the installation incarnation does not match its lifetime")
	// ErrMutationRootGone means the root is missing or is no longer the live
	// owner for the scope: a deleted root, a rotated profile lifetime, a
	// replaced membership incarnation, or a revoked family. Apply and read are
	// both refused; a retry never recreates the root.
	ErrMutationRootGone = errors.New("the mutation root is gone")
	// ErrMutationRootScope means the authenticated caller is not the owner,
	// profile, family or device the root was registered for.
	ErrMutationRootScope = errors.New("the mutation root belongs to another owner")
	// ErrMutationRootSlot means the installation already holds a draining root
	// with retained receipts. A switch may not steal a third slot; the old
	// root's receipts are replayed or acknowledged first.
	ErrMutationRootSlot = errors.New("the installation has no free mutation root slot")
	// ErrMutationLifetimeCapacity means the shared P251 byte quota has no room
	// for one more root.
	ErrMutationLifetimeCapacity = errors.New("mutation lifetime capacity exhausted")
)

// MutationScope is the authenticated owner a registration is for. Every field
// is re-proved inside the writer transaction; the caller cannot substitute an
// installation, family or profile the session does not hold.
type MutationScope struct {
	Authority      string
	AccountID      string
	ProfileID      string
	FamilyID       string
	InstallationID string
}

// MutationRegistration is the client's registration attempt. The incarnation is
// the lifetime the client believes its installation secret carries. The nonce
// identifies this attempt so a lost response can be replayed exactly; it is not
// a request identifier for a user-data mutation.
type MutationRegistration struct {
	Incarnation      string
	ExpectedRevision int64
	Nonce            string
}

// MutationRoot is the durable owner a receipt is bound to. It holds identifiers
// only; no result, body or response is stored here.
type MutationRoot struct {
	ID                      string `json:"id"`
	InstallationIncarnation string `json:"installationIncarnation"`
	AccountID               string `json:"-"`
	ProfileID               string `json:"-"`
	AccountIncarnation      string `json:"-"`
	ProfileIncarnation      string `json:"-"`
	FamilyID                string `json:"-"`
	RegistrationRevision    int64  `json:"registrationRevision"`
	State                   string `json:"state"`
}

// MutationLifetimes is the installation watermark. It never contains the
// installation secret or anything derived from it beyond the opaque hash key.
type MutationLifetimes struct {
	InstallationIncarnation string `json:"installationIncarnation"`
	RegistrationRevision    int64  `json:"registrationRevision"`
	RootID                  string `json:"rootId,omitempty"`
	Retired                 bool   `json:"retired"`
}

// mutationInstallationHash is the SHA256 of the client's current installation
// secret. The secret itself is never stored for this table.
func mutationInstallationHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func newMutationID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func newMutationIncarnation() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}

// validMutationID is the 22-character root and nonce grammar: 128 bits written
// without padding.
func validMutationID(v string) bool {
	if len(v) != MutationRootIDLength {
		return false
	}
	b, e := base64.RawURLEncoding.DecodeString(v)
	return e == nil && len(b) == 16 && base64.RawURLEncoding.EncodeToString(b) == v
}

func validMutationIncarnation(v string) bool {
	if len(v) != MutationIncarnationLength {
		return false
	}
	for _, c := range v {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validMutationOwnerID(v string) bool {
	return len(v) >= 1 && len(v) <= 128 && !strings.ContainsRune(v, 0)
}

func mutationLifetimeCapacity(err error) error {
	if err != nil && strings.Contains(err.Error(), "user_mutation_capacity") {
		return fmt.Errorf("%w: %v", ErrMutationLifetimeCapacity, err)
	}
	return err
}

// enrollInstallationTx records the installation lifetime behind a real
// authenticated device registration. It never rotates an existing lifetime and
// never overwrites one, so a re-registration from the same installation keeps
// its incarnation and its registration watermark. A server-made installation id
// that no client keeps is not an installation lifetime.
func (s *Service) enrollInstallationTx(ctx context.Context, tx *sql.Tx, installation string) error {
	if strings.HasPrefix(installation, unnamedInstallationPrefix) {
		return nil
	}
	if !validInstallationID(installation) {
		return ErrDeviceInput
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO identity_installations(installation_hash,incarnation) VALUES(?,?) ON CONFLICT(installation_hash) DO NOTHING`, mutationInstallationHash(installation), newMutationIncarnation())
	return err
}

// MutationLifetimesTx reads the installation watermark without creating one.
// The caller proves the installation secret by presenting it; this read grants
// nothing by itself.
func (s *Service) MutationLifetimesTx(ctx context.Context, tx *sql.Tx, installation string) (MutationLifetimes, error) {
	if tx == nil || !validInstallationID(installation) {
		return MutationLifetimes{}, ErrMutationLifetimeInput
	}
	var out MutationLifetimes
	var retired int
	err := tx.QueryRowContext(ctx, `SELECT incarnation,registration_revision,registration_root,retired FROM identity_installations WHERE installation_hash=?`, mutationInstallationHash(installation)).Scan(&out.InstallationIncarnation, &out.RegistrationRevision, &out.RootID, &retired)
	if errors.Is(err, sql.ErrNoRows) {
		return MutationLifetimes{}, ErrInstallationUnknown
	}
	if err != nil {
		return MutationLifetimes{}, err
	}
	out.Retired = retired != 0
	return out, nil
}

const mutationRootColumns = `id,installation_incarnation,account_id,profile_id,account_incarnation,profile_incarnation,family_id,registration_revision,state`

func scanMutationRoot(row interface{ Scan(...any) error }) (MutationRoot, error) {
	var r MutationRoot
	e := row.Scan(&r.ID, &r.InstallationIncarnation, &r.AccountID, &r.ProfileID, &r.AccountIncarnation, &r.ProfileIncarnation, &r.FamilyID, &r.RegistrationRevision, &r.State)
	return r, e
}

func (s *Service) mutationRootTx(ctx context.Context, tx *sql.Tx, id string) (MutationRoot, error) {
	if !validMutationID(id) {
		return MutationRoot{}, ErrMutationRootGone
	}
	root, err := scanMutationRoot(tx.QueryRowContext(ctx, `SELECT `+mutationRootColumns+` FROM user_mutation_roots WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return MutationRoot{}, ErrMutationRootGone
	}
	return root, err
}

// mutationRootScopeTx loads a root and proves it is still the live owner for the
// scope. A rotated profile lifetime, a replaced membership incarnation, a
// revoked family or another device all make the root gone rather than valid.
func (s *Service) mutationRootScopeTx(ctx context.Context, tx *sql.Tx, id string, scope MutationScope, incarnation, membership, lifetime string) (MutationRoot, error) {
	root, err := s.mutationRootTx(ctx, tx, id)
	if err != nil {
		return MutationRoot{}, err
	}
	if root.InstallationIncarnation != incarnation || root.AccountID != scope.AccountID || root.ProfileID != scope.ProfileID || root.FamilyID != scope.FamilyID || root.AccountIncarnation != membership || root.ProfileIncarnation != lifetime {
		return MutationRoot{}, ErrMutationRootScope
	}
	if root.State != "active" && root.State != "draining" {
		return MutationRoot{}, ErrMutationRootGone
	}
	return root, nil
}

// mutationScopeTx re-proves the authenticated owner inside the writer: the
// family is live and belongs to this account and profile, the family's device is
// this installation, the membership is current and enabled, and the profile is
// live. It returns the current membership incarnation and profile lifetime, which
// the root and the receipts are bound to.
func (s *Service) mutationScopeTx(ctx context.Context, tx *sql.Tx, scope MutationScope) (string, string, error) {
	var familyAccount, familyProfile, serverID, role, horizon string
	var revoked, epoch int
	err := tx.QueryRowContext(ctx, `SELECT account_id,profile_id,revoked,server_id,role,epoch,authorization_horizon FROM authorization_session_families WHERE id=? AND authority='local'`, scope.FamilyID).Scan(&familyAccount, &familyProfile, &revoked, &serverID, &role, &epoch, &horizon)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrMutationRootScope
	}
	if err != nil {
		return "", "", err
	}
	if revoked != 0 || familyAccount != scope.AccountID || familyProfile != scope.ProfileID {
		return "", "", ErrMutationRootScope
	}
	expires, err := familyTime(horizon)
	if err != nil || !expires.After(time.Now()) {
		return "", "", ErrMutationRootScope
	}
	if err = s.checkFamilyPrincipalTx(ctx, tx, Principal{Viewer: Viewer{Authority: "local", AccountID: familyAccount, ProfileID: familyProfile, ServerID: serverID, Role: role}, Epoch: epoch}); err != nil {
		return "", "", ErrMutationRootScope
	}

	var bound string
	err = tx.QueryRowContext(ctx, `SELECT d.installation_id FROM identity_device_families f JOIN identity_devices d ON d.id=f.device_id WHERE f.family_id=? AND d.authority='local' AND d.account_id=?`, scope.FamilyID, scope.AccountID).Scan(&bound)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrMutationRootScope
	}
	if err != nil {
		return "", "", err
	}
	if bound != scope.InstallationID {
		return "", "", ErrMutationRootScope
	}
	var membership string
	var disabled int
	err = tx.QueryRowContext(ctx, `SELECT incarnation,disabled FROM direct_memberships WHERE account_id=?`, scope.AccountID).Scan(&membership, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrMutationRootScope
	}
	if err != nil {
		return "", "", err
	}
	if disabled != 0 {
		return "", "", ErrMutationRootScope
	}
	var lifetime string
	var deleted int
	err = tx.QueryRowContext(ctx, `SELECT lifetime,deleted FROM direct_profiles WHERE account_id=? AND id=?`, scope.AccountID, scope.ProfileID).Scan(&lifetime, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrMutationRootScope
	}
	if err != nil {
		return "", "", err
	}
	if deleted != 0 {
		return "", "", ErrMutationRootScope
	}
	return membership, lifetime, nil
}

// MutationLifetimeStatus maps a registration error to the status the typed
// registration route answers with, so the caller does not infer protocol
// semantics from error text. It is a plain int: this package does not own HTTP.
func MutationLifetimeStatus(err error) int {
	switch {
	case err == nil:
		return 200
	case errors.Is(err, ErrMutationLifetimeInput):
		return 400
	case errors.Is(err, ErrRegistrationStale), errors.Is(err, ErrRegistrationNonce), errors.Is(err, ErrInstallationIncarnation), errors.Is(err, ErrMutationRootScope), errors.Is(err, ErrMutationRootSlot):
		return 409
	case errors.Is(err, ErrInstallationUnknown), errors.Is(err, ErrInstallationRetired), errors.Is(err, ErrMutationRootGone):
		return 410
	case errors.Is(err, ErrMutationLifetimeCapacity):
		return 503
	}
	return 500
}

// RegisterMutationRootTx is the P2-62 registration CAS. The caller has already
// authenticated the family session; this method re-proves the exact owner,
// profile, family and device in the same writer, then:
//
//   - replays the recorded root when the same revision and nonce are repeated,
//     including after a lost response;
//   - refuses a revision that another nonce already committed;
//   - refuses a replay whose root was deleted instead of allocating a new one;
//   - keeps at most one active and one draining root for the installation,
//     draining the previous active root only after the new scope is checked.
//
// The returned bool reports a replay. Every error leaves the transaction to the
// caller to roll back, exactly as the other P251 writers do.
func (s *Service) RegisterMutationRootTx(ctx context.Context, tx *sql.Tx, scope MutationScope, q MutationRegistration) (MutationRoot, bool, error) {
	var none MutationRoot
	if tx == nil || scope.Authority != "local" || !validMutationOwnerID(scope.AccountID) || !validMutationOwnerID(scope.ProfileID) || !validFamilyID(scope.FamilyID) || !validInstallationID(scope.InstallationID) ||
		!validMutationIncarnation(q.Incarnation) || !validMutationID(q.Nonce) || q.ExpectedRevision < 0 || q.ExpectedRevision > MutationRegistrationMax {
		return none, false, ErrMutationLifetimeInput
	}
	hash := mutationInstallationHash(scope.InstallationID)
	var incarnation, nonce, storedRoot string
	var revision int64
	var retired int
	err := tx.QueryRowContext(ctx, `SELECT incarnation,registration_revision,registration_nonce,registration_root,retired FROM identity_installations WHERE installation_hash=?`, hash).Scan(&incarnation, &revision, &nonce, &storedRoot, &retired)
	if errors.Is(err, sql.ErrNoRows) {
		// A registration attempt is not an enrollment. Only registerDeviceTx
		// creates an installation lifetime.
		return none, false, ErrInstallationUnknown
	}
	if err != nil {
		return none, false, err
	}
	if retired != 0 {
		return none, false, ErrInstallationRetired
	}
	if incarnation != q.Incarnation {
		return none, false, ErrInstallationIncarnation
	}
	storedIncarnation := incarnation
	membership, lifetime, err := s.mutationScopeTx(ctx, tx, scope)
	if err != nil {
		return none, false, err
	}

	switch {
	case q.ExpectedRevision == revision:
		if nonce != "" && q.Nonce == nonce {
			root, e := s.mutationRootScopeTx(ctx, tx, storedRoot, scope, incarnation, membership, lifetime)
			return root, true, e
		}

	case revision > 0 && q.ExpectedRevision+1 == revision:
		if q.Nonce != nonce {
			return none, false, ErrRegistrationNonce
		}
		root, e := s.mutationRootScopeTx(ctx, tx, storedRoot, scope, incarnation, membership, lifetime)
		return root, true, e
	default:
		return none, false, ErrRegistrationStale
	}

	next := revision + 1
	if next > MutationRegistrationMax {
		return none, false, ErrMutationLifetimeCapacity
	}

	var active MutationRoot
	haveActive := true
	active, err = scanMutationRoot(tx.QueryRowContext(ctx, `SELECT `+mutationRootColumns+` FROM user_mutation_roots WHERE installation_incarnation=? AND state='active'`, incarnation))
	if errors.Is(err, sql.ErrNoRows) {
		haveActive = false
	} else if err != nil {
		return none, false, err
	}

	// The same owner, profile lifetime, family and installation keep their root:
	// signing in again does not churn roots or reset the receipts bound to them.
	newRoot := storedRoot
	if haveActive && active.AccountID == scope.AccountID && active.ProfileID == scope.ProfileID && active.ProfileIncarnation == lifetime && active.FamilyID == scope.FamilyID {
		if _, err = tx.ExecContext(ctx, `UPDATE user_mutation_roots SET registration_revision=? WHERE id=? AND state='active' AND installation_incarnation=?`, next, active.ID, incarnation); err != nil {
			return none, false, mutationLifetimeCapacity(err)
		}
		newRoot = active.ID
	} else {
		// At most one draining root. A previous drained root that retains no
		// receipt is removed in this same bounded writer; one that still holds
		// receipts must be replayed or acknowledged first.
		var draining string
		err = tx.QueryRowContext(ctx, `SELECT id FROM user_mutation_roots WHERE installation_incarnation=? AND state='draining'`, incarnation).Scan(&draining)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return none, false, err
		}
		if draining != "" {
			var retained int
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM user_mutation_receipts WHERE root_id=?)`, draining).Scan(&retained); err != nil {
				return none, false, err
			}
			if retained != 0 {
				return none, false, ErrMutationRootSlot
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM user_mutation_roots WHERE id=? AND state='draining'`, draining); err != nil {
				return none, false, err
			}
		}
		if haveActive {
			// The new scope is already checked above; only now does the old root
			// stop accepting new intents. Its recorded results remain readable.
			if _, err = tx.ExecContext(ctx, `UPDATE user_mutation_roots SET state='draining' WHERE id=? AND state='active' AND installation_incarnation=?`, active.ID, incarnation); err != nil {
				return none, false, err
			}
		}
		rootID := newMutationID()
		if _, err = tx.ExecContext(ctx, `INSERT INTO user_mutation_roots(id,installation_incarnation,account_id,profile_id,account_incarnation,profile_incarnation,family_id,registration_revision,state) VALUES(?,?,?,?,?,?,?,?,'active')`, rootID, incarnation, scope.AccountID, scope.ProfileID, membership, lifetime, scope.FamilyID, next); err != nil {
			return none, false, mutationLifetimeCapacity(err)
		}
		newRoot = rootID
	}

	// Full CAS on the values read above: the installation row is the single
	// watermark, so a concurrent registration cannot commit this revision too.
	result, err := tx.ExecContext(ctx, `UPDATE identity_installations SET incarnation=?,registration_revision=?,registration_nonce=?,registration_root=? WHERE installation_hash=? AND incarnation=? AND registration_revision=? AND registration_nonce=? AND registration_root=?`, incarnation, next, q.Nonce, newRoot, hash, storedIncarnation, revision, nonce, storedRoot)
	if err != nil {
		return none, false, err
	}
	if changed, e := result.RowsAffected(); e != nil {
		return none, false, e
	} else if changed != 1 {
		return none, false, ErrRegistrationStale
	}
	root, err := s.mutationRootTx(ctx, tx, newRoot)
	return root, false, err
}
