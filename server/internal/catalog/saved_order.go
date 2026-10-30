package catalog

import (
	"database/sql"
	"errors"
	"sort"

	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
)

// A personal collection keeps its own order the way a playlist does: each entry
// has an order key, a new entry takes a key after the last, and a move writes
// new keys for the moved entries only (between their new neighbours), so a
// reorder never rewrites a large collection. The collection's revision fences
// every change: a reorder made against an older revision is refused.

// appendSavedEntry adds an item at the end of a collection (nothing when it is
// already there).
func appendSavedEntry(tx *sql.Tx, resource, entry string, item int64) error {
	var last string
	e := tx.QueryRow(`SELECT order_key FROM saved_resource_entries INDEXED BY saved_resource_entries_order WHERE resource_id=? ORDER BY order_key DESC,id DESC LIMIT 1`, resource).Scan(&last)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	_, e = tx.Exec(`INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key) VALUES(?,?,?,?) ON CONFLICT(resource_id,item_id) DO NOTHING`, entry, resource, item, playlistEndKey(last))
	return e
}

// reorderSavedEntries moves entries of one collection. With after, they go, in
// the order given, just after that entry ("" the front); without it they swap
// among the places they already hold and everything else stays put.
func reorderSavedEntries(tx *sql.Tx, resource string, ids []string, after *string) error {
	seen := map[string]bool{}
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			return requestinput.New("a reorder names distinct entries")
		}
		seen[id] = true
		var key string
		if e := tx.QueryRow(`SELECT order_key FROM saved_resource_entries WHERE resource_id=? AND id=?`, resource, id).Scan(&key); e != nil {
			return e
		}
		keys = append(keys, key)
	}
	if after == nil {
		sort.Strings(keys)
	} else {
		if seen[*after] {
			return requestinput.New("an entry can't move after itself")
		}
		lower, upper := "", ""
		if *after != "" {
			if e := tx.QueryRow(`SELECT order_key FROM saved_resource_entries WHERE resource_id=? AND id=?`, resource, *after).Scan(&lower); e != nil {
				return e
			}
		}
		e := tx.QueryRow(`SELECT order_key FROM saved_resource_entries INDEXED BY saved_resource_entries_order WHERE resource_id=? AND order_key>? AND NOT EXISTS(SELECT 1 FROM json_each(?) moved WHERE moved.value=id) ORDER BY order_key,id LIMIT 1`, resource, lower, idsJSON(ids)).Scan(&upper)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		for i := range keys {
			keys[i] = playlistRank(lower, upper, int64(i), int64(len(keys)))
		}
	}
	for i, id := range ids {
		if _, e := tx.Exec(`UPDATE saved_resource_entries SET order_key=? WHERE resource_id=? AND id=?`, keys[i], resource, id); e != nil {
			return e
		}
	}
	return nil
}
