package catalog

import (
	"context"
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// markVisibilityUnavailable records one unavailable entity in a class
// generation and updates the corresponding count only when the row changes.
func markVisibilityUnavailable(ctx context.Context, tx *sql.Tx, classID, generation, libraryID int64, kind int, id int64, unavailable bool) error {
	if unavailable {
		result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO compact_visibility_unavailable(class_id,generation,entity_id,library_id,kind) VALUES(?,?,?,?,?)`,
			classID, generation, id, libraryID, kind)
		if err != nil {
			return err
		}
		changed, _ := result.RowsAffected()
		if changed == 0 {
			return nil
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO compact_visibility_unavailable_counts(class_id,generation,library_id,kind,total) VALUES(?,?,?,?,1)
		 ON CONFLICT(class_id,generation,library_id,kind) DO UPDATE SET total=total+1`,
			classID, generation, libraryID, kind)
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM compact_visibility_unavailable WHERE class_id=? AND generation=? AND entity_id=?`,
		classID, generation, id)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE compact_visibility_unavailable_counts SET total=total-1
	 WHERE class_id=? AND generation=? AND library_id=? AND kind=? AND total>0`,
		classID, generation, libraryID, kind)
	return err
}

// visibilityBulkBacklog is the journal backlog above which a class is rebuilt
// rather than updated entity by entity. An incremental update shifts every
// later anchor (one per 256 rows), so a bulk change (an import, a rating-age
// edit) applied one entity at a time would cost backlog × rows/256; a rebuild
// costs one pass over the library.
const visibilityBulkBacklog = 2048

// refreshVisibilityClass brings a published class up to date from its journal
// (compact_visibility_dirty). Each batch is read, applied and deleted in one
// write transaction, so a change journaled while the batch runs waits for the
// next batch rather than being lost. A large backlog rebuilds the class instead.
func (s *Service) refreshVisibilityClass(ctx context.Context, library string, r identity.ContentRestrictions, classID, libraryID, generation int64) error {
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	var backlog, rows int
	read := dbwork.ReadHandle(ctx, s.db)
	if err := read.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM compact_visibility_dirty WHERE class_id=? LIMIT ?)`, classID, visibilityBulkBacklog).Scan(&backlog); err != nil {
		return err
	}
	if err := read.QueryRowContext(ctx, `SELECT COALESCE(sum(total),0) FROM compact_visibility_counts WHERE class_id=? AND generation=?`, classID, generation).Scan(&rows); err != nil {
		return err
	}
	if backlog >= visibilityBulkBacklog || (rows >= 64 && backlog > rows/2) {
		return s.RebuildVisibilityClass(ctx, library, r)
	}
	var target, sourceRevision int64
	if err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		var err error
		target, sourceRevision, err = captureVisibilityTargetTx(ctx, tx, classID, library)
		return err
	}); err != nil {
		return err
	}
	if err := s.settleVisibilitySourceTarget(ctx, target); err != nil {
		return err
	}
	clause, clauseArgs := visibilityMembershipSQL("e.entity_id", r)
	started := time.Now()
	for {
		if err := visibilityBoundary(ctx, started); err != nil {
			return err
		}
		started = time.Now()
		applied := 0
		err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			applied = 0
			rows, err := tx.QueryContext(ctx, `SELECT entity_id FROM compact_visibility_dirty INDEXED BY compact_visibility_dirty_frontier WHERE class_id=? AND first_sequence<=? ORDER BY first_sequence,entity_id LIMIT ?`, classID, target, compactcatalog.BatchLimit)
			if err != nil {
				return err
			}
			ids := []int64{}
			for rows.Next() {
				var id int64
				if err = rows.Scan(&id); err != nil {
					break
				}
				ids = append(ids, id)
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				return err
			}
			for _, id := range ids {
				if err = updateVisibleEntity(ctx, tx, classID, generation, libraryID, id, clause, clauseArgs); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `DELETE FROM compact_visibility_dirty WHERE class_id=? AND entity_id=?`, classID, id); err != nil {
					return err
				}
			}
			applied = len(ids)
			if applied == 0 {
				ready, err := visibilityTargetReadyTx(ctx, tx, classID, target)
				if err != nil || !ready {
					return err
				}
				_, err = tx.ExecContext(ctx, `UPDATE compact_visibility_classes SET catalog_revision=?,captured_sequence=?,built_ms=? WHERE id=? AND active_generation=?`, sourceRevision, target, time.Now().UnixMilli(), classID, generation)
				return err
			}
			return nil
		})
		if err != nil || applied == 0 {
			return err
		}
	}
}

type visibleEntity struct {
	valid, available bool
	id               int64
	kind, decade     int
	sort, head       string
	// The browse row's sort keys (browse_contract.go), for the class's blocks.
	added             string
	year              int
	duration, rating  float64
	recent            sql.NullString
	random            int64
	availabilityState string
}

func (a visibleEntity) sameRow(b visibleEntity) bool {
	return a.kind == b.kind && a.sort == b.sort && a.head == b.head && a.decade == b.decade &&
		a.added == b.added && a.year == b.year && a.duration == b.duration && a.rating == b.rating && a.recent == b.recent && a.random == b.random && a.availabilityState == b.availabilityState
}

// updateVisibleEntity re-evaluates one entity against the class: it leaves,
// joins or moves within the class's rows and counts (the rows' triggers keep
// the class's counted blocks), and its
// unavailable mark follows its availability. It reads exactly what
// RebuildVisibilityClass reads for the entity.
func updateVisibleEntity(ctx context.Context, tx *sql.Tx, classID, generation, libraryID, id int64, clause string, clauseArgs []any) error {
	old := visibleEntity{id: id}
	err := tx.QueryRowContext(ctx, `SELECT kind,sort_key,head,decade,added,year,duration,rating,recent,random_key,availability_state FROM compact_visibility_rows WHERE class_id=? AND generation=? AND entity_id=?`, classID, generation, id).Scan(&old.kind, &old.sort, &old.head, &old.decade, &old.added, &old.year, &old.duration, &old.rating, &old.recent, &old.random, &old.availabilityState)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	old.valid = err == nil
	if old.valid {
		var unavailable bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM compact_visibility_unavailable WHERE class_id=? AND generation=? AND entity_id=?)`, classID, generation, id).Scan(&unavailable); err != nil {
			return err
		}
		old.available = !unavailable
	}
	next := visibleEntity{id: id}
	args := append([]any{libraryID, id}, clauseArgs...)
	err = tx.QueryRowContext(ctx, `SELECT e.kind,e.sort_key,e.head,COALESCE((e.year/10)*10,0),e.available,`+visibleRowKeys+`
	 FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id
	 WHERE e.library_id=? AND e.entity_id=? AND ce.retired=0 AND `+clause, args...).Scan(&next.kind, &next.sort, &next.head, &next.decade, &next.available, &next.added, &next.year, &next.duration, &next.rating, &next.recent, &next.random, &next.availabilityState)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	next.valid = err == nil
	// Availability is tracked beside the membership: withdraw the old mark,
	// record the new one.
	if old.valid && !old.available && (!next.valid || next.available || next.kind != old.kind) {
		if err = markVisibilityUnavailable(ctx, tx, classID, generation, libraryID, old.kind, id, false); err != nil {
			return err
		}
	}
	if next.valid && !next.available && (!old.valid || old.available || next.kind != old.kind) {
		if err = markVisibilityUnavailable(ctx, tx, classID, generation, libraryID, next.kind, id, true); err != nil {
			return err
		}
	}
	if old.valid && next.valid && old.sameRow(next) {
		return nil
	}
	if old.valid {
		if _, err = tx.ExecContext(ctx, `DELETE FROM compact_visibility_rows WHERE class_id=? AND generation=? AND entity_id=?`, classID, generation, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE compact_visibility_counts SET total=total-1 WHERE class_id=? AND generation=? AND library_id=? AND kind=? AND head=? AND decade=?`, classID, generation, libraryID, old.kind, old.head, old.decade); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM compact_visibility_counts WHERE class_id=? AND generation=? AND library_id=? AND kind=? AND head=? AND decade=? AND total=0`, classID, generation, libraryID, old.kind, old.head, old.decade); err != nil {
			return err
		}
	}
	if next.valid {
		if _, err = tx.ExecContext(ctx, `INSERT INTO compact_visibility_rows(class_id,generation,entity_id,library_id,kind,sort_key,head,decade,added,year,duration,rating,recent,random_key,availability_state) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			classID, generation, id, libraryID, next.kind, next.sort, next.head, next.decade, next.added, next.year, next.duration, next.rating, next.recent, next.random, next.availabilityState); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO compact_visibility_counts(class_id,generation,library_id,kind,head,decade,total) VALUES(?,?,?,?,?,?,1)
		 ON CONFLICT(class_id,generation,library_id,kind,head,decade) DO UPDATE SET total=total+1`, classID, generation, libraryID, next.kind, next.head, next.decade); err != nil {
			return err
		}
	}
	return nil
}

// visibilityBoundary is the builder's cooperative boundary between write
// batches, outside any transaction: the owner's "pause background work" holds
// the builder here, foreground pressure gets a brief opening, and cancellation
// stops it. Published classes keep serving while the builder waits.
func visibilityBoundary(ctx context.Context, batchStarted time.Time) error {
	if !dbwork.PaceBackground(ctx, batchStarted) {
		if err := ctx.Err(); err != nil {
			return err
		}
		return context.Canceled
	}
	return nil
}

// visibleRowKeys selects a browse row's sort keys as a class row stores them.
const visibleRowKeys = `COALESCE(e.added_text,''),e.year,COALESCE(e.duration_max,0),COALESCE(e.rating_max,0),e.recent_text,e.random_key,e.availability_state`
