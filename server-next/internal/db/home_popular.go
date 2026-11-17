package db

import (
	"context"
	"fmt"
	"time"
)

// popularHomeItems keeps the seven-day window fixed across pages. New plays do not move
// a title ahead of the reader. Sharing and the caller's restrictions are checked on every read.
func (d *DB) popularHomeItems(ctx context.Context, v Viewer, q ItemQuery) (ItemPage, error) {
	if q.Sort == "" {
		q.Sort, q.Desc = SortOrder, true
	}
	after, err := decodeCursor(q.Cursor, q.Sort, q.Desc)
	if err != nil {
		return ItemPage{}, err
	}
	now := q.HomeAt
	if now.IsZero() {
		now = time.Now()
	}
	q.homePopular = now.UnixMilli()
	if after != nil {
		if after.AsOf <= 0 || after.AsOf > q.homePopular {
			return ItemPage{}, ErrCursorExpired
		}
		q.homePopular = after.AsOf
	}
	return d.Items(ctx, v, q)
}

// Start from the seven days of plays, not from every title in every library. Fold a play
// to the show or album it belongs to only after checking that both are visible to the caller.
func (q ItemQuery) popularSource(w *query, acc access) {
	title := fmt.Sprintf(`CASE WHEN leaf.kind=%d THEN ifnull(leaf.root_id,leaf.id)
  WHEN leaf.kind=%d THEN ifnull(leaf.parent_id,leaf.id) ELSE leaf.id END`, KindEpisode, KindTrack)
	source := fmt.Sprintf(`(SELECT i.id AS item_id,count(*) AS score FROM play_history h
  CROSS JOIN items leaf ON leaf.id=h.item_id CROSS JOIN items i ON i.id=%s
  WHERE h.played_at>=%d AND h.played_at<=%d AND %s AND %s AND %s GROUP BY i.id) popular`,
		title, q.homePopular-(7*24*time.Hour).Milliseconds(), q.homePopular, sharedPlay, acc.visible("leaf"), acc.visible("i"))
	// Whole-library/tag shortcuts cannot count a ranked subset.
	w.tagTotal, w.plainKinds, w.plainLibrary, w.whole = "", "", 0, false
	w.scope = "SELECT item_id FROM " + source
	w.from += " JOIN " + source + " ON popular.item_id=i.id"
	w.countFrom += " JOIN " + source + " ON popular.item_id=i.id"
}
