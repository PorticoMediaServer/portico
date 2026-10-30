package dvr

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	workerloop "github.com/PorticoMediaServer/portico/server/internal/worker"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
)

const workerTTL = 30 * time.Second

func randomID() (string, error) {
	var b [24]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}
func (s *Store) ConfigureCapture(driver CaptureDriver, locks *livechannels.PhysicalLocks) error {
	if driver == nil || locks == nil {
		return ErrInvalid
	}
	id, e := randomID()
	if e != nil {
		return e
	}
	s.driver, s.locks, s.instance = driver, locks, id
	s.captureAvailable = driver.Available()
	if v, ok := driver.(StorageDriver); ok {
		s.storage = v
		v.SetPolicySource(s.StorageLimits)
	}
	if e = s.refreshStoragePolicy(context.Background()); e != nil {
		return e
	}
	return nil
}
func (s *Store) CaptureAvailable() bool { return s.captureAvailable }

type claim struct {
	sharedFrom        string
	request           CaptureRequest
	token             string
	cancelGeneration  int64
	recovery          bool
	recoveryContinues bool
	checkpoint        CaptureCheckpoint
}

// Run owns startup recovery, bounded/fair due work and physical worker lifetime.
// There is deliberately no generic CPU-based cap on independent captures.
func (s *Store) Run(ctx context.Context) {
	s.wg.Add(1)
	supervise.Go("dvr.intent-maintenance", func() { defer s.wg.Done(); s.RunIntentMaintenance(ctx) })
	defer func() {
		s.mu.Lock()
		for _, claims := range s.active {
			for _, cancel := range claims {
				cancel()
			}
		}
		s.mu.Unlock()
		s.wg.Wait()
	}()
	// A recording is due at a time the database knows, so the loop sleeps until
	// that time rather than asking every second whether it has arrived. Any
	// committed write — a new rule, a guide refresh, a cancellation — wakes it at
	// once, and the safety tick is the backstop for a wake that never came.
	wake := workerloop.NewSignal()
	unregister := dbwork.WakeOnTables(wake, "dvr_*", "live_*", "personal_items")
	defer unregister()
	workerloop.Run(ctx, "dvr.capture", wake, func(ctx context.Context) time.Duration {
		return s.capturePass(ctx)
	})
}

// capturePass does one round of capture work and says when the next is due.
func (s *Store) capturePass(ctx context.Context) time.Duration {
	if s.locks != nil {
		check, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
		_, _ = s.locks.ReconcileExpired(check, s.db, s.now())
		cancel()
	}
	for n := 0; n < 8; n++ {
		check, cancel := context.WithTimeout(ctx, time.Second)
		c, found, e := s.claimNext(check)
		cancel()
		if e != nil || !found {
			break
		}
		if c == nil {
			continue
		}
		worker, stop := context.WithCancel(ctx)
		s.trackWorker(c.request.Recording.ID, c.token, stop)
		s.wg.Add(1)
		claimed := c
		supervise.Go("dvr.claim", func() {
			defer s.wg.Done()
			defer stop()
			defer s.untrackWorker(claimed.request.Recording.ID, claimed.token)
			s.runClaim(worker, claimed)
		})
	}
	maintenance, cancel := context.WithTimeout(ctx, 5*time.Second)
	_ = s.refreshStoragePolicy(maintenance)
	_ = s.ExpireMissed(maintenance)
	_ = s.RetentionOne(maintenance)
	_ = s.DeleteOne(maintenance)
	_ = s.CleanupOne(maintenance)
	cancel()
	return s.nextCaptureDue(ctx)
}

// nextCaptureDue asks the database when it next has something to do, so an idle
// server sleeps instead of polling. A recording that starts in three hours costs
// one query now and nothing until then.
func (s *Store) nextCaptureDue(ctx context.Context) time.Duration {
	s.mu.Lock()
	active := len(s.active)
	s.mu.Unlock()
	if active > 0 {
		// Something is capturing: leases need renewing and progress checking.
		return time.Second
	}
	if !s.captureAvailable {
		return 0
	}
	var due sql.NullInt64
	now := s.now().UnixMilli()
	read, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	err := dbwork.QueryRow(read, s.db, `SELECT min(x) FROM (SELECT min(max(next_attempt_ms,start_ms)) AS x FROM dvr_recordings WHERE state IN('scheduled','conflicted','waiting-source','waiting-guide') UNION ALL SELECT min(max(next_attempt_ms,lease_until_ms)) FROM dvr_recordings WHERE state IN('preparing','recording','finalizing'))`).Scan(&due)
	if err != nil || !due.Valid {
		// Nothing scheduled at all: wait to be told.
		return 0
	}
	if wait := time.Duration(due.Int64-now) * time.Millisecond; wait > 0 {
		return wait
	}
	// Already due, and the pass above did not clear it: try again shortly rather
	// than spinning.
	return time.Second
}

// Worker tokens, rather than just recording IDs, track physical activity. A
// stale worker's return must not remove its successor's cancellation/IO fence.
func (s *Store) trackWorker(id, token string, stop context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		s.active = map[string]map[string]context.CancelFunc{}
	}
	if s.active[id] == nil {
		s.active[id] = map[string]context.CancelFunc{}
	}
	s.active[id][token] = stop
}
func (s *Store) untrackWorker(id, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active[id], token)
	if len(s.active[id]) == 0 {
		delete(s.active, id)
	}
}
func (s *Store) claimNext(ctx context.Context) (*claim, bool, error) {
	if !s.captureAvailable {
		return nil, false, nil
	}
	var storageError error
	// retained is whether retention has had its turn since the volume was
	// found short. It runs here, before any recording is judged, rather than
	// only after the claims: a pass over several due recordings on a slow
	// volume can outlast the wait before one is looked at again.
	retained := true
	if s.storage != nil {
		storageError = s.storage.CheckFloor(ctx)
		if errors.Is(storageError, ErrStorageFloor) || errors.Is(storageError, ErrStorageCap) {
			retained = s.RetentionOne(ctx) == nil
		}
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
	if e != nil {
		return nil, false, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var id string
	e = tx.QueryRowContext(ctx, `SELECT id FROM dvr_recordings WHERE next_attempt_ms<=? AND ((state IN('scheduled','conflicted','waiting-source','waiting-guide') AND start_ms<=?) OR ((state IN('preparing','recording','finalizing') OR (state IN('cancelled','pending-delete') AND reason IN('owner-cancelled','owner-delete') AND lease_until_ms>0 AND EXISTS(SELECT 1 FROM dvr_recording_shares x JOIN dvr_recordings follower ON follower.id=x.recording_id WHERE x.capture_recording_id=dvr_recordings.id AND follower.state IN('scheduled','conflicted','waiting-source','waiting-guide','preparing','recording','finalizing')))) AND lease_until_ms<=?)) ORDER BY priority DESC,start_ms,id LIMIT 1`, s.now().UnixMilli(), s.now().UnixMilli(), s.now().UnixMilli()).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, false, nil
	}
	if e != nil {
		return nil, false, e
	}
	c := &claim{}
	var generation int64
	var allocationID, checkpoint string
	var recoveryAttempt int
	e = tx.QueryRowContext(ctx, `SELECT authority,account_id,profile_id,claim_generation,cancel_generation,allocation_id,checkpoint_json,recovery_attempt FROM dvr_recordings WHERE id=?`, id).Scan(&c.request.Owner.Authority, &c.request.Owner.AccountID, &c.request.Owner.ProfileID, &generation, &c.cancelGeneration, &allocationID, &checkpoint, &recoveryAttempt)
	if e != nil {
		return nil, false, e
	}
	r, e := loadTx(ctx, tx, c.request.Owner, id)
	if e != nil {
		return nil, false, e
	}
	c.request.Recording = r
	endEstimate, _ := time.Parse(time.RFC3339Nano, r.End)
	c.request.EstimatedBytes, e = estimateBytesTx(ctx, tx, r.Occurrence.SourceID, int64(endEstimate.Sub(s.now()).Seconds()))
	if e != nil {
		return nil, false, e
	}
	fail := func(reason string) (*claim, bool, error) {
		if _, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET state='failed',reason=?,revision=revision+1,lease_until_ms=0,finished_ms=?,updated_ms=? WHERE id=?`, reason, s.now().UnixMilli(), s.now().UnixMilli(), id); e != nil {
			return nil, false, e
		}
		if e = unreserveTx(ctx, tx, id); e != nil {
			return nil, false, e
		}
		if e = touchTx(ctx, tx, c.request.Owner); e != nil {
			return nil, false, e
		}
		// A recording that lost its tuner or its storage is the one DVR outcome a
		// viewer has to be told about; it commits with the state change.
		if reason == "tuner-conflict" || reason == "tuner-in-use-live" || reason == "storage-floor" || reason == "storage-cap" {
			if _, e = notify.NotifyRecordingConflict(tx, s.now().UnixMilli(), id, r.Programme.Title, reason); e != nil {
				return nil, false, e
			}
		}
		return nil, true, gated.Commit()
	}
	later := func(reason string) (*claim, bool, error) {
		_, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET reason=?,next_attempt_ms=? WHERE id=?`, reason, s.now().Add(3*time.Second).UnixMilli(), id)
		if e != nil {
			return nil, false, e
		}
		return nil, true, gated.Commit()
	}
	if r.State == "cancelled" || r.State == "pending-delete" {
		e = s.sharedAuthorityTx(ctx, tx, r.ID)
	} else {
		e = s.durable(ctx, tx, c.request.Owner, r.Occurrence.SourceID, r.Occurrence.ChannelID)
	}
	if e != nil {
		if errors.Is(e, livechannels.ErrDenied) || errors.Is(e, ErrDenied) {
			return fail("permission-denied")
		}
		return later("authorization-unavailable")
	}
	if r.State != "cancelled" && r.State != "pending-delete" {
		if e = programmeVisibleTx(ctx, tx, c.request.Owner, r.Programme); e != nil {
			if errors.Is(e, ErrDenied) {
				return fail("permission-denied")
			}
			return later("authorization-unavailable")
		}
	}
	end, e := time.Parse(time.RFC3339Nano, r.End)
	if e != nil {
		return nil, false, e
	}
	if !capturing(r.State) && r.State != "cancelled" && r.State != "pending-delete" {
		if r.RuleID != "" {
			var enabled, deleted bool
			var currentRevision, acceptedRevision int64
			e = tx.QueryRowContext(ctx, `SELECT r.enabled,r.deleted,r.revision,x.rule_revision FROM dvr_rules r JOIN dvr_recordings x ON x.rule_id=r.id WHERE x.id=?`, id).Scan(&enabled, &deleted, &currentRevision, &acceptedRevision)
			if e != nil {
				return nil, false, e
			}
			if !enabled || deleted {
				return fail("rule-disabled")
			}
			if currentRevision != acceptedRevision {
				return later("rule-reconciliation-pending")
			}
		}
	}
	if shared, waiting, err := s.sharedCaptureTx(ctx, tx, c, r, generation); err != nil {
		return nil, false, err
	} else if shared {
		if waiting {
			return later("shared-capture")
		}
		if recoveryAttempt >= 3 {
			return fail("shared-capture-unavailable")
		}
		return c, true, gated.Commit()
	}
	c.recovery = capturing(r.State) || r.State == "cancelled" || r.State == "pending-delete"
	if c.recovery {
		if recoveryAttempt >= 2 {
			return fail("capture-validation-failed")
		}
		var state string
		e = tx.QueryRowContext(ctx, `SELECT id,source_id,channel_id,resource_id,kind,token,generation,state FROM live_allocations WHERE id=?`, allocationID).Scan(&c.request.Allocation.ID, &c.request.Allocation.SourceID, &c.request.Allocation.ChannelID, &c.request.Allocation.ResourceID, &c.request.Allocation.Kind, &c.request.Allocation.Token, &c.request.Allocation.Generation, &state)
		if e != nil {
			return nil, false, e
		}
		if state != "released" {
			return later("previous-worker-retiring")
		}
		if json.Unmarshal([]byte(checkpoint), &c.checkpoint) != nil || c.checkpoint.Object.Size <= 0 {
			return fail("capture-no-media")
		}
		if end.After(s.now()) {
			var active string
			if e = tx.QueryRowContext(ctx, `SELECT active_generation FROM live_sources WHERE id=? AND state='active'`, r.Occurrence.SourceID).Scan(&active); e != nil {
				return later("source-disabled")
			}
			c.request.Input, e = s.live.InputTx(ctx, tx, r.Occurrence.SourceID, r.Occurrence.ChannelID, active)
			if e != nil {
				return nil, false, e
			}
			c.request.Allocation, e = livechannels.AcquireTx(ctx, tx, livechannels.Allocation{SourceID: r.Occurrence.SourceID, ChannelID: r.Occurrence.ChannelID, ResourceID: id, Kind: "recording", Owner: c.request.Owner}, s.now())
			if e != nil {
				return later("tuner-retiring")
			}
			c.recoveryContinues = true
		}
		c.request.Generation = generation + 1
		if e = tx.QueryRowContext(ctx, `SELECT artifact_id FROM dvr_recordings WHERE id=?`, id).Scan(&c.request.ArtifactKey); e != nil {
			return nil, false, e
		}
	} else {
		var active, state string
		e = tx.QueryRowContext(ctx, `SELECT active_generation,state FROM live_sources WHERE id=?`, r.Occurrence.SourceID).Scan(&active, &state)
		if e != nil {
			return nil, false, e
		}
		if state != "active" {
			return later("source-disabled")
		}
		currentProgramme, e := s.committedProgrammeTx(ctx, tx, r, active)
		if e != nil {
			return nil, false, e
		}
		if active != r.Occurrence.Generation || digest(currentProgramme) != digest(r.Programme) {
			start, end, e := padded(currentProgramme, r.Options)
			if e != nil {
				return nil, false, e
			}
			body, _ := json.Marshal(currentProgramme)
			_, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET programme_id=?,guide_generation=?,programme_json=?,start_ms=?,end_ms=?,revision=revision+1,updated_ms=? WHERE id=?`, currentProgramme.ID, active, string(body), start.UnixMilli(), end.UnixMilli(), s.now().UnixMilli(), id)
			if e != nil {
				return nil, false, e
			}
			if e = reserveTx(ctx, tx, id, r.Occurrence.SourceID, start, end, r.Options.Priority); e != nil {
				return nil, false, e
			}
			if e = touchTx(ctx, tx, c.request.Owner); e != nil {
				return nil, false, e
			}
			return nil, true, gated.Commit()
		}
		// Reconcile the exact current guide occurrence before judging its end
		// or consuming storage. An extension may have moved the old window.
		if storageError != nil {
			reason := "storage-unavailable"
			if errors.Is(storageError, ErrStorageFloor) {
				reason = "storage-floor"
			} else if errors.Is(storageError, ErrStorageCap) {
				reason = "storage-cap"
			}
			// A full disk is not final while space is on its way: retention has
			// just queued what the owner's policy lets it delete, and a queued
			// deletion frees its bytes after a short grace. Until then the
			// recording is looked at again shortly and starts as soon as there
			// is room. It fails once retention has run and nothing is queued,
			// and never waits past its own window. Nothing is deleted for its
			// sake that retention would not delete anyway.
			if reason != "storage-unavailable" && end.After(s.now()) {
				reclaiming := !retained
				if !reclaiming {
					if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dvr_delete_work)`).Scan(&reclaiming); e != nil {
						return nil, false, e
					}
				}
				if reclaiming {
					return later(reason)
				}
			}
			return fail(reason)
		}
		if !end.After(s.now()) {
			if r.State == "conflicted" || r.Reason == "tuner-conflict" {
				return fail("tuner-conflict")
			}
			if r.Reason == "tuner-in-use-live" {
				// Every tuner stayed with live viewers for the whole window.
				return fail("tuner-in-use-live")
			}
			return fail("capture-window-missed")
		}
		captureStart, captureEnd, blocked, e := s.captureWindowTx(ctx, tx, r)
		if e != nil {
			return nil, false, e
		}
		if blocked {
			return later("tuner-conflict")
		}
		if captureStart.Format(time.RFC3339Nano) != r.Start || captureEnd.Format(time.RFC3339Nano) != r.End {
			r.Start, r.End = captureStart.UTC().Format(time.RFC3339Nano), captureEnd.UTC().Format(time.RFC3339Nano)
			c.request.Recording = r
			if _, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET start_ms=?,end_ms=? WHERE id=?`, captureStart.UnixMilli(), captureEnd.UnixMilli(), r.ID); e != nil {
				return nil, false, e
			}
			if e = reserveTx(ctx, tx, r.ID, r.Occurrence.SourceID, captureStart, captureEnd, r.Options.Priority); e != nil {
				return nil, false, e
			}
		}
		c.request.Input, e = s.live.InputTx(ctx, tx, r.Occurrence.SourceID, r.Occurrence.ChannelID, active)
		if e != nil {
			return nil, false, e
		}
		allocation, e := livechannels.AcquireTx(ctx, tx, livechannels.Allocation{SourceID: r.Occurrence.SourceID, ChannelID: r.Occurrence.ChannelID, ResourceID: id, Kind: "recording", Owner: c.request.Owner}, s.now())
		if errors.Is(e, livechannels.ErrPreemptPending) {
			return later("tuner-preemption-countdown")
		}
		if errors.Is(e, livechannels.ErrLiveInUse) {
			// The owner keeps live viewers watching: the recording waits and starts
			// (late, kept as a partial recording) as soon as a tuner is free. The
			// owner is told once, when it first has to wait.
			if r.Reason != "tuner-in-use-live" {
				if _, e = notify.NotifyRecordingConflict(tx, s.now().UnixMilli(), id, r.Programme.Title, "tuner-in-use-live-waiting"); e != nil {
					return nil, false, e
				}
			}
			return later("tuner-in-use-live")
		}
		if errors.Is(e, livechannels.ErrCapacity) || errors.Is(e, livechannels.ErrLease) {
			return later("tuner-retiring")
		}
		if e != nil {
			return nil, false, e
		}
		if r.Reason == "tuner-in-use-live" {
			// It waited for live viewing to give a tuner back: the notice says
			// it started late rather than that it is still waiting.
			if _, e = notify.NotifyRecordingConflict(tx, s.now().UnixMilli(), id, r.Programme.Title, "tuner-in-use-live-late"); e != nil {
				return nil, false, e
			}
		}
		c.request.Allocation = allocation
		c.request.Generation = generation + 1
		c.request.ArtifactKey, e = mediaartifact.RetainedCaptureKey(id, c.request.Generation)
		if e != nil {
			return nil, false, e
		}
	}
	c.token, e = randomID()
	if e != nil {
		return nil, false, e
	}
	state := "preparing"
	if c.recovery {
		state = "finalizing"
		if r.State == "cancelled" || r.State == "pending-delete" {
			state = r.State
		}
	}
	_, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET state=?,reason=CASE WHEN state IN('cancelled','pending-delete') THEN reason ELSE '' END,claim_generation=?,claim_token=?,owner_instance=?,lease_until_ms=?,allocation_id=?,artifact_id=?,next_attempt_ms=0,recovery_attempt=recovery_attempt+?,revision=revision+1,updated_ms=? WHERE id=?`, state, c.request.Generation, c.token, s.instance, s.now().Add(workerTTL).UnixMilli(), c.request.Allocation.ID, c.request.ArtifactKey, c.recovery, s.now().UnixMilli(), id)
	if e != nil {
		return nil, false, e
	}
	if e = touchTx(ctx, tx, c.request.Owner); e != nil {
		return nil, false, e
	}
	return c, true, gated.Commit()
}
func (s *Store) currentClaimTx(ctx context.Context, tx *sql.Tx, c *claim) error {
	var valid bool
	e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dvr_recordings WHERE id=? AND owner_key=? AND claim_token=? AND claim_generation=? AND cancel_generation=? AND lease_until_ms>? AND state IN('preparing','recording','finalizing'))`, c.request.Recording.ID, c.request.Owner.Key(), c.token, c.request.Generation, c.cancelGeneration, s.now().UnixMilli()).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dvr_recordings WHERE id=? AND owner_key=? AND claim_token=? AND claim_generation=? AND lease_until_ms>? AND state IN('cancelled','pending-delete') AND reason IN('owner-cancelled','owner-delete'))`, c.request.Recording.ID, c.request.Owner.Key(), c.token, c.request.Generation, s.now().UnixMilli()).Scan(&valid)
		if err != nil {
			return err
		}
		if !valid {
			return ErrLease
		}
		return s.sharedAuthorityTx(ctx, tx, c.request.Recording.ID)
	}
	if e = s.durable(ctx, tx, c.request.Owner, c.request.Recording.Occurrence.SourceID, c.request.Recording.Occurrence.ChannelID); e != nil {
		if !c.recovery && s.retainedCapture != nil {
			var started bool
			err := tx.QueryRowContext(ctx, `SELECT state IN('recording','finalizing') FROM dvr_recordings WHERE id=?`, c.request.Recording.ID).Scan(&started)
			if err != nil {
				return err
			}
			if started {
				err = s.retainedCapture(ctx, tx, c.request.Owner, c.request.Recording.Occurrence.SourceID, c.request.Recording.Occurrence.ChannelID)
				if err == nil {
					return nil
				}
				if !errors.Is(err, livechannels.ErrDenied) && !errors.Is(err, ErrDenied) {
					return err
				}
			}
		}
		return e
	}
	return programmeVisibleTx(ctx, tx, c.request.Owner, c.request.Recording.Programme)
}
func (s *Store) runClaim(parent context.Context, c *claim) {
	if c.sharedFrom != "" {
		s.runSharedClaim(parent, c)
		return
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	lock, e := s.locks.Lock(c.request.Allocation)
	if e != nil {
		s.failWorker(c, "previous-worker-retiring", true)
		return
	}
	defer lock.Close()
	c.request.PhysicalLock = lock
	var retired atomic.Bool
	var once sync.Once
	release := func() {
		once.Do(func() {
			retired.Store(true)
			work, done := context.WithTimeout(context.Background(), time.Second)
			defer done()
			gated2, e := dbwork.Begin(work, s.db, dbwork.ClassProtectedCapture)
			tx := gated2.Tx()
			if e == nil {
				defer gated2.Rollback()
				if livechannels.ReleaseAllocationTx(work, tx, c.request.Allocation) == nil {
					_ = gated2.Commit()
				}
			}
		})
	}
	defer release()
	// We own the physical lock, including when profile deletion or a privilege
	// change invalidated this claim during startup. Register exact-generation
	// retirement before reauthorization so that early denial cannot leak a slot.
	// No input request or decoder has been created at this point.
	if e = s.renew(ctx, c, false); e != nil {
		return
	}
	c.request.OnRetired = release
	c.request.Checkpoint = func(cp CaptureCheckpoint) error { return s.checkpoint(ctx, c, cp) }
	renewalDone := make(chan struct{})
	supervise.Go("dvr.renewal", func() {
		defer close(renewalDone)
		tick := time.NewTicker(4 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if e := s.renew(ctx, c, retired.Load() || c.recovery && !c.recoveryContinues); e != nil {
					cancel()
					return
				}
			}
		}
	})
	var result CaptureResult
	if c.recovery {
		result, e = s.driver.Recover(ctx, c.request, c.checkpoint)
	} else {
		result, e = s.driver.Capture(ctx, c.request, func(cp CaptureCheckpoint) error { return s.checkpoint(ctx, c, cp) })
	}
	if e == nil {
		result, e = s.prepareRecordingLayout(ctx, c, result)
	}
	if e == nil {
		e = s.publish(ctx, c, result)
	}
	cancel()
	<-renewalDone
	if e != nil && parent.Err() == nil {
		reason, recoverable := "capture-interrupted", !c.recovery
		if errors.Is(e, ErrStorageFloor) {
			reason = "storage-floor"
			recoverable = false
		}
		if errors.Is(e, ErrStorageCap) {
			reason = "storage-cap"
			recoverable = false
		}
		if errors.Is(e, ErrStorageUnavailable) {
			reason = "storage-unavailable"
			recoverable = false
		}
		s.failWorker(c, reason, recoverable)
	}
	// Shutdown deliberately leaves the durable checkpoint for startup recovery.
}
func (s *Store) renew(ctx context.Context, c *claim, released bool) error {
	gated3, e := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
	if e != nil {
		return e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	if e = s.currentClaimTx(ctx, tx, c); e != nil {
		if errors.Is(e, livechannels.ErrDenied) || errors.Is(e, ErrDenied) {
			_, _ = tx.ExecContext(ctx, `UPDATE dvr_recordings SET state='cancelled',reason='permission-denied',cancel_generation=cancel_generation+1,revision=revision+1,updated_ms=? WHERE id=? AND claim_token=? AND state IN('preparing','recording','finalizing')`, s.now().UnixMilli(), c.request.Recording.ID, c.token)
			_ = unreserveTx(ctx, tx, c.request.Recording.ID)
			_ = touchTx(ctx, tx, c.request.Owner)
			_ = gated3.Commit()
		}
		return e
	}
	if !released && (!c.recovery || c.recoveryContinues) {
		var active, state string
		e = tx.QueryRowContext(ctx, `SELECT active_generation,state FROM live_sources WHERE id=?`, c.request.Input.SourceID).Scan(&active, &state)
		if e != nil {
			return e
		}
		if state != "active" {
			return ErrLease
		}
		input, e := s.live.InputTx(ctx, tx, c.request.Input.SourceID, c.request.Input.ChannelID, active)
		if e != nil || input.Fence() != c.request.Input.Fence() {
			return ErrLease
		}
		if _, e = livechannels.RenewAllocationTx(ctx, tx, c.request.Allocation, s.now()); e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET lease_until_ms=? WHERE id=? AND claim_token=?`, s.now().Add(workerTTL).UnixMilli(), c.request.Recording.ID, c.token)
	if e != nil {
		return e
	}
	return gated3.Commit()
}
func (s *Store) checkpoint(ctx context.Context, c *claim, cp CaptureCheckpoint) error {
	gated4, e := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
	if e != nil {
		return e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	if e = s.currentClaimTx(ctx, tx, c); e != nil {
		return e
	}
	var previous string
	if e = tx.QueryRowContext(ctx, `SELECT state FROM dvr_recordings WHERE id=?`, c.request.Recording.ID).Scan(&previous); e != nil {
		return e
	}
	state := "recording"
	if cp.WriterRetired {
		state = "finalizing"
	}
	if c.request.Input.Locator != "" {
		cp.SourceRevision = c.request.Input.Revision
		cp.SourceFence = c.request.Input.Fence()
	}
	b, _ := json.Marshal(cp)
	_, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET revision=revision+CASE WHEN state IN('cancelled','pending-delete') OR state=? THEN 0 ELSE 1 END,state=CASE WHEN state IN('cancelled','pending-delete') THEN state ELSE ? END,artifact_digest=?,bytes=?,captured_start_ms=?,captured_end_ms=?,checkpoint_json=?,updated_ms=? WHERE id=? AND claim_token=?`, state, state, cp.Object.Digest, cp.Object.Size, cp.First.UnixMilli(), cp.Last.UnixMilli(), string(b), s.now().UnixMilli(), c.request.Recording.ID, c.token)
	if e != nil {
		return e
	}
	if previous != state {
		if e = touchTx(ctx, tx, c.request.Owner); e != nil {
			return e
		}
	}
	return gated4.Commit()
}
func (s *Store) failWorker(c *claim, reason string, recoverable bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	gated5, e := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
	if e != nil {
		return
	}
	tx := gated5.Tx()
	defer gated5.Rollback()
	state := "failed"
	if recoverable {
		state = "finalizing"
	}
	res, e := tx.ExecContext(ctx, `UPDATE dvr_recordings SET state=?,reason=?,lease_until_ms=0,next_attempt_ms=?,revision=revision+1,updated_ms=? WHERE id=? AND claim_token=? AND cancel_generation=? AND state IN('preparing','recording','finalizing')`, state, reason, s.now().Add(3*time.Second).UnixMilli(), s.now().UnixMilli(), c.request.Recording.ID, c.token, c.cancelGeneration)
	if e != nil {
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		_, _ = tx.ExecContext(ctx, `UPDATE dvr_recordings SET lease_until_ms=0 WHERE id=? AND claim_token=? AND claim_generation=? AND state IN('cancelled','pending-delete')`, c.request.Recording.ID, c.token, c.request.Generation)
	}
	if n > 0 {
		_ = unreserveTx(ctx, tx, c.request.Recording.ID)
		_ = touchTx(ctx, tx, c.request.Owner)
	}
	_ = gated5.Commit()
}
