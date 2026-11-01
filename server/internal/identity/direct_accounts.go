package identity

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrProfileSelection = errors.New("choose a profile to finish signing in")
var ErrProfilePIN = errors.New("That profile PIN is not correct. Try again after the delay.")
var ErrProfileLocked = errors.New("Profile PIN attempts are temporarily locked. Try again later.")

// ErrProfilePINReentry: this profile's PIN changed, or its remembered devices
// were forgotten, after this sign-in began. The sign-in stays; the device asks
// for the PIN again by choosing the profile.
var ErrProfilePINReentry error = profilePINRefusal("Enter this profile's PIN to continue.")

// ErrProfilePINManage: a remembered main profile with a PIN must prove its PIN
// again before it uses the account's management powers.
var ErrProfilePINManage error = profilePINRefusal("Enter this profile's PIN to manage this account.")

// profilePINRefusal is also an ErrForbidden, so every route's error mapper
// answers 403 for it (a valid session that may not do this now), never 500 or
// 401. The edge's shared mappers name the exact code.
type profilePINRefusal string

func (e profilePINRefusal) Error() string        { return string(e) }
func (e profilePINRefusal) Is(target error) bool { return target == ErrForbidden }
var ErrProfileCapacity = errors.New("An account can have up to eight active profiles, including its primary profile.")
var ErrProfileChanged = errors.New("This profile changed. Refresh before trying again.")
var ErrDirectInput = requestinput.New("invalid Direct Sign-In account or profile input")
var ErrPrimaryProfile = errors.New("The primary profile cannot be deleted.")

// An unknown username takes the same bcrypt work as a current direct account.
// The password used to make this fixed dummy hash is never an accepted login.
var unknownAccountPasswordHash = []byte("$2b$10$k3FsQOAfb3qWTvCzZUCG6OIvqW6jBwEohdbNPHuoiacwA92kZ16LG")

type DirectAccount struct {
	ID               string   `json:"id"`
	Username         string   `json:"username"`
	PrimaryProfileID string   `json:"primaryProfileId"`
	Role             string   `json:"role"`
	Revision         int64    `json:"revision"`
	Disabled         bool     `json:"disabled"`
	AllowedLibraries []string `json:"allowedLibraries"`
	// DisabledAt and DisabledReason are set while the account is off: when it
	// was turned off, and whether an owner or administrator did it ("owner")
	// or the person left at their Portico Account ("left").
	DisabledAt     string `json:"disabledAt,omitempty"`
	DisabledReason string `json:"disabledReason,omitempty"`
	// HostedAccountID is set for a Portico Account member: its password, email
	// and second factor are managed at Hosted, everything else here.
	HostedAccountID string `json:"hostedAccountId,omitempty"`
}
type DirectSnapshot struct {
	TwoStepSetupRequired bool                   `json:"twoStepSetupRequired,omitempty"`
	Authority            string                 `json:"authority"`
	ServerID             string                 `json:"serverId"`
	Account              DirectAccount          `json:"account"`
	Profiles             []DirectProfile        `json:"profiles"`
	CanManage            bool                   `json:"canManage"`
	Capabilities         ManagementCapabilities `json:"capabilities"`
	// CustodyPending: this account owns the server and is a Portico Account,
	// but Hosted's custody of the server's claim (retiring it, its
	// certificates) is not with it. The owner accepts it with a custody
	// assertion (POST /v1/direct/ownership/custody, INT M6).
	CustodyPending bool `json:"custodyPending,omitempty"`
	// RememberProfiles: this server lets a device remember a profile picked
	// with its PIN ("Remember PIN for" is not Off).
	RememberProfiles bool `json:"rememberProfiles"`
	// ManagePINRequired: this sign-in remembered its main profile, and that
	// profile's PIN must be entered again before the account's management
	// powers are used (profile_pin_manage).
	ManagePINRequired bool `json:"managePinRequired,omitempty"`
}
type DirectSignIn struct {
	ResetFileOwnerSession  bool   `json:"resetFileOwnerSession,omitempty"`
	PasswordChangeRequired bool   `json:"passwordChangeRequired,omitempty"`
	DeviceID               string `json:"deviceId,omitempty"`
	InstallationID         string `json:"installationId,omitempty"`
	DirectSnapshot
	// Internal compatibility aliases for the one device-bound Session.
	AccountToken     string    `json:"-"`
	AccountExpiresAt string    `json:"-"`
	Session          *Envelope `json:"session,omitempty"`
	// Challenge stands in place of AccountToken when the account has a confirmed
	// second factor: the password alone has proven nothing the client may keep.
	Challenge *SignInChallenge `json:"challenge,omitempty"`
}
type directCaller struct {
	account   DirectAccount
	epoch     int
	manage    bool
	deviceID  string
	profileID string
	familyID  string
	// accountScoped: this session proved the account and has not been bound to a
	// viewing profile yet. Its profileID is the primary profile's id, which the
	// sign-in wrote as a placeholder rather than a choice, so the first profile
	// chosen from it is a launch, not a switch away from anything.
	accountScoped bool
}

func directName(v string) bool {
	return strings.TrimSpace(v) != "" && len(v) <= 120 && utf8.ValidString(v) && !strings.ContainsAny(v, "\r\n\t\x00") && strings.IndexFunc(v, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

// directPassword is the one password rule for every local account (owner,
// members, invitations): at least 8 characters. Clients show a
// strength hint, but a weak password is the user's choice. The 72-byte cap is
// bcrypt's input limit, not a policy.
func directPassword(v string) bool {
	return utf8.ValidString(v) && utf8.RuneCountInString(v) >= 8 && len(v) <= 72
}
func directLibraries(ids []string) bool {
	if len(ids) > 1000 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !validFamilyID(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func (s *Service) passwordAccount(ctx context.Context, user, password string) (directCaller, []byte, error) {
	if len(user) > 64 || len(password) > 72 {
		return directCaller{}, nil, ErrUnauthorized
	}
	var c directCaller
	var hash []byte
	var libs string
	var linked bool
	e := s.db.QueryRowContext(ctx, `SELECT a.id,a.username,a.profile_id,a.epoch,a.password_hash,m.role,m.revision,m.disabled,m.allowed_libraries,EXISTS(SELECT 1 FROM account_portico_links l WHERE l.account_id=a.id) FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE a.username=?`, strings.ToLower(strings.TrimSpace(user))).Scan(&c.account.ID, &c.account.Username, &c.account.PrimaryProfileID, &c.epoch, &hash, &c.account.Role, &c.account.Revision, &c.account.Disabled, &libs, &linked)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return directCaller{}, nil, e
	}
	if errors.Is(e, sql.ErrNoRows) {
		hash = unknownAccountPasswordHash
		if err := s.unknownCooloff(ctx, user); err != nil {
			return directCaller{}, nil, err
		}
	} else if err := s.credentialCooloff(ctx, c.account.ID); err != nil {
		return directCaller{}, nil, err
	}
	if slotErr := s.takeHashSlot(ctx); slotErr != nil {
		return directCaller{}, nil, slotErr
	}
	// A Portico Account member has no password here. It pays the same bcrypt
	// cost as any other account, so the answer's timing does not tell a prober
	// which usernames are linked (INT M15).
	passwordless := e == nil && (len(hash) == 0 || linked)
	if passwordless {
		hash = unknownAccountPasswordHash
	}
	valid := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil && !passwordless
	<-s.hashSlots
	outcome := credentialFailure
	if e == nil && valid && !c.account.Disabled {
		outcome = credentialProbe
	}
	var attemptErr error
	if errors.Is(e, sql.ErrNoRows) {
		attemptErr = s.unknownAttempt(ctx, user)
	} else {
		attemptErr = s.passwordAttempt(ctx, c.account.ID, outcome)
	}
	if attemptErr != nil {
		return directCaller{}, nil, attemptErr
	}
	if e != nil || !valid || c.account.Disabled {
		return directCaller{}, nil, ErrUnauthorized
	}
	if json.Unmarshal([]byte(libs), &c.account.AllowedLibraries) != nil {
		return directCaller{}, nil, ErrUnauthorized
	}
	hash = s.rehashOldPassword(ctx, c.account.ID, hash, password)
	c.manage = true
	return c, hash, nil
}

// Older direct accounts used cost 12. Upgrade a proven password to the current
// cost outside the writer, then swap it only if the verified hash is still
// current. A concurrent credential change remains fenced by DirectLoginFrom.
func (s *Service) rehashOldPassword(ctx context.Context, account string, oldHash []byte, password string) []byte {
	cost, err := bcrypt.Cost(oldHash)
	// Any other cost converges on PasswordCost (lead L2), lower or higher.
	if err != nil || cost == PasswordCost {
		return oldHash
	}
	select {
	case s.hashSlots <- struct{}{}:
	default:
		return oldHash
	}
	updated, err := bcrypt.GenerateFromPassword([]byte(password), PasswordCost)
	<-s.hashSlots
	if err != nil {
		return oldHash
	}
	result, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassSecurityFence, `UPDATE accounts SET password_hash=? WHERE id=? AND password_hash=?`, updated, account, oldHash)
	if err != nil {
		return oldHash
	}
	if n, err := result.RowsAffected(); err == nil && n == 1 {
		return updated
	}
	// Another sign-in may have upgraded this same password first. The caller
	// retries its admission against that live hash if it moved.
	return oldHash
}

// Account grants cannot be used for media. Their only audience is /v1/direct/*.
// A selected viewing family remains the existing authorization-session family.
func (s *Service) DirectLogin(ctx context.Context, user, password string) (DirectSignIn, error) {
	return s.DirectLoginFrom(ctx, user, password, true)
}
func (s *Service) DirectLoginFrom(ctx context.Context, user, password string, private bool) (DirectSignIn, error) {
	if err := s.RequireSignInKind(ctx, SignInLocal); err != nil {
		return DirectSignIn{}, err
	}
	if RemoteSignInClosed(ctx) {
		return DirectSignIn{}, ErrRemoteSignIn
	}
	if err := s.sourceBlocked(ctx); err != nil {
		return DirectSignIn{}, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		out, stale, err := s.directLoginOnce(ctx, user, password, private)
		if !stale {
			return out, err
		}
	}
	return DirectSignIn{}, ErrUnauthorized
}

func (s *Service) directLoginOnce(ctx context.Context, user, password string, private bool) (DirectSignIn, bool, error) {
	if err := ctx.Err(); err != nil {
		return DirectSignIn{}, false, err
	}
	c, hash, e := s.passwordAccount(ctx, user, password)
	if e != nil {
		return DirectSignIn{}, false, e
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return DirectSignIn{}, false, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var stored []byte
	var epoch, disabled int
	if e = tx.QueryRowContext(ctx, `SELECT a.password_hash,a.epoch,m.disabled FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE a.id=?`, c.account.ID).Scan(&stored, &epoch, &disabled); e != nil {
		return DirectSignIn{}, false, e
	}
	if epoch != c.epoch || disabled != 0 {
		return DirectSignIn{}, false, ErrUnauthorized
	}
	if !bytes.Equal(hash, stored) {
		return DirectSignIn{}, true, nil
	}
	// A confirmed second factor replaces the account session with a challenge. The
	// password has been proven in this transaction; nothing else has.
	required, e := s.accountFactorRequiredTx(ctx, tx, c.account.ID)
	if e != nil {
		return DirectSignIn{}, false, e
	}
	if required {
		challenge, err := s.issueSignInChallengeTx(ctx, tx, c.account.ID)
		if err != nil {
			return DirectSignIn{}, false, err
		}
		return DirectSignIn{Challenge: &challenge}, false, gated.Commit()
	}
	out, e := s.directSignInTx(ctx, tx, c, private)
	if e != nil {
		return out, false, e
	}
	if e = s.passwordAttemptTx(ctx, tx, c.account.ID, credentialSuccess); e != nil {
		return DirectSignIn{}, false, e
	}
	return out, false, gated.Commit()
}

// directAccountTx rebuilds a caller from an account id whose credentials were
// already proven in this transaction. It never reads a token.
func (s *Service) directAccountTx(ctx context.Context, tx *sql.Tx, account string) (directCaller, error) {
	c := directCaller{manage: true}
	c.account.ID = account
	var libs string
	e := tx.QueryRowContext(ctx, `SELECT a.username,a.profile_id,a.epoch,m.role,m.revision,m.disabled,m.allowed_libraries FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE a.id=?`, account).Scan(&c.account.Username, &c.account.PrimaryProfileID, &c.epoch, &c.account.Role, &c.account.Revision, &c.account.Disabled, &libs)
	if e != nil || c.account.Disabled {
		return directCaller{}, ErrUnauthorized
	}
	if json.Unmarshal([]byte(libs), &c.account.AllowedLibraries) != nil {
		return directCaller{}, ErrUnauthorized
	}
	return c, nil
}

// directSignInTx mints one rotating device session. Until profile selection,
// that family has account scope and cannot authorize media.
func (s *Service) directSignInTx(ctx context.Context, tx *sql.Tx, c directCaller, private bool) (DirectSignIn, error) {
	// This is a person's sign-in, whichever credential proved them: a device
	// it creates for the first time is worth telling the person and the owner
	// about.
	ctx = withNewDeviceNotice(ctx)
	// Apply access admission before registering a device or creating a family.
	if err := s.admitSignInTx(ctx, tx, c.account.ID); err != nil {
		return DirectSignIn{}, err
	}
	if err := admitAccountTx(ctx, tx, c.account.ID); err != nil {
		return DirectSignIn{}, err
	}
	out := DirectSignIn{}
	var e error
	out.DirectSnapshot, e = s.directSnapshotTx(ctx, tx, c)
	if e != nil {
		return out, e
	}
	device, e := s.issuingDeviceTx(ctx, tx, "local", c.account.ID)
	if e != nil {
		return out, e
	}
	out.DeviceID = device.ID
	out.InstallationID = device.InstallationID
	required, err := passwordChangeRequired(ctx, tx, c.account.ID)
	if err != nil {
		return out, err
	}
	out.PasswordChangeRequired = required
	factorSetup, e := missingRequiredFactorTx(ctx, tx, c.account.ID)
	if e != nil {
		return out, e
	}
	out.TwoStepSetupRequired = factorSetup
	if factorSetup {
		out.CanManage = false
		out.Capabilities = ManagementCapabilities{}
	}
	role := "account"
	if !required && !factorSetup && len(out.Profiles) == 1 && !out.Profiles[0].PINRequired {
		role = c.account.Role
	}
	envelope, err := s.IssueTx(withExistingDevice(ctx, device.ID), tx, c.account.ID, c.account.PrimaryProfileID, "local", role, c.epoch, time.Time{})
	if err != nil {
		return out, err
	}
	out.Session = &envelope
	out.AccountToken, out.AccountExpiresAt = envelope.AccessToken, envelope.ExpiresAt
	return out, nil
}
func (s *Service) directCallerTx(ctx context.Context, tx *sql.Tx, bearer string, manage bool) (directCaller, error) {
	var c directCaller
	var grantEpoch int
	var sessionRole string
	// An API key or personal access token acts as the member and profile that
	// made it, but only on a request the edge resolved it on and whose route
	// scope it holds (admitBearer): routes that must stay a signed-in
	// session's (passwords, two-factor, ownership, keys) are refused there.
	if CredentialShaped(bearer) {
		if err := admitBearer(ctx, bearer); err != nil {
			return c, err
		}
	} else if len(bearer) != 43 {
		return c, ErrUnauthorized
	}
	record, err := s.familyTokenTx(ctx, tx, Digest(bearer))
	if err != nil {
		return c, err
	}
	if record.principal.Authority != "local" {
		return c, ErrUnauthorized
	}
	if _, err = s.SessionFamilyTx(ctx, tx, record.principal); err != nil {
		return c, err
	}
	// Narrowed families have no ordinary account authority, regardless of role.
	if record.principal.Grant != "" {
		return c, ErrForbidden
	}
	c.account.ID = record.principal.AccountID
	c.familyID = record.ID
	c.profileID = record.principal.ProfileID
	if err = tx.QueryRowContext(ctx, `SELECT device_id FROM identity_device_families WHERE family_id=?`, record.ID).Scan(&c.deviceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c, ErrUnauthorized
		}
		return c, err
	}
	grantEpoch = record.principal.Epoch
	var primary int
	if err = tx.QueryRowContext(ctx, `SELECT is_primary FROM direct_profiles WHERE account_id=? AND id=? AND deleted=0`, c.account.ID, record.principal.ProfileID).Scan(&primary); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c, ErrUnauthorized
		}
		return c, err
	}
	c.manage = primary == 1
	// A sign-in whose profile PIN changed may still read its account and choose
	// a profile again; it manages nothing until then.
	if manage && record.principal.PINGated {
		return c, ErrProfilePINReentry
	}
	sessionRole = record.principal.Role
	// The family record, not any client input, says whether this session is
	// account-scoped: directSignInTx mints role "account" until a profile is
	// chosen, and no profile selection ever mints that role.
	c.accountScoped = sessionRole == "account"

	var libs string
	e := tx.QueryRowContext(ctx, `SELECT a.username,a.profile_id,a.epoch,m.role,m.revision,m.disabled,m.allowed_libraries FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE a.id=?`, c.account.ID).Scan(&c.account.Username, &c.account.PrimaryProfileID, &c.epoch, &c.account.Role, &c.account.Revision, &c.account.Disabled, &libs)
	if e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return c, ErrUnauthorized
		}
		return c, e
	}
	if sessionRole != "account" && sessionRole != c.account.Role {
		c.manage = false
		c.account.Role = sessionRole
	}
	if c.epoch != grantEpoch || c.account.Disabled {
		return c, ErrUnauthorized
	}
	if manage && !c.manage {
		// A valid profile session that cannot manage the account (CD-51).
		return c, ErrForbidden
	}
	if sessionRole == "account" && ctx.Value(factorSetupContextKey{}) != true && ctx.Value(passwordChangeContextKey{}) != true {
		missing, e := missingRequiredFactorTx(ctx, tx, c.account.ID)
		if e != nil {
			return c, e
		}
		if missing {
			return c, ErrFactorSetupRequired
		}
	}
	if json.Unmarshal([]byte(libs), &c.account.AllowedLibraries) != nil {
		return c, ErrUnauthorized
	}
	if required, err := passwordChangeRequired(ctx, tx, c.account.ID); err != nil {
		return c, err
	} else if required && ctx.Value(passwordChangeContextKey{}) != true {
		return c, ErrPasswordChangeRequired
	}
	return c, nil
}
func (s *Service) DirectMe(ctx context.Context, bearer string) (DirectSnapshot, error) {
	var out DirectSnapshot
	err := dbwork.WithReadSnapshot(factorSetupContext(ctx), s.db, func(snapshot context.Context) error {
		tx := dbwork.Snapshot(snapshot)
		c, err := s.directCallerTx(snapshot, tx, bearer, false)
		if err != nil {
			return err
		}
		out, err = s.directSnapshotTx(snapshot, tx, c)
		return err
	})
	if err != nil {
		return DirectSnapshot{}, err
	}
	return out, nil
}
func (s *Service) EndDirectAccountSession(ctx context.Context, bearer string) error {
	return s.LogoutToken(ctx, bearer)
}

// Explicit revocation traverses the existing family hook, preserving queue,
// controller and physical-reader retirement owned by the playback integration.
func (s *Service) revokeDirectTx(ctx context.Context, tx *sql.Tx, account, profile, family string) error {
	return s.revokeDirectWithReasonTx(ctx, tx, account, profile, family, RevokedAdminRevoke)
}
func (s *Service) revokeDirectWithReasonTx(ctx context.Context, tx *sql.Tx, account, profile, family string, reason RevocationReason) error {
	return s.revokeFamiliesTx(ctx, tx, "local", account, profile, family, reason)
}

// RetireFamilyTx ends one session family of one viewer inside the caller's
// transaction (a Cast receiver's superseded session). An empty family is a no-op,
// never "every family".
func (s *Service) RetireFamilyTx(ctx context.Context, tx *sql.Tx, authority, account, profile, family string) error {
	if family == "" || account == "" || profile == "" {
		return nil
	}
	return s.revokeFamiliesTx(ctx, tx, authority, account, profile, family, RevokedExplicitSignout)
}

// revokeFamiliesTx retires viewing families for one account under one authority. Signing a
// device out has to work the same way for a Portico Account as for a local one, and the only
// thing that differs is which authority's families are being ended.
func (s *Service) revokeFamiliesTx(ctx context.Context, tx *sql.Tx, authority, account, profile, family string, reason RevocationReason) error {
	rows, e := tx.QueryContext(ctx, `SELECT id,current_generation,authorization_horizon FROM authorization_session_families WHERE authority=? AND account_id=? AND (?='' OR profile_id=?) AND (?='' OR id=?) AND revoked=0`, authority, account, profile, profile, family, family)
	if e != nil {
		return e
	}
	var families []FamilyState
	for rows.Next() {
		var f FamilyState
		var expiry string
		if e = rows.Scan(&f.ID, &f.TokenGeneration, &expiry); e != nil {
			rows.Close()
			return e
		}
		f.AuthorizationHorizon, _ = time.Parse(time.RFC3339, expiry)
		families = append(families, f)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, f := range families {
		if s.OnFamilyRevokedTx != nil {
			if e = s.OnFamilyRevokedTx(ctx, tx, f); e != nil {
				return e
			}
		}
		if e = RevokeFamilyTx(ctx, tx, f.ID, reason); e != nil {
			return e
		}
	}
	return nil
}

type DirectSession struct {
	ID            string `json:"id"`
	ProfileID     string `json:"profileId"`
	ProfileName   string `json:"profileName"`
	ExpiresAt     string `json:"expiresAt"`
	Current       bool   `json:"current"`
	RevokedReason string `json:"revokedReason,omitempty"`
	RevokedAt     string `json:"revokedAt,omitempty"`
	StatusText    string `json:"statusText,omitempty"`
}

func sessionRevocationText(reason string) string {
	switch RevocationReason(reason) {
	case RevokedRefreshReuse:
		return "Signed out: sign-in was used twice"
	case RevokedExplicitSignout:
		return "Signed out"
	case RevokedAdminRevoke:
		return "Signed out by an administrator"
	case RevokedMembershipRemoved:
		return "Signed out: account access was removed"
	case RevokedPasswordChange:
		return "Signed out: account security changed"
	case RevokedSigningKeyRevoked:
		return "Signed out: the Portico Account sign-in was withdrawn"
	case RevokedRestored:
		return "Signed out: the server was restored from a backup"
	default:
		return "Signed out"
	}
}

func (s *Service) DirectSessions(ctx context.Context, bearer string) ([]DirectSession, error) {
	gated3, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return nil, e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	c, e := s.directCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return nil, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT f.id,f.profile_id,p.name,f.authorization_horizon,EXISTS(SELECT 1 FROM authorization_family_tokens t WHERE t.family_id=f.id AND t.token_hash=?),f.revoked,f.revoked_reason,f.revoked_at FROM authorization_session_families f JOIN direct_profiles p ON p.account_id=f.account_id AND p.id=f.profile_id WHERE f.authority='local' AND f.account_id=? AND (f.revoked=1 OR f.authorization_horizon>?) ORDER BY f.revoked ASC,f.revoked_at DESC,f.id DESC`, Digest(bearer), c.account.ID, time.Now().UTC().Format(time.RFC3339))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []DirectSession{}
	for rows.Next() {
		var f DirectSession
		var revoked bool
		if e = rows.Scan(&f.ID, &f.ProfileID, &f.ProfileName, &f.ExpiresAt, &f.Current, &revoked, &f.RevokedReason, &f.RevokedAt); e != nil {
			return nil, e
		}
		if revoked {
			f.Current = false
			f.StatusText = sessionRevocationText(f.RevokedReason)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
func (s *Service) RevokeDirectSessions(ctx context.Context, bearer, family string) error {
	gated4, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	c, e := s.directCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return e
	}
	if e = s.revokeDirectWithReasonTx(ctx, tx, c.account.ID, "", family, RevokedExplicitSignout); e != nil {
		return e
	}
	if family == "" {
		if _, e = tx.ExecContext(ctx, `UPDATE accounts SET epoch=epoch+1 WHERE id=?`, c.account.ID); e != nil {
			return e
		}
	}
	return gated4.Commit()
}
func (s *Service) ChangeDirectPassword(ctx context.Context, bearer, current, next, code string) error {
	return s.ChangeDirectPasswordWithReset(ctx, bearer, current, next, code, false)
}

// DirectPasswordChange keeps the ordinary password action and adds only the
// post-file-proof owner choice. No ordinary or member reset family may use it
// to turn off a factor. The choice commits with the replacement password.
type DirectPasswordChange struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
	Code            string `json:"code"`
	TurnOffTwoStep  bool   `json:"turnOffTwoStep,omitempty"`
}

func (DirectPasswordChange) String() string   { return "[password change redacted]" }
func (DirectPasswordChange) GoString() string { return "[password change redacted]" }

func (s *Service) ChangeDirectPasswordWithReset(ctx context.Context, bearer, current, next, code string, turnOffTwoStep bool) error {
	if err := s.RequireSignInKind(ctx, SignInLocal); err != nil {
		return err
	}
	ctx = context.WithValue(ctx, passwordChangeContextKey{}, true)
	if !directPassword(next) || len(current) > 72 || current == next {
		return ErrDirectInput
	}
	proof, e := s.proveCurrentPassword(ctx, bearer, current)
	if e != nil {
		return e
	}
	var hash []byte
	if slotErr := s.takeHashSlot(ctx); slotErr != nil {
		return slotErr
	}
	hash, e = bcrypt.GenerateFromPassword([]byte(next), PasswordCost)
	<-s.hashSlots
	if e != nil {
		return e
	}
	gated5, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return e
	}
	tx := gated5.Tx()
	defer gated5.Rollback()
	c, e := s.checkCurrentPasswordProofTx(ctx, tx, bearer, proof)
	if e != nil {
		return e
	}
	ownerFileProof := proof.reset && c.account.Role == TierOwner
	if turnOffTwoStep && !ownerFileProof {
		return ErrForbidden
	}
	// Possession of the spent reset file authorized this bounded owner-only
	// password step. Keeping the factor leaves it required at the next sign-in;
	// it does not require another proof merely to set this password.
	if !ownerFileProof {
		if e = s.verifySecondFactorTx(ctx, tx, c.account.ID, code); e != nil {
			return e
		}
	}
	if e = ValidateNewLocalPasswordTx(ctx, tx, next); e != nil {
		return e
	}
	if turnOffTwoStep {
		if e = disableResetFactorTx(ctx, tx, c.account.ID); e != nil {
			return e
		}
	}
	if e = s.revokeCredentialsTx(ctx, tx, c.account.ID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE accounts SET password_hash=?,epoch=epoch+1 WHERE id=?`, hash, c.account.ID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM identity_credential_state WHERE account_id=?`, c.account.ID); e != nil {
		return e
	}
	// A credential change is a security event the account holder must see; it
	// commits with the new password or not at all.
	var primaryProfile string
	if e = tx.QueryRowContext(ctx, `SELECT profile_id FROM accounts WHERE id=?`, c.account.ID).Scan(&primaryProfile); e != nil {
		return e
	}
	if _, e = notify.NotifySecurityEvent(tx, time.Now().UnixMilli(), "local", c.account.ID, primaryProfile, notify.SecurityPasswordChange, "", ""); e != nil {
		return e
	}
	return gated5.Commit()
}

type DirectMemberCreate struct {
	Username         string   `json:"username"`
	Password         string   `json:"password"`
	Name             string   `json:"name"`
	AllowedLibraries []string `json:"allowedLibraries"`
}

type DirectMemberInput struct {
	Username         string   `json:"username"`
	Name             string   `json:"name"`
	AllowedLibraries []string `json:"allowedLibraries"`
	ExpectedRevision int64    `json:"expectedRevision"`
	Disabled         bool     `json:"disabled"`
}

func (s *Service) DirectMembers(ctx context.Context, bearer string) ([]DirectAccount, error) {
	gated6, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return nil, e
	}
	tx := gated6.Tx()
	defer gated6.Rollback()
	c, e := s.directCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return nil, e
	}
	if c.account.Role != "owner" {
		return nil, ErrForbidden
	}
	rows, e := tx.QueryContext(ctx, `SELECT a.id,a.username,a.profile_id,m.role,m.revision,m.disabled,m.allowed_libraries,COALESCE(l.hosted_account_id,''),m.disabled_ms,m.disabled_reason FROM accounts a JOIN direct_memberships m ON m.account_id=a.id LEFT JOIN account_portico_links l ON l.account_id=a.id ORDER BY a.username`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []DirectAccount{}
	for rows.Next() {
		var a DirectAccount
		var raw string
		var disabledMs int64
		if e = rows.Scan(&a.ID, &a.Username, &a.PrimaryProfileID, &a.Role, &a.Revision, &a.Disabled, &raw, &a.HostedAccountID, &disabledMs, &a.DisabledReason); e != nil {
			return nil, e
		}
		if disabledMs > 0 {
			a.DisabledAt = time.UnixMilli(disabledMs).UTC().Format(time.RFC3339)
		}
		if e = json.Unmarshal([]byte(raw), &a.AllowedLibraries); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Service) CreateDirectMember(ctx context.Context, bearer string, q DirectMemberCreate) (DirectAccount, error) {
	if err := s.RequireSignInKind(ctx, SignInLocal); err != nil {
		return DirectAccount{}, err
	}
	q.Username = strings.ToLower(strings.TrimSpace(q.Username))
	q.Name = strings.TrimSpace(q.Name)
	if len(q.Username) < 3 || len(q.Username) > 64 || strings.ContainsAny(q.Username, " \t\r\n@") || !directPassword(q.Password) || !directName(q.Name) || !directLibraries(q.AllowedLibraries) {
		return DirectAccount{}, ErrDirectInput
	}
	if slotErr := s.takeHashSlot(ctx); slotErr != nil {
		return DirectAccount{}, slotErr
	}
	defer func() { <-s.hashSlots }()
	hash, e := bcrypt.GenerateFromPassword([]byte(q.Password), PasswordCost)
	if e != nil {
		return DirectAccount{}, e
	}
	gated7, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return DirectAccount{}, e
	}
	tx := gated7.Tx()
	defer gated7.Rollback()
	c, e := s.directCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return DirectAccount{}, e
	}
	if c.account.Role != "owner" {
		return DirectAccount{}, ErrForbidden
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts WHERE username=?`, q.Username).Scan(&count); e != nil {
		return DirectAccount{}, e
	}
	if count != 0 {
		return DirectAccount{}, ErrConflict
	}
	if q.AllowedLibraries == nil {
		q.AllowedLibraries = []string{}
	}
	if e = ValidateNewLocalPasswordTx(ctx, tx, q.Password); e != nil {
		return DirectAccount{}, e
	}
	out := DirectAccount{ID: Token(), Username: q.Username, PrimaryProfileID: Token(), Role: "member", Revision: 1, AllowedLibraries: q.AllowedLibraries}
	if _, e = tx.ExecContext(ctx, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,?,?)`, out.ID, out.Username, hash, out.PrimaryProfileID); e != nil {
		return out, e
	}
	raw, _ := json.Marshal(q.AllowedLibraries)
	if _, e = tx.ExecContext(ctx, `UPDATE direct_memberships SET allowed_libraries=? WHERE account_id=?`, string(raw), out.ID); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE direct_profiles SET name=? WHERE id=?`, q.Name, out.PrimaryProfileID); e != nil {
		return out, e
	}
	return out, gated7.Commit()
}
func (s *Service) UpdateDirectMember(ctx context.Context, bearer, id string, q DirectMemberInput) error {
	if !validFamilyID(id) || q.ExpectedRevision < 1 || !directLibraries(q.AllowedLibraries) {
		return ErrDirectInput
	}
	gated8, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return e
	}
	tx := gated8.Tx()
	defer gated8.Rollback()
	c, e := s.directCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return e
	}
	// A signed-in person who may not do this is refused (403), never told to
	// authenticate (401): clients end a sign-in on a 401.
	if !Administrative(c.account.Role) {
		return ErrForbidden
	}
	var role string
	var revision int64
	var wasDisabled bool
	if e = tx.QueryRowContext(ctx, `SELECT role,revision,disabled FROM direct_memberships WHERE account_id=?`, id).Scan(&role, &revision, &wasDisabled); e != nil {
		return e
	}
	if !ManagesTier(c.account.Role, role) {
		return ErrForbidden
	}
	if revision != q.ExpectedRevision {
		return ErrProfileChanged
	}
	if q.AllowedLibraries == nil {
		q.AllowedLibraries = []string{}
	}
	raw, _ := json.Marshal(q.AllowedLibraries)
	// The member is signed out only when they lose access altogether (the
	// account is turned off) or their password is replaced. Giving or taking a
	// library applies to their next request and signs nobody out: every request
	// reads the live library list.
	if q.Disabled {
		if !wasDisabled {
			if e = s.recordOwnerMembershipEndingTx(ctx, tx, id); e != nil {
				return e
			}
		}
		reason := RevokedPasswordChange
		if q.Disabled {
			reason = RevokedMembershipRemoved
		}
		if e = s.revokeCredentialsWithReasonTx(ctx, tx, id, reason); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE accounts SET epoch=epoch+1 WHERE id=?`, id); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE direct_memberships SET disabled=?,allowed_libraries=?,revision=revision+1 WHERE account_id=?`, q.Disabled, string(raw), id); e != nil {
		return e
	}
	return gated8.Commit()
}
func (s *Service) TransferDirectOwnership(ctx context.Context, bearer, target, password string, expected int64) error {
	if err := s.RequireSignInKind(ctx, SignInLocal); err != nil {
		return err
	}
	if password == "" || len(password) > 72 || target == "" {
		return ErrDirectInput
	}
	proof, e := s.proveCurrentPassword(ctx, bearer, password)
	if e != nil {
		return e
	}
	gated9, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return e
	}
	tx := gated9.Tx()
	defer gated9.Rollback()
	c, e := s.checkCurrentPasswordProofTx(ctx, tx, bearer, proof)
	if e != nil {
		return e
	}
	if e = s.transferOwnershipTx(ctx, tx, c, target, expected); e != nil {
		return e
	}
	return gated9.Commit()
}

// TransferPorticoOwnership is TransferDirectOwnership for an owner whose
// identity is a Portico Account: it has no password here, so a fresh Hosted
// identity assertion for that same account is the confirmation. The journal
// then carries the owner change to Hosted, which moves the claim with it.
func (s *Service) TransferPorticoOwnership(ctx context.Context, bearer, target string, who PorticoIdentity, expected int64) error {
	if target == "" || !validHostedAccountID(who.AccountID) {
		return ErrDirectInput
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	c, e := s.directCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return e
	}
	var linked string
	if e = tx.QueryRowContext(ctx, `SELECT hosted_account_id FROM account_portico_links WHERE account_id=?`, c.account.ID).Scan(&linked); e != nil || linked != who.AccountID {
		// A valid credential with the wrong linked account is refused (403),
		// never unauthorized (401): a 401 would sign the owner out.
		return ErrForbidden
	}
	if e = s.transferOwnershipTx(ctx, tx, c, target, expected); e != nil {
		return e
	}
	return gated.Commit()
}

func (s *Service) transferOwnershipTx(ctx context.Context, tx *sql.Tx, c directCaller, target string, expected int64) error {
	var e error
	if c.account.Role != "owner" {
		return ErrForbidden
	}
	if expected != c.account.Revision {
		return ErrProfileChanged
	}
	if target == c.account.ID {
		return ErrForbidden
	}
	var active bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM direct_memberships WHERE account_id=? AND role='member' AND disabled=0)`, target).Scan(&active); e != nil {
		return e
	}
	if !active {
		return ErrForbidden
	}
	if _, e = tx.ExecContext(ctx, `UPDATE direct_memberships SET role='member',allowed_libraries='[]',revision=revision+1 WHERE account_id=?`, c.account.ID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE direct_memberships SET role='owner',revision=revision+1 WHERE account_id=?`, target); e != nil {
		return e
	}
	// Both people stay signed in. Their sessions take their new tiers in this
	// same transaction, so the former owner's devices stop being an owner's at
	// once and the new owner's become one without signing in again.
	if e = RetierFamiliesTx(ctx, tx, c.account.ID, TierMember); e != nil {
		return e
	}
	if e = RetierFamiliesTx(ctx, tx, target, TierOwner); e != nil {
		return e
	}
	return s.ownerChangedTx(ctx, tx)
}
