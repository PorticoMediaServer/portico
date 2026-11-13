package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
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
			// A file of one of its versions came or went: its widest playable version may differ.
			if err := b.versionsChanged(id); err != nil {
				return err
			}
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

// removeItem takes an item and everything under it out of the library, with its extras, and
// forgets it: what each profile had on it (watched marks, progress, ratings) goes with it, and
// a title added again later is a new title. The finished plays in play_history stay, each
// with the title's name as it was. The rows of its files go too: a file that is still on disk
// is found as new by the next scan.
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
	series, err := queryInts(b.tx, subtree+`SELECT DISTINCT r.item_id FROM item_relations r JOIN t ON t.id = r.related_item_id WHERE r.kind = ?`,
		id, RelationSeriesMember)
	if err != nil {
		return err
	}
	// The item cascade removes list entries too. Store each affected list's surviving count
	// in this same transaction, including repeated entries and entries of descendants.
	if _, err := b.exec(subtree+`UPDATE lists SET entry_count = (SELECT count(*) FROM list_entries e
		WHERE e.list_id = lists.id AND e.item_id NOT IN (SELECT id FROM t))
		WHERE id IN (SELECT e.list_id FROM list_entries e JOIN t ON t.id = e.item_id)`, id); err != nil {
		return err
	}
	if _, err := b.exec(`DELETE FROM items WHERE id = ?`, id); err != nil {
		return err
	}
	// Whoever had watched everything else under them may now have watched all of it.
	// Settled here and not left to the caller, so every path that removes a title is covered:
	// a missing file, the emptied trash, a deleted title, a deleted library.
	b.leavesLeft(parent.Int64, root.Int64)
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
		} else if err := b.collectionsRated(holder); err != nil {
			return err
		}
	}
	// A series that held one of these volumes is rated again from the ones it has left.
	return b.collectionsRated(series...)
}

func queryInts(tx *sql.Tx, query string, args ...any) ([]int64, error) {
	return ids(context.Background(), tx, query, args...)
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
		err := d.write(ctx, Background, func(tx *sql.Tx) (err error) {
			ids, err := queryInts(tx, `SELECT i.id`+topMissing+` AND i.missing_since_at <= ? LIMIT 20`, libraryID, KindExtra, missingBy.UnixMilli())
			if err != nil {
				return err
			}
			b := newBatchTx(tx)
			defer b.close()
			now := time.Now().UnixMilli()
			defer func() {
				if err == nil {
					err = b.settleWatchedForAll(now)
				}
			}()
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
