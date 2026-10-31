package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/preferenceownership"
	"strings"
	"time"
)

type DirectProfile struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Primary          bool     `json:"primary"`
	Position         int      `json:"position"`
	Art              string   `json:"art"`
	Revision         int64    `json:"revision"`
	PINRevision      int64    `json:"pinRevision"`
	PINRequired      bool     `json:"pinRequired"`
	AllowedLibraries []string `json:"allowedLibraries"`
}
type ProfilePolicy struct {
	AllowedLibraries []string `json:"allowedLibraries"`
}
type DirectProfileEdit struct {
	ExpectedRevision int64          `json:"expectedRevision"`
	Name             *string        `json:"name,omitempty"`
	Art              *string        `json:"art,omitempty"`
	PIN              *string        `json:"pin,omitempty"`
	Policy           *ProfilePolicy `json:"policy,omitempty"`
}

func validProfileArt(v string) bool {
	switch v {
	case "blue", "violet", "mint", "coral", "gold", "slate", "rose", "sky":
		return true
	}
	return false
}
func validPIN(v string) bool {
	if len(v) != 4 {
		return false
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
func (s *Service) directSnapshotTx(ctx context.Context, tx *sql.Tx, c directCaller) (DirectSnapshot, error) {
	if c.account.AllowedLibraries == nil {
		c.account.AllowedLibraries = []string{}
	}
	var hosted sql.NullString
	if e := tx.QueryRowContext(ctx, `SELECT hosted_account_id FROM account_portico_links WHERE account_id=?`, c.account.ID).Scan(&hosted); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return DirectSnapshot{}, e
	}
	c.account.HostedAccountID = hosted.String
	out := DirectSnapshot{Authority: "local", ServerID: s.ID(), Account: c.account, Profiles: []DirectProfile{}, CanManage: c.manage}
	if c.familyID == "" && out.CanManage {
		var protected bool
		if e := tx.QueryRowContext(ctx, `SELECT pin_hash IS NOT NULL FROM direct_profiles WHERE id=? AND account_id=? AND deleted=0`, c.account.PrimaryProfileID, c.account.ID).Scan(&protected); e != nil {
			return out, e
		}
		out.CanManage = !protected
	}
	if c.familyID != "" && out.CanManage {
		var pinBlocked bool
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM authorization_session_families f JOIN direct_profiles p ON p.id=? AND p.account_id=f.account_id WHERE f.id=? AND f.role='account' AND p.pin_hash IS NOT NULL)`, c.account.PrimaryProfileID, c.familyID).Scan(&pinBlocked); e != nil {
			return out, e
		}
		out.CanManage = !pinBlocked
	}
	out.Capabilities = ManagementCapabilities{ManageAccount: out.CanManage, ManagePeople: out.CanManage && Administrative(c.account.Role), ManageServer: out.CanManage && Administrative(c.account.Role), ManageSignIn: out.CanManage && c.account.Role == TierOwner}
	var accountScope bool
	if c.familyID == "" {
		accountScope = true
	} else if e := tx.QueryRowContext(ctx, `SELECT role='account' FROM authorization_session_families WHERE id=?`, c.familyID).Scan(&accountScope); e != nil {
		return out, e
	}
	if accountScope {
		missing, e := missingRequiredFactorTx(ctx, tx, c.account.ID)
		if e != nil {
			return out, e
		}
		out.TwoStepSetupRequired = missing
		if missing {
			out.CanManage = false
			out.Capabilities = ManagementCapabilities{}
		}
	}
	if hosted.Valid && c.account.Role == TierOwner {
		var custodian sql.NullString
		if e := tx.QueryRowContext(ctx, `SELECT custodian FROM hosted_membership_sync WHERE singleton=1`).Scan(&custodian); e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		out.CustodyPending = custodian.Valid && custodian.String != hosted.String
	}
	policy, e := ReadSignInPolicyTx(ctx, tx)
	if e != nil {
		return out, e
	}
	out.RememberProfiles = policy.Values.PINRememberDays > 0
	if out.CanManage {
		locked, e := manageLockedTx(ctx, tx, c.familyID)
		if e != nil {
			return out, e
		}
		out.ManagePINRequired = locked
	}
	rows, e := tx.QueryContext(ctx, `SELECT id,name,is_primary,position,art,revision,pin_revision,pin_hash IS NOT NULL,allowed_libraries FROM direct_profiles WHERE account_id=? AND deleted=0 ORDER BY position,id`, c.account.ID)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var p DirectProfile
		var libraries sql.NullString
		if e = rows.Scan(&p.ID, &p.Name, &p.Primary, &p.Position, &p.Art, &p.Revision, &p.PINRevision, &p.PINRequired, &libraries); e != nil {
			return out, e
		}
		if libraries.Valid {
			if e = json.Unmarshal([]byte(libraries.String), &p.AllowedLibraries); e != nil {
				return out, e
			}
		}
		if p.AllowedLibraries == nil {
			p.AllowedLibraries = []string{}
		}
		out.Profiles = append(out.Profiles, p)
	}
	return out, rows.Err()
}

// profileManagerTx uses the existing primary-profile selection as the PIN proof.
// An account-scoped sign-in may manage only when that profile has no PIN.
func (s *Service) profileManagerTx(ctx context.Context, tx *sql.Tx, bearer string) (directCaller, error) {
	c, e := s.directCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return c, e
	}
	var role string
	var protected bool
	e = tx.QueryRowContext(ctx, `SELECT f.role,p.pin_hash IS NOT NULL FROM authorization_session_families f JOIN direct_profiles p ON p.id=? AND p.account_id=f.account_id WHERE f.id=?`, c.account.PrimaryProfileID, c.familyID).Scan(&role, &protected)
	if e != nil {
		return c, e
	}
	if role == "account" && protected {
		return c, ErrForbidden
	}
	if e = s.requireManagePINTx(ctx, tx, c.familyID); e != nil {
		return c, e
	}
	return c, nil
}

// rememberedManageWindow is how long a PIN entered on a remembered main profile
// unlocks the account's management powers. Apps ask once per app session; this
// is the server's floor under that.
const rememberedManageWindow = time.Hour

// manageLockedTx reports whether familyID is a remembered sign-in of a profile
// with a PIN that has not entered that PIN within rememberedManageWindow. A
// sign-in that is not remembered proved the PIN when the profile was chosen.
func manageLockedTx(ctx context.Context, tx *sql.Tx, familyID string) (bool, error) {
	if familyID == "" {
		return false, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var locked bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM authorization_session_families f JOIN direct_profiles p ON p.id=f.profile_id AND p.account_id=f.account_id WHERE f.id=? AND f.remember_until>? AND f.manage_until<=? AND p.pin_hash IS NOT NULL)`, familyID, now, now).Scan(&locked)
	return locked, err
}

// requireManagePINTx refuses the account's management powers to a remembered
// main profile until its PIN is entered again (profile_pin_manage).
func (s *Service) requireManagePINTx(ctx context.Context, tx *sql.Tx, familyID string) error {
	locked, err := manageLockedTx(ctx, tx, familyID)
	if err != nil {
		return err
	}
	if locked {
		return ErrProfilePINManage
	}
	return nil
}

// gateProfileFamiliesTx makes every live sign-in of profile, except keep (the
// one that made the change), enter the profile's PIN again before it does
// anything but choose a profile, read its account, finish a playback or sign
// out. remembered limits it to sign-ins that remembered the profile.
func gateProfileFamiliesTx(ctx context.Context, tx *sql.Tx, account, profile, keep string, remembered bool) error {
	_, err := tx.ExecContext(ctx, `UPDATE authorization_session_families SET pin_gate=1,remember_until='' WHERE account_id=? AND profile_id=? AND id<>? AND revoked=0 AND (?=0 OR remember_until<>'')`, account, profile, keep, remembered)
	return err
}

// ProfilePINError carries the actual next permitted attempt, preserving the error identity.
type ProfilePINError struct {
	Locked            bool
	RetryAfterSeconds int64
}

func (e *ProfilePINError) Error() string { return e.Unwrap().Error() }
func (e *ProfilePINError) Unwrap() error {
	if e.Locked {
		return ErrProfileLocked
	}
	return ErrProfilePIN
}
func pinWait(locked, now int64) *ProfilePINError {
	return &ProfilePINError{Locked: true, RetryAfterSeconds: max(int64(1), (locked-now+999)/1000)}
}

func (s *Service) CreateDirectProfile(ctx context.Context, bearer, name, art string) (DirectSnapshot, error) {
	name = strings.TrimSpace(name)
	if art == "" {
		art = "blue"
	}
	if !directName(name) || !validProfileArt(art) {
		return DirectSnapshot{}, ErrDirectInput
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return DirectSnapshot{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	c, e := s.profileManagerTx(ctx, tx, bearer)
	if e != nil {
		return DirectSnapshot{}, e
	}
	var count, position int
	if e = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(max(position),-1)+1 FROM direct_profiles WHERE account_id=? AND deleted=0`, c.account.ID).Scan(&count, &position); e != nil {
		return DirectSnapshot{}, e
	}
	var maximum int
	if e = tx.QueryRowContext(ctx, `SELECT max_profiles FROM direct_memberships WHERE account_id=?`, c.account.ID).Scan(&maximum); e != nil {
		return DirectSnapshot{}, e
	}
	if count >= maximum {
		return DirectSnapshot{}, ErrProfileCapacity
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO direct_profiles(id,account_id,name,position,art) VALUES(?,?,?,?,?)`, Token(), c.account.ID, name, position, art); e != nil {
		return DirectSnapshot{}, e
	}
	out, e := s.directSnapshotTx(ctx, tx, c)
	if e != nil {
		return out, e
	}
	return out, gated.Commit()
}
func (s *Service) EditDirectProfile(ctx context.Context, bearer, id string, q DirectProfileEdit) (DirectSnapshot, error) {
	if !validFamilyID(id) || q.ExpectedRevision < 1 || q.Name == nil && q.Art == nil && q.PIN == nil && q.Policy == nil {
		return DirectSnapshot{}, ErrDirectInput
	}
	if q.Name != nil {
		v := strings.TrimSpace(*q.Name)
		q.Name = &v
		if !directName(v) {
			return DirectSnapshot{}, ErrDirectInput
		}
	}
	if q.Art != nil && !validProfileArt(*q.Art) || q.PIN != nil && *q.PIN != "" && !validPIN(*q.PIN) || q.Policy != nil && !directLibraries(q.Policy.AllowedLibraries) {
		return DirectSnapshot{}, ErrDirectInput
	}
	var hash []byte
	var e error
	if q.PIN != nil && *q.PIN != "" {
		if slotErr := s.takeHashSlot(ctx); slotErr != nil {
			return DirectSnapshot{}, slotErr
		}
		defer func() { <-s.hashSlots }()
		hash, e = bcrypt.GenerateFromPassword([]byte(*q.PIN), PINCost)
		if e != nil {
			return DirectSnapshot{}, e
		}
	}
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return DirectSnapshot{}, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	c, e := s.profileManagerTx(ctx, tx, bearer)
	if e != nil {
		return DirectSnapshot{}, e
	}
	var revision int64
	if e = tx.QueryRowContext(ctx, `SELECT revision FROM direct_profiles WHERE account_id=? AND id=? AND deleted=0`, c.account.ID, id).Scan(&revision); e != nil {
		return DirectSnapshot{}, e
	}
	if revision != q.ExpectedRevision {
		return DirectSnapshot{}, ErrProfileChanged
	}
	if q.Name != nil {
		if _, e = tx.ExecContext(ctx, `UPDATE direct_profiles SET name=? WHERE id=?`, *q.Name, id); e != nil {
			return DirectSnapshot{}, e
		}
	}
	if q.Art != nil {
		if _, e = tx.ExecContext(ctx, `UPDATE direct_profiles SET art=? WHERE id=?`, *q.Art, id); e != nil {
			return DirectSnapshot{}, e
		}
	}
	if q.PIN != nil {
		var value any
		if len(hash) > 0 {
			value = hash
		}
		if _, e = tx.ExecContext(ctx, `UPDATE direct_profiles SET pin_hash=?,pin_revision=pin_revision+1 WHERE id=?`, value, id); e != nil {
			return DirectSnapshot{}, e
		}
		// A new or changed PIN: the profile's other sign-ins ask for it again.
		// Removing a PIN asks nobody for anything.
		if len(hash) > 0 {
			if e = gateProfileFamiliesTx(ctx, tx, c.account.ID, id, c.familyID, false); e != nil {
				return DirectSnapshot{}, e
			}
		}
	}
	if q.Policy != nil {
		var value any
		if q.Policy.AllowedLibraries != nil {
			raw, _ := json.Marshal(q.Policy.AllowedLibraries)
			value = string(raw)
		}
		if _, e = tx.ExecContext(ctx, `UPDATE direct_profiles SET allowed_libraries=? WHERE id=?`, value, id); e != nil {
			return DirectSnapshot{}, e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE direct_profiles SET revision=revision+1 WHERE id=?`, id); e != nil {
		return DirectSnapshot{}, e
	}
	// A new PIN is a new credential: the devices already on this profile got
	// there with the old one, so they ask for the new one (gated above); none is
	// signed out. A change to which libraries the profile may see is not a
	// credential. It takes effect on the profile's next request, because every
	// request reads the live list, and nobody is signed out for it.
	if q.PIN != nil {
		if e = s.retireProfilePINGrantsTx(ctx, tx, c.account.ID, id); e != nil {
			return DirectSnapshot{}, e
		}
	}
	out, e := s.directSnapshotTx(ctx, tx, c)
	if e != nil {
		return out, e
	}
	return out, gated2.Commit()
}

type ProfileOrderEntry struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

func (s *Service) OrderDirectProfiles(ctx context.Context, bearer string, order []ProfileOrderEntry) (DirectSnapshot, error) {
	if len(order) < 1 || len(order) > 8 {
		return DirectSnapshot{}, ErrDirectInput
	}
	gated3, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return DirectSnapshot{}, e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	c, e := s.profileManagerTx(ctx, tx, bearer)
	if e != nil {
		return DirectSnapshot{}, e
	}
	old, e := s.directSnapshotTx(ctx, tx, c)
	if e != nil {
		return old, e
	}
	if len(old.Profiles) != len(order) {
		return old, ErrProfileChanged
	}
	revisions := map[string]int64{}
	for _, p := range old.Profiles {
		revisions[p.ID] = p.Revision
	}
	for i, p := range order {
		if p.Revision < 1 || revisions[p.ID] != p.Revision {
			return old, ErrProfileChanged
		}
		delete(revisions, p.ID)
		if _, e = tx.ExecContext(ctx, `UPDATE direct_profiles SET position=?,revision=revision+1 WHERE account_id=? AND id=?`, i, c.account.ID, p.ID); e != nil {
			return old, e
		}
	}
	out, e := s.directSnapshotTx(ctx, tx, c)
	if e != nil {
		return out, e
	}
	return out, gated3.Commit()
}

type DirectSelection struct {
	PIN            string `json:"pin"`
	InstallationID string `json:"installationId"`
	// Remember: the device asks to open this profile at launch without the
	// picker or PIN. Honoured only when "Remember PIN for" is not Off.
	Remember bool `json:"remember"`
}

// DirectRemembered says the new sign-in remembers its profile until ExpiresAt.
type DirectRemembered struct {
	ExpiresAt string `json:"expiresAt"`
}
type DirectSelectionResult struct {
	Session    Envelope          `json:"session"`
	Remembered *DirectRemembered `json:"remembered,omitempty"`
}

// profilePINProof is what was established about a PIN outside the selection
// transaction.
type profilePINProof struct {
	// checked: the profile has a PIN, no remembered trust covers this selection
	// (an account-scoped launch or a same-profile one; a switch from another
	// profile is never covered by it), and the PIN offered was compared with it.
	checked bool
	correct bool
	// pinRevision is the PIN the comparison was made against.
	pinRevision int64
}

// proveProfilePIN compares the offered PIN with the profile's, reading what it
// needs in a snapshot and hashing with no transaction open. A profile with no
// PIN needs no proof; every other choice of a profile proves its PIN freshly,
// whichever profile it is chosen from. A remembered profile is resumed by the
// device keeping its sign-in, never by skipping this check. A device chain in
// its own retry delay is refused before hashing; other device chains remain
// unaffected.
func (s *Service) proveProfilePIN(ctx context.Context, bearer, id string, q DirectSelection) (profilePINProof, error) {
	var proof profilePINProof
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return proof, e
	}
	tx := gated.Tx()
	c, e := s.directCallerTx(ctx, tx, bearer, false)
	if e != nil {
		gated.Rollback()
		return proof, e
	}
	var hash []byte
	if e = tx.QueryRowContext(ctx, `SELECT pin_hash,pin_revision FROM direct_profiles WHERE account_id=? AND id=? AND deleted=0`, c.account.ID, id).Scan(&hash, &proof.pinRevision); e != nil {
		gated.Rollback()
		return proof, ErrUnauthorized
	}
	if len(hash) == 0 {
		gated.Rollback()
		return proof, nil
	}
	lineage, e := familyPINLineageTx(ctx, tx, c.familyID)
	if e != nil {
		gated.Rollback()
		return proof, e
	}
	nowMS := time.Now().UnixMilli()
	var lastFailed, locked int64
	e = tx.QueryRowContext(ctx, `SELECT last_failed_ms,locked_until_ms FROM direct_profile_pin_attempts WHERE profile_id=? AND lineage_id=?`, id, lineage).Scan(&lastFailed, &locked)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		gated.Rollback()
		return proof, e
	}
	if nowMS-lastFailed <= int64((24*time.Hour)/time.Millisecond) && locked > nowMS {
		gated.Rollback()
		return proof, pinWait(locked, nowMS)
	}
	gated.Rollback()
	proof.checked = true
	if !validPIN(q.PIN) {
		return proof, nil
	}
	if e = s.takeHashSlot(ctx); e != nil {
		return proof, e
	}
	proof.correct = bcrypt.CompareHashAndPassword(hash, []byte(q.PIN)) == nil
	<-s.hashSlots
	return proof, nil
}

func (s *Service) SelectDirectProfile(ctx context.Context, bearer, id string, q DirectSelection) (DirectSelectionResult, error) {
	return s.SelectDirectProfileFrom(ctx, bearer, id, q, true)
}
func (s *Service) SelectDirectProfileFrom(ctx context.Context, bearer, id string, q DirectSelection, private bool) (DirectSelectionResult, error) {
	out := DirectSelectionResult{}
	if !validFamilyID(id) || len(q.PIN) > 4 || len(q.InstallationID) > 128 {
		return out, ErrDirectInput
	}
	// The PIN is checked before the transaction opens. Hashing takes tens of
	// milliseconds, longer on a small machine, and may wait for a hashing
	// slot; the database has one writer, and every other write on the server
	// would wait behind it. The transaction below only compares what was
	// proved against the profile as it is then.
	proof, e := s.proveProfilePIN(ctx, bearer, id, q)
	if e != nil {
		return out, e
	}
	gated4, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return out, e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	c, e := s.directCallerTx(ctx, tx, bearer, false)
	if e != nil {
		return out, e
	}
	var hash []byte
	var pinRevision int64
	var primary int
	e = tx.QueryRowContext(ctx, `SELECT pin_hash,pin_revision,is_primary FROM direct_profiles WHERE account_id=? AND id=? AND deleted=0`, c.account.ID, id).Scan(&hash, &pinRevision, &primary)
	if e != nil {
		return out, ErrUnauthorized
	}
	lineage, e := familyPINLineageTx(ctx, tx, c.familyID)
	if e != nil {
		return out, e
	}
	if len(hash) > 0 {
		var attempts int
		var lastFailed, locked int64
		e = tx.QueryRowContext(ctx, `SELECT failed_attempts,last_failed_ms,locked_until_ms FROM direct_profile_pin_attempts WHERE profile_id=? AND lineage_id=?`, id, lineage).Scan(&attempts, &lastFailed, &locked)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		nowMS := time.Now().UnixMilli()
		if nowMS-lastFailed > int64((24*time.Hour)/time.Millisecond) {
			attempts = 0
			locked = 0
		}
		if locked > nowMS {
			return out, pinWait(locked, nowMS)
		}
		// The proof was made against the PIN as it was a moment ago. If the PIN
		// has been changed, removed or set since, nothing was proved about the
		// one in force now.
		if !proof.checked || proof.pinRevision != pinRevision {
			return out, ErrProfileChanged
		}
		if !proof.correct {
			attempts++
			policy, err := ReadSignInPolicyTx(ctx, tx)
			if err != nil {
				return out, err
			}
			delay := pinDelay(policy.Values.PINAttempts, attempts)
			if _, e = tx.ExecContext(ctx, `INSERT INTO direct_profile_pin_attempts(profile_id,lineage_id,failed_attempts,last_failed_ms,locked_until_ms) VALUES(?,?,?,?,?) ON CONFLICT(profile_id,lineage_id) DO UPDATE SET failed_attempts=excluded.failed_attempts,last_failed_ms=excluded.last_failed_ms,locked_until_ms=excluded.locked_until_ms`, id, lineage, attempts, nowMS, nowMS+delay*1000); e != nil {
				return out, e
			}
			if e = gated4.Commit(); e != nil {
				return out, e
			}
			return out, &ProfilePINError{RetryAfterSeconds: delay}
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM direct_profile_pin_attempts WHERE profile_id=? AND lineage_id=?`, id, lineage); e != nil {
			return out, e
		}
	}
	// directCallerTx narrows c.account.Role to the role of the session that
	// asked, so a Guest or Kids session would hand "member" to the account's
	// own main profile. The session this selection mints takes the account's
	// current membership role, read here in the same writer; a non-primary
	// profile is always a member. The caller's role and client input never set
	// it. The target's PIN gate above already ran.
	role := TierMember
	if primary != 0 {
		if e = tx.QueryRowContext(ctx, `SELECT role FROM direct_memberships WHERE account_id=? AND disabled=0`, c.account.ID).Scan(&role); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return out, ErrUnauthorized
			}
			return out, e
		}
	}
	// Choosing a profile continues the sign-in this device already holds; the
	// remote sign-in policy judged that sign-in when it was made.
	issueCtx, e := withParentPorticoHorizonTx(ctx, tx, c.familyID)
	if e != nil {
		return out, e
	}
	// The profile's session continues this sign-in's PIN-attempt lineage.
	issueCtx = withPINLineage(issueCtx, lineage)
	out.Session, e = s.IssueTx(withExistingDevice(continuedSignIn(issueCtx), c.deviceID), tx, c.account.ID, id, "local", role, c.epoch, time.Time{})
	if e != nil {
		return out, e
	}
	if e = inheritPorticoAdmissionTx(ctx, tx, c.familyID, out.Session.SessionFamilyID); e != nil {
		return out, e
	}

	// Remembering is the new sign-in's: the device opens this profile at launch
	// while it holds this family. A PIN entered just now also unlocks the
	// account's management powers on it for a while (manageLockedTx).
	now := time.Now().UTC()
	rememberUntil, manageUntil := "", ""
	if len(hash) > 0 {
		manageUntil = now.Add(rememberedManageWindow).Format(time.RFC3339)
	}
	if q.Remember {
		policy, err := ReadSignInPolicyTx(ctx, tx)
		if err != nil {
			return out, err
		}
		if policy.Values.PINRememberDays > 0 {
			rememberUntil = now.Add(time.Duration(policy.Values.PINRememberDays) * 24 * time.Hour).Format(time.RFC3339)
			out.Remembered = &DirectRemembered{ExpiresAt: rememberUntil}
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE authorization_session_families SET remember_until=?,manage_until=? WHERE id=?`, rememberUntil, manageUntil, out.Session.SessionFamilyID); e != nil {
		return out, e
	}
	return out, gated4.Commit()
}
// ForgetRememberedDevices ("Forget remembered devices") makes every sign-in
// that remembered profile (or, with no profile, any of the account's profiles)
// ask for its PIN again. installation, when given, limits it to that device.
// The caller's own sign-in is kept.
func (s *Service) ForgetRememberedDevices(ctx context.Context, bearer, profile, installation string) error {
	gated5, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return e
	}
	tx := gated5.Tx()
	defer gated5.Rollback()
	c, e := s.profileManagerTx(ctx, tx, bearer)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE authorization_session_families SET pin_gate=1,remember_until='' WHERE account_id=? AND (?='' OR profile_id=?) AND id<>? AND revoked=0 AND remember_until<>'' AND (?='' OR id IN (SELECT b.family_id FROM identity_device_families b JOIN identity_devices d ON d.id=b.device_id WHERE d.installation_id=?))`, c.account.ID, profile, profile, c.familyID, installation, installation); e != nil {
		return e
	}
	return gated5.Commit()
}

type ProfileDeletionPreview struct {
	Lifetime       string `json:"lifetime"`
	ProfileID      string `json:"profileId"`
	Revision       int64  `json:"revision"`
	Name           string `json:"name"`
	Playlists      int    `json:"playlists"`
	SavedResources int    `json:"savedResources"`
	Primary        bool   `json:"primary"`
}

func (s *Service) directDeletionTx(ctx context.Context, tx *sql.Tx, account, id string) (ProfileDeletionPreview, error) {
	p := ProfileDeletionPreview{ProfileID: id}
	e := tx.QueryRowContext(ctx, `SELECT name,revision,is_primary,lifetime FROM direct_profiles WHERE account_id=? AND id=? AND deleted=0`, account, id).Scan(&p.Name, &p.Revision, &p.Primary, &p.Lifetime)
	if e != nil {
		return p, e
	}
	e = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM catalog_playlists WHERE owner_authority='local' AND owner_account=? AND owner_profile=? AND deleted=0),(SELECT count(*) FROM saved_resources WHERE owner_authority='local' AND owner_account=? AND owner_profile=? AND deleted=0)`, account, id, account, id).Scan(&p.Playlists, &p.SavedResources)
	return p, e
}
func (s *Service) PreviewDirectProfileDeletion(ctx context.Context, bearer, id string) (ProfileDeletionPreview, error) {
	gated6, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return ProfileDeletionPreview{}, e
	}
	tx := gated6.Tx()
	defer gated6.Rollback()
	c, e := s.profileManagerTx(ctx, tx, bearer)
	if e != nil {
		return ProfileDeletionPreview{}, e
	}
	return s.directDeletionTx(ctx, tx, c.account.ID, id)
}

// EraseProfileSavedDataTx is deliberately authority-qualified. Physical media and
// recordings are NOT deleted; existing playback revocation closes their readers.
func EraseProfileSavedDataTx(ctx context.Context, tx *sql.Tx, v Viewer) error {
	if v.Authority != "local" {
		return ErrUnauthorized
	}
	if e := preferenceownership.EraseProfile(ctx, tx, v.Authority, v.AccountID, v.ProfileID); e != nil {
		return e
	}
	key := PersonalKey(v)
	for _, q := range []string{
		`DELETE FROM playlist_shares WHERE authority=? AND account_id=? AND profile_id=?`,
		`DELETE FROM saved_resource_shares WHERE authority=? AND account_id=? AND profile_id=?`,
		`DELETE FROM playlist_receipts WHERE authority=? AND account_id=? AND profile_id=?`,
		`DELETE FROM catalog_playlists WHERE owner_authority=? AND owner_account=? AND owner_profile=?`,
		`DELETE FROM saved_resources WHERE owner_authority=? AND owner_account=? AND owner_profile=?`,
	} {
		if _, e := tx.ExecContext(ctx, q, v.Authority, v.AccountID, v.ProfileID); e != nil {
			return e
		}
	}
	for _, table := range profilePersonalErasure {
		if _, e := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE profile_id=?`, key); e != nil {
			return e
		}
	}
	for _, table := range profileSavedErasure {
		if _, e := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE owner_key=?`, key); e != nil {
			return e
		}
	}
	return eraseProfileRuntimeDataTx(ctx, tx, v)
}

// Current local membership can be used by the share-target validator without a
// token. It is never an account or session authentication shortcut.
func DirectShareTarget(tx *sql.Tx, account, profile string) error {
	var valid bool
	e := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM direct_profiles p JOIN direct_memberships m ON m.account_id=p.account_id WHERE p.account_id=? AND p.id=? AND p.deleted=0 AND m.disabled=0)`, account, profile).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return errors.New("share target is not an active server profile")
	}
	return nil
}
