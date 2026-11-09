package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"sync"
	"time"
)

// ErrBadCredentials is the one answer for every failed proof: wrong password, unknown
// username, disabled account, a refresh token that is no longer good.
var ErrBadCredentials = errors.New("credentials refused")

// accessTokenLife is the longest an access token can outlive the change that should end it.
// It must not be shorter than the lifetime the API mints them with.
const accessTokenLife = 16 * time.Minute

// tokenMarks is the in-memory note of sessions whose earlier access tokens must be refused:
// a revoked session refuses all of them, a session whose profile or role changed refuses
// those minted before the change. Access tokens are checked against this and nothing else,
// so a request never reads the database to be let in.
//
// "Before" is exact, not a matter of clocks. Every transaction that touches sessions is
// numbered as it commits, by the one writer, and a token carries the number that was current
// when its claims were read. A session's mark is the number of the change; a token with a
// lower number was read before it.
type tokenMarks struct {
	mu sync.RWMutex
	// boot names this run of the server. Marks are not kept across a restart, so a token
	// from an earlier run is refused: each app renews once, and the renewal reads what is
	// true now from the database.
	boot  int64
	seq   int64
	marks map[int64]tokenMark
}

type tokenMark struct {
	refuseBelow int64
	at          time.Time
}

// TokenStamp is what a token carries so it can be checked against the marks.
type TokenStamp struct{ Boot, Seq int64 }

// changes collects what one transaction did to sessions, to be applied once it has committed.
type changes struct {
	revoked []int64
	stale   []int64
}

// apply runs on the writer, straight after the commit. It returns the stamp for a token
// minted from what that transaction read.
func (m *tokenMarks) apply(c changes) TokenStamp {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(c.revoked) > 0 || len(c.stale) > 0 {
		if m.marks == nil {
			m.marks = map[int64]tokenMark{}
		}
		now := time.Now()
		for id, mark := range m.marks {
			if now.Sub(mark.at) > accessTokenLife {
				delete(m.marks, id)
			}
		}
		m.seq++
		for _, id := range c.stale {
			if m.marks[id].refuseBelow != math.MaxInt64 {
				m.marks[id] = tokenMark{m.seq, now}
			}
		}
		for _, id := range c.revoked {
			m.marks[id] = tokenMark{math.MaxInt64, now}
		}
	}
	return TokenStamp{m.boot, m.seq}
}

// TokenRefused reports whether an access token of this session carrying this stamp must be
// refused although it has not expired.
func (d *DB) TokenRefused(sessionID int64, stamp TokenStamp) bool {
	d.marks.mu.RLock()
	defer d.marks.mu.RUnlock()
	return stamp.Boot != d.marks.boot || stamp.Seq < d.marks.marks[sessionID].refuseBelow
}

// writeSessions is write for a transaction that may end sessions or change what their
// tokens should say. The marks change on the writer straight after the commit, so once a
// revoke has returned no request with that session's token is let in, and a token minted
// by a request that overlapped a change is refused if it read the state from before it.
func (d *DB) writeSessions(ctx context.Context, fn func(tx *sql.Tx, c *changes) error) error {
	_, err := d.writeSessionsStamped(ctx, fn)
	return err
}

// writeSessionsStamped is writeSessions for a transaction whose result a token is minted
// from. The stamp goes into that token.
func (d *DB) writeSessionsStamped(ctx context.Context, fn func(tx *sql.Tx, c *changes) error) (TokenStamp, error) {
	var c changes
	var stamp TokenStamp
	err := d.writeThen(ctx, User, func(tx *sql.Tx) error {
		c = changes{}
		return fn(tx, &c)
	}, func() { stamp = d.marks.apply(c) })
	return stamp, err
}

// revoke ends every live session matching where.
func revoke(tx *sql.Tx, c *changes, now time.Time, reason, where string, args ...any) error {
	rows, err := tx.Query(`UPDATE sessions SET revoked_at = ?, revoked_reason = ? WHERE revoked_at IS NULL AND `+where+` RETURNING id`,
		append([]any{now.UnixMilli(), reason}, args...)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		c.revoked = append(c.revoked, id)
	}
	return rows.Err()
}

// markStale makes the access tokens already minted for the matching sessions stop working,
// so each app renews and is given a token that says what is true now.
func markStale(tx *sql.Tx, c *changes, where string, args ...any) error {
	rows, err := tx.Query(`SELECT id FROM sessions WHERE revoked_at IS NULL AND `+where, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		c.stale = append(c.stale, id)
	}
	return rows.Err()
}

// NewSessionID returns the id for a new sign-in. It is random, not the next number, because
// it is part of the refresh token, which is minted before the row is written.
func NewSessionID() int64 {
	var b [8]byte
	rand.Read(b[:])
	return int64(binary.BigEndian.Uint64(b[:])>>2) + 1
}

// SessionState is everything needed to mint tokens for a session and describe it to its app.
type SessionState struct {
	ID         int64
	PublicID   string
	Generation int64
	ExpiresAt  time.Time
	// RenewedAt is when the current refresh token replaced the previous one.
	RenewedAt time.Time
	// Stamp goes into a token minted from this state.
	Stamp TokenStamp

	UserID       int64
	UserPublicID string
	UserRole     string

	// ProfileID is 0 until a profile is selected.
	ProfileID       int64
	ProfilePublicID string
	ProfilePrimary  bool
	// PrimaryHasPIN: the account's main profile is protected by a PIN, so a sign-in that has
	// not selected it may not manage the account.
	PrimaryHasPIN bool

	DevicePublicID string
	InstallationID string

	revoked, disabled         bool
	refreshHash, previousHash string
	previousRequestID         string
}

func sessionState(ctx context.Context, q queryer, id int64) (SessionState, error) {
	var s SessionState
	var expires, renewed int64
	err := q.QueryRowContext(ctx, `
		SELECT s.id, s.public_id, s.token_generation, s.expires_at, ifnull(s.previous_at, s.created_at),
			s.revoked_at IS NOT NULL, s.refresh_hash, ifnull(s.previous_refresh_hash, ''), ifnull(s.previous_request_id, ''),
			u.id, u.public_id, u.role, u.disabled_at IS NOT NULL,
			ifnull(p.id, 0), ifnull(p.public_id, ''), ifnull(p.is_primary, 0),
			EXISTS (SELECT 1 FROM profiles pp WHERE pp.user_id = u.id AND pp.is_primary = 1 AND pp.pin_hash IS NOT NULL),
			d.public_id, d.client_id
		FROM sessions s JOIN users u ON u.id = s.user_id JOIN devices d ON d.id = s.device_id
		LEFT JOIN profiles p ON p.id = s.profile_id
		WHERE s.id = ?`, id).Scan(&s.ID, &s.PublicID, &s.Generation, &expires, &renewed,
		&s.revoked, &s.refreshHash, &s.previousHash, &s.previousRequestID,
		&s.UserID, &s.UserPublicID, &s.UserRole, &s.disabled,
		&s.ProfileID, &s.ProfilePublicID, &s.ProfilePrimary, &s.PrimaryHasPIN,
		&s.DevicePublicID, &s.InstallationID)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	s.ExpiresAt, s.RenewedAt = time.UnixMilli(expires), time.UnixMilli(renewed)
	return s, err
}

// DeviceInfo is what an app says about the install it runs on.
type DeviceInfo struct {
	// InstallationID is the id the app made for itself and keeps for as long as it is installed.
	InstallationID string
	Name           string
	Platform       string
	App            string
	AppVersion     string
}

// NewSession is the session a successful proof starts.
type NewSession struct {
	ID          int64
	RefreshHash string
	ExpiresAt   time.Time
	Device      DeviceInfo
	RemoteAddr  string
	// ProfileID starts the session on a profile; 0 starts it at the profile chooser.
	ProfileID int64
}

// startSession registers the device and inserts the session, for a user already proven.
func startSession(tx *sql.Tx, userID int64, n NewSession, now time.Time) (SessionState, error) {
	at := now.UnixMilli()
	var deviceID int64
	// A name someone gave the device is kept; the app's own facts are brought up to date.
	err := tx.QueryRow(`INSERT INTO devices (public_id, client_id, name, platform, app, app_version, created_at, last_seen_at)
		VALUES (?, ?, ?, nullif(?, ''), nullif(?, ''), nullif(?, ''), ?, ?)
		ON CONFLICT (client_id) DO UPDATE SET platform = excluded.platform, app = excluded.app,
			app_version = excluded.app_version, last_seen_at = excluded.last_seen_at
		RETURNING id`, NewID("dev"), n.Device.InstallationID, n.Device.Name, n.Device.Platform, n.Device.App, n.Device.AppVersion, at, at).Scan(&deviceID)
	if err != nil {
		return SessionState{}, err
	}
	// Sessions that ended long ago are of no more use to the session list.
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ? AND min(expires_at, ifnull(revoked_at, expires_at)) < ?`,
		userID, now.Add(-endedSessionsKept).UnixMilli()); err != nil {
		return SessionState{}, err
	}
	if _, err := tx.Exec(`INSERT INTO sessions (id, public_id, user_id, profile_id, device_id, refresh_hash, created_at, expires_at, last_used_at, remote_addr)
		VALUES (?, ?, ?, nullif(?, 0), ?, ?, ?, ?, ?, nullif(?, ''))`,
		n.ID, NewID("ses"), userID, n.ProfileID, deviceID, n.RefreshHash, at, n.ExpiresAt.UnixMilli(), at, n.RemoteAddr); err != nil {
		return SessionState{}, err
	}
	return sessionState(context.Background(), tx, n.ID)
}

// SignInAttempt is a password sign-in whose password has already been compared, outside any
// transaction, against CheckedHash.
type SignInAttempt struct {
	// UserID is 0 for a username nobody has.
	UserID      int64
	CheckedHash string
	PasswordOK  bool
	Limits      []Limit
	Session     NewSession
	// Challenge is stored, and no session started, when the account has two-step sign-in.
	Challenge Code
	Now       time.Time
}

// SignIn records the attempt and, if it is good, starts a session. A wrong guess is counted
// in the same commit. The answer is ErrBadCredentials or a *LimitedError for every refusal,
// whether or not the account exists. For an account with two-step sign-in the password is
// only the first step: challenged is true, the challenge is stored and there is no session yet.
func (d *DB) SignIn(ctx context.Context, a SignInAttempt) (out SessionState, challenged bool, err error) {
	var refused error
	var stamp TokenStamp
	stamp, err = d.writeSessionsStamped(ctx, func(tx *sql.Tx, _ *changes) error {
		refused, challenged = nil, false
		ok, err := proofStands(tx, a.Now, a.Limits, a.PasswordOK, a.UserID, a.CheckedHash, &refused)
		if err != nil || !ok {
			return err
		}
		if err := tx.QueryRow(`SELECT two_factor_enabled_at IS NOT NULL FROM users WHERE id = ?`, a.UserID).Scan(&challenged); err != nil {
			return err
		}
		if challenged {
			// One sign-in at a time is waiting for its second step.
			if _, err := tx.Exec(`DELETE FROM auth_codes WHERE user_id = ? AND kind = ?`, a.UserID, a.Challenge.Kind); err != nil {
				return err
			}
			a.Challenge.UserID = a.UserID
			return insertCode(tx, a.Challenge, a.Now)
		}
		out, err = startSession(tx, a.UserID, a.Session, a.Now)
		return err
	})
	if err == nil {
		err = refused
	}
	out.Stamp = stamp
	return out, challenged, err
}

// proofStands settles a password proof inside the transaction that acts on it. It refuses
// while the source must still wait, even if the guess was right, so guesses sent together
// gain nothing. It checks the account is not disabled and its password is still the one the
// proof was made against. A failure is counted; a success clears the count.
// When it returns false with a nil error, *refused says why and the transaction should commit.
func proofStands(tx *sql.Tx, now time.Time, limits []Limit, ok bool, userID int64, checkedHash string, refused *error) (bool, error) {
	wait, err := limitWait(context.Background(), tx, now, limits)
	if err != nil {
		return false, err
	}
	if wait > 0 {
		*refused = &LimitedError{Wait: wait}
		return false, nil
	}
	if ok {
		var hash sql.NullString
		var disabled bool
		err := tx.QueryRow(`SELECT password_hash, disabled_at IS NOT NULL FROM users WHERE id = ?`, userID).Scan(&hash, &disabled)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		ok = err == nil && !disabled && hash.Valid && hash.String == checkedHash
	}
	if !ok {
		*refused = ErrBadCredentials
		return false, limitFail(tx, now, limits)
	}
	return true, limitClear(tx, limits)
}

// Renewal is a refresh token presented for a new pair of tokens. The token itself was
// checked to be one this server minted; whether it is still good is decided here.
type Renewal struct {
	SessionID  int64
	Generation int64
	// Hash is the presented token's hash; NextHash is the hash of the token that replaces it.
	Hash, NextHash string
	// RequestID is the id the app saved before sending, and sends again if the reply is lost.
	RequestID      string
	InstallationID string
	ExpiresAt      time.Time
	RemoteAddr     string
	Now            time.Time
}

// RenewSession rotates the refresh token. replayed is true when this is the same renewal
// sent again (the previous token with the request id it was renewed with): nothing changes
// and the caller answers with the same tokens as before. Any other use of a token that has
// been replaced means it leaked, so the session is revoked. Every refusal is ErrBadCredentials.
func (d *DB) RenewSession(ctx context.Context, r Renewal) (out SessionState, replayed bool, err error) {
	var refused error
	var stamp TokenStamp
	stamp, err = d.writeSessionsStamped(ctx, func(tx *sql.Tx, c *changes) error {
		refused, replayed = ErrBadCredentials, false
		s, err := sessionState(ctx, tx, r.SessionID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		// A request from another install is refused without touching the session.
		if s.revoked || s.disabled || !s.ExpiresAt.After(r.Now) || s.InstallationID != r.InstallationID {
			return nil
		}
		switch {
		case r.Generation == s.Generation && r.Hash == s.refreshHash:
			at := r.Now.UnixMilli()
			if _, err := tx.Exec(`UPDATE sessions SET refresh_hash = ?, token_generation = token_generation + 1,
					previous_refresh_hash = ?, previous_request_id = ?, previous_at = ?,
					expires_at = ?, last_used_at = ?, remote_addr = nullif(?, '') WHERE id = ?`,
				r.NextHash, r.Hash, r.RequestID, at, r.ExpiresAt.UnixMilli(), at, r.RemoteAddr, s.ID); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE devices SET last_seen_at = ? WHERE client_id = ?`, at, s.InstallationID); err != nil {
				return err
			}
		case r.Generation == s.Generation-1 && r.Hash == s.previousHash && r.RequestID == s.previousRequestID:
			replayed = true
		default:
			return revoke(tx, c, r.Now, "refresh_reuse", `id = ?`, s.ID)
		}
		refused = nil
		out, err = sessionState(ctx, tx, s.ID)
		return err
	})
	if err == nil {
		err = refused
	}
	out.Stamp = stamp
	return out, replayed, err
}

// SessionState returns a live session, for minting a token that says what is true now.
func (d *DB) SessionState(ctx context.Context, id int64, now time.Time) (SessionState, error) {
	s, err := sessionState(ctx, d.read, id)
	if err == nil && (s.revoked || s.disabled || !s.ExpiresAt.After(now)) {
		err = ErrNotFound
	}
	return s, err
}

// RevokeSession ends one session by its internal id (sign-out). Ending one already ended changes nothing.
func (d *DB) RevokeSession(ctx context.Context, id int64, reason string, now time.Time) error {
	return d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		return revoke(tx, c, now, reason, `id = ?`, id)
	})
}

// RevokeUserSession ends one of a user's sessions by its public id. Ending one that is
// already ended, or that is not this user's, changes nothing.
func (d *DB) RevokeUserSession(ctx context.Context, userID int64, publicID, reason string, now time.Time) error {
	return d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		return revoke(tx, c, now, reason, `user_id = ? AND public_id = ?`, userID, publicID)
	})
}

// RevokeOtherSessions ends every session of a user except the one asking: "sign out everywhere else".
func (d *DB) RevokeOtherSessions(ctx context.Context, userID, keepSessionID int64, reason string, now time.Time) error {
	return d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		return revoke(tx, c, now, reason, `user_id = ? AND id <> ?`, userID, keepSessionID)
	})
}

// endedSessionsKept is how long an ended session stays in the session list.
const endedSessionsKept = 30 * 24 * time.Hour

// SessionInfo is one row of a person's session list.
type SessionInfo struct {
	ID              int64
	PublicID        string
	DevicePublicID  string
	DeviceName      string
	ProfilePublicID string
	CreatedAt       time.Time
	LastUsedAt      time.Time
	ExpiresAt       time.Time
	// EndedAt is zero while the session is live.
	EndedAt    time.Time
	EndReason  string
	RemoteAddr string
}

// Sessions lists a user's live sessions and those that ended in the last 30 days, newest use first.
func (d *DB) Sessions(ctx context.Context, userID int64, now time.Time) ([]SessionInfo, error) {
	rows, err := d.read.QueryContext(ctx, `
		SELECT s.id, s.public_id, d.public_id, d.name, ifnull(p.public_id, ''), s.created_at, s.last_used_at, s.expires_at,
			ifnull(s.revoked_at, 0), ifnull(s.revoked_reason, ''), ifnull(s.remote_addr, '')
		FROM sessions s JOIN devices d ON d.id = s.device_id LEFT JOIN profiles p ON p.id = s.profile_id
		WHERE s.user_id = ? AND min(s.expires_at, ifnull(s.revoked_at, s.expires_at)) > ?
		ORDER BY s.last_used_at DESC, s.id`, userID, now.Add(-endedSessionsKept).UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var s SessionInfo
		var created, used, expires, revoked int64
		if err := rows.Scan(&s.ID, &s.PublicID, &s.DevicePublicID, &s.DeviceName, &s.ProfilePublicID, &created, &used, &expires,
			&revoked, &s.EndReason, &s.RemoteAddr); err != nil {
			return nil, err
		}
		s.CreatedAt, s.LastUsedAt, s.ExpiresAt = time.UnixMilli(created), time.UnixMilli(used), time.UnixMilli(expires)
		switch {
		case revoked != 0:
			s.EndedAt = time.UnixMilli(revoked)
		case !s.ExpiresAt.After(now):
			s.EndedAt, s.EndReason = s.ExpiresAt, "expired"
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
