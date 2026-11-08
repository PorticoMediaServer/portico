package writer

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/stateownership"
	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
)

// ScanDriver is the sole internal scan principal for one native opening. There
// is no public constructor, raw connection, scope accessor, callback or budget
// override. Trusted startup supplies the already-owned Writer and observer.
// Copying the value cannot copy authority. A new opening is a new lease epoch.
type ScanDriver struct {
	self          *ScanDriver
	mu            sync.Mutex
	opening       *scanOpening
	owner         *stateownership.Lock
	ownership     *stateownership.Lease
	writer        *Writer
	observer      nativeinventory.Observer
	lifetime      context.Context
	cancel        context.CancelFunc
	stopOpening   func() bool
	closed        atomic.Bool
	released      bool
	work          *scanDriverWork // ONE original, not a queue or resource ledger
	birth         *scanBirth
	retention     *scanOwnerRetention
	preparation   *scanPreparation // attached BEFORE exposing/starting the native read
	leaseOwner    int64
	leaseDuration time.Duration
	batchPolicy   inventoryBatchPolicy
	readyPlan     scanPreparationPlan
	lease         *ScanRunLease
	predecessor   *scanReturnedWork
	operation     *ScanRunOperation
}

func newScanDriver(ctx context.Context, opening *scanOpening, owner *stateownership.Lock, w *Writer, observer nativeinventory.Observer) (out *ScanDriver, err error) {
	lifetime := ctx
	var candidate *ScanDriver
	// The final check may discard the capability. Cleanup observes that final
	// decision, unregisters only an unpublished candidate and closes its borrow.
	defer func() {
		out, err = finishCommand(ctx, lifetime, out, err)
		if out == nil && candidate != nil {
			candidate.closed.Store(true)
			candidate.cancel()
			candidate.stopOpening()
			err = errors.Join(err, candidate.ownership.Close())
			opening.mu.Lock()
			if opening.driver == candidate {
				opening.driver = nil
			}
			opening.mu.Unlock()
		}
	}()
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if ctx == nil || opening == nil || owner == nil || w == nil || w.effective == nil || opening.binding == nil || opening.executor == nil || opening.lifetime == nil ||
		w.effective.executor != opening.executor || w.effective.path != opening.binding.path {
		return nil, ErrInvalid
	}
	if err = errors.Join(ctx.Err(), opening.binding.check()); err != nil {
		return nil, err
	}
	if err = w.effective.authority.VerifyComposition(w.effective.governor, w.effective.wal); err != nil {
		return nil, err
	}
	if !w.effective.mu.TryLock() {
		return nil, ErrBusy
	}
	defer w.effective.mu.Unlock()
	lease, err := owner.Borrow()
	if err != nil {
		return nil, err
	}
	path, pathErr := lease.LockPath()
	want, wantErr := opening.ownership.LockPath()
	if pathErr != nil || wantErr != nil || path != want {
		return nil, errors.Join(ErrInvalid, pathErr, wantErr, lease.Close())
	}
	d := &ScanDriver{opening: opening, owner: owner, ownership: lease, writer: w, observer: observer}
	d.self = d
	d.lifetime, d.cancel = context.WithCancel(ctx)
	d.stopOpening = context.AfterFunc(opening.lifetime, d.cancel)
	candidate, lifetime = d, d.lifetime
	if w.effective.registry != nil {
		d.retention, err = scanOwnerRetentionFor(opening.executor, w.effective.registry)
		if err != nil {
			return nil, err
		}
	}
	opening.mu.Lock()
	defer opening.mu.Unlock()
	if opening.closed || opening.driver != nil {
		return nil, ErrBusy
	}
	opening.driver = d
	if err = d.current(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *ScanDriver) current() error {
	if d == nil || d.self != d || d.opening == nil || d.opening.binding == nil || d.writer == nil || d.writer.effective == nil || d.ownership == nil || d.lifetime == nil {
		return ErrInvalid
	}
	if d.closed.Load() {
		return ErrClosed
	}
	if err := errors.Join(d.lifetime.Err(), d.opening.binding.check(), d.ownership.Check()); err != nil {
		return errors.Join(ErrClosed, err)
	}
	return nil
}

// Close revokes first. Native helpers, frames and grants must be physically
// retired before the driver drops its native borrow. Unknown receipt state is
// not rewritten into rollback or fresh authority; restart resolves it normally.
func (d *ScanDriver) Close(ctx context.Context) error {
	if d == nil {
		return nil
	}
	if d.self != d || ctx == nil {
		return ErrInvalid
	}
	d.closed.Store(true)
	d.cancel()
	if !d.mu.TryLock() {
		return ErrBusy
	}
	defer d.mu.Unlock()
	if d.released {
		return nil
	}
	// Revoke observation before any wait, but retain its memory while a native
	// command can still use the frozen input. Cancellation is not native return.
	if d.preparation != nil {
		d.preparation.revoke()
	}
	if d.work != nil {
		if d.work.preparation != d.preparation {
			return ErrInvalid
		}
		if !d.work.nativeReturned() {
			return ErrResolveRequired
		}
	}
	if d.birth != nil && d.birth.preparation != nil && d.birth.preparation != d.preparation {
		return ErrInvalid
	}
	// No native frame can use these command/input roots now. Drop them BEFORE
	// preparation retirement refunds memory. The preparation itself clears its
	// owned request and observed page before that refund, even if another owner
	// still has a closed handle. A failed helper join keeps d.preparation charged.
	d.work, d.birth = nil, nil
	if d.preparation != nil {
		if err := d.preparation.Close(ctx); err != nil {
			return err
		}
		d.preparation = nil
	}
	if err := d.ownership.Close(); err != nil {
		return err
	}
	d.stopOpening()
	d.released = true
	d.observer, d.retention = nativeinventory.Observer{}, nil
	if d.lease != nil {
		d.lease.revoked.Store(true)
		d.lease = nil
	}
	d.readyPlan = scanPreparationPlan{}
	d.predecessor = nil
	d.leaseOwner, d.leaseDuration = 0, 0
	// Only closed issuer/opening identity remains on the permanent tombstone;
	// no preparation, birth command or frame roots survive their memory refund.
	return nil
}

// Construction's caller owns p on failure. Once attached, the driver also
// retains it through Close, even before there is a command or submitted work.
// This is ONE lifetime slot, not an extra grant, queue or accounting ledger.
func (d *ScanDriver) keepPreparation(ctx context.Context, p *scanPreparation) (err error) {
	lifetime := ctx
	if d != nil && d.self == d {
		lifetime = d.lifetime
	}
	defer func() { _, err = finishCommand(ctx, lifetime, struct{}{}, err) }()
	if err = d.current(); err != nil {
		return err
	}
	if p == nil || p.self != p || p.opening != d.opening || p.runtime != d.writer.effective {
		return ErrInvalid
	}
	if !d.mu.TryLock() {
		return ErrBusy
	}
	defer d.mu.Unlock()
	if err = d.current(); err != nil {
		return err
	}
	if d.preparation == p {
		return p.current()
	}
	if d.preparation != nil || d.work != nil {
		return ErrBusy
	}
	if err = p.current(); err != nil {
		return err
	}
	d.preparation = p
	return nil
}
