package catalog

import (
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Removing an entry from a playlist or a personal collection can be undone for
// a short while, like a container's watched mark: the removal answers with an
// undo token, and sending that token back puts the very occurrences it removed
// back where they were (the same entry ids and, in a playlist, the same order
// key). One live token per person and list; expired ones are cleared a few at
// a time by later removals.
const savedEntryUndoLifetime = 30 * time.Second

type removedEntry struct {
	entry    string
	item     sql.NullInt64
	orderKey string
}

// captureEntryUndo records removed occurrences under a new token and answers
// the token and when it lapses. No removed entries, no token.
func captureEntryUndo(tx *sql.Tx, owner, kind, resource string, removed []removedEntry) (string, string, error) {
	now := time.Now()
	if _, e := tx.Exec(`DELETE FROM saved_entry_undo WHERE token IN(SELECT token FROM saved_entry_undo WHERE expires_ms<? LIMIT 16) OR (owner_key=? AND kind=? AND resource_id=?)`, now.UnixMilli(), owner, kind, resource); e != nil {
		return "", "", e
	}
	if len(removed) == 0 {
		return "", "", nil
	}
	token := identity.Token()
	until := now.UTC().Add(savedEntryUndoLifetime)
	for _, r := range removed {
		if _, e := tx.Exec(`INSERT INTO saved_entry_undo(token,owner_key,kind,resource_id,entry_id,item_id,order_key,expires_ms) VALUES(?,?,?,?,?,?,?,?)`, token, owner, kind, resource, r.entry, r.item, r.orderKey, until.UnixMilli()); e != nil {
			return "", "", e
		}
	}
	return token, until.Format("2006-01-02T15:04:05.000Z"), nil
}

// takeEntryUndo answers what a live token removed from this list and spends
// it. A lapsed, spent or someone else's token is not found.
func takeEntryUndo(tx *sql.Tx, owner, kind, resource, token string) ([]removedEntry, error) {
	rows, e := tx.Query(`SELECT entry_id,item_id,order_key FROM saved_entry_undo WHERE token=? AND owner_key=? AND kind=? AND resource_id=? AND expires_ms>=? ORDER BY entry_id`, token, owner, kind, resource, time.Now().UnixMilli())
	if e != nil {
		return nil, e
	}
	var out []removedEntry
	for rows.Next() {
		var r removedEntry
		if e = rows.Scan(&r.entry, &r.item, &r.orderKey); e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, r)
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return nil, e
	}
	rows.Close()
	if len(out) == 0 {
		return nil, sql.ErrNoRows
	}
	_, e = tx.Exec(`DELETE FROM saved_entry_undo WHERE token=?`, token)
	return out, e
}
