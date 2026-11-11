package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// A Google Cast receiver is signed in for playback only: a phone that casts makes a short code
// for its profile, and the receiver, a page Google's device loads, trades it for a sign-in that
// can play what that profile may see and nothing else (sessions.scope 'cast'). It lasts a day
// without use.

// CastIdle is how long a Cast receiver's sign-in lasts without being renewed.
const CastIdle = 24 * time.Hour

type castCode struct {
	ProfileID int64 `json:"profileId"`
}

// StartCast stores a Cast pairing code for one profile.
func (d *DB) StartCast(ctx context.Context, codeHash string, userID, profileID int64, expires, now time.Time) error {
	payload, _ := json.Marshal(castCode{ProfileID: profileID})
	return d.CreateCode(ctx, Code{Kind: "cast", Hash: codeHash, UserID: userID, Payload: string(payload), ExpiresAt: expires}, now)
}

// RedeemCast uses a pairing code up and signs the receiver in on the code's profile, for
// playback only. A code that is unknown, used or expired, or whose account can no longer sign
// in, is ErrBadCredentials.
func (d *DB) RedeemCast(ctx context.Context, codeHash string, session NewSession, now time.Time) (SessionState, error) {
	var out SessionState
	stamp, err := d.writeSessionsStamped(ctx, func(tx *sql.Tx, _ *changes) error {
		used, err := redeemCode(tx, "cast", codeHash, now)
		if err != nil {
			return err
		}
		var code castCode
		if err := json.Unmarshal([]byte(used.Payload), &code); err != nil {
			return err
		}
		var ok bool
		err = tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM profiles p JOIN users u ON u.id = p.user_id
			WHERE p.id = ? AND u.id = ? AND u.disabled_at IS NULL)`, code.ProfileID, used.UserID).Scan(&ok)
		if err != nil {
			return err
		}
		if !ok {
			return ErrBadCredentials
		}
		session.ExpiresAt = now.Add(CastIdle)
		if _, err := startSession(tx, used.UserID, session, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE sessions SET scope = 'cast', profile_id = ? WHERE id = ?`, code.ProfileID, session.ID); err != nil {
			return err
		}
		out, err = sessionState(ctx, tx, session.ID)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		err = ErrBadCredentials
	}
	out.Stamp = stamp
	return out, err
}
