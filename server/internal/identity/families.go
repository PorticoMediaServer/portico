package identity

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strconv"
	"strings"
	"time"
)

var ErrRenewalRetired = errors.New("authorization generation retired or renewal response expired")
var ErrRenewalConflict = errors.New("authorization renewal identity conflicts")
var ErrFamilyKey = errors.New("authorization replay encryption key unavailable")

type FamilyState struct {
	ID                   string
	TokenGeneration      int64
	AuthorizationHorizon time.Time
	DeviceID             string `json:"-"`
}
type RenewalBinding struct {
	ControllerID     string
	ControllerEpoch  string
	RenewalRequestID string
}
type AuthorizationEnvelope struct {
	ProtocolVersion      string `json:"protocolVersion"`
	AccessToken          string `json:"accessToken"`
	ExpiresAt            string `json:"expiresAt"`
	AuthorizationHorizon string `json:"authorizationHorizon"`
	SessionFamilyID      string `json:"sessionFamilyId"`
	TokenGeneration      int64  `json:"tokenGeneration,string"`
}

// RenewalVerifier must check controller ownership/proof and current cached hosted
// authority in this transaction. Hosted success returns its verified policy
// horizon; local success returns zero. No nested connection or network call.
type RenewalVerifier func(context.Context, *sql.Tx, Principal, RenewalBinding) (time.Time, error)

type familyRecord struct {
	FamilyState
	principal         Principal
	revoked           bool
	tokenExpiry       time.Time
	retired           bool
	currentGeneration int64
}

func validFamilyID(v string) bool {
	if len(v) < 1 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func familyTime(raw string) (time.Time, error) {
	t, e := time.Parse(time.RFC3339, raw)
	if e != nil {
		return time.Time{}, ErrUnauthorized
	}
	return t, nil
}
func accessExpiry(now, horizon time.Time) time.Time {
	expiry := now.Add(15 * time.Minute)
	if horizon.Before(expiry) {
		return horizon
	}
	return expiry
}

// setupHashKeyName keeps the setup-hash HMAC key in the configuration table.
// Hashing that is not encryption stays: rate-limit subjects, code hashes and
// the TOTP handling below are HMACs under this key. There is no key file and
// no lost-key refusal: folder permissions are the protection, as in Plex.
const setupHashKeyName = "setup_hash_key"

// initSetupHashKey loads the setup-hash key, generating and storing it on
// first use, then migrates any rows the old file key sealed to plaintext.
// Called only by identity.New before backup service creation.
func (s *Service) initSetupHashKey(state string) error {
	if raw := persistence.Get(s.db, setupHashKeyName); raw != "" {
		if key, err := hex.DecodeString(raw); err == nil && len(key) == 32 {
			s.setupHashKey = key
			return s.migrateSealedToPlain(state)
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	if err := persistence.Set(s.db, setupHashKeyName, hex.EncodeToString(raw)); err != nil {
		return err
	}
	s.setupHashKey = raw
	return s.migrateSealedToPlain(state)
}

// IssueTx creates one distinct family within the caller's authority transaction.
// Hosted callers must supply a verified cached policy horizon; no default hosted
// entitlement is invented. The caller owns membership/restriction validation.
func (s *Service) IssueTx(ctx context.Context, tx *sql.Tx, aid, pid, authority, role string, epoch int, unsupportedHorizon time.Time) (Envelope, error) {
	if tx == nil || !validFamilyID(aid) || !validFamilyID(pid) || epoch < 1 {
		return Envelope{}, ErrUnauthorized
	}
	now := time.Now().UTC().Truncate(time.Second)
	window, err := sessionWindowTx(ctx, tx)
	if err != nil {
		return Envelope{}, err
	}
	horizon := now.Add(window)
	if parent, ok := ctx.Value(inheritedHorizonKey{}).(time.Time); ok {
		if !parent.After(now) {
			return Envelope{}, ErrUnauthorized
		}
		if parent.Before(horizon) {
			horizon = parent
		}
	}
	p := Principal{Viewer: Viewer{AccountID: aid, ProfileID: pid, ServerID: s.serverID, Authority: authority, Role: role}, Epoch: epoch}
	if err := s.checkFamilyPrincipalTx(ctx, tx, p); err != nil {
		return Envelope{}, err
	}
	if role != "account" {
		missing, e := missingRequiredFactorTx(ctx, tx, aid)
		if e != nil {
			return Envelope{}, e
		}
		if missing {
			return Envelope{}, ErrFactorSetupRequired
		}
	}
	// The owner's remote sign-in policy is applied here, at the one place a
	// session is created, rather than on each route that leads here.
	if err := s.admitSignInTx(ctx, tx, aid); err != nil {
		return Envelope{}, err
	}
	if err := admitAccountTx(ctx, tx, aid); err != nil {
		return Envelope{}, err
	}
	if authority != "local" || !unsupportedHorizon.IsZero() {
		return Envelope{}, ErrUnauthorized
	}
	var serverIdentity *NativeServerIdentity
	if s.NativeIdentityTx != nil {
		pin, err := s.NativeIdentityTx(ctx, tx)
		if err != nil {
			return Envelope{}, err
		}
		serverIdentity = &pin
	}
	device, err := s.issuingDeviceTx(ctx, tx, authority, aid)
	if err != nil {
		return Envelope{}, err
	}
	family, token, refresh := Token(), Token(), Token()
	expiry := accessExpiry(now, horizon).Format(time.RFC3339)
	horizonText := horizon.Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked) VALUES(?,?,?,?,?,?,?,?,1,0)`, family, s.serverID, aid, pid, authority, role, epoch, horizonText); err != nil {
		return Envelope{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO identity_device_families(family_id,device_id) VALUES(?,?)`, family, device.ID); err != nil {
		return Envelope{}, err
	}
	if err := bindPINLineageTx(ctx, tx, aid, device.ID, family); err != nil {
		return Envelope{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at,retired) VALUES(?,?,1,?,0)`, Digest(token), family, expiry); err != nil {
		return Envelope{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO identity_refresh_credentials(family_id,device_id,token_hash,generation,expires_at) VALUES(?,?,?,?,?)`, family, device.ID, Digest(refresh), 1, horizonText); err != nil {
		return Envelope{}, err
	}
	return Envelope{ServerIdentity: serverIdentity, AccessToken: token, RefreshToken: refresh, DeviceID: device.ID, InstallationID: device.InstallationID, ExpiresAt: expiry, Viewer: p.Viewer, SessionFamilyID: family, TokenGeneration: 1, AuthorizationHorizon: horizonText}, nil
}

// LocalSignInWindow is the default local idle window and maximum Portico
// proof lifetime. Local issuance/refresh use the normalized sign-in policy;
// refresh and descendant grants never extend a Portico admission horizon.
const LocalSignInWindow = 90 * 24 * time.Hour

func (s *Service) checkFamilyPrincipalTx(ctx context.Context, tx *sql.Tx, p Principal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.ServerID != s.serverID {
		return ErrUnauthorized
	}
	switch p.Authority {
	case "local":
		return s.checkDirectPrincipalTx(tx, p)
	default:
		return ErrUnauthorized
	}
}
func (s *Service) familyTokenTx(ctx context.Context, tx *sql.Tx, hash string) (familyRecord, error) {
	var r familyRecord
	var horizon, expires string
	var revoked, retired int
	r.principal.Hash = hash
	err := tx.QueryRowContext(ctx, `SELECT f.id,f.server_id,f.account_id,f.profile_id,f.authority,f.role,f.epoch,f.authorization_horizon,f.current_generation,f.revoked,f.pin_gate,t.generation,t.expires_at,t.retired,COALESCE((SELECT g.grant_kind FROM authorization_family_grants g WHERE g.family_id=f.id),'') FROM authorization_family_tokens t JOIN authorization_session_families f ON f.id=t.family_id WHERE t.token_hash=?`, hash).Scan(&r.ID, &r.principal.ServerID, &r.principal.AccountID, &r.principal.ProfileID, &r.principal.Authority, &r.principal.Role, &r.principal.Epoch, &horizon, &r.currentGeneration, &revoked, &r.principal.PINGated, &r.TokenGeneration, &expires, &retired, &r.principal.Grant)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return r, ErrUnauthorized
		}
		return r, err
	}
	r.AuthorizationHorizon, err = familyTime(horizon)
	if err != nil {
		return r, err
	}
	r.tokenExpiry, err = familyTime(expires)
	if err != nil {
		return r, err
	}
	r.principal.ExpiresAt = r.tokenExpiry
	if r.AuthorizationHorizon.Before(r.principal.ExpiresAt) {
		r.principal.ExpiresAt = r.AuthorizationHorizon
	}
	r.revoked = revoked != 0
	r.retired = retired != 0
	return r, nil
}

// SessionFamilyTx is the current-token seam. It must not be used by renewal replay
// or family logout, which intentionally accept authentic retired generations.
func (s *Service) SessionFamilyTx(ctx context.Context, tx *sql.Tx, p Principal) (FamilyState, error) {
	// The family read now runs before the re-authorisation rather than after it,
	// so the two guards ReauthorizeTx applied first are applied here instead.
	if tx == nil || p.Hash == "" {
		return FamilyState{}, ErrUnauthorized
	}
	if err := ctx.Err(); err != nil {
		return FamilyState{}, err
	}
	r, err := s.familyTokenTx(ctx, tx, p.Hash)
	if err != nil {
		return FamilyState{}, err
	}
	return s.sessionFamilyRecordTx(ctx, tx, p, r)
}

// PrincipalByDigestTx is the principal whose current session token has the
// given digest, under the same family rules as authentication; ErrUnauthorized
// once that sign-in has ended, rotated or expired.
func (s *Service) PrincipalByDigestTx(ctx context.Context, tx *sql.Tx, hash string) (Principal, error) {
	if tx == nil || hash == "" {
		return Principal{}, ErrUnauthorized
	}
	r, err := s.familyTokenTx(ctx, tx, hash)
	if err != nil {
		return Principal{}, err
	}
	if _, err = s.sessionFamilyRecordTx(ctx, tx, r.principal, r); err != nil {
		return Principal{}, err
	}
	return r.principal, nil
}

// sessionFamilyRecordTx is SessionFamilyTx with the family record already read.
// Authentication reads it to resolve the principal and then had it read again,
// with identical arguments, inside the same transaction — so the second read
// could not return anything different, and cost a statement to prove it.
func (s *Service) sessionFamilyRecordTx(ctx context.Context, tx *sql.Tx, p Principal, r familyRecord) (FamilyState, error) {
	if _, err := s.ReauthorizeTx(ctx, tx, p); err != nil {
		return FamilyState{}, err
	}
	if r.revoked || r.retired || r.TokenGeneration != r.currentGeneration || r.principal.Viewer != p.Viewer || r.principal.Epoch != p.Epoch || !r.AuthorizationHorizon.After(time.Now()) || !r.tokenExpiry.After(time.Now()) {
		return FamilyState{}, ErrUnauthorized
	}
	// The existing approval lookup also provides accounting metadata. It does
	// not add a statement or change the approval/authority predicates.
	if err := tx.QueryRowContext(ctx, `SELECT d.id FROM identity_device_families b JOIN identity_devices d ON d.id=b.device_id WHERE b.family_id=?`, r.ID).Scan(&r.DeviceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FamilyState{}, ErrUnauthorized
		}
		return FamilyState{}, err
	}
	return r.FamilyState, nil
}

// CastTargetParentTx validates a renewal parent after the caller proves its
// exact Cast device credential in the same transaction. Its result carries
// facts only; it is never an authenticated principal or playback permission.
func (s *Service) CastTargetParentTx(ctx context.Context, tx *sql.Tx, family string, expected Principal) (FamilyState, error) {
	if tx == nil || !validFamilyID(family) || expected.Grant != GrantCastTarget || expected.Role != TierMember {
		return FamilyState{}, ErrUnauthorized
	}
	if err := ctx.Err(); err != nil {
		return FamilyState{}, err
	}
	var hash string
	if err := tx.QueryRowContext(ctx, `SELECT t.token_hash FROM authorization_session_families f JOIN authorization_family_tokens t ON t.family_id=f.id AND t.generation=f.current_generation WHERE f.id=?`, family).Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FamilyState{}, ErrUnauthorized
		}
		return FamilyState{}, err
	}
	r, err := s.familyTokenTx(ctx, tx, hash)
	if err != nil {
		return FamilyState{}, err
	}
	if r.ID != family || r.revoked || r.retired || r.TokenGeneration != r.currentGeneration || r.principal.Viewer != expected.Viewer || r.principal.Epoch != expected.Epoch || r.principal.Grant != GrantCastTarget || !r.AuthorizationHorizon.After(time.Now()) {
		return FamilyState{}, ErrUnauthorized
	}
	if _, err = s.reauthorizeTx(ctx, tx, r.principal, true); err != nil {
		return FamilyState{}, err
	}
	if err = refuseResetDescendantTx(ctx, tx, family); err != nil {
		return FamilyState{}, err
	}
	var revokedSigner int
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM portico_admitted_families a JOIN hosted_signing_revocations r ON r.key_id=a.signer_key_id WHERE a.family_id=? AND a.signer_key_id<>'')`, family).Scan(&revokedSigner); err != nil {
		return FamilyState{}, err
	}
	if revokedSigner != 0 {
		return FamilyState{}, ErrUnauthorized
	}
	if err = tx.QueryRowContext(ctx, `SELECT d.id FROM identity_device_families b JOIN identity_devices d ON d.id=b.device_id WHERE b.family_id=?`, family).Scan(&r.DeviceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FamilyState{}, ErrUnauthorized
		}
		return FamilyState{}, err
	}
	return r.FamilyState, ctx.Err()
}

// FamilyAuthorityTx checks a durable source-family binding. The caller must
// independently validate controller proof and cached hosted authority in tx.
// Rebind requires this for the source AND SessionFamilyTx for the current target.
func (s *Service) FamilyAuthorityTx(ctx context.Context, tx *sql.Tx, id string, expected Principal) (FamilyState, error) {
	if tx == nil || !validFamilyID(id) {
		return FamilyState{}, ErrUnauthorized
	}
	var hash string
	if err := tx.QueryRowContext(ctx, `SELECT t.token_hash FROM authorization_session_families f JOIN authorization_family_tokens t ON t.family_id=f.id AND t.generation=f.current_generation WHERE f.id=? AND f.revoked=0`, id).Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FamilyState{}, ErrUnauthorized
		}
		return FamilyState{}, err
	}
	expected.Hash = hash
	return s.SessionFamilyTx(ctx, tx, expected)
}
func renewalAAD(family, predecessor string, b RenewalBinding, generation int64, expiry, horizon string) []byte {
	return []byte(strings.Join([]string{"portico-authorization-renewal-v1", family, predecessor, b.ControllerID, b.ControllerEpoch, b.RenewalRequestID, strconv.FormatInt(generation, 10), expiry, horizon}, "\x00"))
}
func (s *Service) RenewAuthorization(ctx context.Context, bearer string, b RenewalBinding, verify RenewalVerifier) (AuthorizationEnvelope, error) {
	// A key or token never rotates: renewing one would retire the only secret
	// its maker holds.
	if len(bearer) < 43 || len(bearer) > 2048 || CredentialShaped(bearer) || !validFamilyID(b.ControllerID) || !validFamilyID(b.ControllerEpoch) || !validFamilyID(b.RenewalRequestID) || verify == nil {
		return AuthorizationEnvelope{}, ErrUnauthorized
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if err != nil {
		return AuthorizationEnvelope{}, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	r, err := s.familyTokenTx(ctx, tx, Digest(bearer))
	if err != nil {
		return AuthorizationEnvelope{}, err
	}
	// Owner completion is a single captured operation, never a renewable
	// playback/controller family. Refuse before calling a controller verifier or
	// looking up a historical renewal response. Existing Cast playback remains
	// the only narrower grant that participates in this protocol.
	if r.principal.Grant != "" && r.principal.Grant != GrantCastTarget {
		return AuthorizationEnvelope{}, ErrForbidden
	}
	now := time.Now().UTC().Truncate(time.Second)
	if r.revoked || !r.AuthorizationHorizon.After(now) {
		return AuthorizationEnvelope{}, ErrUnauthorized
	}
	if err = s.checkFamilyPrincipalTx(ctx, tx, r.principal); err != nil {
		return AuthorizationEnvelope{}, err
	}
	verifiedHorizon, err := verify(ctx, tx, r.principal, b)
	if err != nil {
		return AuthorizationEnvelope{}, err
	}
	if _, err = s.FamilyAuthorityTx(ctx, tx, r.ID, r.principal); err != nil {
		return AuthorizationEnvelope{}, err
	}
	// Playback control proof can rotate a short access token, but it is not a
	// refresh credential. Only the device-bound refresh path can extend the
	// family's authorization horizon.
	horizon := r.AuthorizationHorizon
	if !verifiedHorizon.IsZero() {
		return AuthorizationEnvelope{}, ErrUnauthorized
	}
	var prior, controller, epoch, expires, fixedHorizon string
	var generation int64
	var nonce, ciphertext []byte
	err = tx.QueryRowContext(ctx, `SELECT predecessor_hash,controller_id,controller_epoch,result_generation,expires_at,authorization_horizon,nonce,ciphertext FROM authorization_family_renewals WHERE family_id=? AND request_id=?`, r.ID, b.RenewalRequestID).Scan(&prior, &controller, &epoch, &generation, &expires, &fixedHorizon, &nonce, &ciphertext)
	if err == nil {
		if prior != r.principal.Hash || controller != b.ControllerID || epoch != b.ControllerEpoch {
			return AuthorizationEnvelope{}, ErrRenewalConflict
		}
		resultExpiry, e := familyTime(expires)
		if e != nil || !resultExpiry.After(now) {
			return AuthorizationEnvelope{}, ErrRenewalRetired
		}
		if len(nonce) != 0 || len(ciphertext) != 43 {
			return AuthorizationEnvelope{}, ErrFamilyKey
		}
		if err = ctx.Err(); err != nil {
			return AuthorizationEnvelope{}, err
		}
		// A historical response may contain an already-retired successor. Replay does
		// not change family generation, expiry, token retirement or any media state.
		return AuthorizationEnvelope{"2.0", string(ciphertext), expires, fixedHorizon, r.ID, generation}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AuthorizationEnvelope{}, err
	}
	if r.retired || r.TokenGeneration != r.currentGeneration {
		return AuthorizationEnvelope{}, ErrRenewalRetired
	}
	if !r.tokenExpiry.After(now) {
		return AuthorizationEnvelope{}, ErrUnauthorized
	}
	if _, err = s.SessionFamilyTx(ctx, tx, r.principal); err != nil {
		return AuthorizationEnvelope{}, err
	}
	if r.currentGeneration == math.MaxInt64 {
		return AuthorizationEnvelope{}, ErrRenewalRetired
	}
	generation = r.currentGeneration + 1
	token := Token()
	expires = accessExpiry(now, horizon).Format(time.RFC3339)
	horizonText := horizon.Format(time.RFC3339)
	// The renewal response is stored plainly: folder permissions are the
	// protection, as in Plex. An empty nonce marks the plaintext format.
	nonce = []byte{}
	ciphertext = []byte(token)
	if _, err = tx.ExecContext(ctx, `UPDATE authorization_family_tokens SET retired=1 WHERE token_hash=? AND retired=0`, r.principal.Hash); err != nil {
		return AuthorizationEnvelope{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at,retired) VALUES(?,?,?,?,0)`, Digest(token), r.ID, generation, expires); err != nil {
		return AuthorizationEnvelope{}, err
	}
	p := r.principal
	if _, err = tx.ExecContext(ctx, `UPDATE authorization_session_families SET current_generation=?,authorization_horizon=? WHERE id=? AND revoked=0`, generation, horizonText, r.ID); err != nil {
		return AuthorizationEnvelope{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO authorization_family_renewals(family_id,request_id,predecessor_hash,controller_id,controller_epoch,result_generation,expires_at,authorization_horizon,nonce,ciphertext) VALUES(?,?,?,?,?,?,?,?,?,?)`, r.ID, b.RenewalRequestID, p.Hash, b.ControllerID, b.ControllerEpoch, generation, expires, horizonText, nonce, ciphertext); err != nil {
		return AuthorizationEnvelope{}, err
	}
	if err = ctx.Err(); err != nil {
		return AuthorizationEnvelope{}, err
	}
	if err = gated.Commit(); err != nil {
		return AuthorizationEnvelope{}, err
	}
	return AuthorizationEnvelope{"2.0", token, expires, horizonText, r.ID, generation}, nil
}

// LogoutToken accepts any authentic retained token generation solely to revoke
// its family. It does not require unexpired/current ordinary authorization.
func (s *Service) LogoutToken(ctx context.Context, bearer string) error {
	// A key or token is ended where it is managed, not by signing out with it.
	if len(bearer) < 43 || len(bearer) > 2048 || CredentialShaped(bearer) {
		return ErrUnauthorized
	}
	return s.logoutFamilyHash(ctx, Digest(bearer))
}
func (s *Service) logoutFamilyHash(ctx context.Context, hash string) error {
	gated2, err := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if err != nil {
		return err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	r, err := s.familyTokenTx(ctx, tx, hash)
	if err != nil {
		return err
	}
	if r.principal.ServerID != s.serverID {
		return ErrUnauthorized
	}
	if err = RevokeFamilyTx(ctx, tx, r.ID, RevokedExplicitSignout); err != nil {
		return err
	}
	if s.OnFamilyRevokedTx != nil {
		if err = s.OnFamilyRevokedTx(ctx, tx, r.FamilyState); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return gated2.Commit()
}

// PruneAuthorizationRenewals removes at most 128 expired encrypted responses per
// call. Token digests are retained for authentic old-generation family logout.
// The integration owner schedules this local maintenance independently of media.
func (s *Service) PruneAuthorizationRenewals(ctx context.Context) (int64, error) {
	result, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassMaintenance, `DELETE FROM authorization_family_renewals WHERE rowid IN (SELECT rowid FROM authorization_family_renewals WHERE expires_at<=? ORDER BY expires_at LIMIT 128)`, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
