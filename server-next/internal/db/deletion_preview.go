package db

import (
	"context"
	"database/sql"
	"errors"
)

// DeletionImpact is the complete impact of a successful deletion, including emptied holders.
// Deletion remains the execution selection; AlsoRemoves never becomes a media deletion list.
type DeletionImpact struct {
	Deletion
	AlsoRemoves []SharedTitle
}

func (d *DB) ItemDeletionPreview(ctx context.Context, v Viewer, id string) (DeletionImpact, error) {
	del, err := d.ItemDeletion(ctx, v, id)
	if err != nil {
		return DeletionImpact{}, err
	}
	return d.deletionPreview(ctx, v, []Deletion{del})
}

func (d *DB) VersionDeletionPreview(ctx context.Context, v Viewer, id string) (DeletionImpact, error) {
	del, err := d.VersionDeletion(ctx, v, id)
	if err != nil {
		return DeletionImpact{}, err
	}
	return d.deletionPreview(ctx, v, []Deletion{del})
}

// SelectionDeletionPreview uses the same dry run for selections already read and validated
// by the caller. No selection lookup or subtree expansion takes place on the writer.
func (d *DB) SelectionDeletionPreview(ctx context.Context, v Viewer, all []Deletion) (DeletionImpact, error) {
	return d.deletionPreview(ctx, v, all)
}

var errPreviewRollback = errors.New("deletion preview: roll back database changes")

// Only the exact database deletion runs on the writer. Its deliberate rollback discards
// rows, changes-feed entries and watched-state settlement. No file deletion or notification
// runs here. Read selections may be a moment stale; recordDeleted tolerates vanished rows.
func (d *DB) deletionPreview(ctx context.Context, v Viewer, all []Deletion) (DeletionImpact, error) {
	var out DeletionImpact
	selected, whole, files := map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
	for n, del := range all {
		if n == 0 {
			out.Deletion = del
			out.Files, out.Bytes = nil, 0
		}
		selected[del.ItemID], whole[del.ItemID] = true, del.MediaID == 0
		for _, f := range del.Files {
			if !files[f.ID] {
				files[f.ID] = true
				out.Files = append(out.Files, f)
				out.Bytes += f.Size
			}
		}
	}
	out.Titles, out.ListEntries, out.OtherStates = 0, 0, 0
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		removing := func(itemID int64) error {
			var titles, entries, states int
			err := tx.QueryRowContext(ctx, subtreeSQL+`SELECT count(*),
                (SELECT count(*) FROM list_entries e JOIN t ON t.id = e.item_id),
                (SELECT count(*) FROM user_item_state s JOIN t ON t.id = s.item_id WHERE s.profile_id <> ?) FROM t`, itemID, v.ProfileID).
				Scan(&titles, &entries, &states)
			if err != nil {
				return err
			}
			out.Titles += titles
			out.ListEntries += entries
			out.OtherStates += states
			var other SharedTitle
			var parent, root int64
			if err := tx.QueryRowContext(ctx, `SELECT id, public_id, title, ifnull(parent_id, 0), ifnull(root_id, 0) FROM items WHERE id = ?`, itemID).
				Scan(&other.ItemID, &other.PublicID, &other.Title, &parent, &root); err != nil {
				return err
			}
			// Descendants of a whole selected title are already described by that title.
			// Empty parents and titles removed through shared files or relations are additional.
			if !selected[itemID] && !whole[parent] && !whole[root] {
				out.AlsoRemoves = append(out.AlsoRemoves, other)
			}
			return nil
		}
		for _, del := range all {
			deleted := make([]int64, len(del.Files))
			for n, f := range del.Files {
				deleted[n] = f.ID
			}
			if err := recordDeleted(tx, del, deleted, removing); err != nil {
				return err
			}
		}
		return errPreviewRollback
	})
	if errors.Is(err, errPreviewRollback) {
		err = nil
	}
	return out, err
}
