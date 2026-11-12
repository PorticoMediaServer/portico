package db

import (
	"context"
	"database/sql"
)

// loadLeafCounts fills Leaves and WatchedLeaves for the items that hold others, and from them
// whether the viewer has watched the whole of each. For a profile with a rating limit or a
// blocked label it also counts each holder's children again, so a show's seasons or an
// artist's albums are only those the profile can list.
func loadLeafCounts(ctx context.Context, tx *sql.Tx, v Viewer, acc access, items []Item) error {
	var holders, series []Item
	byID := map[int64]*Item{}
	for n := range items {
		if items[n].Kind == KindSeries {
			series = append(series, items[n])
		}
		if !items[n].Kind.Playable() && (items[n].ChildCount > 0 || items[n].Kind == KindSeries) {
			holders = append(holders, items[n])
			byID[items[n].ID] = &items[n]
		}
	}
	if len(holders) == 0 {
		return nil
	}
	// Titles nest at most three deep, so a holder is the root or the parent of its leaves.
	// Each is read through its own index, named here because a viewer limited to some libraries
	// otherwise tempts SQLite into reading those libraries whole.
	for column, index := range map[string]string{"c.root_id": "items_descendants", "c.parent_id": "items_children"} {
		rows, err := tx.QueryContext(ctx, `SELECT `+column+`, count(*), count(w.item_id) FROM items c INDEXED BY `+index+`
			LEFT JOIN user_item_state w ON w.profile_id = @viewer_profile AND w.item_id = c.id AND w.played = 1
			WHERE `+column+` IN `+idList(holders)+` AND c.kind IN `+playableKindsSQL+` AND `+acc.visible("c")+` GROUP BY 1`, v.args()...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var leaves, watched int
			if err := rows.Scan(&id, &leaves, &watched); err != nil {
				rows.Close()
				return err
			}
			// Set, not added: an album with no artist above it is found both ways.
			byID[id].Leaves, byID[id].WatchedLeaves = leaves, watched
			byID[id].State.Watched = leaves > 0 && watched == leaves
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	// The stored child count is right while every child is visible: a child is always in its
	// holder's library, so only a rating limit or a blocked label can hide one. Otherwise the
	// children are counted under the list's own condition, through the parent index; this
	// reads the children of one page of holders, fewer rows than the leaves read above.
	if acc.maxAge != nil || len(acc.blockedTags) > 0 {
		for _, h := range holders {
			byID[h.ID].ChildCount = 0
		}
		rows, err := tx.QueryContext(ctx, `SELECT c.parent_id, count(*) FROM items c WHERE c.parent_id IN `+idList(holders)+
			` AND `+acc.visible("c")+` GROUP BY 1`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var children int
			if err := rows.Scan(&id, &children); err != nil {
				rows.Close()
				return err
			}
			byID[id].ChildCount = children
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	if len(series) == 0 {
		return nil
	}
	// A series page lists both direct children and related volumes. UNION counts a volume
	// only once when it is held both ways, under the same visibility rules as that page.
	for _, h := range series {
		it := byID[h.ID]
		it.ChildCount, it.Leaves, it.WatchedLeaves, it.State.Watched = 0, 0, 0, false
	}
	rows, err := tx.QueryContext(ctx, `SELECT g.holder, count(*), count(w.item_id) FROM (
		SELECT c.parent_id AS holder, c.id FROM items c WHERE c.parent_id IN `+idList(series)+` AND `+acc.visible("c")+`
		UNION SELECT r.item_id, c.id FROM item_relations r JOIN items c ON c.id = r.related_item_id
		WHERE r.kind = `+itoa(int64(RelationSeriesMember))+` AND r.item_id IN `+idList(series)+` AND `+acc.visible("c")+`
	) g LEFT JOIN user_item_state w ON w.profile_id = @viewer_profile AND w.item_id = g.id AND w.played = 1 GROUP BY g.holder`, v.args()...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var count, watched int
		if err := rows.Scan(&id, &count, &watched); err != nil {
			return err
		}
		it := byID[id]
		it.ChildCount, it.Leaves, it.WatchedLeaves = count, count, watched
		it.State.Watched = count > 0 && watched == count
	}
	return rows.Err()
}
