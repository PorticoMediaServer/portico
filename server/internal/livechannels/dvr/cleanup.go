package dvr

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"time"
)

// CleanupOne handles unpublishable/cancelled private staging after physical
// retirement. It never removes completed, Keep, or ambiguously owned media.
func (s *Store) CleanupOne(ctx context.Context) error {
	if s.driver == nil {
		return nil
	}
	_, e := dbwork.ExecWrite(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), `INSERT OR IGNORE INTO dvr_cleanup_work SELECT id,? FROM dvr_recordings WHERE state IN('failed','cancelled') AND item_id=0 AND artifact_id!='' AND NOT EXISTS(SELECT 1 FROM dvr_cleanup_work w WHERE w.recording_id=dvr_recordings.id) ORDER BY updated_ms,id LIMIT 32`, s.now().Add(2*time.Minute).UnixMilli())
	if e != nil {
		return e
	}
	var id, key, hash, allocation string
	var size, generation int64
	var o livechannels.Owner
	e = s.db.QueryRowContext(ctx, `SELECT r.id,r.authority,r.account_id,r.profile_id,r.artifact_id,r.artifact_digest,r.bytes,r.allocation_id,r.claim_generation FROM dvr_cleanup_work w JOIN dvr_recordings r ON r.id=w.recording_id WHERE w.not_before_ms<=? AND r.state IN('failed','cancelled') AND r.item_id=0 ORDER BY w.not_before_ms,r.id LIMIT 1`, s.now().UnixMilli()).Scan(&id, &o.Authority, &o.AccountID, &o.ProfileID, &key, &hash, &size, &allocation, &generation)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if !o.Valid() {
		return ErrDenied
	}
	s.mu.Lock()
	_, busy := s.active[id]
	s.mu.Unlock()
	var allocated bool
	if e = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM live_allocations WHERE id=? AND state!='released')`, allocation).Scan(&allocated); e != nil {
		return e
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	shared, err := s.pendingSharedTx(ctx, snapshot.Tx(), id)
	snapshot.Rollback()
	if err != nil {
		return err
	}
	if busy || allocated || shared {
		_, e = dbwork.ExecWrite(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), `UPDATE dvr_cleanup_work SET not_before_ms=? WHERE recording_id=?`, s.now().Add(2*time.Minute).UnixMilli(), id)
		return e
	}
	e = s.retireRecordingLayout(ctx, id, o, key, mediaartifact.Object{Digest: hash, Size: size})
	if e == nil {
		e = s.driver.Remove(ctx, o, key, mediaartifact.Object{Digest: hash, Size: size})
	}
	if e != nil {
		_, _ = dbwork.ExecWrite(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), `UPDATE dvr_cleanup_work SET not_before_ms=? WHERE recording_id=?`, s.now().Add(2*time.Minute).UnixMilli(), id)
		return e
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	res, e := tx.ExecContext(ctx, `UPDATE dvr_recordings SET artifact_id='',artifact_digest='',bytes=0,checkpoint_json='{}' WHERE id=? AND claim_generation=? AND state IN('failed','cancelled') AND item_id=0`, id, generation)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM dvr_cleanup_work WHERE recording_id=?`, id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM live_source_dependencies WHERE kind='recording' AND id=?`, id); e != nil {
		return e
	}
	return gated.Commit()
}
