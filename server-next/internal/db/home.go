package db

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"
)

// Home rows the server composes.
const (
	continueWindow = 16 * 7 * 24 * time.Hour // a title untouched for longer drops off Continue Watching
	homeRowSize    = 40
)

// ContinueWatching lists what the viewer has started and not finished, most recent first. A
// title the viewer removed from the row stays off it until it is played again. Not interested
// does not take a title off it: that is for recommendation rows.
func (d *DB) ContinueWatching(ctx context.Context, v Viewer) ([]Item, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	// Read from the viewer's own recent state, newest first, and stop at a row's worth.
	rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns+` FROM user_item_state s CROSS JOIN items i ON i.id = s.item_id
		WHERE s.profile_id = @viewer_profile AND s.last_played_at > @since AND s.played = 0 AND s.last_played_at > ifnull(s.continue_dismissed_at, 0)
		AND (s.position_ms > 0 OR s.position_text IS NOT NULL) AND `+acc.visible("i")+`
		ORDER BY s.last_played_at DESC LIMIT `+fmt.Sprint(homeRowSize),
		append(v.args(), sql.Named("since", time.Now().Add(-continueWindow).UnixMilli()))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var it Item
		if err := scanItem(rows, &it); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, loadImages(ctx, tx, items)
}

// RecentlyAdded lists the newest titles across the libraries the viewer may see: films and
// books by when they were added, shows and albums by their newest episode or track, so a show
// with a new episode is new again. It reads the newest few hundred playable items of each
// library through the "date added" index, whatever the size of the library.
func (d *DB) RecentlyAdded(ctx context.Context, v Viewer) ([]Item, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	type newest struct {
		id      int64
		addedAt int64
	}
	libraries, err := ids(ctx, tx, `SELECT l.id FROM libraries l WHERE `+acc.libraryVisible("l.id"))
	if err != nil {
		return nil, err
	}
	var titles []newest
	seen := map[int64]bool{}
	for _, leaf := range []struct {
		kind  ItemKind
		title string // the column that names the title a leaf belongs to
	}{{KindMovie, "i.id"}, {KindEpisode, "ifnull(i.root_id, i.id)"}, {KindTrack, "ifnull(i.parent_id, i.id)"}, {KindBook, "i.id"},
		{KindAudiobook, "i.id"}} {
		for _, library := range libraries {
			rows, err := queryAll(ctx, tx, `SELECT `+leaf.title+`, i.added_at FROM items i WHERE i.library_id = ? AND i.kind = ? AND `+
				acc.visible("i")+` ORDER BY i.added_at DESC, i.id DESC LIMIT 400`, []any{library, leaf.kind},
				func(n *newest) []any { return []any{&n.id, &n.addedAt} })
			if err != nil {
				return nil, err
			}
			inLibrary := 0
			for _, row := range rows {
				if !seen[row.id] && inLibrary < homeRowSize {
					seen[row.id] = true
					inLibrary++
					titles = append(titles, row)
				}
			}
		}
	}
	slices.SortStableFunc(titles, func(a, b newest) int { return cmp.Compare(b.addedAt, a.addedAt) })
	titles = titles[:min(len(titles), homeRowSize)]
	if len(titles) == 0 {
		return nil, nil
	}
	list := make([]int64, len(titles))
	place := map[int64]int{}
	for n, title := range titles {
		list[n], place[title.id] = title.id, n
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns+itemFrom+`i.id IN `+intList(list)+` AND `+acc.visible("i"), v.args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var it Item
		if err := scanItem(rows, &it); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, b Item) int { return cmp.Compare(place[a.ID], place[b.ID]) })
	if err := loadImages(ctx, tx, items); err != nil {
		return nil, err
	}
	return items, loadLeafCounts(ctx, tx, v, acc, items)
}
