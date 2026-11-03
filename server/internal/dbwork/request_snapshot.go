package dbwork

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"time"
)

// A page is composed from several read snapshots in a row: a title page opens
// three, a facet page seven. Each one takes a snapshot permit and a pooled
// connection, and runs BEGIN and COMMIT; under load each is also one more turn
// in the pool's queue, so a request that needs three waits three times.
//
// A request snapshot lets a request keep its read snapshot between those calls
// and hand it to the next one, but only while nothing has committed anywhere
// since it opened. A snapshot nothing has changed under shows exactly what a
// new one would, so reusing it changes no answer: a GET that writes and then
// reads again, or waits for a worker's write and then reads, sees that write,
// because the commit ends reuse.
//
// What reuse does cost is holding the connection between calls, so it is held
// only briefly: the snapshot closes once no call has used it for
// requestSnapshotIdle, when the request ends, or once it is an eighth of its
// lifetime old. A second call made while the first is still running (two goroutines of
// one request) opens its own snapshot, as before. Only foreground snapshots
// take part; background work and owner-long reads keep their own.
// A reused snapshot has less of its lifetime left than a new one would; reusing
// it only in its first 250 ms keeps at least seven eighths for the next call.
const requestSnapshotMaxReuse = ForegroundSnapshotLifetime / 8

var requestSnapshotIdle = 5 * time.Millisecond

// dataEpoch counts commits that changed the database, in any connection of
// this process. A request snapshot is reused only while it is unchanged.
var dataEpoch atomic.Uint64

func noteDataChanged() { dataEpoch.Add(1) }

var requestSnapshotReuses atomic.Uint64

// RequestSnapshotReuses counts read snapshots a request reused instead of
// opening another.
func RequestSnapshotReuses() uint64 { return requestSnapshotReuses.Load() }

type requestSnapshotKey struct{}

type requestSnapshot struct {
	base context.Context

	mu       sync.Mutex
	tx       *sql.Tx
	db       *sql.DB
	ctx      context.Context // the snapshot's own lifetime
	finish   func()
	epoch    uint64
	opened   time.Time
	busy     bool
	ended    bool
	idle     *time.Timer
	idleTurn uint64
}

// WithRequestSnapshot lets the read snapshots of the request ctx belongs to
// reuse one another. HTTP admission calls it once per request; end closes any
// snapshot still held and must run when the request finishes.
func WithRequestSnapshot(ctx context.Context) (context.Context, func()) {
	if ctx == nil {
		return ctx, func() {}
	}
	h := &requestSnapshot{base: ctx}
	return context.WithValue(ctx, requestSnapshotKey{}, h), h.end
}

func requestSnapshotFrom(ctx context.Context) *requestSnapshot {
	if ctx == nil {
		return nil
	}
	h, _ := ctx.Value(requestSnapshotKey{}).(*requestSnapshot)
	return h
}

// reuse runs fn in the request's held snapshot, opening one if none is held.
// handled is false when the caller should open a private snapshot instead.
func (h *requestSnapshot) reuse(ctx context.Context, db *sql.DB, fn func(context.Context) error) (handled bool, err error) {
	// A cancelled call cannot read, as when it would begin its own snapshot.
	if err := ctx.Err(); err != nil {
		return true, err
	}
	h.mu.Lock()
	if h.ended || h.busy {
		h.mu.Unlock()
		return false, nil
	}
	if h.tx != nil && (h.db != db || dataEpoch.Load() != h.epoch || time.Since(h.opened) > requestSnapshotMaxReuse || h.ctx.Err() != nil || h.base.Err() != nil) {
		h.closeLocked()
	}
	h.busy = true
	h.idleTurn++
	tx, snapshotCtx := h.tx, h.ctx
	h.mu.Unlock()
	if tx != nil {
		requestSnapshotReuses.Add(1)
	} else {
		if tx, snapshotCtx, err = h.open(ctx, db); err != nil {
			h.release(false)
			return true, err
		}
	}
	// The caller's own deadline still applies, and so does the snapshot's.
	runCtx, cancel := ctx, context.CancelFunc(func() {})
	if deadline, ok := snapshotCtx.Deadline(); ok {
		runCtx, cancel = context.WithDeadline(ctx, deadline)
	}
	err = fn(withSnapshot(runCtx, tx))
	cancel()
	h.release(err != nil)
	if err != nil {
		return true, snapshotError(snapshotCtx, snapshotError(runCtx, err))
	}
	return true, nil
}

// open begins the request's snapshot. Its life is its own: the snapshot
// lifetime, closed when idle, or when the request ends once no call is using
// it. It is not bound to the call that opened it, whose shorter deadline would
// roll back a snapshot the next call uses, nor to the request's cancellation,
// which would end it under work the request started and left running. Each
// call's statements still run under that call's own context.
func (h *requestSnapshot) open(ctx context.Context, db *sql.DB) (*sql.Tx, context.Context, error) {
	epoch := dataEpoch.Load()
	releasePermit, err := acquireSnapshot(ctx)
	if err != nil {
		return nil, nil, err
	}
	snapshotCtx, cancel := snapshotContext(context.WithoutCancel(h.base))
	tx, err := ReadHandle(ctx, db).BeginTx(snapshotCtx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		cancel()
		releasePermit()
		return nil, nil, err
	}
	forget := registerReadSnapshot(tx)
	h.mu.Lock()
	h.tx, h.db, h.ctx, h.epoch, h.opened = tx, db, snapshotCtx, epoch, time.Now()
	h.finish = func() {
		forget()
		_ = tx.Rollback()
		cancel()
		releasePermit()
	}
	h.mu.Unlock()
	return tx, snapshotCtx, nil
}

// release ends one call's use. A failed call's snapshot is not reused: the
// failure may have been the snapshot's own deadline.
func (h *requestSnapshot) release(failed bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.busy = false
	if h.tx == nil {
		return
	}
	if failed || h.ended {
		h.closeLocked()
		return
	}
	turn := h.idleTurn
	if h.idle == nil {
		h.idle = time.AfterFunc(requestSnapshotIdle, func() { h.closeIdle(turn) })
		return
	}
	h.idle.Stop()
	h.idle = time.AfterFunc(requestSnapshotIdle, func() { h.closeIdle(turn) })
}

func (h *requestSnapshot) closeIdle(turn uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.busy && h.idleTurn == turn {
		h.closeLocked()
	}
}

func (h *requestSnapshot) closeLocked() {
	if h.idle != nil {
		h.idle.Stop()
		h.idle = nil
	}
	if h.tx == nil {
		return
	}
	finish := h.finish
	h.tx, h.db, h.ctx, h.finish = nil, nil, nil, nil
	finish()
}

func (h *requestSnapshot) end() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ended = true
	if !h.busy {
		h.closeLocked()
	}
}
