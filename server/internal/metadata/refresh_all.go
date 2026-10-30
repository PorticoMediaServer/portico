package metadata

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// refreshAllPage bounds one sweep turn: it wakes at most this many titles, and
// never more than the refresh allowance has left, so Refresh all spends the
// daily allowance at the allowance's pace instead of waking a whole library.
const refreshAllPage = 32

// RefreshAll is Server › Metadata › Refresh all. Every document checked before
// now is stale from here on, whatever Check for new details says, and a paced
// sweep wakes each held title in turn. The provider is still asked only within
// the refresh allowance, and details an owner edited stay locked. Asking again
// while a sweep runs restarts it from the beginning.
func (s *Service) RefreshAll(ctx context.Context, authorize func(*sql.Tx) error) (MetadataSettings, error) {
	write, e := dbwork.Begin(ctx, s.db, dbwork.ClassInteractive)
	if e != nil {
		return MetadataSettings{}, e
	}
	defer write.Rollback()
	tx := write.Tx()
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return MetadataSettings{}, e
		}
	}
	// The epoch never moves ahead of the clock: asking twice in one second marks
	// the same documents stale, not documents from the future.
	if _, e = tx.ExecContext(ctx, `UPDATE metadata_runtime_settings SET refresh_all_epoch=max(?,refresh_all_epoch),refresh_all_running=1,refresh_all_cursor_kind='',refresh_all_cursor_id=0,refresh_all_page_kind='',refresh_all_page_id=0 WHERE singleton=1`, s.publicationTime().Unix()); e != nil {
		return MetadataSettings{}, e
	}
	if e = write.Commit(); e != nil {
		return MetadataSettings{}, e
	}
	return s.MetadataSettings(ctx)
}

// RefreshAllStep advances an owner's Refresh all by one bounded page. It wakes
// only titles that are settled (matched and not leased), never more than the
// allowance has left, and never while the titles of the page it woke last are
// still waiting for the worker, so the allowance it measured is the one they spend. It writes
// nothing when no sweep is running, the allowance is spent or a page is pending
// (a title in a provider's retry backoff never holds it up).
// A title a competing refresh outspends stays stale and is refreshed on its
// next wake.
func (s *Service) RefreshAllStep(ctx context.Context) error {
	var running bool
	if e := dbwork.QueryRow(ctx, s.db, `SELECT refresh_all_running FROM metadata_runtime_settings WHERE singleton=1`).Scan(&running); e != nil || !running {
		return e
	}
	remaining, e := s.RefreshBudgetRemaining(ctx, "tmdb", s.publicationTime().UTC())
	if e != nil {
		return e
	}
	size := min(refreshAllPage, remaining["hour"], remaining["day"])
	if size <= 0 {
		return nil
	}
	w, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	defer w.Rollback()
	tx := w.Tx()
	var kind, pageKind string
	var id, pageID int64
	if e = tx.QueryRowContext(ctx, `SELECT refresh_all_running,refresh_all_cursor_kind,refresh_all_cursor_id,refresh_all_page_kind,refresh_all_page_id FROM metadata_runtime_settings WHERE singleton=1`).Scan(&running, &kind, &id, &pageKind, &pageID); e != nil || !running {
		return e
	}
	if kind != "" {
		// Only the page it woke, (page start, cursor], holds the sweep back.
		var waiting bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM screen_metadata_work INDEXED BY screen_metadata_refresh_sweep_waiting WHERE status='pending' AND next_attempt='' AND (target_kind,target_id)>(?,?) AND (target_kind,target_id)<=(?,?))`, pageKind, pageID, kind, id).Scan(&waiting); e != nil || waiting {
			return e
		}
	}
	pageKind, pageID = kind, id
	rows, e := tx.QueryContext(ctx, `SELECT target_kind,target_id FROM screen_metadata_work INDEXED BY screen_metadata_refresh_sweep WHERE (target_kind,target_id)>(?,?) AND status='matched' AND lease='' ORDER BY target_kind,target_id LIMIT ?`, kind, id, size)
	if e != nil {
		return e
	}
	type target struct {
		kind string
		id   int64
	}
	page := []target{}
	for rows.Next() {
		var t target
		if e = rows.Scan(&t.kind, &t.id); e != nil {
			rows.Close()
			return e
		}
		page = append(page, t)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, t := range page {
		kind, id = t.kind, t.id
		if _, e = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status='pending',generation=generation+1,revision=revision+1,next_attempt='',error='',attempts=0 WHERE target_kind=? AND target_id=? AND status='matched' AND lease=''`, t.kind, t.id); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE metadata_runtime_settings SET refresh_all_running=?,refresh_all_cursor_kind=?,refresh_all_cursor_id=?,refresh_all_page_kind=?,refresh_all_page_id=? WHERE singleton=1`, len(page) == size, kind, id, pageKind, pageID); e != nil {
		return e
	}
	return w.Commit()
}
