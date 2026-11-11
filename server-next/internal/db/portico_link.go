package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// The link between this server and a Portico Account, and what follows from it. A server
// with local sign-in has no link and no queued leave, and that absence is what keeps it from
// ever contacting Portico Services. Everything here lives in two settings rows.

const (
	linkKey  = "hosted.link"
	leaveKey = "hosted.leave"
)

// Link is this server's standing link to Portico Services. Times are Unix seconds.
type Link struct {
	// AccountID is the Portico Account that owns the server at Portico Services.
	AccountID string `json:"accountId"`
	// IssuedAt is when the setup or switch approval that made this link was issued. Notes of
	// this link are numbered from it, and no request is signed with an earlier time.
	IssuedAt int64 `json:"issuedAt"`
	// NoteRevision and Fleet are the last note numbers applied. They only ever rise within a link.
	NoteRevision int64 `json:"noteRevision"`
	Fleet        int64 `json:"fleet"`
	// Certificate is the newest signing certificate seen, as JSON; its list of withdrawn keys
	// is what signed documents are checked against after a restart.
	Certificate         json.RawMessage `json:"certificate,omitempty"`
	CertificateRevision uint64          `json:"certificateRevision,omitempty"`
	// DocumentRevision numbers this server's document (name, owner, members, addresses);
	// DocumentDigest is the content it had at that revision, and DocumentSent the revision
	// Portico Services has confirmed.
	DocumentRevision int64  `json:"documentRevision"`
	DocumentDigest   string `json:"documentDigest,omitempty"`
	DocumentSent     int64  `json:"documentSent"`
	// LastContact is the last time Portico Services answered a call from this server.
	LastContact int64 `json:"lastContact"`
	// CheckInAt is when the next check-in or its retry is due, and CheckInTries how many
	// have failed in a row. DocumentAt and DocumentTries are the same for a document waiting
	// to be sent. Saved, so a restart never turns a wait into a call.
	CheckInAt     int64 `json:"checkInAt,omitempty"`
	CheckInTries  int   `json:"checkInTries,omitempty"`
	DocumentAt    int64 `json:"documentAt,omitempty"`
	DocumentTries int   `json:"documentTries,omitempty"`
	// Unknown counts the check-ins, at least a day apart, that Portico Services answered
	// with "I do not know this server", and UnknownAt is when the last one was counted. One
	// such answer proves nothing (Portico Services may have been put back empty), so the
	// link ends only at the third. A good check-in sets both back to zero.
	Unknown   int   `json:"unknown,omitempty"`
	UnknownAt int64 `json:"unknownAt,omitempty"`
	// Unlinked: this link has ended. Portico Services said so in a signed note (the owner
	// removed the server from their account), or did not know the server three days running.
	// Nothing more is asked until the server is linked again.
	Unlinked bool `json:"unlinked,omitempty"`
}

// QueuedLeave is the one "this server has left" message of a server that switched to local
// sign-in, kept until it is delivered or its give-up time passes.
type QueuedLeave struct {
	// LinkIssuedAt names the link that was left.
	LinkIssuedAt int64 `json:"linkIssuedAt"`
	GiveUpAt     int64 `json:"giveUpAt"`
	TryAt        int64 `json:"tryAt,omitempty"`
	Tries        int   `json:"tries,omitempty"`
}

var (
	// ErrNoLink: this server is not linked, or the link the caller acted for has been replaced.
	ErrNoLink = errors.New("this server is not linked to a portico account")
	// ErrStaleApproval: the approval is no newer than a link this server has already made.
	// Accepting it would set the note numbers back and let an old link's notes through.
	ErrStaleApproval = errors.New("the approval is older than this server's link")
	// ErrNotLinkOwner: the approval is from a Portico Account other than the one that owns
	// the standing link. Moving a server to another account is Change owner.
	ErrNotLinkOwner = errors.New("the approval is not from the portico account that owns this server")
	// ErrNotMember: the Portico Account has no account on this server.
	ErrNotMember = errors.New("the portico account is not a member of this server")
	// ErrLeft: the person left this server, or their Portico Account was deleted.
	ErrLeft = errors.New("the account has left this server")
)

func readJSONSetting(ctx context.Context, q queryer, key string, into any) (bool, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if json.Unmarshal([]byte(raw), into) != nil {
		return false, errors.New("the stored " + key + " setting is damaged")
	}
	return true, nil
}

func saveJSONSetting(tx *sql.Tx, key string, value any, at int64) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, revision = revision + 1, updated_at = excluded.updated_at`, key, string(raw), at)
	return err
}

// Link returns the standing link, and whether there is one.
func (d *DB) Link(ctx context.Context) (Link, bool, error) {
	var l Link
	ok, err := readJSONSetting(ctx, d.read, linkKey, &l)
	return l, ok, err
}

// Leave returns the queued "this server has left", and whether there is one.
func (d *DB) Leave(ctx context.Context) (QueuedLeave, bool, error) {
	var l QueuedLeave
	ok, err := readJSONSetting(ctx, d.read, leaveKey, &l)
	return l, ok, err
}

// linkServer records a new link, made by a setup or switch approval from accountID issued
// at issuedAt. It is called inside the transaction that makes whatever else the setup or the
// switch changes, so a server is never half linked. The link's note numbers start at the
// approval's issue time, which puts every note of an earlier link below them; an approval
// no newer than a link already made is refused for the same reason. While a link stands,
// only the account that owns it may make another. A queued leave for an earlier link is
// cancelled: sent later, it must not remove this one.
func linkServer(tx *sql.Tx, accountID string, issuedAt, now time.Time) error {
	ctx := context.Background()
	var old Link
	had, err := readJSONSetting(ctx, tx, linkKey, &old)
	if err != nil {
		return err
	}
	var left QueuedLeave
	leaving, err := readJSONSetting(ctx, tx, leaveKey, &left)
	if err != nil {
		return err
	}
	at := issuedAt.Unix()
	switch {
	case accountID == "" || at <= 0:
		return ErrStaleApproval
	case had && old.AccountID != accountID:
		return ErrNotLinkOwner
	case had && at <= old.IssuedAt, leaving && at <= left.LinkIssuedAt:
		return ErrStaleApproval
	}
	if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, leaveKey); err != nil {
		return err
	}
	// The fleet number and the certificate are not the link's: they are kept. The document
	// starts again as unsent, at a revision above any this server has used.
	return saveJSONSetting(tx, linkKey, Link{AccountID: accountID, IssuedAt: at, NoteRevision: at, Fleet: old.Fleet,
		Certificate: old.Certificate, CertificateRevision: old.CertificateRevision,
		DocumentRevision: old.DocumentRevision + 1, LastContact: now.Unix()}, now.UnixMilli())
}

// unlinkServer ends the link of a server that is switching to local sign-in, in the switch's
// own transaction, and queues the one "this server has left" for it, tried until giveUp. A
// server that had no link queues nothing: it has never contacted Portico Services and never will.
func unlinkServer(tx *sql.Tx, giveUp, now time.Time) error {
	var old Link
	had, err := readJSONSetting(context.Background(), tx, linkKey, &old)
	if err != nil || !had {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, linkKey); err != nil {
		return err
	}
	return saveJSONSetting(tx, leaveKey, QueuedLeave{LinkIssuedAt: old.IssuedAt, GiveUpAt: giveUp.Unix()}, now.UnixMilli())
}

// setLinkOwner records a change of owner in the change's own transaction. It is not a new
// link: the numbers stay where they are on both sides.
func setLinkOwner(tx *sql.Tx, accountID string, now time.Time) error {
	var l Link
	had, err := readJSONSetting(context.Background(), tx, linkKey, &l)
	if err != nil || !had {
		return err
	}
	l.AccountID = accountID
	return saveJSONSetting(tx, linkKey, l, now.UnixMilli())
}

// NewLink is a server being linked to its owner's Portico Account with nothing else changing.
type NewLink struct {
	// OwnerID is the owner's account on this server.
	OwnerID int64
	// AccountID and IssuedAt are the approving Portico Account and the approval's issue time.
	AccountID string
	IssuedAt  time.Time
	Now       time.Time
}

// LinkServer links the server to the owner's Portico Account in one commit: the owner's
// account carries the Portico Account, and the link is stored. It is "Link again" for a
// server whose link ended. Setup and a switch make the same link inside their own commits.
// The refusals are linkServer's, and ErrConflict for a Portico Account already on this
// server as somebody else.
func (d *DB) LinkServer(ctx context.Context, n NewLink) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		var role string
		if err := tx.QueryRow(`SELECT role FROM users WHERE id = ?`, n.OwnerID).Scan(&role); err != nil || role != "owner" {
			return errors.Join(ErrNotPermitted, err)
		}
		var taken bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE portico_account_id = ? AND id <> ?)`, n.AccountID, n.OwnerID).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return ErrConflict
		}
		if err := linkServer(tx, n.AccountID, n.IssuedAt, n.Now); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE users SET portico_account_id = ?, updated_at = ? WHERE id = ?`, n.AccountID, n.Now.UnixMilli(), n.OwnerID); err != nil {
			return err
		}
		return audit(tx, n.OwnerID, "portico.link", "", n.AccountID, n.Now)
	})
}

// UpdateLink changes the standing link's bookkeeping in one commit. issuedAt names the link
// the caller acted for: if the server has been linked again or unlinked meanwhile, nothing
// is written and the answer is ErrNoLink, so work done for an old link never marks a new one.
func (d *DB) UpdateLink(ctx context.Context, issuedAt int64, now time.Time, change func(*Link)) (Link, error) {
	var l Link
	err := d.write(ctx, Background, func(tx *sql.Tx) error {
		l = Link{}
		had, err := readJSONSetting(ctx, tx, linkKey, &l)
		if err != nil {
			return err
		}
		if !had || l.IssuedAt != issuedAt {
			return ErrNoLink
		}
		change(&l)
		return saveJSONSetting(tx, linkKey, l, now.UnixMilli())
	})
	return l, err
}

// SaveLeave keeps the queued leave's next try, or removes it when done is true: delivered,
// or given up for good.
func (d *DB) SaveLeave(ctx context.Context, l QueuedLeave, done bool, now time.Time) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		var held QueuedLeave
		had, err := readJSONSetting(ctx, tx, leaveKey, &held)
		if err != nil || !had || held.LinkIssuedAt != l.LinkIssuedAt {
			return err
		}
		if done {
			_, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, leaveKey)
			return err
		}
		return saveJSONSetting(tx, leaveKey, l, now.UnixMilli())
	})
}

// markLeft turns off each named Portico Account as Left and ends its sign-ins. Nothing is
// deleted, and the owner is never turned off by anything Portico Services says: the server
// decides who may use it.
func markLeft(tx *sql.Tx, c *changes, accountIDs []string, now time.Time) error {
	for _, id := range accountIDs {
		var userID int64
		err := tx.QueryRow(`UPDATE users SET state = 'left', updated_at = ? WHERE portico_account_id = ? AND role <> 'owner' AND state = 'active'
			RETURNING id`, now.UnixMilli(), id).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if err := revoke(tx, c, now, "left_server", `user_id = ?`, userID); err != nil {
			return err
		}
		if err := audit(tx, userID, "portico.left", "", id, now); err != nil {
			return err
		}
	}
	return nil
}

// Note is a checked note from Portico Services, as the link's rules need it.
type Note struct {
	Revision, Fleet int64
	// Left are the Portico Accounts that left this server or were deleted.
	Left []string
	// Unlinked says the owner removed this server from their Portico Account.
	Unlinked bool
	// Certificate is the newest signing certificate the caller holds, to keep.
	Certificate         json.RawMessage
	CertificateRevision uint64
}

// ApplyNote applies a note in one commit and returns the link as it then stands, whose
// numbers the caller acknowledges. Left and Unlinked count only when the note's revision is
// higher than the last applied: a repeat, a late note and a note of an earlier link (whose
// numbers all lie below this link's first) change nothing. A higher fleet number alone moves
// only the fleet number, so no note is ever let through on its strength. changed says the
// note wrote something.
func (d *DB) ApplyNote(ctx context.Context, n Note, now time.Time) (l Link, changed bool, err error) {
	err = d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		l, changed = Link{}, false
		had, err := readJSONSetting(ctx, tx, linkKey, &l)
		if err != nil {
			return err
		}
		if !had {
			return ErrNoLink
		}
		if n.Revision <= l.NoteRevision && n.Fleet <= l.Fleet && n.CertificateRevision <= l.CertificateRevision {
			// Nothing new: a note anyone can send again must not cost a write each time.
			return nil
		}
		changed = true
		if n.Revision > l.NoteRevision {
			if err := markLeft(tx, c, n.Left, now); err != nil {
				return err
			}
			l.NoteRevision = n.Revision
			l.Unlinked = l.Unlinked || n.Unlinked
		}
		l.Fleet = max(l.Fleet, n.Fleet)
		if n.CertificateRevision > l.CertificateRevision {
			l.Certificate, l.CertificateRevision = n.Certificate, n.CertificateRevision
		}
		return saveJSONSetting(tx, linkKey, l, now.UnixMilli())
	})
	return l, changed, err
}

// MarkLeft turns off the accounts Portico Services answered a document with: ones that left
// through their Portico Account or no longer exist. issuedAt names the link the document
// was sent for.
func (d *DB) MarkLeft(ctx context.Context, issuedAt int64, accountIDs []string, now time.Time) error {
	if len(accountIDs) == 0 {
		return nil
	}
	return d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		var l Link
		if had, err := readJSONSetting(ctx, tx, linkKey, &l); err != nil || !had || l.IssuedAt != issuedAt {
			return err
		}
		return markLeft(tx, c, accountIDs, now)
	})
}

// PorticoMember is one account of this server for the document Portico Services keeps.
type PorticoMember struct {
	AccountID string
	Role      string
}

// PorticoMembers lists the Portico Accounts that can sign in to this server now, the owner
// first. An account that is turned off, has left or has not moved is not a member.
func (d *DB) PorticoMembers(ctx context.Context) ([]PorticoMember, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT portico_account_id, role FROM users
		WHERE portico_account_id IS NOT NULL AND state = 'active' AND (disabled_at IS NULL OR role = 'owner')
		ORDER BY role <> 'owner', id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PorticoMember
	for rows.Next() {
		var m PorticoMember
		if err := rows.Scan(&m.AccountID, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PorticoSignIn is a person whose Portico Account has just been proven to this server.
type PorticoSignIn struct {
	// LinkIssuedAt is the link the proof was made under. A proof made under an earlier link
	// signs nobody in.
	LinkIssuedAt int64
	AccountID    string
	// Username is the account's username in its stored form, or "" if this server cannot
	// use it; Email and ProfileName come from the account too.
	Username, Email, ProfileName string
	// InvitationHash is the invitation the person brought, or "". It is needed, and used
	// up, only when the Portico Account has no account on this server yet.
	InvitationHash string
	Session        NewSession
	Now            time.Time
}

// SignInWithPortico starts a session for the account on this server that carries the proven
// Portico Account, bringing its username and email address up to date. With no such account,
// a valid invitation creates one, with the role and libraries the invitation carries. The
// Portico Account's id is the identity: no username or email address ever matches a person
// to an account. joined says an account was created.
//
// Refusals: ErrNoLink (the link changed or ended), ErrNotMember (no account and no usable
// invitation), ErrLeft, ErrAccountUnavailable (turned off, or not yet moved).
func (d *DB) SignInWithPortico(ctx context.Context, p PorticoSignIn) (out SessionState, joined bool, err error) {
	stamp, err := d.writeSessionsStamped(ctx, func(tx *sql.Tx, _ *changes) error {
		joined = false
		var l Link
		if had, err := readJSONSetting(ctx, tx, linkKey, &l); err != nil {
			return err
		} else if !had || l.IssuedAt != p.LinkIssuedAt || l.Unlinked {
			return ErrNoLink
		}
		at := p.Now.UnixMilli()
		var userID int64
		var username, email, state string
		var disabled bool
		err := tx.QueryRow(`SELECT id, username, ifnull(email, ''), state, disabled_at IS NOT NULL FROM users WHERE portico_account_id = ?`, p.AccountID).
			Scan(&userID, &username, &email, &state, &disabled)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if userID, err = joinWithPortico(tx, p); err != nil {
				return err
			}
			joined = true
		case err != nil:
			return err
		case state == "left":
			return ErrLeft
		case state != "active" || disabled:
			return ErrAccountUnavailable
		default:
			// The username follows the Portico Account, unless someone on this server
			// already has the new one: then the old one stays until that is sorted out.
			if p.Username != "" && p.Username != username {
				var taken bool
				if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE username = ?)`, p.Username).Scan(&taken); err != nil {
					return err
				}
				if !taken {
					username = p.Username
				}
			}
			if _, err := tx.Exec(`UPDATE users SET username = ?, email = nullif(?, ''), updated_at = ? WHERE id = ? AND (username <> ?1 OR ifnull(email, '') <> ?2)`,
				username, p.Email, at, userID); err != nil {
				return err
			}
		}
		out, err = startSession(tx, userID, p.Session, p.Now)
		return err
	})
	out.Stamp = stamp
	return out, joined, err
}

// joinWithPortico uses up the invitation and creates the account it allows, carrying the
// Portico Account and no password.
func joinWithPortico(tx *sql.Tx, p PorticoSignIn) (int64, error) {
	if p.InvitationHash == "" {
		return 0, ErrNotMember
	}
	at := p.Now.UnixMilli()
	var invitation int64
	var role string
	var allLibraries bool
	err := tx.QueryRow(`UPDATE invitations SET state = 'accepted', accepted_at = ? WHERE code_hash = ? AND state = 'open' AND expires_at > ?
		RETURNING id, role, all_libraries`, at, p.InvitationHash, at).Scan(&invitation, &role, &allLibraries)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotMember
	}
	if err != nil {
		return 0, err
	}
	// A username this server cannot use, or one somebody here already has, is replaced by
	// one made from the Portico Account's id: the name is only what people see.
	username := p.Username
	var taken bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE username = ?)`, username).Scan(&taken); err != nil {
		return 0, err
	}
	if username == "" || taken {
		username = "portico-" + strings.ToLower(NewID("u")[2:])
	}
	userID, err := insertUser(tx, username, "", role, p.ProfileName, p.Now)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE users SET password_hash = NULL, password_changed_at = NULL, portico_account_id = ?, email = nullif(?, ''),
		all_libraries = ? WHERE id = ?`, p.AccountID, p.Email, allLibraries, userID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO library_access (library_id, user_id) SELECT library_id, ? FROM library_access WHERE invitation_id = ?`, userID, invitation); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE invitations SET accepted_user_id = ? WHERE id = ?`, userID, invitation); err != nil {
		return 0, err
	}
	return userID, audit(tx, userID, "invitation.accept", "", username, p.Now)
}

// LocalReset is the owner taking the server back to local sign-in from its own data folder,
// with no sign-in at all: see ResetToLocalSignIn.
type LocalReset struct {
	// Username, in its stored form, and PasswordHash are the owner's from now on.
	Username, PasswordHash string
	// GiveUp is until when the one "this server has left" is tried.
	GiveUp  time.Time
	Session NewSession
	Now     time.Time
}

// ResetToLocalSignIn switches a server that uses Portico Accounts to local sign-in in one
// commit, for an owner who cannot sign in: the owner gets a username and password, every
// sign-in ends, unused invitations and codes are cancelled, every other account waits to
// move as after any switch, no account keeps a Portico Account, the link ends and its one
// "this server has left" is queued. Nothing is deleted. It asks nobody, so it works with
// Portico Services out of reach. A server with no link is ErrNoLink; a username another
// account has is ErrConflict.
func (d *DB) ResetToLocalSignIn(ctx context.Context, r LocalReset) (SessionState, error) {
	var out SessionState
	stamp, err := d.writeSessionsStamped(ctx, func(tx *sql.Tx, c *changes) error {
		var l Link
		if had, err := readJSONSetting(ctx, tx, linkKey, &l); err != nil {
			return err
		} else if !had {
			return ErrNoLink
		}
		at := r.Now.UnixMilli()
		var ownerID int64
		if err := tx.QueryRow(`SELECT id FROM users WHERE role = 'owner'`).Scan(&ownerID); err != nil {
			return err
		}
		var taken bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE username = ? AND id <> ?)`, r.Username, ownerID).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return ErrConflict
		}
		if err := revoke(tx, c, r.Now, "sign_in_changed", `1 = 1`); err != nil {
			return err
		}
		for _, statement := range []string{
			`UPDATE invitations SET state = 'revoked' WHERE state = 'open'`,
			`DELETE FROM auth_codes`,
			`UPDATE users SET portico_account_id = NULL WHERE portico_account_id IS NOT NULL`,
			// The kind of sign-in as the setup area records it: absent is local.
			`DELETE FROM settings WHERE key = 'sign_in_kind'`,
		} {
			if _, err := tx.Exec(statement); err != nil {
				return err
			}
		}
		res, err := tx.Exec(`UPDATE users SET state = 'waiting', updated_at = ? WHERE id <> ? AND state = 'active'`, at, ownerID)
		if err != nil {
			return err
		}
		if waiting, _ := res.RowsAffected(); waiting > 0 {
			// People waiting to move, as the setup area records a switch that has some.
			if err := saveJSONSetting(tx, "sign_in_move", map[string]any{"to": "local", "at": at}, at); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE users SET username = ?, password_hash = ?, password_changed_at = ?, state = 'active', disabled_at = NULL, updated_at = ?
			WHERE id = ?`, r.Username, r.PasswordHash, at, at, ownerID); err != nil {
			return err
		}
		if err := unlinkServer(tx, r.GiveUp, r.Now); err != nil {
			return err
		}
		if err := audit(tx, ownerID, "sign_in.reset", "", "to local, from the data folder", r.Now); err != nil {
			return err
		}
		out, err = startSession(tx, ownerID, r.Session, r.Now)
		return err
	})
	out.Stamp = stamp
	return out, err
}
