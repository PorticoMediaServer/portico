package db

import (
	"context"
	"database/sql"
	"errors"
)

// An audiobook's page ends with "More audiobooks: other books on your server". The definition
// is silent on the row; it follows the book page of mock-up desktop-listen: other audiobooks
// of the book's own library the profile may see, newest added first (the lead's choice of
// order), finished ones among them, and not the book itself nor the other books of its own
// series, which the page names and which have their own page.

// moreAudiobooksSize is how many the row holds.
const moreAudiobooksSize = 20

// MoreAudiobooks answers the row for an audiobook, and nothing for any other kind of title.
func (d *DB) MoreAudiobooks(ctx context.Context, v Viewer, itemID string) ([]Item, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	var id, library int64
	var kind ItemKind
	err = tx.QueryRowContext(ctx, `SELECT i.id, i.kind, i.library_id FROM items i WHERE i.public_id = ? AND `+acc.visible("i"), itemID).
		Scan(&id, &kind, &library)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil || kind != KindAudiobook {
		return nil, err
	}
	// Read newest first through the library's index of when titles were added.
	rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns(acc)+` FROM items i INDEXED BY items_by_added`+stateJoin+`
		i.library_id = @library AND i.kind = @kind AND i.id <> @id AND `+acc.visible("i")+`
		AND i.id NOT IN (SELECT o.related_item_id FROM item_relations r JOIN item_relations o ON o.item_id = r.item_id AND o.kind = r.kind
			WHERE r.related_item_id = @id AND r.kind = @series)
		ORDER BY i.added_at DESC, i.id DESC LIMIT @limit`,
		append(v.args(), sql.Named("library", library), sql.Named("kind", KindAudiobook), sql.Named("id", id),
			sql.Named("series", RelationSeriesMember), sql.Named("limit", moreAudiobooksSize))...)
	if err != nil {
		return nil, err
	}
	var out []Item
	for rows.Next() {
		var it Item
		if err := scanItem(rows, &it); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, it)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return out, loadImages(ctx, tx, out)
}
