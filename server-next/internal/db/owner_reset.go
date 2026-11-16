package db

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// IssueOwnerReset stores a one-time code for owner recovery on either kind of server.
// publish puts the code where the owner can read it; it runs before the code is
// stored, so a failure in between leaves a file that opens nothing, never a code that works
// and that nobody was given.
//
// While a code is live (not used, not run out, not cancelled by wrong tries) nothing is
// written and nothing recorded: someone outside who keeps asking cannot replace the code the
// owner is typing, nor fill the activity record. Once it is over, a new one is written no
// sooner than every after the old one was made or ended. wait is every, or the time left
// before a new code can be written.
func (d *DB) IssueOwnerReset(ctx context.Context, codeHash string, expires time.Time, every time.Duration, now time.Time, publish func() error) (wait time.Duration, err error) {
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		wait = every
		var ownerID int64
		if err := tx.QueryRow(`SELECT id FROM users WHERE role = 'owner'`).Scan(&ownerID); errors.Is(err, sql.ErrNoRows) {
			return nil // no owner yet: the same answer, nothing to recover
		} else if err != nil {
			return err
		}
		var live bool
		var last int64
		err := tx.QueryRow(`SELECT used_at IS NULL AND expires_at > ?, max(created_at, ifnull(used_at, 0)) FROM auth_codes
			WHERE user_id = ? AND kind = 'owner_reset'`, now.UnixMilli(), ownerID).Scan(&live, &last)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if live {
			return nil
		}
		if since := now.Sub(time.UnixMilli(last)); err == nil && since >= 0 && since < every {
			wait = every - since
			return nil
		}
		if err := publish(); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM auth_codes WHERE user_id = ? AND kind = 'owner_reset'`, ownerID); err != nil {
			return err
		}
		if err := insertCode(tx, Code{Kind: "owner_reset", Hash: codeHash, UserID: ownerID, ExpiresAt: expires}, now); err != nil {
			return err
		}
		return audit(tx, ownerID, "owner.reset_code_written", "", "to a file in the server's data folder, asked for at the sign-in page", now)
	})
	return wait, err
}

// RedeemOwnerReset chooses a local owner's password. A Portico Account owner has no
// password here and instead gets one server session from OwnerResetSession.
func (d *DB) RedeemOwnerReset(ctx context.Context, codeHash, newHash string, turnOffTwoStep bool, limits []Limit, now time.Time) error {
	_, err := d.redeemOwnerReset(ctx, codeHash, newHash, turnOffTwoStep, nil, "", limits, now)
	return err
}

// OwnerResetSession admits the existing Portico Account owner on their main profile.
// Disk proof changes neither the server's sign-in kind nor any Portico Account link.
func (d *DB) OwnerResetSession(ctx context.Context, codeHash, address string, session NewSession, limits []Limit, now time.Time) (SessionState, error) {
	return d.redeemOwnerReset(ctx, codeHash, "", false, &session, address, limits, now)
}

func (d *DB) redeemOwnerReset(ctx context.Context, codeHash, newHash string, turnOffTwoStep bool,
	session *NewSession, address string, limits []Limit, now time.Time) (SessionState, error) {
	var out SessionState
	var refused error
	stamp, err := d.writeSessionsStamped(ctx, func(tx *sql.Tx, c *changes) error {
		refused = nil
		if wait, err := limitWait(ctx, tx, now, limits); err != nil {
			return err
		} else if wait > 0 {
			refused = &LimitedError{Wait: wait}
			return nil
		}
		at := now.UnixMilli()
		var ownerID int64
		// Even when no code is pending, compare two fixed-length digests and take the
		// same refusal path. No comparison stops at the first differing character.
		pending := "0000000000000000000000000000000000000000000000000000000000000000"
		var kind string
		var linked bool
		err := tx.QueryRow(`SELECT u.id, c.code_hash,
            ifnull((SELECT value FROM settings WHERE key = ?), 'local'), u.portico_account_id IS NOT NULL
            FROM users u JOIN auth_codes c ON c.user_id = u.id
            WHERE u.role = 'owner' AND u.state = 'active' AND u.disabled_at IS NULL
            AND c.kind = 'owner_reset' AND c.used_at IS NULL AND c.expires_at > ?`, SignInKindKey, at).
			Scan(&ownerID, &pending, &kind, &linked)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		matches := subtle.ConstantTimeCompare([]byte(pending), []byte(codeHash)) == 1
		correctKind := kind == "local" && !linked && session == nil || kind == SignInPortico && linked && session != nil
		if !matches || err != nil || !correctKind {
			// The count belongs to the code, across every caller and both routes.
			// Leave a cancelled row so asking for a new file still waits a minute.
			if _, err := tx.Exec(`UPDATE auth_codes SET attempts = attempts + 1,
                used_at = CASE WHEN attempts + 1 >= ? THEN ? ELSE used_at END
                WHERE code_hash = ? AND kind = 'owner_reset' AND used_at IS NULL AND expires_at > ?`,
				resetAttempts, at, pending, at); err != nil {
				return err
			}
			refused = ErrBadCredentials
			return limitFail(tx, now, limits)
		}
		if _, err := redeemCode(tx, "owner_reset", codeHash, now); err != nil {
			return err
		}
		if err := revoke(tx, c, now, "owner_reset", `user_id = ?`, ownerID); err != nil {
			return err
		}
		// Pending TV approvals and second-step grants cannot revive an ended sign-in.
		// Keep the spent reset row for the cooldown and the authenticator recovery codes.
		if _, err := tx.Exec(`DELETE FROM auth_codes WHERE user_id = ? AND code_hash <> ? AND kind <> 'recovery'`, ownerID, codeHash); err != nil {
			return err
		}
		if session == nil {
			if _, err := tx.Exec(`UPDATE users SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?`, newHash, at, at, ownerID); err != nil {
				return err
			}
			if turnOffTwoStep {
				if err := clearTwoFactor(tx, ownerID, now); err != nil {
					return err
				}
			}
			return audit(tx, ownerID, "owner.password_reset", "", "with the code from the file in the server's data folder", now)
		}
		if _, err := startSession(tx, ownerID, *session, now); err != nil {
			return err
		}
		// The file proves entry to the main profile once; its PIN is left intact.
		if _, err := tx.Exec(`UPDATE sessions SET profile_id = (SELECT id FROM profiles WHERE user_id = ? AND is_primary = 1)
            WHERE id = ?`, ownerID, session.ID); err != nil {
			return err
		}
		detail := fmt.Sprintf("Entered with the reset file; previous owner sessions signed out; device %q (%s); address %s",
			session.Device.Name, session.Device.InstallationID, address)
		if err := audit(tx, ownerID, "owner.reset_file_sign_in", "", detail, now); err != nil {
			return err
		}
		if _, _, err := sendNotice(tx, NewNotice{To: ToOwner, Kind: "owner_reset", Now: now,
			Body: "This server was entered with the reset file. Previous owner sessions were signed out."}); err != nil {
			return err
		}
		out, err = sessionState(ctx, tx, session.ID)
		return err
	})
	if err == nil {
		err = refused
	}
	out.Stamp = stamp
	return out, err
}
