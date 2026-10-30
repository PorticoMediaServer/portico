package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

// A completion retains only exact process handles/IDs until their committed
// fences finish. It contains no request or response, and a restart has no old
// processes to join. The durable grant fences and receipt are already atomic.
type stopCompletion struct {
	mu     sync.Mutex
	finish func(context.Context) error
	done   bool
}

func (s *Service) finishStop(ctx context.Context, key string, finish func(context.Context) error) error {
	if key == "" {
		return finish(ctx)
	}
	value, ok := s.stopCompletions.Load(key)
	if !ok {
		return nil
	}
	c := value.(*stopCompletion)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return nil
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := c.finish(cleanup); err != nil {
		return err
	}
	c.done = true
	s.releaseStopCompletion(key, c)
	return nil
}
func stopKey(ctx context.Context) string {
	_, e, ok := usermutation.RequestFrom(ctx)
	if ok {
		return e.Key
	}
	return ""
}

// CompleteStopReceipt must be called after the authenticated receipt lookup's
// read snapshot closes, before reporting applied. It never looks up a mutable
// session to discover a target. No process handles survive a server restart.
func (s *Service) CompleteStopReceipt(ctx context.Context, receipt usermutation.Receipt) error {
	if dbwork.Snapshot(ctx) != nil {
		return errors.New("Stop completion requires a closed read snapshot")
	}
	if _, ok := s.stopCompletions.Load(receipt.Key); !ok {
		return nil
	}
	return s.finishStop(ctx, receipt.Key, nil)
}
func (s *Service) replayStop(ctx context.Context, receipt *usermutation.Receipt) error {
	return s.CompleteStopReceipt(ctx, *receipt)
}

// Pending joins are never aged out or evicted. Capacity is reserved before
// any domain effect and released only on rollback or completed exact cleanup.
const maxStopCompletions = 4096

func (s *Service) reserveStopCompletion(key string) (*stopCompletion, error) {
	if key == "" {
		return nil, nil
	}
	s.stopCompletionMu.Lock()
	defer s.stopCompletionMu.Unlock()
	if _, ok := s.stopCompletions.Load(key); ok {
		// Check just proved there is no durable receipt for this key in the
		// accepting writer: a prior unknown COMMIT rolled back.
		s.stopCompletions.Delete(key)
		s.stopCompletionCount--
	}
	if s.stopCompletionCount >= maxStopCompletions {
		return nil, usermutation.ErrCapacity
	}
	c := &stopCompletion{}
	s.stopCompletions.Store(key, c)
	s.stopCompletionCount++
	return c, nil
}
func (s *Service) releaseStopCompletion(key string, c *stopCompletion) {
	if c == nil {
		return
	}
	s.stopCompletionMu.Lock()
	defer s.stopCompletionMu.Unlock()
	if s.stopCompletions.CompareAndDelete(key, c) {
		s.stopCompletionCount--
	}
}

func checkStopReceipt(ctx context.Context, tx *sql.Tx, now int64) (*usermutation.Receipt, error) {
	if _, e, ok := usermutation.RequestFrom(ctx); ok && !e.Stop {
		return nil, usermutation.ErrConflict
	}
	return usermutation.CheckContext(ctx, tx, now)
}

func (s *Service) stopWithReceipt(ctx context.Context, c Caller, id string, position *int64, generation *int, account ...bool) error {
	if generation != nil && (*generation < 1 || int64(*generation) > 2147483647) {
		return &FieldError{Path: "generation"}
	}
	key := stopKey(ctx)
	// Preserve unkeyed ended/refused calls' no-writer behavior. Keyed requests
	// prove current authority and consult the receipt before any mutable target.
	if key == "" && !(len(account) > 0 && account[0]) {
		r, err := s.session(ctx, c, id)
		if err != nil {
			return err
		}
		if generation != nil && r.generation != *generation {
			return ErrRevision
		}
		if r.ended != 0 {
			return nil
		}
	}
	var replay *usermutation.Receipt
	var finish func(context.Context) error
	var completion *stopCompletion
	w, err := dbwork.Begin(ctx, s.DB, dbwork.ClassEstablishedPlayback)
	if err != nil {
		return err
	}
	defer w.Rollback()
	commitStarted := false
	defer func() {
		if !commitStarted {
			s.releaseStopCompletion(key, completion)
		}
	}()
	apply := func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if replay, err = checkStopReceipt(ctx, tx, s.now().UnixMilli()); err != nil {
			return err
		}
		if replay != nil {
			return s.AuthorizeStopReceipt(ctx, c, *replay)
		}
		r, err := loadRow(ctx, tx, id)
		if err != nil {
			return err
		}
		if len(account) > 0 && account[0] {
			if r.authority != c.Principal.Authority || r.account != c.Principal.AccountID {
				return ErrNotFound
			}
			c.Principal.ProfileID, c.AuthorizeItem = r.profile, nil
		}
		if err = c.authorizeSession(ctx, r); err != nil {
			return err
		}
		proved, err := loadRow(ctx, tx, id)
		if err != nil {
			return err
		}
		if !sameStopBinding(r, proved) || proved.ended != r.ended {
			return ErrRevision
		}
		r = proved
		if generation != nil && r.generation != *generation {
			return ErrRevision
		}
		original := r
		result := usermutation.Result{Status: "already_stopped", TargetID: id, Revision: int64(r.revision), Generation: r.generation, PositionMs: r.position, PositionStatus: "already_stopped"}
		if completion, err = s.reserveStopCompletion(key); err != nil {
			return err
		}
		if r.ended == 0 {
			if err = s.drainObservationTx(ctx, tx, id); err != nil {
				return err
			}
			if r, err = loadRow(ctx, tx, id); err != nil {
				return err
			}
			observedBinding := original
			observedBinding.partIndex = r.partIndex
			if !sameStopBinding(observedBinding, r) || r.ended != 0 {
				return ErrRevision
			}
			disposition, err := s.stopPositionTx(ctx, tx, c, r, position)
			if err != nil {
				return err
			}
			if disposition != "policy_rejected" {
				if err = s.recordStoppedActivityTx(ctx, tx, c, r, s.now()); err != nil {
					return err
				}
			}
			if r, err = loadRow(ctx, tx, id); err != nil {
				return err
			}
			observed := original
			observed.partIndex = r.partIndex // the authorized drain may report a part boundary
			if !sameStopBinding(observed, r) || r.ended != 0 {
				return ErrRevision
			}
			now := s.now()
			if err = s.endTxRaw(ctx, tx, r, now, "stopped", ""); err != nil {
				return err
			}

			result.Status, result.Revision, result.PositionMs, result.PositionStatus = "applied", int64(r.revision+1), r.position, disposition
		}
		if finish, err = s.stopCleanupForRowTx(ctx, tx, r, key); err != nil {
			return err
		}
		if completion != nil {
			completion.finish = finish
		}
		return saveStopReceipt(ctx, tx, original, result, s.now().UnixMilli())
	}
	if err = apply(w.Context(), w.Tx()); err != nil {
		return err
	}
	commitStarted = true
	if err = w.Commit(); err != nil {
		return err
	}
	if replay != nil {
		return s.replayStop(ctx, replay)
	}
	if finish == nil {
		return nil
	}
	s.hub.wake()
	// Channel consumers share the committed end event with other sessions.
	s.wakeChannels()
	s.sweepQueues()
	return s.finishStop(ctx, key, finish)
}

// Capture only this occurrence's media and children while its writer is held.
// Already-stopped keyed requests also finish these fences, including a previous
// request whose process join has not completed yet.
func (s *Service) stopCleanupForRowTx(ctx context.Context, tx *sql.Tx, r row, key string) (func(context.Context) error, error) {
	type occurrence struct {
		id         string
		generation int
	}
	var targets []occurrence
	if r.media != "" {
		targets = append(targets, occurrence{r.media, r.mediaGeneration})
	}
	rows, err := tx.QueryContext(ctx, `SELECT media_session_id,device_id FROM playback_v1_prepared WHERE session_id=? AND session_generation=? AND (state='prepared' OR (state='canceled' AND cancel_reason='stopped')) ORDER BY token`, r.id, r.generation)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, device string
		if err = rows.Scan(&id, &device); err != nil {
			rows.Close()
			return nil, err
		}
		if device != r.device || id == "" {
			rows.Close()
			return nil, ErrRevision
		}
		targets = append(targets, occurrence{id: id})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE playback_v1_prepared SET state='canceled',cancel_reason='stopped' WHERE session_id=? AND session_generation=? AND state='prepared'`, r.id, r.generation); err != nil {
		return nil, err
	}
	for i := range targets {
		t := &targets[i]
		var actual int
		if err = tx.QueryRowContext(ctx, `SELECT generation FROM playback_sessions WHERE id=?`, t.id).Scan(&actual); err != nil {
			return nil, err
		}
		if t.generation != 0 && actual != t.generation {
			return nil, ErrRevision
		}
		t.generation = actual
		if _, err = tx.ExecContext(ctx, `UPDATE playback_sessions SET state='stopped' WHERE id=? AND generation=?`, t.id, t.generation); err != nil {
			return nil, err
		}
	}
	return func(ctx context.Context) error {
		failures := make([]error, len(targets))
		var joins sync.WaitGroup
		for i, target := range targets {
			failures[i] = errors.New("stop occurrence cleanup did not complete")
			joins.Add(1)
			supervise.Go("playbackv1.stop-join", func() {
				defer joins.Done()
				if key == "" {
					failures[i] = s.Playback.StopV1(ctx, target.id)
				} else {
					failures[i] = s.Playback.FinishStoppedOccurrence(ctx, target.id, target.generation)
				}
			})
		}
		joins.Wait()
		return errors.Join(failures...)
	}, nil
}

func (s *Service) stopPositionTx(ctx context.Context, tx *sql.Tx, c Caller, r row, position *int64) (string, error) {
	if position == nil || *position < 0 {
		return "unchanged", nil
	}
	if c.DeviceID != r.device {
		return "not_renderer", nil
	}
	if r.media == "" {
		return "unchanged", nil
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT stop_position`); err != nil {
		return "", err
	}
	seq := r.lastSeq + 1
	err := s.Playback.CheckStopOccurrenceTx(ctx, tx, c.Principal, r.media, r.mediaGeneration, r.item)
	if errors.Is(err, playback.ErrOccurrenceChanged) {
		err = errStopEvidenceRefused
	}
	if err == nil {
		var previous int64
		err = tx.QueryRowContext(ctx, `SELECT sequence FROM playback_sessions WHERE id=?`, r.media).Scan(&previous)
		if err == nil && previous >= seq {
			err = errStopEvidenceRefused
		}
	}
	if err == nil {
		err = s.Playback.ObservationEvidenceTx(ctx, tx, c.Principal, r.media, r.mediaGeneration, seq, float64(*position)/1000, "paused", 0, nil, false, s.now(), nil)
	}
	// The legacy evidence port intentionally no-ops stale generations. Prove it
	// accepted this exact sequence before acknowledging the final position.
	if err == nil {
		var accepted bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_sessions WHERE id=? AND generation=? AND sequence=? AND state='paused')`, r.media, r.mediaGeneration, seq).Scan(&accepted)
		if err == nil && !accepted {
			err = errStopEvidenceRefused
		}
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET position_ms=?,last_seq=? WHERE id=? AND ended_ms=0`, *position, seq, r.id)
	}
	if err != nil {
		if !refusedEvidence(err) && !errors.Is(err, errStopEvidenceRefused) {
			return "", err
		}
		if _, rollback := tx.ExecContext(ctx, `ROLLBACK TO stop_position`); rollback != nil {
			return "", rollback
		}
		if _, release := tx.ExecContext(ctx, `RELEASE stop_position`); release != nil {
			return "", release
		}
		return "policy_rejected", nil
	}
	_, err = tx.ExecContext(ctx, `RELEASE stop_position`)
	return "applied", err
}

var errStopEvidenceRefused = errors.New("final position evidence was not accepted")

// AuthorizeStopReceipt re-proves current content access using the accepting
// writer's immutable item evidence, never the possibly replaced session row.
// The caller must first authenticate the receipt's original profile/family in
// this same snapshot. CompleteStopReceipt runs only after the snapshot closes.
func (s *Service) AuthorizeStopReceipt(ctx context.Context, c Caller, receipt usermutation.Receipt) error {
	tx := dbwork.Snapshot(ctx)
	if tx == nil {
		return errors.New("Stop receipt authorization requires a snapshot")
	}
	if receipt.Operation != "stop_playback_session" && receipt.Operation != "stop_account_playback" {
		return ErrNotFound
	}
	var item string
	err := tx.QueryRowContext(ctx, `SELECT r.stop_item_id FROM user_mutation_receipts r JOIN user_mutation_accounts a ON a.id=r.scope_id WHERE r.key=? AND r.operation=? AND r.target_id=? AND r.generation=? AND r.status<>'reserved' AND a.authority=? AND a.account_id=? AND r.profile_id=?`, receipt.Key, receipt.Operation, receipt.TargetID, receipt.Generation, c.Principal.Authority, c.Principal.AccountID, c.Principal.ProfileID).Scan(&item)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	// Empty evidence identifies an older receipt, not a deleted item. Fail
	// closed even if a visible successor currently has this target session ID.
	if item == "" {
		return ErrNotFound
	}
	// Own-account Stop deliberately requires no hidden-title visibility. The
	// sentinel is an explicitly captured channel with no catalogue item.
	if receipt.Operation == "stop_account_playback" || item == stopNoItem {
		return nil
	}
	var present int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM catalog_entities WHERE public_id=pid_blob(?)`, item).Scan(&present)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} // genuinely deleted original content
	if err != nil {
		return err
	}
	if c.AuthorizeItem == nil {
		return identity.ErrForbidden
	}
	return c.AuthorizeItem(ctx, item)
}

// This cannot be a catalogue public ID. Empty is reserved for older receipts
// that lack evidence and must not authorize replay or disclosure.
const stopNoItem = "-"

func saveStopReceipt(ctx context.Context, tx *sql.Tx, original row, result usermutation.Result, now int64) error {
	if err := usermutation.SaveContext(ctx, tx, result, now); err != nil {
		return err
	}
	key := stopKey(ctx)
	if key == "" {
		return nil
	}
	item := original.item
	if item == "" {
		if !isChannel(original) {
			return ErrNotFound
		}
		item = stopNoItem
	}
	updated, err := tx.ExecContext(ctx, `UPDATE user_mutation_receipts SET stop_item_id=? WHERE key=? AND stop_item_id=''`, item, key)
	if err != nil {
		return err
	}
	n, err := updated.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return usermutation.ErrConflict
	}
	return nil
}
