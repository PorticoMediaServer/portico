package db

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Notices are what the server tells people: in each person's Notifications, never by email
// or push. A notice belongs to an account, and to one of its profiles when it is about that
// profile alone (a reply to its report). Server notices go to the main profile of the owner,
// or of the owner and the administrators, so a child's profile on the owner's account never
// reads "the backup failed".

// Audience is who a notice goes to.
type Audience int

const (
	// ToAccount is one account, all its profiles: UserID.
	ToAccount Audience = iota
	// ToProfile is one profile: UserID and ProfileID.
	ToProfile
	// ToOwner is the owner's main profile.
	ToOwner
	// ToOwnerAndAdmins is the main profile of the owner and of each administrator.
	ToOwnerAndAdmins
	// ToEveryone is every account that may sign in, all its profiles.
	ToEveryone
)

// NewNotice is a notice to send.
type NewNotice struct {
	To        Audience
	UserID    int64
	ProfileID int64
	// Kind says what it is about; an app words it from Kind and Params, in its own language.
	Kind string
	// Params are the facts the app needs to word it, as JSON.
	Params string
	// Body is text a person wrote (the owner's message to everyone), shown as it is.
	Body string
	// Ref names what it is about, for the app to open: a report, a backup.
	Ref string
	// ActorID is who sent it, for the idempotency key; 0 for the server itself.
	ActorID                     int64
	IdempotencyKey, RequestHash string
	Now                         time.Time
}

// Notice is one notification as its reader sees it.
type Notice struct {
	ID         int64
	PublicID   string
	Kind       string
	Params     string
	Body       string
	Ref        string
	CreatedAt  time.Time
	ReadAt     *time.Time
	ArchivedAt *time.Time
}

// SendNotice writes a notice for each account in its audience, in one commit, and returns the
// public id of the first and how many were written. A repeat of the same Idempotency-Key
// answers the first sending and writes nothing.
func (d *DB) SendNotice(ctx context.Context, n NewNotice) (id string, sent int, err error) {
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		ref, err := createdBefore(tx, n.ActorID, n.IdempotencyKey, n.RequestHash)
		if err != nil {
			return err
		}
		if ref != "" {
			first, count, _ := strings.Cut(ref, " ")
			id = first
			sent, _ = strconv.Atoi(count)
			return nil
		}
		if id, sent, err = sendNotice(tx, n); err != nil {
			return err
		}
		return rememberCreated(tx, n.ActorID, n.IdempotencyKey, n.RequestHash, id+" "+strconv.Itoa(sent), n.Now)
	})
	return id, sent, err
}

// sendNotice writes a notice inside a transaction that is doing something else, such as
// answering a report.
func sendNotice(tx *sql.Tx, n NewNotice) (first string, sent int, err error) {
	type recipient struct{ user, profile int64 }
	var to []recipient
	query, args := "", []any{}
	main := `(SELECT p.id FROM profiles p WHERE p.user_id = u.id AND p.is_primary = 1)`
	active := `u.disabled_at IS NULL AND u.state = 'active'`
	switch n.To {
	case ToAccount:
		query, args = `SELECT id, 0 FROM users u WHERE id = ?`, []any{n.UserID}
	case ToProfile:
		query, args = `SELECT u.id, p.id FROM users u JOIN profiles p ON p.user_id = u.id WHERE u.id = ? AND p.id = ?`, []any{n.UserID, n.ProfileID}
	case ToOwner:
		query = `SELECT u.id, ifnull(` + main + `, 0) FROM users u WHERE u.role = 'owner'`
	case ToOwnerAndAdmins:
		query = `SELECT u.id, ifnull(` + main + `, 0) FROM users u WHERE u.role IN ('owner', 'admin') AND ` + active
	case ToEveryone:
		query = `SELECT u.id, 0 FROM users u WHERE ` + active
	default:
		return "", 0, errors.New("unknown notice audience")
	}
	rows, err := tx.Query(query, args...)
	if err != nil {
		return "", 0, err
	}
	for rows.Next() {
		var r recipient
		if err := rows.Scan(&r.user, &r.profile); err != nil {
			rows.Close()
			return "", 0, err
		}
		to = append(to, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	for _, r := range to {
		id := NewID("ntf")
		if first == "" {
			first = id
		}
		if _, err := tx.Exec(`INSERT INTO notifications (public_id, user_id, profile_id, kind, params, body, ref, created_at)
			VALUES (?, ?, nullif(?, 0), ?, nullif(?, ''), nullif(?, ''), nullif(?, ''), ?)`,
			id, r.user, r.profile, n.Kind, n.Params, n.Body, n.Ref, n.Now.UnixMilli()); err != nil {
			return "", 0, err
		}
		if err := appendChange(tx, Change{At: n.Now.UnixMilli(), Topic: TopicNotice, Op: OpCreated, RefID: r.user}); err != nil {
			return "", 0, err
		}
	}
	return first, len(to), nil
}

// The views of a person's notifications.
const (
	NoticesUnread   = "unread"
	NoticesAll      = "all"
	NoticesArchived = "archived"
)

// noticeVisible is the notices one profile reads: its account's, and its own.
const noticeVisible = `user_id = ? AND (profile_id IS NULL OR profile_id = ?)`

// Notices lists a profile's notifications in one view, newest first, a page at a time after
// the cursor (the id of the last one read, 0 for the first page), with its unread count.
// "all" leaves out what is archived.
func (d *DB) Notices(ctx context.Context, v Viewer, view string, after int64, limit int) (out []Notice, unread int, err error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM notifications WHERE `+noticeVisible+` AND read_at IS NULL AND archived_at IS NULL`,
		v.UserID, v.ProfileID).Scan(&unread); err != nil {
		return nil, 0, err
	}
	where := ` AND archived_at IS NULL`
	switch view {
	case NoticesUnread:
		where += ` AND read_at IS NULL`
	case NoticesArchived:
		where = ` AND archived_at IS NOT NULL`
	}
	if after > 0 {
		where += ` AND id < ?`
	}
	args := []any{v.UserID, v.ProfileID}
	if after > 0 {
		args = append(args, after)
	}
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, `SELECT id, public_id, kind, ifnull(params, ''), ifnull(body, ''), ifnull(ref, ''), created_at, read_at, archived_at
		FROM notifications WHERE `+noticeVisible+where+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var n Notice
		var created int64
		var read, archived sql.NullInt64
		if err := rows.Scan(&n.ID, &n.PublicID, &n.Kind, &n.Params, &n.Body, &n.Ref, &created, &read, &archived); err != nil {
			return nil, 0, err
		}
		n.CreatedAt, n.ReadAt, n.ArchivedAt = time.UnixMilli(created), optionalTime(read), optionalTime(archived)
		out = append(out, n)
	}
	return out, unread, rows.Err()
}

func optionalTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64)
	return &t
}

// MarkNotice marks one of a profile's notices read or unread, and archived or not; nil leaves
// that as it is. A notice the profile does not read is ErrNotFound.
func (d *DB) MarkNotice(ctx context.Context, v Viewer, publicID string, read, archived *bool, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		set := func(column string, on *bool) error {
			if on == nil {
				return nil
			}
			value := any(nil)
			if *on {
				value = now.UnixMilli()
			}
			// A notice keeps the time it was first read or archived.
			_, err := tx.Exec(`UPDATE notifications SET `+column+` = CASE WHEN ? IS NULL THEN NULL ELSE ifnull(`+column+`, ?) END
				WHERE public_id = ? AND `+noticeVisible, value, value, publicID, v.UserID, v.ProfileID)
			return err
		}
		var found bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM notifications WHERE public_id = ? AND `+noticeVisible+`)`,
			publicID, v.UserID, v.ProfileID).Scan(&found); err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		if err := set("read_at", read); err != nil {
			return err
		}
		if err := set("archived_at", archived); err != nil {
			return err
		}
		return appendChange(tx, Change{At: now.UnixMilli(), Topic: TopicNotice, Op: OpUpdated, RefID: v.UserID})
	})
}

// ReadAllNotices marks every unread notice the profile reads as read.
func (d *DB) ReadAllNotices(ctx context.Context, v Viewer, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE notifications SET read_at = ? WHERE `+noticeVisible+` AND read_at IS NULL`, now.UnixMilli(), v.UserID, v.ProfileID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		return appendChange(tx, Change{At: now.UnixMilli(), Topic: TopicNotice, Op: OpUpdated, RefID: v.UserID})
	})
}

// RemoveOldNotices removes notices made before the time, a batch at a time, and reports how
// many went. The daily tidy-up calls it with the owner's keeping time.
func (d *DB) RemoveOldNotices(ctx context.Context, before time.Time) (int, error) {
	removed := 0
	for {
		var n int64
		err := d.write(ctx, Background, func(tx *sql.Tx) error {
			res, err := tx.Exec(`DELETE FROM notifications WHERE id IN (SELECT id FROM notifications WHERE created_at < ? LIMIT 1000)`, before.UnixMilli())
			if err == nil {
				n, err = res.RowsAffected()
			}
			return err
		})
		removed += int(n)
		if err != nil || n == 0 {
			return removed, err
		}
	}
}
