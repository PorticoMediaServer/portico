package db

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// MergeItems joins two titles of the same kind in the same library: the title merged into
// keeps its details and gains the other's versions, and the other is gone. Nobody loses
// state: for each profile the merged title is watched if either was, keeps the later progress
// and last-played date, adds the play counts, and keeps the rating and favourite of the title
// merged into, else the other's. List entries move, and a list that held both keeps one. Two
// shows merge season by season and episode by episode. The other title's keys move too, so
// the merge holds through every scan: its files are this title's from now on.
func (d *DB) MergeItems(ctx context.Context, fromID, intoID string) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		from, err := itemID(b, fromID)
		if err != nil {
			return err
		}
		into, err := itemID(b, intoID)
		if err != nil {
			return err
		}
		return mergeItems(b, from, into, time.Now().UnixMilli())
	})
}

func itemID(b *batchTx, publicID string) (int64, error) {
	var id int64
	err := b.queryRow(`SELECT id FROM items WHERE public_id = ?`, publicID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// mergeItems merges one top-level title, or one of two siblings, into the other.
func mergeItems(b *batchTx, from, into, now int64) error {
	type row struct {
		library      int64
		kind         ItemKind
		parent, root sql.NullInt64
	}
	var a, z row
	for id, r := range map[int64]*row{from: &a, into: &z} {
		if err := b.queryRow(`SELECT library_id, kind, parent_id, root_id FROM items WHERE id = ?`, id).Scan(&r.library, &r.kind, &r.parent, &r.root); err != nil {
			return err
		}
	}
	switch {
	case from == into:
		return invalid("a title cannot be merged with itself")
	case a.library != z.library || a.kind != z.kind || a.parent != z.parent:
		return invalid("only two titles of the same kind in the same library can be merged")
	}
	if err := mergeInto(b, a.library, from, into, now); err != nil {
		return err
	}
	if _, err := b.exec(`DELETE FROM items WHERE id = ?`, from); err != nil {
		return err
	}
	// Counts are worked out again for the merged title, what it holds and what holds it.
	if _, err := b.exec(`UPDATE items SET child_count = (SELECT count(*) FROM items c WHERE c.parent_id = items.id),
		leaf_count = (SELECT count(*) FROM items c WHERE (c.parent_id = items.id OR c.root_id = items.id) AND c.kind IN `+playableKindsSQL+`)
		WHERE id IN (?, ?, ?) OR parent_id = ?`, into, z.parent.Int64, z.root.Int64, into); err != nil {
		return err
	}
	for _, change := range []Change{{Op: OpDeleted, RefID: from}, {Op: OpUpdated, RefID: into}} {
		change.At, change.Topic, change.LibraryID = now, TopicItem, a.library
		if err := appendChange(b.tx, change); err != nil {
			return err
		}
	}
	// What moved under the remaining title and has no rating of its own takes that title's, so a
	// moved episode is hidden wherever its new show is, and the copies on its tag rows follow.
	root := into
	if z.root.Valid {
		root = z.root.Int64
	}
	if _, err := b.exec(`UPDATE items SET rating_age = (SELECT rating_age FROM items WHERE id = ?1) WHERE root_id = ?1
		AND content_rating IS NULL`, root); err != nil {
		return err
	}
	if err := b.ratingChanged(root); err != nil {
		return err
	}
	return settleItem(b, a.library, into, now)
}

var mergeStateSQL = func() string {
	sets := []string{later("position_ms", "position_at"), later("position_text", "position_at"),
		`played = max(user_item_state.played, excluded.played)`, `favorite = max(user_item_state.favorite, excluded.favorite)`,
		`in_list = max(user_item_state.in_list, excluded.in_list)`, `rating = ifnull(user_item_state.rating, excluded.rating)`,
		`play_count = user_item_state.play_count + excluded.play_count`,
		`audio_language = ifnull(user_item_state.audio_language, excluded.audio_language)`,
		`subtitle_language = ifnull(user_item_state.subtitle_language, excluded.subtitle_language)`,
		`subtitle_offset_ms = ifnull(user_item_state.subtitle_offset_ms, excluded.subtitle_offset_ms)`}
	for _, at := range []string{"position_at", "played_at", "favorite_at", "in_list_at", "rating_at", "last_played_at", "updated_at"} {
		sets = append(sets, at+` = nullif(max(ifnull(excluded.`+at+`, 0), ifnull(user_item_state.`+at+`, 0)), 0)`)
	}
	return `INSERT INTO user_item_state (profile_id, item_id, ` + keptColumns + `) SELECT profile_id, ?, ` + keptColumns + `
		FROM user_item_state WHERE item_id = ? AND true ON CONFLICT (profile_id, item_id) DO UPDATE SET ` + strings.Join(sets, ", ")
}()

// mergeInto moves everything one item has onto another and leaves the first to be deleted:
// versions, everyone's state, list and queue entries, history, extras and keys. What the
// first holds (a show's seasons, a season's episodes) is merged with the other's of the same
// number, or moved under it.
func mergeInto(b *batchTx, library, from, into, now int64) error {
	steps := []struct {
		query string
		args  []any
	}{
		// The other's versions go after this title's, whatever gaps its positions have.
		{`UPDATE media SET item_id = ?, position = position + 1 + (SELECT ifnull(max(position), -1) FROM media WHERE item_id = ?)
			WHERE item_id = ?`, []any{into, into, from}},
		{mergeStateSQL, []any{into, from}},
		// A list whose entries change says so, so an editor open elsewhere cannot save over it.
		{`UPDATE lists SET revision = revision + 1 WHERE id IN (SELECT list_id FROM list_entries WHERE item_id = ?)`, []any{from}},
		// A list that holds both keeps the entry of the title merged into.
		{`UPDATE lists SET entry_count = entry_count - (SELECT count(*) FROM list_entries e WHERE e.list_id = lists.id AND e.item_id = ?)
			WHERE id IN (SELECT list_id FROM list_entries WHERE item_id = ?) AND id IN (SELECT list_id FROM list_entries WHERE item_id = ?)`,
			[]any{from, from, into}},
		{`DELETE FROM list_entries WHERE item_id = ? AND list_id IN (SELECT list_id FROM list_entries WHERE item_id = ?)`, []any{from, into}},
		{`UPDATE list_entries SET item_id = ? WHERE item_id = ?`, []any{into, from}},
		{`UPDATE queue_entries SET item_id = ? WHERE item_id = ?`, []any{into, from}},
		{`UPDATE play_history SET item_id = ? WHERE item_id = ?`, []any{into, from}},
		{`UPDATE OR IGNORE item_relations SET item_id = ? WHERE item_id = ?`, []any{into, from}},
		{`UPDATE OR IGNORE item_relations SET related_item_id = ? WHERE related_item_id = ?`, []any{into, from}},
		// The keys that find a title across scans move, except its place under its parent
		// ("812/s2"): the title merged into has its own place.
		{`UPDATE item_keys SET item_id = ? WHERE item_id = ? AND NOT (kind = ? AND key GLOB '[0-9]*/*')`, []any{into, from, KeyName}},
		// The merged-away title's own id now names the one that remains.
		{`INSERT OR REPLACE INTO item_keys (library_id, kind, key, item_id) SELECT ?, ?, public_id, ? FROM items WHERE id = ?`,
			[]any{library, KeyMerged, into, from}},
	}
	for _, step := range steps {
		if _, err := b.exec(step.query, step.args...); err != nil {
			return err
		}
	}
	type child struct {
		id    int64
		kind  ItemKind
		index sql.NullInt64
	}
	rows, err := b.tx.Query(`SELECT id, kind, index_number FROM items WHERE parent_id = ? ORDER BY id`, from)
	if err != nil {
		return err
	}
	var children []child
	for rows.Next() {
		var c child
		if err := rows.Scan(&c.id, &c.kind, &c.index); err != nil {
			rows.Close()
			return err
		}
		children = append(children, c)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var intoRoot sql.NullInt64
	if err := b.queryRow(`SELECT root_id FROM items WHERE id = ?`, into).Scan(&intoRoot); err != nil {
		return err
	}
	root := into
	if intoRoot.Valid {
		root = intoRoot.Int64
	}
	// Search and open screens follow the change list: every season and episode that goes, and
	// every one that is now somewhere else, is recorded there.
	var changed []Change
	for _, c := range children {
		var twin int64
		err := sql.ErrNoRows
		if c.index.Valid {
			err = b.queryRow(`SELECT id FROM items WHERE parent_id = ? AND kind = ? AND index_number = ? ORDER BY id LIMIT 1`,
				into, c.kind, c.index.Int64).Scan(&twin)
		}
		switch {
		case err == nil:
			if err := mergeInto(b, library, c.id, twin, now); err != nil {
				return err
			}
			if _, err := b.exec(`DELETE FROM items WHERE id = ?`, c.id); err != nil {
				return err
			}
			changed = append(changed, Change{Op: OpDeleted, RefID: c.id}, Change{Op: OpUpdated, RefID: twin})
		case errors.Is(err, sql.ErrNoRows):
			// Nothing of that number there: it moves across with all it holds, and its place
			// key is rewritten for its new parent.
			if _, err := b.exec(`UPDATE items SET parent_id = ?, root_id = ? WHERE id = ?`, into, root, c.id); err != nil {
				return err
			}
			under, err := queryInts(b.tx, `SELECT id FROM items WHERE parent_id = ?`, c.id)
			if err != nil {
				return err
			}
			if _, err := b.exec(`UPDATE items SET root_id = ? WHERE parent_id = ?`, root, c.id); err != nil {
				return err
			}
			for _, id := range append(under, c.id) {
				changed = append(changed, Change{Op: OpUpdated, RefID: id})
			}
			old, moved := strconv.FormatInt(from, 10)+"/", strconv.FormatInt(into, 10)+"/"
			if _, err := b.exec(`UPDATE OR REPLACE item_keys SET key = ? || substr(key, ?) WHERE item_id = ? AND kind = ? AND key GLOB ?`,
				moved, len(old)+1, c.id, KeyName, old+"*"); err != nil {
				return err
			}
		default:
			return err
		}
	}
	for _, change := range changed {
		change.At, change.Topic, change.LibraryID = now, TopicItem, library
		if err := appendChange(b.tx, change); err != nil {
			return err
		}
	}
	return nil
}

// mergedIntoSQL finds the title a merged-away public id now names. item_keys is keyed by
// library first, so the libraries (a handful) are named to keep it a key lookup.
const mergedIntoSQL = `SELECT i.public_id FROM item_keys k JOIN items i ON i.id = k.item_id
	WHERE k.library_id IN (SELECT id FROM libraries) AND k.kind = ? AND k.key = ?`
