package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// settleItem works out whether an item can be played and stores it if that changed, then does
// the same for what holds it. A playable item is available while one version has every file.
// A show, season, album or artist is available while something under it is. An item that has
// nothing left at all (its only file was renamed into another title, its last episode moved
// to another show) is removed at once: there is nothing that could come back to it.
func settleItem(b *batchTx, libraryID, id, now int64) error {
	for id != 0 {
		var kind ItemKind
		var was bool
		var parent sql.NullInt64
		err := b.queryRow(`SELECT kind, available, parent_id FROM items WHERE id = ?`, id).Scan(&kind, &was, &parent)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var has, available bool
		if kind.Playable() {
			err = b.queryRow(`SELECT EXISTS (SELECT 1 FROM media WHERE item_id = ?), EXISTS (SELECT 1 FROM media m WHERE m.item_id = ?
				AND EXISTS (SELECT 1 FROM media_parts p WHERE p.media_id = m.id) AND NOT EXISTS (SELECT 1 FROM media_parts p
				JOIN files f ON f.id = p.file_id WHERE p.media_id = m.id AND f.missing_since_at IS NOT NULL))`, id, id).Scan(&has, &available)
		} else {
			err = b.queryRow(`SELECT EXISTS (SELECT 1 FROM items WHERE parent_id = ?),
				EXISTS (SELECT 1 FROM items WHERE parent_id = ? AND available = 1)`, id, id).Scan(&has, &available)
		}
		if err != nil {
			return err
		}
		switch {
		case !has:
			if err := removeItem(b, libraryID, id, now); err != nil {
				return err
			}
		case available != was:
			var since any
			if !available {
				since = now
			}
			if _, err := b.exec(`UPDATE items SET available = ?, missing_since_at = ?, updated_at = ? WHERE id = ?`, available, since, now, id); err != nil {
				return err
			}
			if err := appendChange(b.tx, Change{At: now, Topic: TopicItem, Op: OpUpdated, RefID: id, LibraryID: libraryID}); err != nil {
				return err
			}
		default:
			return nil
		}
		id = parent.Int64
	}
	return nil
}

// keptColumns are the columns of user_item_state that removed_state keeps.
const keptColumns = `position_ms, position_text, position_at, played, played_at, play_count, last_played_at, favorite, favorite_at,
	in_list, in_list_at, hidden, hidden_at, rating, rating_at, audio_language, subtitle_language, subtitle_offset_ms, updated_at`

// keptStateAge is how long a removed title's state is kept for its return.
const keptStateAge = 365 * 24 * time.Hour

// identities are the names a title's state is kept under and found again by, best first: its
// provider id with its kind when it has one, and its kind, folded title and year. An episode,
// a season or a track is its show's or album's identity and its own numbers
// ("id:tmdb:show:1438/3#0.1/4#1.5", "name:2|the wire|2002/3#0.1/4#1.5"), so it works for a
// show nobody matched, and nothing in it depends on the library.
func identities(b *batchTx, id int64) ([]string, error) {
	tail := ""
	for at := id; ; {
		var kind ItemKind
		var title string
		var year, parent, index, parentIndex sql.NullInt64
		err := b.queryRow(`SELECT kind, title, year, parent_id, index_number, parent_index FROM items WHERE id = ?`, at).
			Scan(&kind, &title, &year, &parent, &index, &parentIndex)
		if err != nil {
			return nil, err
		}
		own := fmt.Sprintf("%d|%s|%d", kind, sorttext.Fold(title), year.Int64)
		if parent.Valid {
			// Under a parent an item is its numbers, or its name when it has none.
			if index.Valid {
				own = fmt.Sprintf("%d#%d.%d", kind, parentIndex.Int64, index.Int64)
			}
			tail = "/" + own + tail
			at = parent.Int64
			continue
		}
		var out []string
		var provider string
		err = b.queryRow(`SELECT key FROM item_keys WHERE item_id = ? AND kind = ? ORDER BY key LIMIT 1`, at, KeyProvider).Scan(&provider)
		if err == nil {
			out = append(out, "id:"+provider+tail)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return append(out, "name:"+own+tail), nil
	}
}

// removeItem takes an item and everything under it out of the library, with its extras. What
// each profile had on it is kept in removed_state. The rows of its files go too: a file that
// is still on disk is found as new by the next scan.
func removeItem(b *batchTx, libraryID, id, now int64) error {
	var kind ItemKind
	var parent, root sql.NullInt64
	err := b.queryRow(`SELECT kind, parent_id, root_id FROM items WHERE id = ?`, id).Scan(&kind, &parent, &root)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	const subtree = `WITH RECURSIVE t(id) AS (SELECT ? UNION ALL SELECT i.id FROM items i JOIN t ON i.parent_id = t.id) `
	ids, err := queryInts(b.tx, subtree+`SELECT id FROM t`, id)
	if err != nil {
		return err
	}
	extras, err := queryInts(b.tx, subtree+`SELECT r.related_item_id FROM item_relations r JOIN t ON t.id = r.item_id WHERE r.kind IN (?, ?)`,
		id, RelationExtra, RelationTrailer)
	if err != nil {
		return err
	}
	for _, extra := range extras {
		if err := removeItem(b, libraryID, extra, now); err != nil {
			return err
		}
	}
	withState, err := queryInts(b.tx, subtree+`SELECT DISTINCT s.item_id FROM user_item_state s JOIN t ON t.id = s.item_id`, id)
	if err != nil {
		return err
	}
	for _, item := range withState {
		names, err := identities(b, item)
		if err != nil {
			return err
		}
		if _, err := b.exec(`INSERT OR REPLACE INTO removed_state (profile_id, identity, removed_at, `+keptColumns+`)
			SELECT profile_id, ?, ?, `+keptColumns+` FROM user_item_state WHERE item_id = ?`, names[0], now, item); err != nil {
			return err
		}
	}
	var leaves int
	if err := b.queryRow(subtree+`SELECT count(*) FROM items i JOIN t ON t.id = i.id WHERE i.kind IN `+playableKindsSQL+` AND i.kind <> ?`,
		id, KindExtra).Scan(&leaves); err != nil {
		return err
	}
	if _, err := b.exec(subtree+`DELETE FROM files WHERE id IN (SELECT p.file_id FROM media_parts p JOIN media m ON m.id = p.media_id
		JOIN t ON t.id = m.item_id UNION SELECT f.id FROM files f JOIN media m ON m.id = f.media_id JOIN t ON t.id = m.item_id)
		AND NOT EXISTS (SELECT 1 FROM media_parts p JOIN media m ON m.id = p.media_id WHERE p.file_id = files.id
		AND m.item_id NOT IN (SELECT id FROM t))`, id); err != nil {
		return err
	}
	if parent.Valid {
		if _, err := b.exec(`UPDATE items SET child_count = child_count - 1, leaf_count = leaf_count - ? WHERE id = ?`, leaves, parent.Int64); err != nil {
			return err
		}
		if root.Int64 != parent.Int64 {
			if _, err := b.exec(`UPDATE items SET leaf_count = leaf_count - ? WHERE id = ?`, leaves, root.Int64); err != nil {
				return err
			}
		}
	}
	// The library's collections that hold this title. One left holding nothing goes with it.
	holders, err := queryInts(b.tx, subtree+`SELECT DISTINCT r.item_id FROM item_relations r JOIN t ON t.id = r.related_item_id WHERE r.kind = ?`,
		id, RelationCollectionMember)
	if err != nil {
		return err
	}
	if _, err := b.exec(`DELETE FROM items WHERE id = ?`, id); err != nil {
		return err
	}
	for _, gone := range ids {
		if err := appendChange(b.tx, Change{At: now, Topic: TopicItem, Op: OpDeleted, RefID: gone, LibraryID: libraryID}); err != nil {
			return err
		}
	}
	for _, holder := range holders {
		var held bool
		if err := b.queryRow(`SELECT EXISTS (SELECT 1 FROM item_relations WHERE item_id = ? AND kind = ?)`, holder,
			RelationCollectionMember).Scan(&held); err != nil {
			return err
		}
		if !held {
			if err := removeItem(b, libraryID, holder, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func queryInts(tx *sql.Tx, query string, args ...any) ([]int64, error) {
	return ids(context.Background(), tx, query, args...)
}

// restore gives a newly made item back what each profile had on it before it was removed.
func (im *importer) restore(id int64) error {
	if im.restores == nil {
		var any bool
		if err := im.queryRow(`SELECT EXISTS (SELECT 1 FROM removed_state)`).Scan(&any); err != nil {
			return err
		}
		im.restores = &any
	}
	if !*im.restores {
		return nil
	}
	return restoreState(im.batchTx, id)
}

// later is the SQL for "the kept value, when it was set later than the one there".
func later(column, at string) string {
	return column + ` = CASE WHEN ifnull(excluded.` + at + `, 0) > ifnull(user_item_state.` + at + `, 0) THEN excluded.` + column +
		` ELSE user_item_state.` + column + ` END`
}

var restoreSQL = func() string {
	sets := []string{later("position_ms", "position_at"), later("position_text", "position_at"), later("played", "played_at"),
		later("favorite", "favorite_at"), later("in_list", "in_list_at"), later("hidden", "hidden_at"), later("rating", "rating_at")}
	// SQLite reads every column as the row was before the update, so each time can become the
	// later of the two in the same statement.
	for _, at := range []string{"position_at", "played_at", "favorite_at", "in_list_at", "hidden_at", "rating_at", "last_played_at", "updated_at"} {
		sets = append(sets, at+` = nullif(max(ifnull(excluded.`+at+`, 0), ifnull(user_item_state.`+at+`, 0)), 0)`)
	}
	sets = append(sets, `play_count = max(excluded.play_count, user_item_state.play_count)`,
		`audio_language = ifnull(user_item_state.audio_language, excluded.audio_language)`,
		`subtitle_language = ifnull(user_item_state.subtitle_language, excluded.subtitle_language)`,
		`subtitle_offset_ms = ifnull(user_item_state.subtitle_offset_ms, excluded.subtitle_offset_ms)`)
	return `INSERT INTO user_item_state (profile_id, item_id, ` + keptColumns + `) SELECT profile_id, ?, ` + keptColumns + `
		FROM removed_state WHERE identity = ? AND true ON CONFLICT (profile_id, item_id) DO UPDATE SET ` + strings.Join(sets, ", ")
}()

// restoreState moves kept state onto an item, in the transaction that made or matched it, and
// drops the kept rows. Where a profile already has state on the item, the later edit wins
// field by field. Matching calls it too, when a title gets the provider id its removed self had.
func restoreState(b *batchTx, id int64) error {
	names, err := identities(b, id)
	if err != nil {
		return err
	}
	for _, identity := range names {
		res, err := b.exec(restoreSQL, id, identity)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		if _, err := b.exec(`DELETE FROM removed_state WHERE identity = ?`, identity); err != nil {
			return err
		}
	}
	return nil
}

// PruneRemovedState drops the kept state of titles that did not return within a year.
func (d *DB) PruneRemovedState(ctx context.Context) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM removed_state WHERE removed_at < ?`, time.Now().Add(-keptStateAge).UnixMilli())
		return err
	})
}

// MissingItem is a title in a library's trash.
type MissingItem struct {
	PublicID     string
	Kind         ItemKind
	Title        string
	Year         int
	MissingSince int64
}

// topMissing picks the missing titles that are not inside another missing title: a missing
// show is one entry, not one for every episode.
const topMissing = ` FROM items i WHERE i.library_id = ? AND i.available = 0
	AND NOT EXISTS (SELECT 1 FROM items p WHERE p.id = i.parent_id AND p.available = 0) AND i.kind <> ? `

// MissingItems lists a library's trash, the longest missing first.
func (d *DB) MissingItems(ctx context.Context, libraryID int64, limit int) ([]MissingItem, int, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT count(*)`+topMissing, libraryID, KindExtra).Scan(&total); err != nil {
		return nil, 0, err
	}
	items, err := queryAll(ctx, tx, `SELECT i.public_id, i.kind, i.title, ifnull(i.year, 0), ifnull(i.missing_since_at, 0)`+topMissing+`
		ORDER BY i.missing_since_at, i.id LIMIT ?`, []any{libraryID, KindExtra, limit}, func(m *MissingItem) []any {
		return []any{&m.PublicID, &m.Kind, &m.Title, &m.Year, &m.MissingSince}
	})
	return items, total, err
}

// EmptyTrash removes a library's missing titles that went missing at or before the given
// time, a few at a time so people's writes go between, and returns how many it removed.
// Nothing on disk is touched.
func (d *DB) EmptyTrash(ctx context.Context, libraryID int64, missingBy time.Time) (int, error) {
	removed := 0
	for {
		n := 0
		err := d.write(ctx, Background, func(tx *sql.Tx) error {
			ids, err := queryInts(tx, `SELECT i.id`+topMissing+` AND i.missing_since_at <= ? LIMIT 20`, libraryID, KindExtra, missingBy.UnixMilli())
			if err != nil {
				return err
			}
			b := newBatchTx(tx)
			defer b.close()
			now := time.Now().UnixMilli()
			for _, id := range ids {
				var parent sql.NullInt64
				if err := b.queryRow(`SELECT parent_id FROM items WHERE id = ?`, id).Scan(&parent); err != nil {
					return err
				}
				if err := removeItem(b, libraryID, id, now); err != nil {
					return err
				}
				if err := settleItem(b, libraryID, parent.Int64, now); err != nil {
					return err
				}
			}
			n = len(ids)
			return nil
		})
		removed += n
		if err != nil || n == 0 {
			return removed, err
		}
	}
}
