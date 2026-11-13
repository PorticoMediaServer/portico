package db

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
)

// Spotlight lists at most five distinct films or shows, newest added first. Each library
// and kind is read through its added index, as on Home's Recently added row. Missing titles,
// empty shows and Not interested are never featured. It does not rank recommendations.
func (d *DB) Spotlight(ctx context.Context, v Viewer) ([]Item, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	libraries, err := ids(ctx, tx, `SELECT id FROM libraries WHERE `+acc.libraryVisible("id"))
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, library := range libraries {
		for _, kind := range []ItemKind{KindMovie, KindShow} {
			rated, err := acc.ratedIn(ctx, tx, library, fmt.Sprintf("(%d)", kind))
			if err != nil {
				return nil, err
			}
			if rated == 0 {
				continue
			}
			from, may := "items i", acc.visible("i")
			if rated < sparseRated {
				from, may = "items i INDEXED BY items_by_rating", "(1"+acc.rated("i", "")+acc.unblocked("i")+acc.filled("i")+")"
			}
			rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns+` FROM `+from+`
    LEFT JOIN user_item_state s ON s.item_id = i.id AND s.profile_id = @viewer_profile
    WHERE i.library_id = @library AND i.kind = @kind AND `+may+`
    AND i.available = 1 AND (i.kind != @show OR i.leaf_count > 0) AND ifnull(s.hidden, 0) = 0
    ORDER BY i.added_at DESC, i.id DESC LIMIT 5`,
				append(v.args(), sql.Named("library", library), sql.Named("kind", kind), sql.Named("show", KindShow))...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var it Item
				if err := scanItem(rows, &it); err != nil {
					rows.Close()
					return nil, err
				}
				items = append(items, it)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	slices.SortFunc(items, func(a, b Item) int {
		if n := cmp.Compare(b.AddedAt, a.AddedAt); n != 0 {
			return n
		}
		return cmp.Compare(b.ID, a.ID)
	})
	items = items[:min(len(items), 5)]
	return items, nil
}

// SpotlightEpisode is one show's playable hero target: an unfinished regular episode first,
// otherwise its first regular episode. A continuation already offered by Home takes priority
// in the API, including a special the person deliberately started.
func (d *DB) SpotlightEpisode(ctx context.Context, v Viewer, show int64) (string, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT i.public_id FROM items i
  LEFT JOIN user_item_state s ON s.item_id = i.id AND s.profile_id = @viewer_profile
  WHERE i.root_id = @show AND i.kind = @episode AND i.available = 1 AND i.parent_index > 0 AND `+acc.visible("i")+`
  ORDER BY ifnull(s.played, 0), i.parent_index, i.index_number, i.id LIMIT 1`,
		append(v.args(), sql.Named("show", show), sql.Named("episode", KindEpisode))...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}
