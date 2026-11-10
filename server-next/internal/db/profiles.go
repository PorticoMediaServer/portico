package db

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// MaxProfiles is how many profiles one account can have, its main profile included, unless
// the owner lowered the number for that account.
const MaxProfiles = 8

// ErrPrimaryProfile: the account's main profile cannot be deleted.
var ErrPrimaryProfile = errors.New("the main profile cannot be deleted")

// ProfileLimitError: the account already has as many profiles as it may. Most is that number.
type ProfileLimitError struct{ Most int }

func (e *ProfileLimitError) Error() string { return "the account has its full number of profiles" }

// Profile is a viewing profile of an account.
type Profile struct {
	ID       int64
	PublicID string
	Name     string
	Color    string
	Primary  bool
	Position int
	// PINHash is empty when the profile has no PIN.
	PINHash string
	// AccessChangedAt moves whenever what the profile may see changes.
	AccessChangedAt int64
	// AvatarAt is when the profile's picture was last set, or 0 if it has none.
	AvatarAt int64
}

const profileColumns = `id, public_id, name, color, is_primary, position, ifnull(pin_hash, ''), access_changed_at, ifnull(avatar_at, 0)`

func scanProfile(row interface{ Scan(...any) error }) (Profile, error) {
	var p Profile
	err := row.Scan(&p.ID, &p.PublicID, &p.Name, &p.Color, &p.Primary, &p.Position, &p.PINHash, &p.AccessChangedAt, &p.AvatarAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return p, err
}

// Profiles lists an account's profiles in the order its people arranged them.
func (d *DB) Profiles(ctx context.Context, userID int64) ([]Profile, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+profileColumns+` FROM profiles WHERE user_id = ? ORDER BY is_primary DESC, position, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Profile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Profile returns one of the account's profiles by public id. Another account's profile is ErrNotFound.
func (d *DB) Profile(ctx context.Context, userID int64, publicID string) (Profile, error) {
	return scanProfile(d.read.QueryRowContext(ctx, `SELECT `+profileColumns+` FROM profiles WHERE user_id = ? AND public_id = ?`, userID, publicID))
}

// ProfileByID returns one of the account's profiles by internal id.
func (d *DB) ProfileByID(ctx context.Context, userID, id int64) (Profile, error) {
	return scanProfile(d.read.QueryRowContext(ctx, `SELECT `+profileColumns+` FROM profiles WHERE user_id = ? AND id = ?`, userID, id))
}

// accessChanged records that what the matching profiles may see has changed: library
// access, a rating limit, blocked labels or the account's role. Each profile's marker moves,
// and its open apps are told on the changes feed to throw away what they have cached.
func accessChanged(tx *sql.Tx, now time.Time, where string, args ...any) error {
	// The marker is a time, pushed on by one when two changes share a millisecond.
	rows, err := tx.Query(`UPDATE profiles SET access_changed_at = max(access_changed_at + 1, ?) WHERE `+where+` RETURNING id`,
		append([]any{now.UnixMilli()}, args...)...)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := appendChange(tx, Change{At: now.UnixMilli(), Topic: TopicAccess, Op: OpUpdated, RefID: id, ProfileID: id}); err != nil {
			return err
		}
	}
	return nil
}

// NewProfile is a profile to add to an account.
type NewProfile struct {
	UserID  int64
	Name    string
	Color   string
	PINHash string
	// IdempotencyKey and RequestHash make a repeated request return the profile it made the first time.
	IdempotencyKey string
	RequestHash    string
	Now            time.Time
}

// CreateProfile adds a profile, up to MaxProfiles per account.
func (d *DB) CreateProfile(ctx context.Context, n NewProfile) (Profile, error) {
	var out Profile
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		ref, err := createdBefore(tx, n.UserID, n.IdempotencyKey, n.RequestHash)
		if err != nil {
			return err
		}
		if ref != "" {
			out, err = scanProfile(tx.QueryRow(`SELECT `+profileColumns+` FROM profiles WHERE user_id = ? AND public_id = ?`, n.UserID, ref))
			return err
		}
		var count, last, most int
		if err := tx.QueryRow(`SELECT count(*), ifnull(max(position), 0), (SELECT ifnull(max_profiles, ?) FROM users WHERE id = ?)
			FROM profiles WHERE user_id = ?`, MaxProfiles, n.UserID, n.UserID).Scan(&count, &last, &most); err != nil {
			return err
		}
		// A number lowered after the profiles were made removes none of them.
		if count >= most {
			return &ProfileLimitError{Most: max(most, count)}
		}
		at := n.Now.UnixMilli()
		out, err = scanProfile(tx.QueryRow(`INSERT INTO profiles (public_id, user_id, name, color, position, pin_hash, access_changed_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, nullif(?, ''), ?, ?, ?) RETURNING `+profileColumns, NewID("prf"), n.UserID, n.Name, n.Color, last+1, n.PINHash, at, at, at))
		if err != nil {
			return err
		}
		return rememberCreated(tx, n.UserID, n.IdempotencyKey, n.RequestHash, out.PublicID, n.Now)
	})
	return out, err
}

// ProfileEdit changes a profile. A nil field is left as it is. The last edit wins.
type ProfileEdit struct {
	UserID   int64
	PublicID string
	Name     *string
	Color    *string
	// PINHash sets the PIN; a pointer to "" removes it.
	PINHash *string
	// SessionID is the session making the edit, which stays on the profile when its PIN changes.
	SessionID int64
	// Proof is set when the edit is allowed by proving the account's password again, not
	// by being on the main profile: a forgotten PIN being replaced.
	Proof *PasswordProof
	Now   time.Time
}

// UpdateProfile edits a profile. When a PIN is set or changed, every other device on that
// profile is sent back to the profile chooser, where the new PIN is asked for; the device
// making the change stays where it is. A Proof that fails is ErrBadCredentials,
// ErrBadSecondStep or a *LimitedError.
func (d *DB) UpdateProfile(ctx context.Context, e ProfileEdit) (Profile, error) {
	var out Profile
	var refused error
	err := d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		refused = nil
		if e.Proof != nil {
			if ok, err := proven(tx, e.Now, *e.Proof, &refused); err != nil || !ok {
				return err
			}
		}
		p, err := scanProfile(tx.QueryRow(`SELECT `+profileColumns+` FROM profiles WHERE user_id = ? AND public_id = ?`, e.UserID, e.PublicID))
		if err != nil {
			return err
		}
		if e.Name != nil {
			p.Name = *e.Name
		}
		if e.Color != nil {
			p.Color = *e.Color
		}
		if e.PINHash != nil {
			p.PINHash = *e.PINHash
			// Guesses at the old PIN say nothing about the new one, and this is how someone
			// kept waiting by another person's wrong guesses gets back in at once.
			if _, err := tx.Exec(`DELETE FROM auth_limits WHERE bucket = ? AND key_hash = ?`, ProfilePINBucket, limitKey("profile:"+strconv.FormatInt(p.ID, 10))); err != nil {
				return err
			}
			if p.PINHash != "" {
				if err := markStale(tx, c, `profile_id = ? AND id <> ?`, p.ID, e.SessionID); err != nil {
					return err
				}
				if _, err := tx.Exec(`UPDATE sessions SET profile_id = NULL WHERE profile_id = ? AND id <> ?`, p.ID, e.SessionID); err != nil {
					return err
				}
			}
		}
		out, err = scanProfile(tx.QueryRow(`UPDATE profiles SET name = ?, color = ?, pin_hash = nullif(?, ''), updated_at = ? WHERE id = ? RETURNING `+profileColumns,
			p.Name, p.Color, p.PINHash, e.Now.UnixMilli(), p.ID))
		return err
	})
	if err == nil {
		err = refused
	}
	return out, err
}

// DeleteProfile deletes a profile and everything saved for it. Devices on it go back to
// the profile chooser. Deleting one that is already gone changes nothing.
func (d *DB) DeleteProfile(ctx context.Context, userID int64, publicID string, now time.Time) error {
	return d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		p, err := scanProfile(tx.QueryRow(`SELECT `+profileColumns+` FROM profiles WHERE user_id = ? AND public_id = ?`, userID, publicID))
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if p.Primary {
			return ErrPrimaryProfile
		}
		if err := markStale(tx, c, `profile_id = ?`, p.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE sessions SET profile_id = NULL WHERE profile_id = ?`, p.ID); err != nil {
			return err
		}
		_, err = tx.Exec(`DELETE FROM profiles WHERE id = ?`, p.ID)
		return err
	})
}

// ProfilePINBucket holds the count of a profile's wrong PINs on all devices together, under
// the key "profile:<id>".
const ProfilePINBucket = "pin_profile"

// ErrBadPIN: the PIN is wrong.
var ErrBadPIN = errors.New("wrong PIN")

// ProfileSelection binds a session to a profile. If the profile has a PIN, it has already
// been compared, outside any transaction, against CheckedPINHash.
type ProfileSelection struct {
	UserID         int64
	SessionID      int64
	ProfileID      int64
	CheckedPINHash string
	PINOK          bool
	Limits         []Limit
	// Remembered: the device showed that it remembers the PIN instead of the PIN being
	// typed. Wrong guesses are not at stake, and the session may not manage the account
	// until the PIN is typed.
	Remembered bool
	// ManageFor, when not zero, is how long a typed PIN lets a device that remembers PINs
	// manage the account from the main profile.
	ManageFor time.Duration
	Now       time.Time
}

// SelectProfile makes the profile the session's own. Tokens the session held for another
// profile stop working. A wrong PIN is counted and is ErrBadPIN; while the profile must
// wait after wrong PINs the answer is a *LimitedError, even for the right PIN.
func (d *DB) SelectProfile(ctx context.Context, s ProfileSelection) (SessionState, error) {
	var out SessionState
	var refused error
	stamp, err := d.writeSessionsStamped(ctx, func(tx *sql.Tx, c *changes) error {
		refused = nil
		var pin string
		var manageUntil any
		err := tx.QueryRow(`SELECT ifnull(pin_hash, '') FROM profiles WHERE id = ? AND user_id = ?`, s.ProfileID, s.UserID).Scan(&pin)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if pin != "" && s.Remembered {
			if !s.PINOK || pin != s.CheckedPINHash {
				refused = ErrBadPIN
				return nil
			}
			manageUntil = s.Now.UnixMilli()
		} else if pin != "" {
			if s.ManageFor > 0 {
				manageUntil = s.Now.Add(s.ManageFor).UnixMilli()
			}
			wait, err := limitWait(ctx, tx, s.Now, s.Limits)
			if err != nil {
				return err
			}
			if wait > 0 {
				refused = &LimitedError{Wait: wait}
				return nil
			}
			if !s.PINOK || pin != s.CheckedPINHash {
				refused = ErrBadPIN
				return limitFail(tx, s.Now, s.Limits)
			}
			if err := limitClear(tx, s.Limits); err != nil {
				return err
			}
		}
		res, err := tx.Exec(`UPDATE sessions SET profile_id = ?, manage_until_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
			s.ProfileID, manageUntil, s.SessionID, s.UserID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			refused = ErrBadCredentials
			return nil
		}
		c.stale = append(c.stale, s.SessionID)
		out, err = sessionState(ctx, tx, s.SessionID)
		return err
	})
	if err == nil {
		err = refused
	}
	out.Stamp = stamp
	return out, err
}
