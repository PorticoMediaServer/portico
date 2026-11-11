package db

import (
	"context"
	"database/sql"
	"time"
)

// When a matched title is due to be asked about again. refresh_due holds it, set each time the
// title is refreshed, and the items_due index lists the due titles in the order they fell due,
// so finding them reads only what is due, whatever the size of the library.
const (
	day = int64(24 * time.Hour / time.Millisecond)
	// A show still running and a title just added change often: new episodes, a rating, a
	// corrected description.
	refreshSoon = 2 * day
	justAdded   = 14 * day
	refreshShow = 14 * day
	refreshFilm = 30 * day
)

// scheduleRefresh sets when a top-level title is next due a refresh, counted from now, and
// forgets the provider's failures on it: it has just answered.
func (b *batchTx) scheduleRefresh(itemID, now int64) error {
	_, err := b.exec(`UPDATE items SET refresh_due = ?2 + CASE WHEN ifnull(extra ->> '$.continuing', 0) OR added_at > ?2 - ?3 THEN ?4
		WHEN kind = ?5 THEN ?6 ELSE ?7 END, extra = json_remove(extra, '$.providerTries', '$.lookupAfter')
		WHERE id = ?1 AND parent_id IS NULL`, itemID, now, justAdded, refreshSoon, KindShow, refreshShow, refreshFilm)
	return err
}

// ProviderFailed records that the provider failed on a title: one not matched yet is not
// looked up again, and a matched one not refreshed again, until a wait has gone by, an hour
// the first time and twice as long each time after, up to a week. So a title the provider keeps
// refusing is not asked about at every run, and the titles behind it are reached.
func (d *DB) ProviderFailed(ctx context.Context, itemID int64, now time.Time) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE items SET
			refresh_due = CASE WHEN match_state IN (?3, ?4) THEN ?2 + min(3600000 << min(ifnull(extra ->> '$.providerTries', 0), 8), 604800000) ELSE refresh_due END,
			extra = json_set(ifnull(extra, '{}'), '$.providerTries', ifnull(extra ->> '$.providerTries', 0) + 1,
				'$.lookupAfter', ?2 + min(3600000 << min(ifnull(extra ->> '$.providerTries', 0), 8), 604800000))
			WHERE id = ?1`, itemID, now.UnixMilli(), MatchFound, MatchChosen)
		return err
	})
}

// DueForRefresh lists a library's matched top-level titles due a refresh at a time, in the
// order they fell due, a page at a time: pass the last line of one page to get the next. It
// reads along the items_due index and nothing that is not due.
func (d *DB) DueForRefresh(ctx context.Context, libraryID, now int64, after *RefreshItem, limit int) ([]RefreshItem, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	where, args := `library_id = ? AND match_state IN (1, 2) AND parent_id IS NULL AND refresh_due <= ?`, []any{libraryID, now}
	if after != nil {
		where += ` AND (refresh_due, id) > (?, ?)`
		args = append(args, after.Due, after.ID)
	}
	return queryAll(ctx, tx, `SELECT id, kind, ifnull(refreshed_at, 0), added_at, refresh_due FROM items WHERE `+where+`
		ORDER BY refresh_due, id LIMIT ?`, append(args, limit),
		func(r *RefreshItem) []any { return []any{&r.ID, &r.Kind, &r.RefreshedAt, &r.AddedAt, &r.Due} })
}

// ScheduleUnscheduled makes due at once a library's matched titles that have no due time, so no
// title can be left never refreshed. Only a database made before refresh_due existed has any;
// finding there are none is one look at the items_due index.
func (d *DB) ScheduleUnscheduled(ctx context.Context, libraryID int64) error {
	var found bool
	if err := d.read.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM items WHERE library_id = ? AND match_state IN (1, 2)
		AND parent_id IS NULL AND refresh_due IS NULL)`, libraryID).Scan(&found); err != nil || !found {
		return err
	}
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE items SET refresh_due = 0 WHERE library_id = ? AND match_state IN (1, 2)
			AND parent_id IS NULL AND refresh_due IS NULL`, libraryID)
		return err
	})
}
