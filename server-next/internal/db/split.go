package db

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"strings"
	"time"
)

// SplitVersion takes one version out of its title and makes it a title of its own, named from
// its file and unmatched. Everyone's state stays with the original. The new title takes the
// file keys of the version's files, which a scan looks up before anything else, so the split
// holds through every rescan. It returns the new title's id.
func (d *DB) SplitVersion(ctx context.Context, versionID string) (string, error) {
	publicID := NewID("itm")
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		var media, item, library int64
		var kind ItemKind
		var parent, root, index, parentIndex, rating sql.NullInt64
		err := tx.QueryRow(`SELECT m.id, i.id, i.library_id, i.kind, i.parent_id, i.root_id, i.index_number, i.parent_index, i.rating_age
			FROM media m JOIN items i ON i.id = m.item_id WHERE m.public_id = ?`, versionID).
			Scan(&media, &item, &library, &kind, &parent, &root, &index, &parentIndex, &rating)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var versions int
		if err := tx.QueryRow(`SELECT count(*) FROM media WHERE item_id = ?`, item).Scan(&versions); err != nil {
			return err
		}
		if versions < 2 {
			return ErrConflict
		}
		// The keys of the version's files: the path's id, a slash and the file's path.
		rows, err := tx.Query(`SELECT d.library_path_id || '/' || CASE WHEN d.rel_path = '' THEN '' ELSE d.rel_path || '/' END || f.name, f.name
			FROM media_parts p JOIN files f ON f.id = p.file_id JOIN directories d ON d.id = f.directory_id
			WHERE p.media_id = ? ORDER BY p.position`, media)
		if err != nil {
			return err
		}
		var keys []string
		name := ""
		for rows.Next() {
			var key, file string
			if err := rows.Scan(&key, &file); err != nil {
				rows.Close()
				return err
			}
			keys = append(keys, key)
			if name == "" {
				name = strings.TrimSuffix(file, path.Ext(file))
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		row := itemRow{ImportItem: &ImportItem{Kind: kind, Title: name}, libraryID: library, publicID: publicID,
			parentID: parent.Int64, rootID: root.Int64, addedAt: now}
		if index.Valid {
			n := int(index.Int64)
			row.IndexNumber = &n
		}
		if parentIndex.Valid {
			n := int(parentIndex.Int64)
			row.ParentIndex = &n
		}
		if rating.Valid {
			age := int(rating.Int64)
			row.ratingAge = &age
		}
		id, err := b.insertItem(row)
		if err != nil {
			return err
		}
		// The owner's labels stay on the version split off, or a profile that blocks one would
		// see the new title.
		labels, err := queryInts(tx, `SELECT it.tag_id FROM item_tags it JOIN tags t ON t.id = it.tag_id
			WHERE it.item_id = ? AND t.kind = ?`, item, TagLabel)
		if err != nil {
			return err
		}
		for _, label := range labels {
			if err := b.tagItem(id, label); err != nil {
				return err
			}
		}
		if _, err := b.exec(`UPDATE media SET item_id = ?, position = 0 WHERE id = ?`, id, media); err != nil {
			return err
		}
		for _, key := range keys {
			if _, err := b.exec(`UPDATE item_keys SET item_id = ? WHERE library_id = ? AND kind = ? AND (key = ? OR key > ? AND key < ?)`,
				id, library, KeyFile, key, key+"#", key+"$"); err != nil {
				return err
			}
			if err := b.insertItemKey(library, ItemKey{Kind: KeyFile, Key: key}, id); err != nil && !isUnique(err) {
				return err
			}
		}
		leaf := 0
		if kind.Playable() {
			leaf = 1
		}
		for _, holder := range []int64{parent.Int64, root.Int64} {
			if holder == 0 || holder == parent.Int64 && holder == root.Int64 && leaf == 0 {
				continue
			}
			children := 0
			if holder == parent.Int64 {
				children = 1
			}
			if _, err := b.exec(`UPDATE items SET child_count = child_count + ?, leaf_count = leaf_count + ? WHERE id = ?`,
				children, leaf, holder); err != nil {
				return err
			}
			if parent.Int64 == root.Int64 {
				break
			}
		}
		for _, change := range []Change{{Op: OpUpdated, RefID: item}, {Op: OpCreated, RefID: id}} {
			change.At, change.Topic, change.LibraryID = now, TopicItem, library
			if err := appendChange(tx, change); err != nil {
				return err
			}
		}
		return nil
	})
	return publicID, err
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
