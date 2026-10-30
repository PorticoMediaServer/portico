package metadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mbaccounting"
)

var errMBAccountingPending = errors.Join(errTVDBStale, mbaccounting.ErrBusy)

func mbAccountingChangeTx(ctx context.Context, tx *sql.Tx, j mbJob, entity int64, link, newRecording, newRelease, proof string) (mbaccounting.Change, error) {
	c := mbaccounting.Change{Owner: mbaccounting.Owner{Kind: "publication", Operation: j.operation, Generation: j.generation}, ID: j.operation, Entity: entity, Link: link, NewRecording: newRecording, NewRelease: newRelease}
	var recordingID, releaseID, track, status, observed string
	var e error
	if link == "album" {
		e = tx.QueryRowContext(ctx, `SELECT release_revision,release_id,requested_id,observed_at FROM mb_album_links WHERE album_id=?`, entity).Scan(&c.OldRelease, &releaseID, &track, &observed)
	} else {
		e = tx.QueryRowContext(ctx, `SELECT COALESCE(recording_revision,''),COALESCE(release_revision,''),COALESCE(recording_id,''),COALESCE(release_id,''),COALESCE(track_id,''),release_status,observed_at FROM mb_song_links WHERE item_id=?`, entity).Scan(&c.OldRecording, &c.OldRelease, &recordingID, &releaseID, &track, &status, &observed)
	}
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return c, e
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%q/%d/%d/%q/%q/%q/%q/%q/%q/%q/%q/%q/%q", j.operation, j.generation, entity, link, c.OldRecording, c.OldRelease, recordingID, releaseID, track, status, observed, newRecording, newRelease+"\x00"+proof)))
	c.Digest = fmt.Sprintf("%x", sum)
	return c, nil
}

// Preparation needs only the exact still-owned job/operation lease. Ordinary
// catalogue changes may retire the base while we copy private counters; the
// original full activeMB fence is rechecked at the final writer before effects.
// A lease is renewed only by this live owner after actual cursor progress. No
// expired generation is revived; a stopped owner has at most the existing90s.
func (s *Service) mbAccountingLeaseTx(ctx context.Context, tx *sql.Tx, j mbJob, progress bool) error {
	now := s.publicationTime()
	var until string
	e := tx.QueryRowContext(ctx, `SELECT j.lease_until FROM mb_jobs j JOIN mb_publication_operations o ON o.id=? AND o.kind=j.kind AND o.entity_id=j.entity_id WHERE j.entity_id=? AND j.kind=? AND j.generation=? AND j.lease=? AND j.lease_until>? AND o.job_generation=j.generation AND o.lease=j.lease AND o.status IN('claimed','staged') AND o.query_digest=?`, j.operation, j.base.Entity, j.kind, j.generation, j.lease, now.Format(time.RFC3339), j.digest).Scan(&until)
	if errors.Is(e, sql.ErrNoRows) {
		return errTVDBStale
	}
	if e != nil {
		return e
	}
	deadline, e := time.Parse(time.RFC3339, until)
	if e != nil {
		return e
	}
	if !progress || deadline.Sub(now) > 30*time.Second {
		return nil
	}
	next := now.Add(90 * time.Second).Format(time.RFC3339)
	if _, e = tx.ExecContext(ctx, `UPDATE mb_jobs SET lease_until=? WHERE kind=? AND entity_id=? AND generation=? AND lease=?`, next, j.kind, j.base.Entity, j.generation, j.lease); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE mb_publication_operations SET lease_until=? WHERE id=? AND job_generation=? AND lease=? AND status IN('claimed','staged')`, next, j.operation, j.generation, j.lease)
	return e
}

// mbPrepareAccounting is an existing-worker operation, not a new worker/queue.
// Its loop releases the real writer after every raw11-track turn. Only the
// prospective normalized counts are durable; the accepted pin stays unchanged.
func (s *Service) mbPrepareAccounting(ctx context.Context, j mbJob, link, canonical, newRecording, newRelease, proof string) (change mbaccounting.Change, result error) {
	e := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		if e := s.activeMB(ctx, tx, j); e != nil {
			return e
		}
		if e := compactcatalog.RequireEntityAcquirableTx(ctx, tx, j.base.Entity); e != nil {
			return e
		}
		if e := mbIdentityAllowed(ctx, tx, j, canonical); e != nil {
			return e
		}
		if link == "song" {
			track, e := readMBTrack(ctx, tx, j, canonical)
			if e != nil {
				return e
			}
			if track.status == "matched" {
				newRelease = track.releaseRevision
			} else {
				// The existing unmatched-song publisher leaves its actual old
				// release columns intact; the accounting must do the same.
				e = tx.QueryRowContext(ctx, `SELECT COALESCE(release_revision,'') FROM mb_song_links WHERE item_id=?`, j.base.Entity).Scan(&newRelease)
				if e != nil && !errors.Is(e, sql.ErrNoRows) {
					return e
				}
			}
		}
		var e error
		change, e = mbAccountingChangeTx(ctx, tx, j, j.base.Entity, link, newRecording, newRelease, proof)
		if e != nil {
			return e
		}
		_, e = mbaccounting.BeginTx(mbaccounting.WithTime(ctx, s.publicationTime()), tx, change)
		return e
	})
	if errors.Is(e, mbaccounting.ErrBusy) {
		return change, errMBAccountingPending
	}
	if e != nil {
		return change, e
	}
	defer func() {
		if result == nil {
			return
		}
		result = s.mbAbortPrivateAccounting(ctx, change.Owner, result)
	}()
	for {
		if e = ctx.Err(); e != nil {
			return change, e
		}
		ready := false
		e = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			if e := s.mbAccountingLeaseTx(ctx, tx, j, false); e != nil {
				return e
			}
			var e error
			ready, e = mbaccounting.PrepareTx(mbaccounting.WithTime(ctx, s.publicationTime()), tx, change)
			if e != nil {
				return e
			}
			if !ready {
				return s.mbAccountingLeaseTx(ctx, tx, j, true)
			}
			return nil
		})
		if e != nil {
			return change, e
		}
		if ready {
			return change, nil
		}
	}
}

// mbAccountingMaintenance runs before provider admission: disabling enrichment
// cannot strand an already accepted fold or an expired private owner. It uses
// the existing provider worker, table wake and retry hint, never a second timer.
func (s *Service) mbAccountingMaintenance(ctx context.Context) (held, worked bool, result error) {
	result = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		state, e := mbaccounting.ReadTx(ctx, tx)
		if e != nil {
			return e
		}
		held = state.Phase != "idle"
		if !held {
			return nil
		}
		now := s.publicationTime()
		live := func(owner mbaccounting.Owner) (bool, error) {
			var present bool
			if owner.Kind == "entity-mutation" {
				e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entity_mutations WHERE mutation_id=? AND generation=? AND status='active' AND entity_id IS NOT NULL)`, owner.Operation, owner.Generation).Scan(&present)
				return present, e
			}
			if owner.Kind != "publication" {
				return false, mbaccounting.ErrInvariant
			}
			e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mb_publication_operations o JOIN mb_jobs j ON j.kind=o.kind AND j.entity_id=o.entity_id AND j.generation=o.job_generation AND j.lease=o.lease WHERE o.id=? AND o.job_generation=? AND o.status IN('claimed','staged') AND o.lease_until>? AND j.lease_until>?)`, owner.Operation, owner.Generation, now.Format(time.RFC3339), now.Format(time.RFC3339)).Scan(&present)
			return present, e
		}
		worked, e = mbaccounting.MaintainTx(mbaccounting.WithTime(ctx, now), tx, live)
		if e == nil && !worked && state.ExpiresMS > now.UnixMilli() {
			s.noteMBAdoptionRetry(time.UnixMilli(state.ExpiresMS))
		}
		return e
	})
	return
}

type mbAccountingFinalKey struct{}

func mbFinalAccountingContext(ctx context.Context, c mbaccounting.Change) context.Context {
	return context.WithValue(ctx, mbAccountingFinalKey{}, c.Owner)
}
func mbAccountingProducerTx(ctx context.Context, tx *sql.Tx, j mbJob) error {
	state, e := mbaccounting.ReadTx(ctx, tx)
	if e != nil {
		return e
	}
	if state.Phase == "idle" {
		return nil
	}
	permit, ok := ctx.Value(mbAccountingFinalKey{}).(mbaccounting.Owner)
	if ok && permit == state.Owner && permit.Kind == "publication" && permit.Operation == j.operation && permit.Generation == j.generation && (state.Phase == "ready" || state.Phase == "flip") {
		return nil
	}
	return errMBAccountingPending
}

// An error after preparation must not strand the background producers. Abort
// changes only private state. A committed flip refuses abort and is folded by
// the existing maintenance pump, independently of the publication lease.
func (s *Service) mbAbortPrivateAccounting(ctx context.Context, owner mbaccounting.Owner, cause error) error {
	clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	e := dbwork.WithWriteTx(clean, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { return mbaccounting.AbortTx(clean, tx, owner) })
	if e != nil && !errors.Is(e, mbaccounting.ErrStale) && !errors.Is(e, mbaccounting.ErrBusy) {
		return errors.Join(cause, e)
	}
	return cause
}

// A live publisher can promptly drain its accepted fold through separate
// bounded writers. Cancellation or storage failure after acceptance never
// rewrites the successful publication; the independent maintenance pump owns
// the remaining durable work. The bound follows the private fact ceiling.
func (s *Service) mbFinishAcceptedAccounting(ctx context.Context, owner mbaccounting.Owner) {
	for turn := 0; turn <= 20002/mbaccounting.FactsPerTurn+2; turn++ {
		done := false
		e := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			st, e := mbaccounting.ReadTx(ctx, tx)
			if e != nil {
				return e
			}
			if st.Phase == "idle" || st.Owner != owner {
				done = true
				return nil
			}
			if st.Phase != "fold" || !st.Published {
				return mbaccounting.ErrInvariant
			}
			done, e = mbaccounting.FoldTx(ctx, tx)
			return e
		})
		if e != nil {
			if ctx.Err() == nil {
				log.Printf("warning: [metadata] accepted accounting fold deferred: %v", e)
			}
			return
		}
		if done {
			return
		}
	}
	log.Printf("warning: [metadata] accepted accounting fold exceeded its fact ceiling")
}
