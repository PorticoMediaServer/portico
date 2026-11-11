package db

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// The activity record (audit_log): who did what and when. Rows are written in the same commit
// as what they record; nothing in the app edits or clears them, and the daily tidy-up removes
// rows older than a year.

// Activity is one entry of the activity record.
type Activity struct {
	ID     int64
	At     time.Time
	Actor  string // the account's username when it was written
	Action string
	Target string
	Detail string
}

// Record writes an entry for something done outside a transaction of the data layer, such as
// stopping a stream that lives in memory.
func (d *DB) Record(ctx context.Context, actorID int64, action, target, detail string, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error { return audit(tx, actorID, action, target, detail, now) })
}

// ActivityRecord lists entries newest first, after the cursor (the id of the last one read, 0
// for the first page). action, when given, keeps the entries whose action starts with it
// ("settings." for every settings change).
func (d *DB) ActivityRecord(ctx context.Context, action string, after int64, limit int) ([]Activity, error) {
	where, args := `1 = 1`, []any{}
	if action != "" {
		// A prefix match on the action, with LIKE's own characters taken literally.
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(action)
		where, args = `action LIKE ? ESCAPE '\'`, append(args, escaped+"%")
	}
	if after > 0 {
		where, args = where+` AND id < ?`, append(args, after)
	}
	rows, err := d.read.QueryContext(ctx, `SELECT id, at, actor_name, action, ifnull(target, ''), ifnull(detail, '')
		FROM audit_log WHERE `+where+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Activity
	for rows.Next() {
		var a Activity
		var at int64
		if err := rows.Scan(&a.ID, &at, &a.Actor, &a.Action, &a.Target, &a.Detail); err != nil {
			return nil, err
		}
		a.At = time.UnixMilli(at)
		out = append(out, a)
	}
	return out, rows.Err()
}

// RemoveOldActivity removes entries written before the time, a batch at a time.
func (d *DB) RemoveOldActivity(ctx context.Context, before time.Time) error {
	for {
		var n int64
		err := d.write(ctx, Background, func(tx *sql.Tx) error {
			res, err := tx.Exec(`DELETE FROM audit_log WHERE id IN (SELECT id FROM audit_log WHERE at < ? ORDER BY at LIMIT 1000)`, before.UnixMilli())
			if err == nil {
				n, err = res.RowsAffected()
			}
			return err
		})
		if err != nil || n == 0 {
			return err
		}
	}
}

// Viewing is how a person playing is named: username first, then the profile when its name
// differs.
type Viewing struct {
	Username, Profile string
}

// ViewingNames names the profiles given, by profile id. A profile that no longer exists is absent.
func (d *DB) ViewingNames(ctx context.Context, profileIDs []int64) (map[int64]Viewing, error) {
	out := map[int64]Viewing{}
	if len(profileIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(profileIDs))
	for n, id := range profileIDs {
		args[n] = id
	}
	rows, err := d.read.QueryContext(ctx, `SELECT p.id, u.username, p.name FROM profiles p JOIN users u ON u.id = p.user_id
		WHERE p.id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var p Viewing
		if err := rows.Scan(&id, &p.Username, &p.Profile); err != nil {
			return nil, err
		}
		if strings.EqualFold(p.Profile, p.Username) {
			p.Profile = ""
		}
		out[id] = p
	}
	return out, rows.Err()
}

// accountSees is the one rule for what the owner's and administrators' pages name: a library
// is theirs to read about when their account has all libraries or that library. It decides
// both Now playing and Play history, so the two never disagree. A profile's own rating limit
// and blocked tags are a choice about what to watch and do not apply here; owner pages are
// entered only from an account's main profile. It takes the account reading as its two
// arguments, in the place it stands among the statement's others: accountSeesArgs.
func accountSees(library string) string {
	return `((SELECT all_libraries FROM users WHERE id = ?) = 1 OR ` + library +
		` IN (SELECT library_id FROM library_access WHERE user_id = ? AND library_id IS NOT NULL))`
}

func accountSeesArgs(userID int64) []any { return []any{userID, userID} }

// AccountTitles names the titles among itemIDs whose library the account may read about.
func (d *DB) AccountTitles(ctx context.Context, userID int64, itemIDs []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	var args []any
	for _, id := range itemIDs {
		args = append(args, id)
	}
	args = append(args, accountSeesArgs(userID)...)
	rows, err := d.read.QueryContext(ctx, `SELECT i.id, i.title FROM items i WHERE i.id IN (`+
		strings.TrimSuffix(strings.Repeat("?,", len(itemIDs)), ",")+`) AND `+accountSees("i.library_id"), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, err
		}
		out[id] = title
	}
	return out, rows.Err()
}
