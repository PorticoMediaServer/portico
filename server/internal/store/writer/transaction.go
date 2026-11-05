package writer

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

var (
	ErrBusy            = errors.New("writer custody unavailable")
	ErrClosed          = errors.New("writer closed")
	ErrResolveRequired = errors.New("writer commit requires durable resolution")
	ErrOverrun         = errors.New("writer complete hold exceeded admitted limit")
)

// ResultState is the domain receipt vocabulary. A transaction commit by itself
// does NOT construct Applied: only a closed domain command with its immutable
// receipt may do that. Zero is deliberately not a successful result.
type ResultState uint8

const (
	Pending ResultState = iota + 1
	Applied
	Superseded
	ResultRetired
	ResolveRequired
)

// turnCustody is a private driver seam, not another governor contract or worker
// grant. The W governor adapter must check the exact v2 request/Grant and retire
// its physical holding here. No caller can register this through a public API.
type turnCustody interface {
	check() error
	retired() error
	fence()
}

type turnRecipe struct {
	limit    Work // complete recipe, including terminal return and receipt effects
	setup    Work // acquire/assert/select durability/BEGIN
	terminal Work // worst COMMIT or rollback plus physical connection return
}

// executor is private infrastructure; no SQL, callback, connection or writable
// transaction is exported. Closed domain adapters and HN statements will use
// this frame after INT's exact receipt DDL and R controls land.
type executor struct {
	mu        sync.Mutex
	db        *sql.DB
	config    sqliteconn.Config
	now       func() time.Time
	closed    bool
	ambiguous bool
	fenced    bool
}

// newEmptyExecutor is harness-only fresh-file startup. It refuses nonempty
// databases through the canonical bootstrap, never mutates an old layout and
// never exposes a driver handle. Restart opening is a separate receipt-aware
// adapter; it must not treat an empty result as proof of a previous commit.
func newEmptyExecutor(ctx context.Context, path string, cacheKiB int32) (*executor, error) {
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: cacheKiB, Synchronous: sqliteconn.Full}
	db, _, err := sqliteconn.Open(ctx, path, cfg)
	if err != nil {
		return nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err = sqliteconn.Assert(ctx, conn, cfg); err == nil {
		err = schema.InitializeEmpty(ctx, conn)
	}
	err = errors.Join(err, conn.Close())
	if err != nil {
		db.Close()
		return nil, err
	}
	return &executor{db: db, config: cfg, now: time.Now}, nil
}

// begin never waits for another writer. Custody validation precedes connection
// acquisition and BEGIN. No command-supplied synchronous/priority override is
// accepted. The recipe is private reviewed qualification data, not caller input.
func (w *executor) begin(ctx context.Context, id CommandID, r turnRecipe, c turnCustody) (tx *transaction, err error) {
	if !w.mu.TryLock() {
		return nil, ErrBusy
	}
	// ErrBusy leaves ownership with the private caller. Once the lock is taken,
	// this frame consumes custody, even when admission fails before SQL begins.
	transferred := false
	var t *transaction
	defer func() {
		if !transferred {
			if t != nil {
				// BEGIN may have executed before a driver reply panics. Until
				// this function returns a transaction, cleanup remains ours.
				_, err = t.finish(rolledBack, true, err)
				return
			}
			defer w.mu.Unlock()
			if c != nil {
				if retireErr := c.retired(); retireErr != nil {
					w.fenced = true
					c.fence()
					err = errors.Join(err, retireErr)
				}
			}
		}
	}()
	if w.closed || w.fenced || w.ambiguous {
		if w.ambiguous {
			return nil, ErrResolveRequired
		}
		return nil, ErrClosed
	}
	start := w.now()
	q, err := NewQuantum(r.limit, r.terminal, start)
	if err != nil {
		return nil, err
	}
	desc, err := Describe(id)
	if err != nil || c == nil {
		if err != nil {
			return nil, err
		}
		return nil, ErrInvalid
	}
	if err = ctx.Err(); err == nil {
		err = c.check()
	}
	if err != nil {
		return nil, err
	}
	if err = q.TryStep(r.setup, w.now()); err != nil {
		return nil, err
	}
	turnCtx, cancel := context.WithDeadline(ctx, start.Add(r.limit.Hold))
	t = &transaction{writer: w, ctx: turnCtx, cancel: cancel, custody: c, quantum: q, start: start, limit: r.limit.Hold, descriptor: desc}
	conn, err := w.db.Conn(turnCtx)
	t.conn = conn
	if err != nil {
		return nil, err
	}
	cfg := w.config
	cfg.Synchronous = sqliteconn.Normal
	pragma := "PRAGMA synchronous=NORMAL"
	if desc.Durability == Full {
		cfg.Synchronous = sqliteconn.Full
		pragma = "PRAGMA synchronous=FULL"
	}
	if _, err = conn.ExecContext(turnCtx, pragma); err == nil {
		t.settings, err = sqliteconn.Assert(turnCtx, conn, cfg)
	}
	if err == nil {
		err = c.check()
	}
	if err == nil {
		_, err = conn.ExecContext(turnCtx, "BEGIN IMMEDIATE")
	}
	if err != nil {
		return nil, err
	}
	t.begun = true
	transferred = true
	return t, nil
}

type transaction struct {
	writer     *executor
	conn       *sql.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	custody    turnCustody
	quantum    *Quantum
	descriptor Descriptor
	settings   sqliteconn.Settings
	start      time.Time
	limit      time.Duration
	begun      bool
	done       bool
	// Set before entering the COMMIT driver: no panic/error cleanup may then
	// classify the outcome as rolled back or attempt an ordinary ROLLBACK.
	commitAttempted bool
	commitElapsed   time.Duration
	result          turnResult
	finishErr       error
}

// before is used before each next static statement/sub-batch. Domain SQL and
// result receipt effects must ALL be charged; no SQL executor escapes this type.
func (t *transaction) before(work Work) error {
	if t.done {
		return ErrClosed
	}
	if err := t.ctx.Err(); err != nil {
		return err
	}
	if err := t.custody.check(); err != nil {
		return err
	}
	return t.quantum.TryStep(work, t.writer.now())
}

type commitState uint8

const (
	rolledBack commitState = iota + 1
	committed
	commitUnknown
)

// turnResult reports physical transaction custody only, never an invented
// Applied receipt. A committed overrun stays committed and fences the driver;
// retrying its effects would be wrong even when an error accompanies it.
type turnResult struct {
	state    commitState
	settings sqliteconn.Settings
	hold     time.Duration
	commit   time.Duration
}

func (t *transaction) commit() (turnResult, error) {
	if t.done {
		if t.result.state == commitUnknown {
			return t.result, t.finishErr
		}
		return turnResult{}, ErrClosed
	}
	err := t.ctx.Err()
	if err == nil {
		err = t.custody.check()
	}
	if err == nil {
		err = t.quantum.TryFinish(t.writer.now())
	}
	if err != nil {
		return t.rollback(err)
	}
	start := t.writer.now()
	t.commitAttempted = true
	defer func() {
		if !t.done {
			// Preserve the original panic for the supervisor after physically
			// joining cleanup. No caller rollback defer is required.
			t.commitElapsed = t.writer.now().Sub(start)
			_, _ = t.unknown(nil)
		}
	}()
	_, err = t.conn.ExecContext(t.ctx, "COMMIT")
	t.commitElapsed = t.writer.now().Sub(start)
	if err != nil {
		return t.unknown(err)
	}

	t.begun = false
	return t.finish(committed, false, nil)
}

func (t *transaction) unknown(cause error) (turnResult, error) {
	t.writer.ambiguous = true
	return t.finish(commitUnknown, true, errors.Join(ErrResolveRequired, cause))
}

func (t *transaction) rollback(cause error) (turnResult, error) {
	if t.done {
		if t.result.state == commitUnknown {
			return t.result, errors.Join(t.finishErr, cause)
		}
		return turnResult{}, ErrClosed
	}
	if t.commitAttempted {
		return t.unknown(cause)
	}
	discard := false
	if t.begun {
		// Producer cancellation must not suppress rollback. Native return/close,
		// not context expiry, is the physical retirement boundary.
		cleanup, cancel := context.WithTimeout(context.Background(), MaxCommandHold)
		_, err := t.conn.ExecContext(cleanup, "ROLLBACK")
		cancel()
		if err != nil {
			cause = errors.Join(cause, err)
			discard = true
		}
		t.begun = false
	}
	return t.finish(rolledBack, discard, cause)
}

func (t *transaction) finish(state commitState, discard bool, cause error) (turnResult, error) {
	w := t.writer
	defer w.mu.Unlock()
	t.done = true
	t.cancel()
	if t.conn != nil {
		if discard {
			// ErrBadConn forces physical Close, including any uncertain SQLite
			// transaction. Returning this handle to the pool would be unsafe.
			err := t.conn.Raw(func(any) error { return driver.ErrBadConn })
			if !errors.Is(err, driver.ErrBadConn) && !errors.Is(err, sql.ErrConnDone) {
				cause = errors.Join(cause, err)
			}
		}
		if err := t.conn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			cause = errors.Join(cause, err)
		}
	}
	result := turnResult{state: state, settings: t.settings, hold: w.now().Sub(t.start), commit: t.commitElapsed}
	if result.hold > t.limit || result.hold < 0 {
		w.fenced = true
		t.custody.fence()
		cause = errors.Join(cause, ErrOverrun)
	}
	// The driver cannot attempt a new turn until physical return AND trusted
	// retirement finish. WAL observation/re-admission follows outside this frame.
	if err := t.custody.retired(); err != nil {
		w.fenced = true
		t.custody.fence()
		cause = errors.Join(cause, err)
	}
	t.result, t.finishErr = result, cause
	return result, cause
}

func (w *executor) close() error {
	if !w.mu.TryLock() {
		return ErrBusy
	}
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.db.Close()
}
