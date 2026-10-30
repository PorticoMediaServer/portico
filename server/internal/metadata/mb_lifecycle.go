package metadata

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// One reconciliation turn stays inside the writer's per-transaction budget:
// at most mbReconcileRaw songs read and mbReconcileEffects jobs requeued.
const (
	mbReconcileRaw     = 128
	mbReconcileEffects = 32
)

func (s *Service) mbReconcileAlbums(ctx context.Context) (bool, error) {
	// The maintained due index omits settled and noncurrent pins. Capture only a key;
	// all facts used by reconciliation are read again after taking the gate.
	// Albums take turns: the pick resumes after the album the last turn
	// served and wraps to the lowest id, so an album whose membership keeps
	// changing can't hold every turn while later albums wait.
	var pickedAlbum int64
	after := s.mbReconcileAfter.Load()
	e := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), s.db, func(readCtx context.Context) error {
		pick := func(after int64) error {
			return dbwork.QueryRow(readCtx, s.db, `SELECT c.album_id FROM mb_album_reconciliations c INDEXED BY mb_album_reconciliations_pending JOIN mb_album_heads h ON h.album_id=c.album_id JOIN mb_album_links l ON l.album_id=c.album_id AND l.release_revision=c.release_revision WHERE c.status='pending' AND c.current_pin=1 AND c.album_id>? ORDER BY c.album_id LIMIT 1`, after).Scan(&pickedAlbum)
		}
		err := pick(after)
		if errors.Is(err, sql.ErrNoRows) && after > 0 {
			err = pick(0)
		}
		return err
	})
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	// Use the original context, not the now-closed read snapshot.
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return false, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var albumID int64
	var release, cursor string
	var membership, currentMembership int64
	e = tx.QueryRowContext(ctx, `SELECT c.album_id,c.release_revision,c.membership_revision,c.cursor,h.membership_revision FROM mb_album_reconciliations c JOIN mb_album_heads h ON h.album_id=c.album_id JOIN mb_album_links l ON l.album_id=c.album_id AND l.release_revision=c.release_revision WHERE c.album_id=? AND (c.status='pending' OR c.membership_revision<>h.membership_revision)`, pickedAlbum).Scan(&albumID, &release, &membership, &cursor, &currentMembership)
	if errors.Is(e, sql.ErrNoRows) {
		// A concurrent commit removed/settled/rematched the candidate. Its
		// mb_* commit wake (and the worker safety tick) schedules a fresh pick;
		// do not rescan the remaining catalogue while holding the gate.
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if membership != currentMembership {
		cursor = ""
		membership = currentMembership
	}
	// The cursor is opaque; it now holds an integer entity id as text.
	var cursorID int64
	if cursor != "" {
		cursorID, _ = strconv.ParseInt(cursor, 10, 64)
	}
	// One turn reads at most mbReconcileRaw songs and requeues until
	// mbReconcileEffects jobs actually changed; the cursor records the last
	// song examined, so songs without a job never cost a later turn.
	rows, e := tx.QueryContext(ctx, `SELECT cs.entity_id FROM catalog_songs cs WHERE cs.album_id=? AND cs.entity_id>? ORDER BY cs.entity_id LIMIT ?`, albumID, cursorID, mbReconcileRaw+1)
	if e != nil {
		return false, e
	}
	items := []int64{}
	for rows.Next() {
		var item int64
		if e = rows.Scan(&item); e != nil {
			rows.Close()
			return false, e
		}
		items = append(items, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return false, e
	}
	exhausted := len(items) <= mbReconcileRaw
	if !exhausted {
		items = items[:mbReconcileRaw]
	}
	processed, effects := 0, int64(0)
	for _, item := range items {
		if effects >= mbReconcileEffects {
			break
		}
		result, e := tx.ExecContext(ctx, `UPDATE mb_jobs SET status='pending',attempts=0,next_attempt='',error='',generation=generation+1,revision=revision+1,lease='',lease_until='' WHERE kind='song' AND entity_id=?`, item)
		if e != nil {
			return false, e
		}
		changed, e := result.RowsAffected()
		if e != nil {
			return false, e
		}
		effects += changed
		processed++
		cursor = strconv.FormatInt(item, 10)
	}
	complete := exhausted && processed == len(items)
	status := "pending"
	if complete {
		status = "complete"
	}
	if _, e = tx.ExecContext(ctx, `UPDATE mb_album_reconciliations SET membership_revision=?,cursor=?,status=?,revision=revision+1 WHERE album_id=? AND release_revision=?`, membership, cursor, status, albumID, release); e != nil {
		return false, e
	}
	if e = gated.Commit(); e != nil {
		return false, e
	}
	s.mbReconcileAfter.Store(albumID)
	return true, nil
}
func (s *Service) cleanupMusicBrainz(ctx context.Context) error {
	if err := s.mbCleanupReceipts(ctx); err != nil {
		return err
	}
	return s.mbRetireEvidence(ctx)
}
