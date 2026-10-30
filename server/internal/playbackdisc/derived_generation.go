package playbackdisc

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sync/atomic"
	"time"
)

// DerivedGeneration is a process-only claim on one exact active parent inside
// one accepting writer. Copies share a single publish/activate latch. It is not
// reconstructible from rows, a snapshot ID or a matching digest.
type DerivedGeneration struct{ state *derivedGeneration }

type derivedGeneration struct {
	store         *Store
	tx            *sql.Tx
	original      context.Context
	parent, child Snapshot
	parentLife    lifecycle
	currentHash   string
	parentCheck   Check
	phase         atomic.Int32 // 0 issued, 1 publishing, 2 pending, 3 activating, 4 active, 5 failed
}

// ClaimDerivedGenerationTx runs BEFORE the actual legacy generation changes.
// It requires the active parent's ordinary current Check, including its actual
// retained input, source, actor, queue and accepted operation. The child keeps
// ALL original input/source/actor/operation/facts/tools and the parent's expiry;
// this port does not accept replacement measurement or renew any horizon.
func (s *Store) ClaimDerivedGenerationTx(ctx context.Context, tx *sql.Tx, parent Key, parentID string, child Key, childID string, check Check) (*DerivedGeneration, error) {
	if err := writeTx(ctx, tx); err != nil {
		return nil, err
	}
	if check == nil || !validKey(parent) || !validKey(child) || parent.Generation == int(^uint(0)>>1) || child.SessionID != parent.SessionID || child.Generation != parent.Generation+1 || !text(childID, 256, false) || childID == parentID {
		return nil, ErrInput
	}
	v, err := s.ResolveTx(ctx, tx, parent, parentID, check)
	if err != nil {
		return nil, err
	}
	l, err := s.identityTx(ctx, tx, parent, parentID)
	if err != nil {
		return nil, err
	}
	var hash string
	if err = tx.QueryRowContext(ctx, `SELECT session_hash FROM playback_sessions WHERE id=? AND generation=?`, parent.SessionID, parent.Generation).Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCurrent
		}
		return nil, err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_disc_snapshots WHERE id=? OR (session_id=? AND generation=?))`, childID, child.SessionID, child.Generation).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrConflict
	}
	next := v
	next.ID, next.SessionID, next.Generation = childID, child.SessionID, child.Generation
	next, err = freeze(next)
	if err != nil {
		return nil, err
	}
	return &DerivedGeneration{state: &derivedGeneration{store: s, tx: tx, original: ctx, parent: v, child: next, parentLife: l, currentHash: hash, parentCheck: check}}, nil
}

func (s *Store) derivedTx(ctx context.Context, tx *sql.Tx, claim *DerivedGeneration) (*derivedGeneration, error) {
	if err := writeTx(ctx, tx); err != nil {
		return nil, err
	}
	if claim == nil || claim.state == nil || claim.state.store != s || claim.state.tx != tx {
		return nil, ErrInput
	}
	if err := claim.state.original.Err(); err != nil {
		return nil, err
	}
	return claim.state, nil
}

// checkDerivedTx repeats both the frozen original parent proof and the current
// accepting child-generation proof. Parent lifecycle stays active until child
// activation; the legacy row already has the caller's actual new generation.
func (s *Store) checkDerivedTx(ctx context.Context, tx *sql.Tx, d *derivedGeneration, check Check) (int64, error) {
	if check == nil {
		return 0, ErrInput
	}
	if err := d.original.Err(); err != nil {
		return 0, err
	}
	l, err := s.identityTx(ctx, tx, d.parent.key(), d.parent.ID)
	if err != nil || l != d.parentLife || l.state != "active" || !l.sealed || l.expires <= time.Now().UnixMilli() {
		return 0, ErrCurrent
	}
	parent, err := s.readTx(ctx, tx, d.parent.key(), d.parent.ID, l)
	if err != nil {
		return 0, err
	}
	if !reflect.DeepEqual(parent, d.parent) {
		return 0, ErrCurrent
	}
	if err = d.parentCheck(ctx, tx, parent); err != nil {
		return 0, err
	}
	child, err := freeze(d.child)
	if err != nil {
		return 0, err
	}
	if err = check(ctx, tx, child); err != nil {
		return 0, err
	}
	// Even a same-family token change after issue is not silently adopted. The
	// exact current legacy hash observed while resolving the parent is fixed.
	var hash string
	if err = tx.QueryRowContext(ctx, `SELECT session_hash FROM playback_sessions WHERE id=? AND generation=?`, d.child.SessionID, d.child.Generation).Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrCurrent
		}
		return 0, err
	}
	if hash != d.currentHash {
		return 0, ErrCurrent
	}
	until, err := s.sessionProofTx(ctx, tx, d.child, func(current string) error {
		if current != d.currentHash {
			return ErrCurrent
		}
		return s.activeFamilySessionTx(ctx, tx, d.parent, current)
	})
	if err != nil {
		return 0, err
	}
	if err = s.checkCaptureTx(ctx, tx, d.child); err != nil {
		return 0, err
	}
	after, err := s.identityTx(ctx, tx, d.parent.key(), d.parent.ID)
	if err != nil || after != d.parentLife || after.expires <= time.Now().UnixMilli() || until < d.parentLife.expires {
		return 0, ErrCurrent
	}
	if err = ctx.Err(); err != nil {
		return 0, err
	}
	if err = d.original.Err(); err != nil {
		return 0, err
	}
	return d.parentLife.expires, nil
}

// PublishDerivedGenerationTx runs only AFTER the real legacy generation change.
// The mandatory Check proves the actual current accepted child plan/operation;
// the frozen original parent checker is independently repeated. Resources may
// be attached to the resulting sealed pending child in this SAME writer.
// On error the caller must roll back the outer accepting transaction, including
// its legacy generation change; no store method owns or commits that writer.
func (s *Store) PublishDerivedGenerationTx(ctx context.Context, tx *sql.Tx, claim *DerivedGeneration, check Check) error {
	d, err := s.derivedTx(ctx, tx, claim)
	if err != nil {
		return err
	}
	if !d.phase.CompareAndSwap(0, 1) {
		return ErrCurrent
	}
	err = s.publishPendingValueTx(ctx, tx, d.child, func() (int64, error) { return s.checkDerivedTx(ctx, tx, d, check) })
	if err != nil {
		d.phase.Store(5)
		return err
	}
	d.phase.Store(2)
	return nil
}

// ActivateDerivedGenerationTx accepts only this claim's exact stored child,
// before the OUTERMOST writer commits. No returned value starts media early.
// Retire the original parent only after this succeeds, in that same writer.
func (s *Store) ActivateDerivedGenerationTx(ctx context.Context, tx *sql.Tx, claim *DerivedGeneration, check Check) (Snapshot, error) {
	d, err := s.derivedTx(ctx, tx, claim)
	if err != nil {
		return Snapshot{}, err
	}
	if !d.phase.CompareAndSwap(2, 3) {
		return Snapshot{}, ErrCurrent
	}
	v, err := s.activateValueTx(ctx, tx, d.child.key(), d.child.ID, func(v Snapshot) (int64, error) {
		if !reflect.DeepEqual(v, d.child) {
			return 0, ErrCurrent
		}
		return s.checkDerivedTx(ctx, tx, d, check)
	})
	if err != nil {
		d.phase.Store(5)
		return Snapshot{}, err
	}
	d.phase.Store(4)
	return v, nil
}
