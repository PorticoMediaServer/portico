package playback

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
)

// A borrow belongs to one currently admitted original POST. It holds the
// process-private measurement through both accepting writers and the OUTERMOST
// final commit. It never upgrades worker proof into current-request admission.
type discPlayBorrow struct {
	owner        *discPlayPreparation
	measurement  *discPlayMeasurement
	claim        playbackdisc.PreparationClaim
	caller       context.Context
	current      playbackdisc.PreparationContinuationCheck
	released     atomic.Bool
	contextMu    sync.Mutex
	contexts     []func()
	acceptMu     sync.Mutex
	activationTx *sql.Tx
	activated    playbackdisc.PreparationAccepted
}

func (p *discPlayPreparation) borrowPrepared(ctx context.Context, current playbackdisc.PreparationContinuationCheck) (*discPlayBorrow, error) {
	if ctx == nil || dbwork.Snapshot(ctx) != nil || current == nil {
		return nil, disc.ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.ctx.Err(); err != nil {
		return nil, err
	}
	if !p.custody.TryLock() {
		return nil, playbackdisc.ErrCurrent
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.retiring || p.accepted || p.failure != nil || p.operation.State != "prepared" || p.measurement == nil || p.borrow != nil || p.ctx.Err() != nil || ctx.Err() != nil {
		p.custody.Unlock()
		return nil, playbackdisc.ErrCurrent
	}
	deadline, finite := p.ctx.Deadline()
	if !finite {
		p.custody.Unlock()
		return nil, disc.ErrProtocol
	}
	life, cancel := context.WithDeadline(ctx, deadline)
	stop := context.AfterFunc(p.ctx, cancel)
	if p.ctx.Err() != nil {
		cancel()
	}
	b := &discPlayBorrow{owner: p, measurement: p.measurement, claim: p.operation.Claim, caller: life, current: current, contexts: []func(){func() { stop(); cancel() }}}
	p.borrow = b
	return b, nil
}

func (b *discPlayBorrow) held() error {
	if b == nil || b.owner == nil || b.measurement == nil || b.current == nil || b.released.Load() {
		return disc.ErrProtocol
	}
	if err := b.caller.Err(); err != nil {
		return err
	}
	p := b.owner
	p.mu.Lock()
	exact := !p.closed && !p.retiring && !p.accepted && p.borrow == b && p.measurement == b.measurement && p.operation.Claim == b.claim
	p.mu.Unlock()
	if !exact {
		return playbackdisc.ErrCurrent
	}
	return p.ctx.Err()
}

func (b *discPlayBorrow) checkWorkerTx(ctx context.Context, tx *sql.Tx, operation playbackdisc.Preparation) error {
	if err := b.held(); err != nil {
		return err
	}
	if err := b.owner.checkOriginalTx(ctx, tx, operation); err != nil {
		return err
	}
	return b.measurement.CheckPreparedTx(ctx, tx)
}

func (b *discPlayBorrow) checkCurrentTx(ctx context.Context, tx *sql.Tx, operation playbackdisc.Preparation) error {
	if err := b.held(); err != nil {
		return err
	}
	if err := b.current(ctx, tx, operation); err != nil {
		return err
	}
	return b.held()
}

func (b *discPlayBorrow) checkContinuationTx(ctx context.Context, tx *sql.Tx) error {
	if err := b.held(); err != nil {
		return err
	}
	_, err := b.owner.store.CheckPreparedContinuationTx(ctx, tx, b.claim, b.checkWorkerTx, b.checkCurrentTx)
	return err
}

// bindPendingTx belongs after the real legacy insert and before its COMMIT.
// Its exact scoped request ID is already immutable in operation.Binding.Queue;
// the store compares it to the actual stored legacy row. A failed bind never
// retargets the measurement to another session or generation.
func (b *discPlayBorrow) bindPendingTx(ctx context.Context, tx *sql.Tx, key playbackdisc.Key) (string, error) {
	if err := b.checkContinuationTx(ctx, tx); err != nil {
		return "", err
	}
	return b.measurement.BindPendingTx(ctx, tx, b.owner.store, key, b.checkContinuationTx)
}

// activateTx belongs to the final V1 accepting writer after it has inserted
// the REAL V1 row and repeated original queue/capacity/idempotency admission.
// It leaves the preparation claim unconsumed so metadata/subtitle projection
// can still repeat the live continuation proof in this SAME transaction.
func (b *discPlayBorrow) activateTx(ctx context.Context, tx *sql.Tx, result playbackdisc.PreparationAccepted) error {
	if err := b.checkContinuationTx(ctx, tx); err != nil {
		return err
	}
	if err := dbwork.RequireGatedWriteTx(tx); err != nil {
		return err
	}
	b.acceptMu.Lock()
	defer b.acceptMu.Unlock()
	if b.activationTx != nil {
		return playbackdisc.ErrCurrent
	}
	b.measurement.mu.Lock()
	snapshot := b.measurement.snapshot
	bound := b.measurement.bound && snapshot.ID == result.SnapshotID && snapshot.SessionID == result.Legacy.SessionID && snapshot.Generation == result.Legacy.Generation
	b.measurement.mu.Unlock()
	if !bound || result.SessionID == "" {
		return playbackdisc.ErrCurrent
	}
	if err := b.measurement.ActivateTx(ctx, tx, b.owner.store, result.Legacy, b.checkContinuationTx); err != nil {
		return err
	}
	b.activationTx, b.activated = tx, result
	return nil
}

// acceptTx is LAST in that accepting writer, after normal presentation,
// subtitle and queue publication. Neither a different writer nor a rolled-back
// activation can consume the claim. Every failure rolls back the outer writer;
// readers launch only after its COMMIT with independent active authority.
func (b *discPlayBorrow) acceptTx(ctx context.Context, tx *sql.Tx, result playbackdisc.PreparationAccepted) error {
	if err := b.checkContinuationTx(ctx, tx); err != nil {
		return err
	}
	b.acceptMu.Lock()
	exact := b.activationTx == tx && b.activated == result
	b.acceptMu.Unlock()
	if !exact {
		return playbackdisc.ErrCurrent
	}
	if _, err := b.owner.store.ResolveTx(ctx, tx, result.Legacy, result.SnapshotID, b.measurement.acceptingCheck(b.checkContinuationTx)); err != nil {
		return err
	}
	if err := b.owner.store.AcceptPreparationTx(ctx, tx, b.claim, result, b.checkWorkerTx, b.checkCurrentTx); err != nil {
		return err
	}
	return dbwork.AfterCommit(tx, func() {
		p := b.owner
		p.mu.Lock()
		if p.borrow == b && p.operation.Claim == b.claim && !b.released.Load() {
			p.accepted = true
		}
		p.mu.Unlock()
	})
}

// release must follow the outer commit or rollback. It closes the original
// preparation descriptor/process proof in either case. Accepted snapshots keep
// their independent normalized receipt references; an uncertain outcome never
// causes global capture retirement or another presentation's cleanup.
func (b *discPlayBorrow) release(ctx context.Context) error {
	if b == nil {
		return nil
	}
	if ctx == nil || dbwork.Snapshot(ctx) != nil {
		return disc.ErrProtocol
	}
	if !b.released.CompareAndSwap(false, true) {
		return nil
	}
	b.contextMu.Lock()
	contexts := b.contexts
	b.contexts = nil
	b.contextMu.Unlock()
	for _, cancel := range contexts {
		cancel()
	}
	p := b.owner
	p.mu.Lock()
	p.retiring = true
	if p.borrow == b {
		p.borrow = nil
	}
	p.mu.Unlock()
	p.custody.Unlock()
	return p.Close(ctx)
}
