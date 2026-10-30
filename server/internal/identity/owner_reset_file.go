package identity

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/authorityfiles"
	"runtime"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/dirsync"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
)

const ownerResetLifetime = 15 * time.Minute
const resetReissueSeconds int64 = 60
const resetFileName = "reset-code.txt"

var ErrOwnerResetExpired = errors.New("owner reset code expired")
var ErrOwnerResetFileWrite = errors.New("owner reset file could not be written")

type ownerResetFileWriteError struct{ cause error }

func (e *ownerResetFileWriteError) Error() string        { return ErrOwnerResetFileWrite.Error() }
func (e *ownerResetFileWriteError) Unwrap() error        { return e.cause }
func (e *ownerResetFileWriteError) Is(target error) bool { return target == ErrOwnerResetFileWrite }

type OwnerResetRequest struct {
	Username string `json:"username,omitempty"`
}
type OwnerResetLocation struct {
	SystemKind        string `json:"systemKind"`
	DataDirectory     string `json:"dataDirectory"`
	FileDirectory     string `json:"fileDirectory"`
	FileName          string `json:"fileName"`
	RetryAfterSeconds int64  `json:"retryAfterSeconds"`
}
type OwnerResetVerify struct {
	Code           string `json:"code"`
	TurnOffTwoStep bool   `json:"turnOffTwoStep,omitempty"`
}
type MemberResetCode struct {
	ServerID  string `json:"serverId,omitempty"`
	Code      string `json:"code"`
	ExpiresAt string `json:"expiresAt"`
}
type MemberResetVerify struct {
	Username string `json:"username"`
	Code     string `json:"code"`
}

// ConfigureResetFile binds the actual portable layout, before routes are exposed.
// No location is written into the database. The caller supplies Layout.Keys.
func (s *Service) ConfigureResetFile(data, keys string, container bool) error {
	if !filepath.IsAbs(data) || !filepath.IsAbs(keys) || filepath.Clean(data) == string(filepath.Separator) || filepath.Clean(keys) != filepath.Join(filepath.Clean(data), "Keys") {
		return errors.New("invalid reset file directory")
	}
	s.resetData, s.resetDir = filepath.Clean(data), filepath.Clean(keys)
	s.resetSystem = runtime.GOOS
	if s.resetSystem == "darwin" {
		s.resetSystem = "macos"
	}
	if container {
		s.resetSystem = "docker"
	}
	return nil
}

// openResetDirectory refuses symlink ancestors and a shared Keys directory.
// os.Root confines every subsequent file operation to that inspected directory.
func (s *Service) openResetDirectory() (*os.Root, error) {
	if s.resetDir == "" {
		return nil, errors.New("reset file directory unavailable")
	}
	for path := s.resetDir; ; path = filepath.Dir(path) {
		info, e := os.Lstat(path)
		if e != nil {
			return nil, e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("unsafe reset file directory")
		}
		if path == s.resetDir && !resetPermissionsPrivate(path, info) {
			return nil, errors.New("reset file directory must be private")
		}
		if parent := filepath.Dir(path); parent == path {
			break
		}
	}
	info, e := os.Lstat(s.resetDir)
	if e != nil {
		return nil, e
	}
	root, e := os.OpenRoot(s.resetDir)
	if e != nil {
		return nil, e
	}
	opened, e := root.Stat(".")
	if e != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, errors.New("reset directory changed")
	}
	return root, nil
}
func resetFileSafe(root *os.Root, directory string) error {
	info, e := root.Lstat(resetFileName)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || !resetPermissionsPrivate(filepath.Join(directory, resetFileName), info) {
		return errors.New("unsafe reset file")
	}
	return nil
}

type resetFileOperations struct {
	write         func(*os.File, []byte) (int, error)
	sync          func(*os.File) error
	close         func(*os.File) error
	rename        func(*os.Root, string, string) error
	syncDirectory func(string) error
}

func defaultResetFileOperations() resetFileOperations {
	return resetFileOperations{
		write: func(f *os.File, b []byte) (int, error) { return f.Write(b) }, sync: (*os.File).Sync, close: (*os.File).Close,
		rename: func(r *os.Root, a, b string) error { return r.Rename(a, b) }, syncDirectory: dirsync.Sync,
	}
}
func (s *Service) publishResetFile(code string, expires time.Time) error {
	defer authorityfiles.Process.Admit()()
	ops := defaultResetFileOperations()
	if s.resetIO != nil {
		ops = *s.resetIO
	}
	root, e := s.openResetDirectory()
	if e != nil {
		return e
	}
	defer root.Close()
	if e = resetFileSafe(root, s.resetDir); e != nil {
		return e
	}
	name := ".reset-code-" + Token() + ".tmp"
	f, e := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer root.Remove(name)
	body := []byte(fmt.Sprintf("Portico\nOne-time reset code\n%s\nExpires: %s\nDelete this file if you did not request it.\n", code, expires.UTC().Format(time.RFC3339)))
	defer clear(body)
	n, e := ops.write(f, body)
	if e == nil && n != len(body) {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = ops.sync(f)
	}
	if closeErr := ops.close(f); e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = resetFileSafe(root, s.resetDir); e != nil {
		return e
	}
	if e = ops.rename(root, name, resetFileName); e != nil {
		return e
	}
	return ops.syncDirectory(s.resetDir)
}
func (s *Service) removeResetFile() error {
	defer authorityfiles.Process.Admit()()
	root, e := s.openResetDirectory()
	if e != nil {
		return e
	}
	defer root.Close()
	if e = resetFileSafe(root, s.resetDir); e != nil {
		return e
	}
	if e = root.Remove(resetFileName); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return dirsync.Sync(s.resetDir)
}

// RequestOwnerReset ignores the alleged username: the same private owner file
// and public response are produced for every input, without account enumeration.
func (s *Service) RequestOwnerReset(ctx context.Context, _ OwnerResetRequest) (OwnerResetLocation, error) {
	out := OwnerResetLocation{SystemKind: s.resetSystem, DataDirectory: s.resetData, FileDirectory: s.resetDir, FileName: resetFileName, RetryAfterSeconds: resetReissueSeconds}
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		now := time.Now().Unix()
		var issued int64
		e := tx.QueryRowContext(ctx, `SELECT issued_at FROM owner_reset_file WHERE singleton=1`).Scan(&issued)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if issued+resetReissueSeconds > now {
			out.RetryAfterSeconds = issued + resetReissueSeconds - now
			return nil
		}
		var account string
		var epoch int
		var linked bool
		e = tx.QueryRowContext(ctx, `SELECT a.id,a.epoch FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE m.role='owner'`).Scan(&account, &epoch)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		linked, e = s.ownerResetPorticoTx(ctx, tx, account)
		if e != nil {
			return e
		}
		code := Token()
		expires := time.Unix(now, 0).Add(ownerResetLifetime)
		// The file is durable before the digest commits. An interrupted transaction
		// can leave an unusable private file, never a publicly usable credential.
		if e = s.publishResetFile(code, expires); e != nil {
			return &ownerResetFileWriteError{cause: e}
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO owner_reset_file(singleton,account_id,epoch,portico,code_hash,issued_at,expires_at,failed_attempts) VALUES(1,?,?,?,?,?,?,0) ON CONFLICT(singleton) DO UPDATE SET account_id=excluded.account_id,epoch=excluded.epoch,portico=excluded.portico,code_hash=excluded.code_hash,issued_at=excluded.issued_at,expires_at=excluded.expires_at,failed_attempts=0`, account, epoch, linked, s.setupHash("owner-reset", code), now, expires.Unix()); e != nil {
			return e
		}
		return s.securityNoticeTx(ctx, tx, account, "security.owner_reset_file_written")
	})
	return out, err
}

func (s *Service) VerifyOwnerReset(ctx context.Context, q OwnerResetVerify) (DirectSignIn, error) {
	if len(q.Code) > 128 {
		q.Code = ""
	}
	if e := s.SetupLimit(ctx, resetPeer(ctx), "owner-reset-verify", 30); e != nil {
		return DirectSignIn{}, e
	}
	var out DirectSignIn
	invalid := false
	expired := false
	var limited *SetupProtocolError
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		var account, hash string
		var epoch, tries int
		var linked bool
		var expires, issued int64
		e := tx.QueryRowContext(ctx, `SELECT account_id,epoch,portico,code_hash,expires_at,failed_attempts,issued_at FROM owner_reset_file WHERE singleton=1`).Scan(&account, &epoch, &linked, &hash, &expires, &tries, &issued)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		stored := hash
		if len(stored) != 43 {
			stored = s.setupHash("invalid-reset", "")
		}
		match := subtle.ConstantTimeCompare([]byte(stored), []byte(s.setupHash("owner-reset", q.Code))) == 1
		if e != nil || hash == "" || expires <= time.Now().Unix() || tries >= 5 || !match {
			invalid = true
			// Expiry is disclosed only for possession of the exact still-stored
			// original code AND unchanged owner identity. Arbitrary, used and
			// unknown inputs retain the one neutral invalid-code outcome.
			if e == nil && len(hash) == 43 && match && tries < 5 && expires <= time.Now().Unix() {
				owner, readErr := s.directAccountTx(ctx, tx, account)
				if readErr == nil && owner.epoch == epoch && owner.account.Role == TierOwner {
					live, linkErr := s.ownerResetPorticoTx(ctx, tx, account)
					expired = linkErr == nil && live == linked
				}
			}
			if e == nil && hash != "" && !match && tries == 4 && expires > time.Now().Unix() {
				limited = &SetupProtocolError{Code: "rate_limited", Interval: int(max(1, issued+resetReissueSeconds-time.Now().Unix()))}
			}
			if e == nil && hash != "" {
				_, e = tx.ExecContext(ctx, `UPDATE owner_reset_file SET failed_attempts=MIN(5,failed_attempts+1),code_hash=CASE WHEN failed_attempts>=4 OR expires_at<=? THEN '' ELSE code_hash END WHERE singleton=1`, time.Now().Unix())
			}
			if errors.Is(e, sql.ErrNoRows) {
				return nil
			}
			return e
		}
		c, e := s.directAccountTx(ctx, tx, account)
		if e != nil || c.epoch != epoch || c.account.Role != TierOwner {
			invalid = true
			_, e = tx.ExecContext(ctx, `UPDATE owner_reset_file SET code_hash='' WHERE singleton=1`)
			return e
		}
		live, e := s.ownerResetPorticoTx(ctx, tx, account)
		if e != nil {
			return e
		}
		if live != linked {
			invalid = true
			return nil
		}
		if _, e = tx.ExecContext(ctx, `UPDATE owner_reset_file SET code_hash='' WHERE singleton=1`); e != nil {
			return e
		}
		// Only possession of the successfully verified file proof permits this
		// owner session from anywhere, even if ordinary remote sign-ins are off.
		ctx = continuedSignIn(ctx)
		if e = s.resetCredentialTx(ctx, tx, account, !linked, q.TurnOffTwoStep); e != nil {
			return e
		}
		c, e = s.directAccountTx(ctx, tx, account)
		if e != nil {
			return e
		}
		if linked {
			out.DirectSnapshot, e = s.directSnapshotTx(ctx, tx, c)
			if e != nil {
				return e
			}
			// Access is one session, not a Hosted admission or a refresh credential.
			session, e := s.IssueTx(WithVouchedDevice(ctx), tx, account, c.account.PrimaryProfileID, "local", TierOwner, c.epoch, time.Time{})
			if e != nil {
				return e
			}
			if e = markResetSessionTx(ctx, tx, &session, "owner"); e != nil {
				return e
			}
			out.Session = &session
			out.ResetFileOwnerSession = true
		} else {
			out, e = s.directSignInTx(WithVouchedDevice(ctx), tx, c, true)
			if e != nil {
				return e
			}
			if out.Session == nil {
				return ErrUnauthorized
			}
			if e = markResetSessionTx(ctx, tx, out.Session, "password"); e != nil {
				return e
			}
		}
		if e = s.removeResetFile(); e != nil {
			return e
		}
		return s.securityNoticeTx(ctx, tx, account, "security.owner_reset_file_used")
	})
	if err != nil {
		return DirectSignIn{}, err
	}
	if expired {
		return DirectSignIn{}, ErrOwnerResetExpired
	}
	if limited != nil {
		return DirectSignIn{}, limited
	}
	if invalid {
		return DirectSignIn{}, ErrUnauthorized
	}
	return out, nil
}
func resetPeer(ctx context.Context) string { source, _ := credentialSource(ctx); return source }

func (s *Service) resetCredentialTx(ctx context.Context, tx *sql.Tx, account string, mustChange, turnOff bool) error {
	if e := s.revokeCredentialsTx(ctx, tx, account); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `UPDATE accounts SET epoch=epoch+1 WHERE id=?`, account); e != nil {
		return e
	}
	if mustChange {
		// The old password ceases to be a credential immediately, including before
		// the holder of the code submits a replacement.
		if _, e := tx.ExecContext(ctx, `UPDATE accounts SET password_hash=X'' WHERE id=?`, account); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO identity_credential_state(account_id,must_change) VALUES(?,1) ON CONFLICT(account_id) DO UPDATE SET must_change=1`, account); e != nil {
			return e
		}
	}
	if !mustChange {
		if _, e := tx.ExecContext(ctx, `DELETE FROM identity_credential_state WHERE account_id=?`, account); e != nil {
			return e
		}
	}
	if turnOff {
		if e := disableResetFactorTx(ctx, tx, account); e != nil {
			return e
		}
	}
	for _, table := range []string{"identity_credential_attempts", "identity_account_attempt_budget"} {
		if _, e := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE account_id=?`, account); e != nil {
			return e
		}
	}
	return nil
}

func disableResetFactorTx(ctx context.Context, tx *sql.Tx, account string) error {
	var enabled bool
	if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_account_factors WHERE account_id=?)`, account).Scan(&enabled); e != nil {
		return e
	}
	if enabled {
		if e := twoFactorResetNoticeTx(ctx, tx, account); e != nil {
			return e
		}
	}
	for _, table := range []string{"identity_account_factors", "identity_recovery_codes", "identity_factor_challenges"} {
		if _, e := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE account_id=?`, account); e != nil {
			return e
		}
	}
	return nil
}
func markResetSessionTx(ctx context.Context, tx *sql.Tx, session *Envelope, kind string) error {
	until := time.Now().UTC().Truncate(time.Second).Add(time.Hour).Format(time.RFC3339)
	if _, e := tx.ExecContext(ctx, `INSERT INTO reset_file_sessions(family_id,kind) VALUES(?,?)`, session.SessionFamilyID, kind); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `UPDATE authorization_session_families SET authorization_horizon=? WHERE id=?`, until, session.SessionFamilyID); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `DELETE FROM identity_refresh_credentials WHERE family_id=?`, session.SessionFamilyID); e != nil {
		return e
	}
	session.RefreshToken = ""
	session.AuthorizationHorizon = until
	if session.ExpiresAt > until {
		session.ExpiresAt = until
		if _, e := tx.ExecContext(ctx, `UPDATE authorization_family_tokens SET expires_at=? WHERE family_id=?`, until, session.SessionFamilyID); e != nil {
			return e
		}
	}
	return nil
}
func resetSessionTx(ctx context.Context, tx *sql.Tx, family string) (string, error) {
	var kind string
	e := tx.QueryRowContext(ctx, `SELECT kind FROM reset_file_sessions WHERE family_id=?`, family).Scan(&kind)
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	return kind, e
}
func refuseResetDescendantTx(ctx context.Context, tx *sql.Tx, family string) error {
	kind, e := resetSessionTx(ctx, tx, family)
	if e != nil {
		return e
	}
	if kind != "" {
		return ErrForbidden
	}
	return nil
}

func (s *Service) MakeMemberResetCode(ctx context.Context, bearer, target string, q MemberResetRequest) (MemberResetCode, error) {
	if e := s.RequireSignInKind(ctx, SignInLocal); e != nil {
		return MemberResetCode{}, e
	}
	if !validFamilyID(target) || q.ExpectedRevision < 1 {
		return MemberResetCode{}, ErrDirectInput
	}
	if q.OriginalResetCode != nil && !validMemberResetOriginalCode(*q.OriginalResetCode) {
		return MemberResetCode{}, ErrDirectInput
	}
	out := MemberResetCode{Code: Token()}
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if e := RequireSignInKindTx(ctx, tx, SignInLocal); e != nil {
			return e
		}
		c, e := s.profileManagerTx(ctx, tx, bearer)
		if e != nil {
			return e
		}
		member, e := s.directAccountTx(ctx, tx, target)
		if e != nil {
			return e
		}
		if c.account.ID == target || !ManagesTier(c.account.Role, member.account.Role) {
			return ErrForbidden
		}
		if member.account.Revision != q.ExpectedRevision {
			return ErrProfileChanged
		}
		linked, e := porticoLinkedTx(ctx, tx, target)
		if e != nil {
			return e
		}
		if linked {
			return ErrForbidden
		}
		if q.OriginalResetCode != nil {
			return s.replaceMemberResetCodeTx(ctx, tx, c, member, *q.OriginalResetCode, q.TurnOffTwoStep, &out)
		}
		out.ServerID, e = LocalLinkServerIDTx(ctx, tx)
		if e != nil {
			return e
		}
		// Issuing a replacement does not disable MFA; the choice commits only when
		// the recipient successfully proves the reset code.
		if e = s.resetCredentialTx(ctx, tx, target, true, false); e != nil {
			return e
		}
		policy, e := ReadSignInPolicyTx(ctx, tx)
		if e != nil {
			return e
		}
		expires := time.Now().UTC().Truncate(time.Second).Add(time.Duration(policy.Values.MemberResetCodeMinutes) * time.Minute)
		out.ExpiresAt = expires.Format(time.RFC3339)
		if _, e = tx.ExecContext(ctx, `INSERT INTO member_password_resets(account_id,code_hash,epoch,expires_at,failed_attempts,turn_off_two_step,membership_revision,issuer_id,issuer_epoch) VALUES(?,?,?, ?,0,?,?,?,?) ON CONFLICT(account_id) DO UPDATE SET code_hash=excluded.code_hash,epoch=excluded.epoch,expires_at=excluded.expires_at,failed_attempts=0,turn_off_two_step=excluded.turn_off_two_step,membership_revision=excluded.membership_revision,issuer_id=excluded.issuer_id,issuer_epoch=excluded.issuer_epoch`, target, s.setupHash("member-reset", out.Code), member.epoch+1, expires.Unix(), q.TurnOffTwoStep, member.account.Revision, c.account.ID, c.epoch); e != nil {
			return e
		}
		return s.securityNoticeTx(ctx, tx, target, "security.password_reset")
	})
	if err != nil {
		return MemberResetCode{}, err
	}
	return out, nil
}

// A result-sheet switch replaces only its still-pending reset. It is never a
// second credential reset, and cannot fall through to the fresh Make path.
func (s *Service) replaceMemberResetCodeTx(ctx context.Context, tx *sql.Tx, issuer, member directCaller, original string, turnOff bool, out *MemberResetCode) error {
	var hash, issuerID string
	var epoch, issuerEpoch, tries int
	var revision, expires int64
	err := tx.QueryRowContext(ctx, `SELECT code_hash,epoch,expires_at,failed_attempts,membership_revision,issuer_id,issuer_epoch FROM member_password_resets WHERE account_id=?`, member.account.ID).Scan(&hash, &epoch, &expires, &tries, &revision, &issuerID, &issuerEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrProfileChanged
	}
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(s.setupHash("member-reset", original))) != 1 || len(hash) != 43 ||
		epoch != member.epoch || revision != member.account.Revision || issuerID != issuer.account.ID || issuerEpoch != issuer.epoch ||
		expires <= time.Now().Unix() || tries < 0 || tries >= 5 {
		return ErrProfileChanged
	}
	// Both accounts and the strict managing relationship were re-admitted by the
	// caller in this same accepting writer, before reaching this exact-row CAS.
	out.ServerID, err = LocalLinkServerIDTx(ctx, tx)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE member_password_resets SET code_hash=?,turn_off_two_step=? WHERE account_id=? AND code_hash=? AND epoch=? AND membership_revision=? AND issuer_id=? AND issuer_epoch=? AND expires_at=? AND expires_at>? AND failed_attempts=?`, s.setupHash("member-reset", out.Code), turnOff, member.account.ID, hash, epoch, revision, issuerID, issuerEpoch, expires, time.Now().Unix(), tries)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrProfileChanged
	}
	out.ExpiresAt = time.Unix(expires, 0).UTC().Format(time.RFC3339)
	return nil
}

func (s *Service) VerifyMemberReset(ctx context.Context, q MemberResetVerify) (DirectSignIn, error) {
	if e := s.RequireSignInKind(ctx, SignInLocal); e != nil {
		return DirectSignIn{}, e
	}
	if len(q.Username) > 64 || len(q.Code) > 128 {
		q.Username = ""
		q.Code = ""
	}
	if e := s.SetupLimit(ctx, resetPeer(ctx), "member-reset-verify", 30); e != nil {
		return DirectSignIn{}, e
	}
	invalid := false
	var out DirectSignIn
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if e := RequireSignInKindTx(ctx, tx, SignInLocal); e != nil {
			return e
		}
		var account, hash, issuer string
		var membershipRevision int64
		var issuerEpoch int
		var epoch, tries int
		var expires int64
		var turnOff bool
		e := tx.QueryRowContext(ctx, `SELECT r.account_id,r.code_hash,r.epoch,r.expires_at,r.failed_attempts,r.turn_off_two_step,r.membership_revision,r.issuer_id,r.issuer_epoch FROM member_password_resets r JOIN accounts a ON a.id=r.account_id WHERE a.username=?`, strings.ToLower(strings.TrimSpace(q.Username))).Scan(&account, &hash, &epoch, &expires, &tries, &turnOff, &membershipRevision, &issuer, &issuerEpoch)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		stored := hash
		if len(stored) != 43 {
			stored = s.setupHash("invalid-reset", "")
		}
		match := subtle.ConstantTimeCompare([]byte(stored), []byte(s.setupHash("member-reset", q.Code))) == 1
		if e != nil || hash == "" || !match || expires <= time.Now().Unix() || tries >= 5 {
			invalid = true
			if e == nil && hash != "" {
				_, e = tx.ExecContext(ctx, `UPDATE member_password_resets SET failed_attempts=MIN(5,failed_attempts+1),code_hash=CASE WHEN failed_attempts>=4 OR expires_at<=? THEN '' ELSE code_hash END WHERE account_id=?`, time.Now().Unix(), account)
			}
			if errors.Is(e, sql.ErrNoRows) {
				return nil
			}
			return e
		}
		c, e := s.directAccountTx(ctx, tx, account)
		if e != nil || c.epoch != epoch || c.account.Role == TierOwner || c.account.Revision != membershipRevision {
			invalid = true
			return nil
		}
		actor, e := s.directAccountTx(ctx, tx, issuer)
		if e != nil || actor.epoch != issuerEpoch || !ManagesTier(actor.account.Role, c.account.Role) {
			invalid = true
			return nil
		}
		linked, e := porticoLinkedTx(ctx, tx, account)
		if e != nil {
			return e
		}
		if linked {
			invalid = true
			return nil
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM member_password_resets WHERE account_id=?`, account); e != nil {
			return e
		}
		if e = s.resetCredentialTx(ctx, tx, account, true, turnOff); e != nil {
			return e
		}
		c, e = s.directAccountTx(ctx, tx, account)
		if e != nil {
			return e
		}
		out, e = s.directSignInTx(WithVouchedDevice(ctx), tx, c, true)
		if e != nil {
			return e
		}
		if out.Session == nil {
			return ErrUnauthorized
		}
		return markResetSessionTx(ctx, tx, out.Session, "password")
	})
	if err != nil {
		return DirectSignIn{}, err
	}
	if invalid {
		return DirectSignIn{}, ErrUnauthorized
	}
	return out, nil
}

// The account's authenticator recovery codes remain separate from reset files.
func twoFactorResetNoticeTx(ctx context.Context, tx *sql.Tx, account string) error {
	var profile string
	if e := tx.QueryRowContext(ctx, `SELECT profile_id FROM accounts WHERE id=?`, account).Scan(&profile); e != nil {
		return e
	}
	_, e := notify.Raise(tx, time.Now().UnixMilli(), notify.Draft{Audience: notify.AudienceProfile, Scope: notify.ProfileScope("local", account, profile), Severity: notify.SeverityWarning, Source: notify.SourceSecurity, Category: "two_factor_reset", DedupeKey: "two_factor_reset:" + Token(), Title: "Two-factor sign-in was turned off", Body: "Two-factor sign-in was turned off while resetting access to your account. Set it up again to keep your account protected.", Arguments: map[string]string{"code": "two_factor_reset"}, Actions: []notify.Action{{Kind: "navigate", Label: "Set up two-factor sign-in", Target: &notify.NavigateTarget{View: "settings-security"}}}})
	return e
}

// The server's selected method survives a deleted/unlinked Portico account.
// Losing that link must never turn a reset-file proof into local password setup.
func (s *Service) ownerResetPorticoTx(ctx context.Context, tx *sql.Tx, _ string) (bool, error) {
	kind, err := SignInKindTx(ctx, tx)
	return kind == SignInPortico, err
}
