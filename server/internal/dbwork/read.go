package dbwork

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Reads never touch the write gate. In WAL mode a reader does not block the
// writer and the writer does not block a reader, so gating reads would add
// serialisation SQLite does not require. Read admission belongs at the HTTP
// layer, where it can be per-lane and can answer a caller honestly with 503.

// Query runs a read. If ctx carries a snapshot or a gated write transaction the
// read joins it, so a projection built from several queries cannot see two
// different states and a request cannot fail to see its own uncommitted write.
func Query(ctx context.Context, db *sql.DB, query string, args ...any) (*sql.Rows, error) {
	countCall()
	if tx := Snapshot(ctx); tx != nil {
		return tx.QueryContext(ctx, query, args...)
	}
	if db == nil {
		return nil, errors.New("dbwork: nil database")
	}
	return ReadHandle(ctx, db).QueryContext(ctx, query, args...)
}

// QueryRow is Query for a single row.
func QueryRow(ctx context.Context, db *sql.DB, query string, args ...any) *sql.Row {
	countCall()
	if tx := Snapshot(ctx); tx != nil {
		return tx.QueryRowContext(ctx, query, args...)
	}
	return ReadHandle(ctx, db).QueryRowContext(ctx, query, args...)
}

// WithReadSnapshot runs fn inside a read-only WAL transaction carried in the
// context. One snapshot is what makes offset-based paging and multi-row
// projections self-consistent; it pins one pooled connection and one WAL frame
// boundary, and it never blocks the writer.
//
// A snapshot is bounded by the request deadline on purpose. It is cheap, but a
// long-lived one holds back WAL checkpointing, so it must not outlive the
// response it exists for.
func WithReadSnapshot(ctx context.Context, db *sql.DB, fn func(context.Context) error) error {
	countCall()
	if ctx == nil {
		ctx = context.Background()
	}
	if Snapshot(ctx) != nil {
		// Already inside one: joining costs no second connection and so needs no
		// second permit, and it does not renew the parent's lifetime.
		return fn(ctx)
	}
	if !longRead(ctx) {
		// The owner-long read has its own single permit; it must not hold the
		// background snapshot permit for minutes. Waiting for a permit is
		// bounded by the caller's deadline; the snapshot's own lifetime starts
		// once it has one.
		release, err := acquireSnapshot(ctx)
		if err != nil {
			return err
		}
		defer release()
	}
	ctx, cancel := snapshotContext(ctx)
	defer cancel()
	tx, err := ReadHandle(ctx, db).BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(withSnapshot(ctx, tx)); err != nil {
		return snapshotError(ctx, err)
	}
	return snapshotError(ctx, tx.Commit())
}

// snapshotError reports a deadline as a deadline.
//
// `database/sql` rolls a transaction back from a watchdog goroutine as soon as
// its context is done, so the next statement on it fails with `ErrTxDone`
// rather than with the deadline that actually ended it. Reported as-is that
// reaches a caller as "the request was malformed", which is both untrue and
// unactionable; reported as the context's own error it becomes the 503 with a
// Retry-After that it is.
func snapshotError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	// A long read ended by a WAL recovery reports that refusal, whatever the
	// interrupted statement said.
	if cause := context.Cause(ctx); errors.Is(cause, ErrReadPressure) {
		return fmt.Errorf("%w: %w", ErrReadPressure, err)
	}
	if cause := ctx.Err(); cause != nil && errors.Is(err, sql.ErrTxDone) {
		return cause
	}
	return err
}

// ReadSnapshotLifetime bounds waiting for a pooled read connection on a path
// that then detaches its own transaction lifetime. Snapshots themselves are
// bounded by snapshot_lifetime.go: a read snapshot pins one WAL frame boundary,
// and a PASSIVE checkpoint silently does nothing while any reader holds a
// snapshot older than the log head.
const ReadSnapshotLifetime = 30 * time.Second

// BeginRead opens a read-only transaction for call sites that still hold their
// own handle. The returned close is idempotent and safe to call from another
// goroutine than the one that opened it.
func BeginRead(ctx context.Context, db *sql.DB) (*sql.Tx, func(), error) {
	countCall()
	if ctx == nil {
		ctx = context.Background()
	}
	// The snapshot lifetime bounds this read even when the caller's deadline
	// is later, or absent.
	ctx, cancel := snapshotContext(ctx)
	tx, err := ReadHandle(ctx, db).BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		cancel()
		return nil, nil, err
	}
	var once sync.Once
	return tx, func() {
		once.Do(func() {
			_ = tx.Rollback()
			cancel()
		})
	}, nil
}

// RawConn unwraps an instrumented driver connection. `sql.Conn.Raw` hands out
// whatever the driver returned, and this package returns a wrapper, so a caller
// reaching for a driver-specific facility — the online backup API, for one —
// asks for the connection underneath rather than type-asserting the wrapper and
// quietly finding nothing.
func RawConn(raw any) any {
	if wrapper, ok := raw.(interface{ Unwrap() driver.Conn }); ok {
		return wrapper.Unwrap()
	}
	return raw
}

// A read snapshot pins one pooled connection for as long as it is open. That is
// the point — a page composed from one connection and one WAL frame boundary
// cannot see two different states — but it makes the number of simultaneous
// snapshots a pool-sized resource, and an unbounded number of them is not a
// queue at the edge, it is a queue inside `database/sql` where nobody can see
// it.
//
// Before the writer had a dedicated connection, snapshots could also starve it.
// The separate writer pool removes that coupling; snapshot admission now keeps
// room in each read pool for single-statement work.
//
// So snapshots are admitted, with headroom left for writers and for the short
// single-statement reads that do not open one. A caller that cannot get a permit
// waits at a place its request deadline reaches, rather than inside the pool.
const snapshotReserve = 2

var foregroundSnapshotPermits = make(chan struct{}, max(1, safeMaxOpenConns-snapshotReserve))
var backgroundSnapshotPermits = make(chan struct{}, 1)

var snapshotWaiting atomic.Int64
var snapshotHeld atomic.Int64
var snapshotWaitNanos atomic.Int64

// SnapshotStats reports read-snapshot admission.
type SnapshotStats struct {
	Held       int64 `json:"held"`
	Capacity   int   `json:"capacity"`
	Waiting    int64 `json:"waiting"`
	WaitMillis int64 `json:"waitMillis"`
}

// Snapshots snapshots read-snapshot admission counters.
func Snapshots() SnapshotStats {
	return SnapshotStats{
		Held:       snapshotHeld.Load(),
		Capacity:   cap(foregroundSnapshotPermits) + cap(backgroundSnapshotPermits),
		Waiting:    snapshotWaiting.Load(),
		WaitMillis: snapshotWaitNanos.Load() / int64(time.Millisecond),
	}
}

func acquireSnapshot(ctx context.Context) (func(), error) {
	permits := foregroundSnapshotPermits
	if ClassFrom(ctx, ClassInteractive).Background() {
		permits = backgroundSnapshotPermits
	}
	select {
	case permits <- struct{}{}:
		snapshotHeld.Add(1)
		return func() { releaseSnapshot(permits) }, nil
	default:
	}
	start := time.Now()
	snapshotWaiting.Add(1)
	defer func() {
		snapshotWaiting.Add(-1)
		snapshotWaitNanos.Add(int64(time.Since(start)))
	}()
	select {
	case permits <- struct{}{}:
		snapshotHeld.Add(1)
		return func() { releaseSnapshot(permits) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func releaseSnapshot(permits chan struct{}) {
	snapshotHeld.Add(-1)
	select {
	case <-permits:
	default:
	}
}
