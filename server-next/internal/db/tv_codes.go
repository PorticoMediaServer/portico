package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Signing in on a TV: the TV shows a short code and holds a long secret. A signed-in phone
// looks the code up and approves it; the TV, asking with its secret, is then given a
// session. The two are separate rows of auth_codes so each is used exactly once: one
// decision per code, one session per approval.

type tvCode struct {
	Poll   string     `json:"poll"`
	Device DeviceInfo `json:"device"`
}

type tvPoll struct {
	Device   DeviceInfo `json:"device"`
	Decision string     `json:"decision,omitempty"`
	Profile  int64      `json:"profile,omitempty"`
}

// TVRequest is a TV asking to be signed in.
type TVRequest struct {
	// CodeHash is the hash of the code shown on screen; PollHash of the secret the TV keeps.
	CodeHash, PollHash string
	Device             DeviceInfo
	ExpiresAt          time.Time
}

// StartTVSignIn stores a TV's request until it is decided or expires.
func (d *DB) StartTVSignIn(ctx context.Context, r TVRequest, now time.Time) error {
	code, _ := json.Marshal(tvCode{Poll: r.PollHash, Device: r.Device})
	poll, _ := json.Marshal(tvPoll{Device: r.Device})
	return d.write(ctx, User, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM auth_codes WHERE expires_at < ?`, now.Add(-24*time.Hour).UnixMilli()); err != nil {
			return err
		}
		if err := insertCode(tx, Code{Kind: "tv_code", Hash: r.CodeHash, Payload: string(code), ExpiresAt: r.ExpiresAt}, now); err != nil {
			return err
		}
		return insertCode(tx, Code{Kind: "tv_poll", Hash: r.PollHash, Payload: string(poll), ExpiresAt: r.ExpiresAt}, now)
	})
}

// TVSignIn returns the device waiting behind a code, for the approver to look at. A code
// that is unknown, decided or expired is ErrNotFound.
func (d *DB) TVSignIn(ctx context.Context, codeHash string, now time.Time) (DeviceInfo, time.Time, error) {
	var payload string
	var expires int64
	err := d.read.QueryRowContext(ctx, `SELECT payload, expires_at FROM auth_codes
		WHERE code_hash = ? AND kind = 'tv_code' AND used_at IS NULL AND expires_at > ?`, codeHash, now.UnixMilli()).Scan(&payload, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return DeviceInfo{}, time.Time{}, ErrNotFound
	}
	if err != nil {
		return DeviceInfo{}, time.Time{}, err
	}
	var code tvCode
	err = json.Unmarshal([]byte(payload), &code)
	return code.Device, time.UnixMilli(expires), err
}

// DecideTVSignIn approves or denies the TV behind a code, once. Approving signs the TV in
// as the approver, on the profile the approver is using. A code that is unknown, already
// decided or expired is ErrNotFound.
func (d *DB) DecideTVSignIn(ctx context.Context, codeHash string, userID, profileID int64, approve bool, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		used, err := redeemCode(tx, "tv_code", codeHash, now)
		if errors.Is(err, ErrBadCredentials) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var code tvCode
		if err := json.Unmarshal([]byte(used.Payload), &code); err != nil {
			return err
		}
		decision := tvPoll{Device: code.Device, Decision: "denied"}
		if approve {
			decision.Decision, decision.Profile = "approved", profileID
		}
		payload, _ := json.Marshal(decision)
		_, err = tx.Exec(`UPDATE auth_codes SET user_id = ?, payload = ? WHERE code_hash = ? AND kind = 'tv_poll' AND used_at IS NULL`,
			userID, string(payload), code.Poll)
		return err
	})
}

// What a TV is told when it asks whether it has been signed in yet.
var (
	// ErrPending: nobody has decided yet. Ask again shortly.
	ErrPending = errors.New("not decided yet")
	// ErrDenied: the request was denied, or whoever approved it can no longer sign in.
	ErrDenied = errors.New("denied")
)

// CollectTVSignIn gives the TV its session once its request has been approved. The answer
// is given once: of many askers with the same secret, one gets the session. Before a
// decision it is ErrPending, after a denial ErrDenied, and for a secret that is unknown,
// expired or already collected, ErrNotFound.
func (d *DB) CollectTVSignIn(ctx context.Context, pollHash string, session NewSession, now time.Time) (SessionState, error) {
	var out SessionState
	var refused error
	stamp, err := d.writeSessionsStamped(ctx, func(tx *sql.Tx, _ *changes) error {
		refused = nil
		var payload string
		err := tx.QueryRow(`SELECT payload FROM auth_codes WHERE code_hash = ? AND kind = 'tv_poll' AND used_at IS NULL AND expires_at > ?`,
			pollHash, now.UnixMilli()).Scan(&payload)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var poll tvPoll
		if err := json.Unmarshal([]byte(payload), &poll); err != nil {
			return err
		}
		if poll.Decision == "" {
			return ErrPending
		}
		used, err := redeemCode(tx, "tv_poll", pollHash, now)
		if err != nil {
			return err
		}
		// The approver must still be able to sign in, and the profile must still be theirs.
		var ok bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM users u JOIN profiles p ON p.user_id = u.id
			WHERE u.id = ? AND u.disabled_at IS NULL AND p.id = ?)`, used.UserID, poll.Profile).Scan(&ok); err != nil {
			return err
		}
		if poll.Decision != "approved" || !ok {
			refused = ErrDenied
			return nil
		}
		session.Device, session.ProfileID = poll.Device, poll.Profile
		out, err = startSession(tx, used.UserID, session, now)
		return err
	})
	if err == nil {
		err = refused
	}
	out.Stamp = stamp
	return out, err
}
