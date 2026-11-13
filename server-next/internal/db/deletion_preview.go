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
	return d.deletionPreview(ctx, v, `i.public_id = ?`, id, false)
}

func (d *DB) VersionDeletionPreview(ctx context.Context, v Viewer, id string) (DeletionImpact, error) {
	return d.deletionPreview(ctx, v, `i.id = (SELECT item_id FROM media WHERE public_id = ?)`, id, true)
}

var errPreviewRollback = errors.New("deletion preview: roll back database changes")

// Read the selection on the read handle; a preview may be a moment stale. Only the exact
// database deletion runs on the writer. Its deliberate rollback discards rows, changes-feed
// entries and watched-state settlement. No disk deletion or change notification runs here.
func (d *DB) deletionPreview(ctx context.Context, v Viewer, where, id string, version bool) (DeletionImpact, error) {
	var out DeletionImpact
	del, err := d.deletion(ctx, v, where, id, version)
	if err != nil {
		return out, err
	}
	out.Deletion = del
	out.Titles, out.ListEntries, out.OtherStates = 0, 0, 0
	err = d.write(ctx, User, func(tx *sql.Tx) error {
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
			if itemID != del.ItemID && (version || parent != del.ItemID && root != del.ItemID) {
				out.AlsoRemoves = append(out.AlsoRemoves, other)
			}
			return nil
		}
		files := make([]int64, len(del.Files))
		for n, f := range del.Files {
			files[n] = f.ID
		}
		if err := recordDeleted(tx, del, files, removing); err != nil {
			return err
		}
		return errPreviewRollback
	})
	if errors.Is(err, errPreviewRollback) {
		err = nil
	}
	return out, err
}
