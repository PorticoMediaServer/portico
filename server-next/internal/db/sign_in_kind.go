package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

// The kind of sign-in, and moving a server from one kind to the other. A switch keeps every
// account with its profiles and history; everyone is signed out, unused invitations are
// cancelled, and every account but the owner's waits to move until its person comes back with
// a code (or, moving to Portico Accounts, their old password) or the owner finishes the move.
// Start over instead removes every account but the owner's.

// MoveKey is the setting that holds a switch that still has people waiting: where it went and when.
const MoveKey = "sign_in_move"

// LeftMessageKey is the setting that holds until when this server, switched from Portico
// Accounts to local sign-in, still tries to tell Portico Services once that it has left.
const LeftMessageKey = "hosted.left_message_until"

// leftMessageTries is how long the "this server has left" message is tried.
const leftMessageTries = 7 * 24 * time.Hour

// OldPasswordWorks is how long after a switch to Portico Accounts a waiting person's old
// password still proves who they are, so they can link their Portico Account.
const OldPasswordWorks = 30 * 24 * time.Hour

// Move is a switch with people still waiting.
type Move struct {
	// To is "local" or "portico".
	To string    `json:"to"`
	At time.Time `json:"-"`
	// AtMs is At as stored.
	AtMs int64 `json:"at"`
}

// ErrAccountWaiting: the password was right, but the account waits to move to Portico Accounts.
var ErrAccountWaiting = errors.New("the account is waiting to move")

func readMove(ctx context.Context, q queryer) (Move, bool, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, MoveKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Move{}, false, nil
	}
	if err != nil {
		return Move{}, false, err
	}
	var m Move
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return Move{}, false, errors.New("the stored move is damaged")
	}
	m.At = time.UnixMilli(m.AtMs)
	return m, true, nil
}

// CurrentMove is the switch that still has people waiting, if there is one.
func (d *DB) CurrentMove(ctx context.Context) (Move, bool, error) { return readMove(ctx, d.read) }

// SwitchCounts are the real numbers a switch or Start over would touch.
type SwitchCounts struct {
	// People are the accounts other than the owner's that can sign in now; a switch makes them
	// wait, Start over removes them with every account that cannot.
	People, OtherAccounts int
	// Profiles are every profile on the server; OtherProfiles those of other accounts.
	Profiles, OtherProfiles int
	// SignedIn are the sign-ins that end, the owner's own included.
	SignedIn int
	// Invitations are the unused invitations that are cancelled.
	Invitations int
	// Keys are other accounts' keys and tokens, which pause on a switch and go on Start over.
	Keys    int
	Waiting int
}

// SignInCounts reads the numbers for the preview.
func (d *DB) SignInCounts(ctx context.Context, ownerID int64, now time.Time) (SwitchCounts, error) {
	var c SwitchCounts
	err := d.read.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM users WHERE id <> ?1 AND state = 'active' AND disabled_at IS NULL),
		(SELECT count(*) FROM users WHERE id <> ?1),
		(SELECT count(*) FROM profiles),
		(SELECT count(*) FROM profiles WHERE user_id <> ?1),
		(SELECT count(*) FROM sessions WHERE revoked_at IS NULL AND expires_at > ?2),
		(SELECT count(*) FROM invitations WHERE state = 'open' AND expires_at > ?2),
		(SELECT count(*) FROM api_keys WHERE user_id <> ?1 AND revoked_at IS NULL),
		(SELECT count(*) FROM users WHERE state = 'waiting' AND disabled_at IS NULL)`, ownerID, now.UnixMilli()).
		Scan(&c.People, &c.OtherAccounts, &c.Profiles, &c.OtherProfiles, &c.SignedIn, &c.Invitations, &c.Keys, &c.Waiting)
	return c, err
}

// Switch is the owner confirming a change of the kind of sign-in.
type Switch struct {
	// Proof is the owner's current password (and two-step code), proven in the switch's own
	// commit; nil on a server that uses Portico Accounts, where the owner has no password.
	Proof   *PasswordProof
	OwnerID int64
	// To is "local" or "portico".
	To        string
	StartOver bool
	// Username and PasswordHash are the owner's on a server moving to local sign-in;
	// Username is already in its stored form.
	Username, PasswordHash string
	// PorticoAccountID is the owner's Portico Account on a server moving to Portico Accounts.
	PorticoAccountID string
	// Session is the owner's new sign-in, started in the same commit.
	Session NewSession
	Now     time.Time
}

// SwitchSignIn changes the kind of sign-in in one commit and starts the owner's new sign-in.
// A username or Portico Account another account has is ErrConflict.
func (d *DB) SwitchSignIn(ctx context.Context, sw Switch) (SessionState, error) {
	var out SessionState
	var refused error
	stamp, err := d.writeSessionsStamped(ctx, func(tx *sql.Tx, c *changes) error {
		refused = nil
		if sw.Proof != nil {
			if ok, err := proven(tx, sw.Now, *sw.Proof, &refused); err != nil || !ok {
				return err
			}
		}
		var role string
		if err := tx.QueryRow(`SELECT role FROM users WHERE id = ?`, sw.OwnerID).Scan(&role); err != nil || role != "owner" {
			return errors.Join(ErrNotPermitted, err)
		}
		at := sw.Now.UnixMilli()
		var was string
		if err := tx.QueryRow(`SELECT ifnull((SELECT value FROM settings WHERE key = ?), '')`, SignInKindKey).Scan(&was); err != nil {
			return err
		}
		// Nobody keeps a sign-in made under the kind that is ending.
		if err := revoke(tx, c, sw.Now, "sign_in_changed", `1 = 1`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE invitations SET state = 'revoked' WHERE state = 'open'`); err != nil {
			return err
		}
		// Codes made under the old kind (TV codes, resets, second steps, move codes) go too.
		if _, err := tx.Exec(`DELETE FROM auth_codes`); err != nil {
			return err
		}
		if sw.StartOver {
			if _, err := tx.Exec(`DELETE FROM users WHERE id <> ?`, sw.OwnerID); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM devices WHERE id NOT IN (SELECT device_id FROM sessions)`); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, MoveKey); err != nil {
				return err
			}
		} else {
			// A turned-off account waits too, so turning it on after the switch lets it move. While
			// it is off it is not listed, gets no code and no sign-in.
			if _, err := tx.Exec(`UPDATE users SET state = 'waiting', updated_at = ? WHERE id <> ? AND state = 'active'`, at, sw.OwnerID); err != nil {
				return err
			}
			raw, _ := json.Marshal(Move{To: sw.To, AtMs: at})
			if err := putSetting(tx, MoveKey, string(raw), at); err != nil {
				return err
			}
		}
		switch sw.To {
		case "local":
			// A local server holds no Portico Account anywhere.
			if _, err := tx.Exec(`UPDATE users SET portico_account_id = NULL WHERE portico_account_id IS NOT NULL`); err != nil {
				return err
			}
			var taken bool
			if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE username = ? AND id <> ?)`, sw.Username, sw.OwnerID).Scan(&taken); err != nil {
				return err
			}
			if taken {
				return ErrConflict
			}
			if _, err := tx.Exec(`UPDATE users SET username = ?, password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?`,
				sw.Username, sw.PasswordHash, at, at, sw.OwnerID); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, SignInKindKey); err != nil {
				return err
			}
			// A server leaving Portico Accounts tells Portico Services once that it has left,
			// trying until then and never again; a server that was local tells it nothing. Who
			// sends the message is not built: see internal/hosted.
			if was == SignInPortico {
				if err := putSetting(tx, LeftMessageKey, strconv.FormatInt(sw.Now.Add(leftMessageTries).UnixMilli(), 10), at); err != nil {
					return err
				}
			}
		case "portico":
			if err := porticoAccountFree(tx, sw.PorticoAccountID, sw.OwnerID); err != nil {
				return err
			}
			// The owner signs in with the Portico Account from now on and loses the password.
			if _, err := tx.Exec(`UPDATE users SET portico_account_id = ?, password_hash = NULL, password_changed_at = NULL, updated_at = ? WHERE id = ?`,
				sw.PorticoAccountID, at, sw.OwnerID); err != nil {
				return err
			}
			if err := putSetting(tx, SignInKindKey, SignInPortico, at); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, LeftMessageKey); err != nil {
				return err
			}
			// Portico Accounts keep their pictures with Portico Services, never here. Each
			// person's pictures stored on this server go when that person moves to their
			// Portico Account: the owner's here, everyone else's in CompleteMoveToPortico.
			// Profile pictures (#2215) are not on this base: once they are, clear the owner's
			// profiles' picture column here, and the tidy-up removes the files.
		}
		detail := "to " + sw.To
		if sw.StartOver {
			detail += ", starting over"
		}
		if err := audit(tx, sw.OwnerID, "sign_in.switch", "", detail, sw.Now); err != nil {
			return err
		}
		var err error
		out, err = startSession(tx, sw.OwnerID, sw.Session, sw.Now)
		return err
	})
	if err == nil {
		err = refused
	}
	out.Stamp = stamp
	return out, err
}

// FinishMove turns off every account still waiting, as "Didn't move", keeping its profiles
// and history until the owner deletes it. It returns how many.
func (d *DB) FinishMove(ctx context.Context, actorID int64, now time.Time) (int, error) {
	var n int64
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE users SET state = 'not_moved', updated_at = ? WHERE state = 'waiting'`, now.UnixMilli())
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		if _, err := tx.Exec(`DELETE FROM auth_codes WHERE kind = 'move'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, MoveKey); err != nil {
			return err
		}
		return audit(tx, actorID, "sign_in.finish", "", strconv.FormatInt(n, 10)+" did not move", now)
	})
	return int(n), err
}

// Waiting is an account waiting to move.
type Waiting struct {
	ID       int64
	PublicID string
	Username string
	// CodeExpiresAt is when its move code stops working; zero when it has none.
	CodeExpiresAt time.Time
}

// WaitingAccounts lists the accounts waiting to move, by username.
func (d *DB) WaitingAccounts(ctx context.Context, now time.Time) ([]Waiting, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT u.id, u.public_id, u.username,
			ifnull((SELECT max(expires_at) FROM auth_codes a WHERE a.user_id = u.id AND a.kind = 'move' AND a.used_at IS NULL AND a.expires_at > ?), 0)
		FROM users u WHERE u.state = 'waiting' AND u.disabled_at IS NULL ORDER BY u.username`, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Waiting
	for rows.Next() {
		var w Waiting
		var expires int64
		if err := rows.Scan(&w.ID, &w.PublicID, &w.Username, &expires); err != nil {
			return nil, err
		}
		if expires > 0 {
			w.CodeExpiresAt = time.UnixMilli(expires)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// MoveCode is a code for one waiting account, by its public id.
type MoveCode struct {
	PublicID string
	Hash     string
}

// IssueMoveCodes stores a new move code for each account, replacing its earlier one. Every
// account must be waiting and one the actor manages; otherwise nothing is stored and the
// answer is ErrNotFound (no such account), ErrNotPermitted or ErrConflict (not waiting).
func (d *DB) IssueMoveCodes(ctx context.Context, actorID int64, actorRole string, codes []MoveCode, expires, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		for _, mc := range codes {
			m, err := managed(tx, actorID, actorRole, mc.PublicID)
			if err != nil {
				return err
			}
			// An account the owner turned off stays off through any switch and gets no code.
			var state string
			if err := tx.QueryRow(`SELECT state FROM users WHERE id = ?`, m.ID).Scan(&state); err != nil {
				return err
			}
			if state != "waiting" || m.Disabled {
				return ErrConflict
			}
			if _, err := tx.Exec(`DELETE FROM auth_codes WHERE user_id = ? AND kind = 'move'`, m.ID); err != nil {
				return err
			}
			if err := insertCode(tx, Code{Kind: "move", Hash: mc.Hash, UserID: m.ID, ExpiresAt: expires}, now); err != nil {
				return err
			}
			if err := audit(tx, actorID, "sign_in.move_code", m.PublicID, m.Username, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// MoveCodeAccount is the waiting account a move code is for, without using the code up.
// A wrong, used or expired code, or one whose account no longer waits, is ErrBadCredentials.
func (d *DB) MoveCodeAccount(ctx context.Context, hash string, now time.Time) (Account, error) {
	var id int64
	err := d.read.QueryRowContext(ctx, `SELECT a.user_id FROM auth_codes a JOIN users u ON u.id = a.user_id
		WHERE a.code_hash = ? AND a.kind = 'move' AND a.used_at IS NULL AND a.expires_at > ? AND u.state = 'waiting' AND u.disabled_at IS NULL`,
		hash, now.UnixMilli()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrBadCredentials
	}
	if err != nil {
		return Account{}, err
	}
	return d.Account(ctx, id)
}

// MoveToLocal is a waiting person finishing a move to local sign-in with their code.
type MoveToLocal struct {
	CodeHash string
	// Username is already in its stored form.
	Username, PasswordHash string
	Limits                 []Limit
	Session                NewSession
	Now                    time.Time
}

// CompleteMoveToLocal uses the code up, gives the account its username and password, makes it
// active and signs it in, in one commit. When nobody is left waiting the move is over. A wrong,
// used or expired code is ErrBadCredentials and counts as a wrong guess; a username another
// account has is ErrConflict.
func (d *DB) CompleteMoveToLocal(ctx context.Context, m MoveToLocal) (SessionState, error) {
	var out SessionState
	var refused error
	stamp, err := d.writeSessionsStamped(ctx, func(tx *sql.Tx, _ *changes) error {
		refused = nil
		code, err := redeemCode(tx, "move", m.CodeHash, m.Now)
		if errors.Is(err, ErrBadCredentials) {
			refused = err
			return limitFail(tx, m.Now, m.Limits)
		}
		if err != nil {
			return err
		}
		var state string
		if err := tx.QueryRow(`SELECT state FROM users WHERE id = ?`, code.UserID).Scan(&state); err != nil || state != "waiting" {
			refused = ErrBadCredentials
			return err
		}
		var taken bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE username = ? AND id <> ?)`, m.Username, code.UserID).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return ErrConflict
		}
		at := m.Now.UnixMilli()
		if _, err := tx.Exec(`UPDATE users SET username = ?, password_hash = ?, password_changed_at = ?, state = 'active', updated_at = ? WHERE id = ?`,
			m.Username, m.PasswordHash, at, at, code.UserID); err != nil {
			return err
		}
		if err := endMoveIfDone(tx); err != nil {
			return err
		}
		out, err = startSession(tx, code.UserID, m.Session, m.Now)
		return err
	})
	if err == nil {
		err = refused
	}
	out.Stamp = stamp
	return out, err
}

// PorticoAccountTaken: the Portico Account is already on this server as another account.
// Accounts are never merged.
type PorticoAccountTaken struct{ Username string }

func (e *PorticoAccountTaken) Error() string {
	return "the portico account is already on this server as " + e.Username
}

func porticoAccountFree(tx *sql.Tx, porticoID string, userID int64) error {
	var holder string
	err := tx.QueryRow(`SELECT username FROM users WHERE portico_account_id = ? AND id <> ?`, porticoID, userID).Scan(&holder)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return &PorticoAccountTaken{Username: holder}
}

// MoveToPortico is a waiting person linking their Portico Account after a switch to Portico
// Accounts, proven by a move code or, for a while after the switch, their old password.
type MoveToPortico struct {
	// CodeHash is the move code's hash; or else UserID with the CheckedHash its old password
	// was compared against, outside the transaction, and found right.
	CodeHash    string
	UserID      int64
	CheckedHash string
	// PorticoAccountID and Username are the Portico Account's; the username is taken when no
	// other account here has it, and the person's old one kept otherwise.
	PorticoAccountID, Username string
	Limits                     []Limit
	Session                    NewSession
	Now                        time.Time
}

// CompleteMoveToPortico links the account to the Portico Account, drops its password, makes it
// active and signs it in, in one commit. A wrong, used or expired code, or an old password
// that no longer stands, is ErrBadCredentials and counts as a wrong guess; a Portico Account
// another account has is *PorticoAccountTaken.
func (d *DB) CompleteMoveToPortico(ctx context.Context, m MoveToPortico) (SessionState, error) {
	var out SessionState
	var refused error
	stamp, err := d.writeSessionsStamped(ctx, func(tx *sql.Tx, _ *changes) error {
		refused = nil
		userID := m.UserID
		if m.CodeHash != "" {
			code, err := redeemCode(tx, "move", m.CodeHash, m.Now)
			if errors.Is(err, ErrBadCredentials) {
				refused = err
				return limitFail(tx, m.Now, m.Limits)
			}
			if err != nil {
				return err
			}
			userID = code.UserID
		} else {
			move, moving, err := readMove(ctx, tx)
			if err != nil {
				return err
			}
			var hash sql.NullString
			err = tx.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err != nil || !hash.Valid || hash.String != m.CheckedHash || !moving || move.To != "portico" || m.Now.Sub(move.At) >= OldPasswordWorks {
				refused = ErrBadCredentials
				return limitFail(tx, m.Now, m.Limits)
			}
		}
		var state string
		if err := tx.QueryRow(`SELECT state FROM users WHERE id = ?`, userID).Scan(&state); err != nil || state != "waiting" {
			refused = ErrBadCredentials
			return err
		}
		if err := porticoAccountFree(tx, m.PorticoAccountID, userID); err != nil {
			return err
		}
		at := m.Now.UnixMilli()
		if _, err := tx.Exec(`UPDATE users SET portico_account_id = ?, password_hash = NULL, password_changed_at = NULL, state = 'active',
			username = CASE WHEN ? <> '' AND NOT EXISTS (SELECT 1 FROM users o WHERE o.username = ? AND o.id <> users.id) THEN ? ELSE username END,
			updated_at = ? WHERE id = ?`, m.PorticoAccountID, m.Username, m.Username, m.Username, at, userID); err != nil {
			return err
		}
		// This person's pictures stored on this server go now, kept by Portico Services from
		// here on. Profile pictures (#2215) are not on this base: once they are, clear this
		// account's profiles' picture column here, and the tidy-up removes the files.
		if err := limitClear(tx, m.Limits); err != nil {
			return err
		}
		if err := endMoveIfDone(tx); err != nil {
			return err
		}
		var err error
		out, err = startSession(tx, userID, m.Session, m.Now)
		return err
	})
	if err == nil {
		err = refused
	}
	out.Stamp = stamp
	return out, err
}

func endMoveIfDone(tx *sql.Tx) error {
	_, err := tx.Exec(`DELETE FROM settings WHERE key = ? AND NOT EXISTS (SELECT 1 FROM users WHERE state = 'waiting')`, MoveKey)
	return err
}

// OwnerChange hands the server to another account, or to a new one.
type OwnerChange struct {
	// Proof is the current owner's password (local sign-in); nil when a Portico approval
	// proved them instead.
	Proof *PasswordProof
	// OwnerID is the current owner, who asked.
	OwnerID int64
	// TargetID is an existing account, or 0 to make New.
	TargetID int64
	New      *NewAccount
	// Previous is what the previous owner becomes: "admin", "member" or "removed".
	Previous string
	Now      time.Time
}

// NewAccount is an account made for a new owner.
type NewAccount struct {
	// Username is already in its stored form.
	Username, PasswordHash, ProfileName string
	PorticoAccountID                    string
}

// ErrNotActive: the account cannot be given the server now (waiting, left, not moved or turned off).
var ErrNotActive = errors.New("the account is not active")

// ChangeOwner makes the target the owner and the previous owner what they chose, in one commit.
// Every sign-in of both is made to renew, so neither keeps a power it no longer has; a removed
// previous owner is signed out and deleted. It returns the new owner's id.
func (d *DB) ChangeOwner(ctx context.Context, o OwnerChange) (int64, error) {
	var refused error
	var target int64
	err := d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		refused, target = nil, o.TargetID
		if o.Proof != nil {
			if ok, err := proven(tx, o.Now, *o.Proof, &refused); err != nil || !ok {
				return err
			}
		}
		var role string
		if err := tx.QueryRow(`SELECT role FROM users WHERE id = ?`, o.OwnerID).Scan(&role); err != nil || role != "owner" {
			return errors.Join(ErrNotPermitted, err)
		}
		at := o.Now.UnixMilli()
		if o.New != nil {
			var err error
			if target, err = insertUser(tx, o.New.Username, o.New.PasswordHash, "owner", o.New.ProfileName, o.Now); err != nil {
				return err
			}
			if o.New.PasswordHash == "" {
				if _, err := tx.Exec(`UPDATE users SET password_hash = NULL, password_changed_at = NULL, portico_account_id = ? WHERE id = ?`,
					o.New.PorticoAccountID, target); err != nil {
					return err
				}
			}
		} else {
			var state string
			var disabled bool
			err := tx.QueryRow(`SELECT state, disabled_at IS NOT NULL FROM users WHERE id = ? AND id <> ?`, target, o.OwnerID).Scan(&state, &disabled)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			if state != "active" || disabled {
				return ErrNotActive
			}
			if _, err := tx.Exec(`UPDATE users SET role = 'owner', updated_at = ? WHERE id = ?`, at, target); err != nil {
				return err
			}
			if err := markStale(tx, c, `user_id = ?`, target); err != nil {
				return err
			}
		}
		if o.Previous == "removed" {
			if err := revoke(tx, c, o.Now, "account_removed", `user_id = ?`, o.OwnerID); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM users WHERE id = ?`, o.OwnerID); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(`UPDATE users SET role = ?, updated_at = ? WHERE id = ?`, o.Previous, at, o.OwnerID); err != nil {
				return err
			}
			if err := markStale(tx, c, `user_id = ?`, o.OwnerID); err != nil {
				return err
			}
		}
		return audit(tx, o.OwnerID, "owner.change", "", "previous owner "+o.Previous, o.Now)
	})
	if err == nil {
		err = refused
	}
	return target, err
}

// LeaveServer is a member of a Portico Accounts server leaving it: with deleteData the account
// goes with everything saved for it, otherwise it is marked Left and kept until the owner
// deletes it. Either way its sign-ins end. The owner cannot leave.
func (d *DB) LeaveServer(ctx context.Context, userID int64, deleteData bool, now time.Time) error {
	return d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		var role string
		if err := tx.QueryRow(`SELECT role FROM users WHERE id = ?`, userID).Scan(&role); err != nil {
			return err
		}
		if role == "owner" {
			return ErrNotPermitted
		}
		if err := revoke(tx, c, now, "left_server", `user_id = ?`, userID); err != nil {
			return err
		}
		if deleteData {
			_, err := tx.Exec(`DELETE FROM users WHERE id = ?`, userID)
			return err
		}
		_, err := tx.Exec(`UPDATE users SET state = 'left', updated_at = ? WHERE id = ?`, now.UnixMilli(), userID)
		return err
	})
}
