package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

// stopBinding deliberately excludes progress, revision and readiness. A delayed
// accepted start may become ready, but may never acquire a different occurrence.
func sameStopBinding(a, b row) bool {
	return a.id == b.id && a.device == b.device && a.account == b.account && a.profile == b.profile && a.authority == b.authority &&
		a.startKey == b.startKey && a.startDigest == b.startDigest && a.request == b.request && a.item == b.item && a.version == b.version &&
		a.queueID == b.queueID && a.entryID == b.entryID && a.generation == b.generation && a.media == b.media && a.mediaGeneration == b.mediaGeneration &&
		a.kind == b.kind && a.role == b.role && a.channel == b.channel && a.partIndex == b.partIndex && a.transfer == b.transfer && a.group == b.group
}

func stopChildren(ctx context.Context, tx *sql.Tx, r row) ([]preparedRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+preparedSelect+` FROM playback_v1_prepared WHERE session_id=? AND session_generation=? AND state='prepared' ORDER BY token`, r.id, r.generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []preparedRow
	for rows.Next() {
		p, e := scanPrepared(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// StopGeneration accepts the original caller and all durable fences in one
// outer writer. Process claims only reserve ownership; rollback cancels nothing.
func (s *Service) StopGeneration(ctx context.Context, c Caller, id string, expectedGeneration int) error {
	if expectedGeneration < 1 || int64(expectedGeneration) > 9007199254740991 {
		return &FieldError{Path: "expectedGeneration"}
	}
	if s.StopAuthorityTx == nil || s.Playback == nil || c.DeviceID == "" || dbwork.Snapshot(ctx) != nil {
		return identity.ErrForbidden
	}
	key := stopKey(ctx)
	if key != "" {
		if replay, err := s.stopGenerationReplay(ctx, c); err != nil || replay != nil {
			if err != nil {
				return err
			}
			return s.replayStop(ctx, replay)
		}
	}
	// The renderer's unwritten sample is written first, in its own short write,
	// while its media session still accepts evidence: the stop's fences below
	// then hold exactly that state. Only this device's own observer drains.
	if key == "" && s.observedBy(id, c) {
		if err := dbwork.WithWriteTxContext(ctx, s.DB, dbwork.ClassEstablishedPlayback, func(ctx context.Context, tx *sql.Tx) error {
			return s.drainObservationTx(ctx, tx, id)
		}); err != nil {
			return err
		}
	}
	var original row
	var children []preparedRow
	check := func(ctx context.Context, tx *sql.Tx) (row, error) {
		if tx == nil || dbwork.Snapshot(ctx) != tx {
			return row{}, identity.ErrForbidden
		}
		if err := s.StopAuthorityTx(ctx, tx, c); err != nil {
			return row{}, err
		}
		r, err := loadRow(ctx, tx, id)
		if err != nil {
			return row{}, err
		}
		if !owns(c, r) || r.device != c.DeviceID {
			return row{}, ErrNotFound
		}
		if r.generation != expectedGeneration {
			return row{}, ErrRevision
		}
		if r.item == "" || isChannel(r) || c.AuthorizeItem == nil {
			return row{}, identity.ErrForbidden
		}
		if err = c.AuthorizeItem(ctx, r.item); err != nil {
			return row{}, err
		}
		// Authorization ports must not revoke the family or replace this row while
		// checking visibility. Reprove both in the exact carried transaction.
		if err = s.StopAuthorityTx(ctx, tx, c); err != nil {
			return row{}, err
		}
		again, err := loadRow(ctx, tx, id)
		if err != nil {
			return row{}, err
		}
		if again != r {
			return row{}, ErrRevision
		}
		return r, nil
	}
	err := dbwork.WithReadSnapshot(ctx, s.DB, func(ctx context.Context) error {
		tx := dbwork.Snapshot(ctx)
		var err error
		original, err = check(ctx, tx)
		if err != nil {
			return err
		}
		if original.ended != 0 {
			return nil
		}
		children, err = stopChildren(ctx, tx, original)
		return err
	})
	if err != nil {
		return err
	}
	type target struct {
		id, item       string
		generation     int
		claim          *playback.StopOccurrenceClaim
		privateOwner   string
		privateExpires int64
	}
	targets := []target{}
	if original.ended == 0 {
		if original.media == "" || original.mediaGeneration < 1 {
			return ErrRevision
		}
		targets = append(targets, target{id: original.media, item: original.item, generation: original.mediaGeneration})
		// Child generations come from their actual legacy rows, never the parent
		// presentation or an unvalidated cached response.
		for _, p := range children {
			if p.device != original.device || p.media == "" {
				return ErrRevision
			}
			var generation int
			if err = dbwork.QueryRow(ctx, s.DB, `SELECT generation FROM playback_sessions WHERE id=?`, p.media).Scan(&generation); err != nil {
				return err
			}
			var owner string
			var expires int64
			if err = dbwork.QueryRow(ctx, s.DB, `SELECT owner_session_id,expires_ms FROM playback_private_presentations WHERE session_id=?`, p.media).Scan(&owner, &expires); err != nil {
				return err
			}
			if owner != original.media {
				return ErrRevision
			}
			targets = append(targets, target{id: p.media, item: p.item, generation: generation, privateOwner: owner, privateExpires: expires})
		}
		sort.Slice(targets, func(i, j int) bool { return targets[i].id < targets[j].id })
		for i := range targets {
			if i > 0 && targets[i-1].id == targets[i].id {
				return ErrRevision
			}
			// Obvious foreign media must not reserve another occurrence's barrier.
			err = dbwork.WithReadSnapshot(ctx, s.DB, func(ctx context.Context) error {
				tx := dbwork.Snapshot(ctx)
				if err := s.StopAuthorityTx(ctx, tx, c); err != nil {
					return err
				}
				return s.Playback.CheckStopOccurrenceTx(ctx, tx, c.Principal, targets[i].id, targets[i].generation, targets[i].item)
			})
			if err != nil {
				return err
			}
		}
	}
	defer func() {
		for i := range targets {
			if targets[i].claim != nil {
				targets[i].claim.Abort()
			}
		}
	}()
	for i := range targets {
		targets[i].claim, err = s.Playback.ClaimStopOccurrence(ctx, targets[i].id, targets[i].generation)
		if err != nil {
			if key != "" {
				if replay, replayErr := s.stopGenerationReplay(ctx, c); replayErr != nil || replay != nil {
					if replayErr != nil {
						return replayErr
					}
					return s.replayStop(ctx, replay)
				}
			}
			if errors.Is(err, playback.ErrOccurrenceChanged) {
				return ErrRevision
			}
			return err
		}
	}
	w, err := dbwork.Begin(ctx, s.DB, dbwork.ClassSecurityFence)
	if err != nil {
		return err
	}
	defer w.Rollback()
	var completion *stopCompletion
	commitStarted := false
	defer func() {
		if !commitStarted {
			s.releaseStopCompletion(key, completion)
		}
	}()
	writer, tx := w.Context(), w.Tx()
	if err = s.StopAuthorityTx(writer, tx, c); err != nil {
		return err
	}
	replay, err := checkStopReceipt(writer, tx, s.now().UnixMilli())
	if err != nil {
		return err
	}
	if replay != nil {
		if err = s.AuthorizeStopReceipt(writer, c, *replay); err != nil {
			return err
		}
		if err = w.Commit(); err != nil {
			return err
		}
		return s.replayStop(ctx, replay)
	}
	current, err := check(writer, tx)
	if err != nil {
		return err
	}
	if !sameStopBinding(original, current) {
		return ErrRevision
	}
	if current.ended != 0 {
		// An ordinary end may race this request. Legacy calls do nothing;
		// keyed calls finish only the captured current generation's fences.
		var finish func(context.Context) error
		if key != "" {
			if completion, err = s.reserveStopCompletion(key); err != nil {
				return err
			}
			if finish, err = s.stopCleanupForRowTx(writer, tx, current, key); err != nil {
				return err
			}
			completion.finish = finish
		}
		if err = saveStopReceipt(writer, tx, current, usermutation.Result{Status: "already_stopped", TargetID: id, Revision: int64(current.revision), Generation: current.generation, PositionMs: current.position, PositionStatus: "already_stopped"}, s.now().UnixMilli()); err != nil {
			return err
		}
		commitStarted = true
		if err = w.Commit(); err != nil {
			return err
		}
		if key != "" {
			return s.finishStop(ctx, key, finish)
		}
		return nil
	}
	if original.ended != 0 {
		return ErrRevision
	}
	actual, err := stopChildren(writer, tx, current)
	if err != nil {
		return err
	}
	if len(actual) != len(children) {
		return ErrRevision
	}
	for i := range children {
		if actual[i] != children[i] {
			return ErrRevision
		}
	}
	if completion, err = s.reserveStopCompletion(key); err != nil {
		return err
	}
	// The keyed drain shares quota, evidence, fences, and receipt in this writer.
	// Claims were captured before the drain, so rebind only its state/login facts.
	if key != "" && s.observedBy(id, c) {
		if err = s.drainObservationTx(writer, tx, id); err != nil {
			return err
		}
		if current, err = check(writer, tx); err != nil {
			return err
		}
		if !sameStopBinding(original, current) {
			return ErrRevision
		}
		for i := range targets {
			t := targets[i]
			if err = t.claim.RebindAfterDrainTx(writer, tx, func(ctx context.Context, tx *sql.Tx) error {
				if err := s.StopAuthorityTx(ctx, tx, c); err != nil {
					return err
				}
				return s.Playback.CheckStopOccurrenceTx(ctx, tx, c.Principal, t.id, t.generation, t.item)
			}); err != nil {
				return err
			}
		}
	}
	for i := range targets {
		t := targets[i]
		err = t.claim.FenceTx(writer, tx, func(ctx context.Context, tx *sql.Tx) error {
			r, e := check(ctx, tx)
			if e != nil {
				return e
			}
			if !sameStopBinding(original, r) || r.ended != 0 {
				return ErrRevision
			}
			if e = c.AuthorizeItem(ctx, t.item); e != nil {
				return e
			}
			if e = s.StopAuthorityTx(ctx, tx, c); e != nil {
				return e
			}
			if t.privateOwner != "" {
				var owner string
				var expires int64
				if e = tx.QueryRowContext(ctx, `SELECT owner_session_id,expires_ms FROM playback_private_presentations WHERE session_id=?`, t.id).Scan(&owner, &expires); e != nil {
					return e
				}
				if owner != t.privateOwner || expires != t.privateExpires {
					return ErrRevision
				}
			}
			return s.Playback.CheckStopOccurrenceTx(ctx, tx, c.Principal, t.id, t.generation, t.item)
		})
		if err != nil {
			if errors.Is(err, playback.ErrOccurrenceChanged) {
				return ErrRevision
			}
			return err
		}
	}
	final, err := loadRow(writer, tx, id)
	if err != nil {
		return err
	}
	if final != current {
		return ErrRevision
	}
	actual, err = stopChildren(writer, tx, current)
	if err != nil {
		return err
	}
	if len(actual) != len(children) {
		return ErrRevision
	}
	for i := range children {
		if actual[i] != children[i] {
			return ErrRevision
		}
	}
	for _, t := range targets {
		if t.privateOwner != "" {
			result, e := tx.ExecContext(writer, `DELETE FROM playback_private_presentations WHERE session_id=? AND owner_session_id=? AND expires_ms=?`, t.id, t.privateOwner, t.privateExpires)
			if e != nil {
				return e
			}
			n, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if n != 1 {
				return ErrRevision
			}
		}
	}
	now := s.now()
	for _, p := range children {
		result, e := tx.ExecContext(writer, `UPDATE playback_v1_prepared SET state='canceled',cancel_reason='stopped' WHERE token=? AND session_id=? AND session_generation=? AND media_session_id=? AND queue_revision=? AND state='prepared'`, p.token, p.session, p.sessionGeneration, p.media, p.queueRevision)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrRevision
		}
	}
	result, err := tx.ExecContext(writer, `UPDATE playback_v1_sessions SET ended_ms=?,end_reason='stopped',message='',revision=revision+1,updated_ms=? WHERE id=? AND generation=? AND media_session_id=? AND media_generation=? AND revision=? AND ended_ms=0`, now.UnixMilli(), now.UnixMilli(), current.id, current.generation, current.media, current.mediaGeneration, current.revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrRevision
	}
	if err = dbwork.AfterCommit(tx, func() { s.observations().retire(current.id) }); err != nil {
		return err
	}
	current.ended, current.endReason, current.message, current.revision = now.UnixMilli(), "stopped", "", current.revision+1
	if err = s.sessionEventsTx(writer, tx, current, now, true); err != nil {
		return err
	}
	if err = saveStopReceipt(writer, tx, current, usermutation.Result{Status: "applied", TargetID: id, Revision: int64(current.revision), Generation: current.generation, PositionMs: current.position, PositionStatus: "unchanged"}, now.UnixMilli()); err != nil {
		return err
	}
	finish := func(cleanup context.Context) error {
		errs := make([]error, len(targets))
		var joins sync.WaitGroup
		for i := range targets {
			errs[i] = errors.New("stop occurrence cleanup did not complete")
			joins.Add(1)
			supervise.Go("playbackv1.stop-join", func() {
				defer joins.Done()
				if key != "" {
					errs[i] = s.Playback.FinishStoppedOccurrence(cleanup, targets[i].id, targets[i].generation)
				} else {
					errs[i] = targets[i].claim.Commit(cleanup)
				}
			})
		}
		joins.Wait()
		return errors.Join(errs...)
	}
	if completion != nil {
		completion.finish = finish
	}
	commitStarted = true
	if err = w.Commit(); err != nil {
		return err
	}
	s.hub.wake()
	// The committed fence is irreversible even if a captured process takes too
	// long to join. Never retry an id-only stop or reopen on this error.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.finishStop(cleanup, key, finish)
}

// A fresh lookup rolls back its capacity reservation. The final writer repeats
// Check before the observation drain or any durable fence.
func (s *Service) stopGenerationReplay(ctx context.Context, c Caller) (*usermutation.Receipt, error) {
	w, err := dbwork.Begin(ctx, s.DB, dbwork.ClassSecurityFence)
	if err != nil {
		return nil, err
	}
	defer w.Rollback()
	if err = s.StopAuthorityTx(w.Context(), w.Tx(), c); err != nil {
		return nil, err
	}
	receipt, err := checkStopReceipt(w.Context(), w.Tx(), s.now().UnixMilli())
	if err != nil || receipt == nil {
		return receipt, err
	}
	if err = s.AuthorizeStopReceipt(w.Context(), c, *receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}
