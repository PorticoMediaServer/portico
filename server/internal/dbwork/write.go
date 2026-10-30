package dbwork

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// gate is the process-wide write gate. One database file means one writer, so
// the gate is a package variable rather than a value threaded through every
// constructor in the server; a test that needs isolation uses Reset.
var gate = NewGate()

// Live gated transactions are also addressed by their exact SQL handle for
// accepting callbacks whose caller still passes the original request context.
var liveWrites sync.Map // *sql.Tx -> *Write

type durableUserKey struct{}

// WithDurableUser keeps the caller's admission class and gives its transaction
// the same FULL commit durability as an authority fence. It is used for a user
// change whose receipt must survive together with the change.
func WithDurableUser(ctx context.Context) context.Context {
	return context.WithValue(ctx, durableUserKey{}, true)
}

// WriteGate exposes the gate for diagnostics and for exclusive maintenance that
// must not start while any writer is active or queued.
func WriteGate() *Gate { return gate }

// publications counts committed gated write transactions, including an
// autocommit ExecWrite statement that changed rows.
//
// It is the batch-granular publication fence the read-path audit asks for, in
// the only form that is both correct and free: one gated transaction is one
// batch, so this moves once per batch rather than four times per catalogue row
// the way `library_revisions` does. An in-memory cache can compare it and know
// that *nothing at all* has been written since it stored an entry, without
// asking the database — and that is the only claim it makes. It is deliberately
// not a substitute for a revision: it is process-wide rather than per library,
// and it is not durable, so a cache still has to carry the exact revision it was
// built against for anything it would otherwise serve stale.
var publications atomic.Uint64

// Publications is the number of gated write transactions this process has
// committed. It only ever increases.
func Publications() uint64 { return publications.Load() }

// Write is one gated write transaction. Commit and Rollback are idempotent and
// both release the gate, so the ordinary `defer w.Rollback()` beside an explicit
// `w.Commit()` is correct and releases exactly once.
//
// A Write begun while its context already holds a gated transaction reuses that
// transaction: Tx returns the outer handle, Commit is a no-op the outer owner
// will perform, and Rollback marks the outer transaction aborted so it cannot
// commit work an inner writer rejected.
type Write struct {
	// changesAtStart is SQLite's row-change counter when this transaction opened.
	// A commit that has not moved it wrote nothing, and a commit that wrote
	// nothing must not wake every background loop in the server.
	changesAtStart int64
	changes        *changeSet
	readOnly       bool
	tx             *sql.Tx
	ctx            context.Context
	// conn is set for security fences and durable user writes, which run on a
	// connection of its own so that synchronous=FULL applies to it and to
	// nothing else. See beginDurable.
	conn         *sql.Conn
	release      func()
	parent       *carried
	owned        *carried
	once         sync.Once
	done         bool
	mu           sync.Mutex
	afterCommit  []func()
	beforeCommit []func() error
	// plan, meter and site measure this transaction against its WritePlan
	// once it commits; see plan.go.
	plan          WritePlan
	meter         *writeMeter
	site          callSite
	accountingWAL AccountingWALPhase
}

// Begin acquires the write gate for class and opens a transaction. It is the
// low-level form; prefer WithWriteTx, which cannot leak a gate hold.
//
// The order is always gate, then BEGIN. Opening the transaction first would let
// SQLite arbitrate the contention the gate exists to arbitrate, and the loser
// would learn about it as SQLITE_BUSY rather than as a place in a queue.
func Begin(ctx context.Context, db *sql.DB, class Class) (*Write, error) {
	countCall()
	if db == nil {
		return nil, errors.New("dbwork: nil database")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if parent := carriedWrite(ctx); parent != nil {
		if strict() {
			return nil, ErrNested
		}
		parent.mu.Lock()
		parent.depth++
		if bearsAuthority(class) {
			parent.authority = true
		}
		parent.mu.Unlock()
		return &Write{tx: parent.tx, ctx: ctx, parent: parent}, nil
	}
	site := captureSite(2)
	ctx = walContext(ctx, db, class, "")
	release, err := acquireCatalogWrite(ctx, db, class)
	if err != nil {
		return nil, err
	}
	changes := &changeSet{}
	meter := &writeMeter{}
	ctx = context.WithValue(context.WithValue(ctx, changeKey{}, changes), meterKey{}, meter)
	var (
		tx   *sql.Tx
		conn *sql.Conn
	)
	if durable, _ := ctx.Value(durableUserKey{}).(bool); class == ClassSecurityFence || durable {
		conn, tx, err = beginDurable(ctx, writerHandle(db))
	} else {
		tx, err = writerHandle(db).BeginTx(ctx, nil)
	}
	if err != nil {
		release()
		return nil, err
	}
	held := &carried{tx: tx, class: class}
	w := &Write{changes: changes, tx: tx, conn: conn, ctx: context.WithValue(ctx, writeKey{}, held), release: release, owned: held, plan: planFor(ctx, class), meter: meter, site: site}
	liveWrites.Store(tx, w)
	w.changesAtStart = rowChanges(ctx, tx)
	return w, nil
}

// Security fences and receipted user changes must survive together with their
// acceptance. Losing the last commit is not merely
// inconvenient. `synchronous=NORMAL` in WAL mode is the right default for
// other work — it never corrupts the database, and the worst a power cut
// can do is lose the tail of the log — but "the tail of the log" is exactly
// where a revocation lives. A sign-in invalidated, a device removed, a
// restriction published: losing one of those means a principal stays authorised
// after the server said it had stopped being, which is the one failure this
// class exists to prevent.
//
// Such a transaction runs on a connection of its own with FULL durability,
// and the setting is restored before that connection goes back to the pool. The
// pragma cannot be set inside a transaction, and setting it through the pool
// would land it on whichever connection the pool handed over and leave it there
// for whoever got that connection next — the pooled-pragma mistake this
// codebase has already paid for once.
//
// The cost is one fsync per durable write, against the gate it already holds.
func beginDurable(ctx context.Context, db *sql.DB) (*sql.Conn, *sql.Tx, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	if _, err = conn.ExecContext(ctx, `PRAGMA synchronous=FULL`); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		releaseDurable(ctx, conn)
		return nil, nil, err
	}
	return conn, tx, nil
}

// releaseDurable puts the connection back the way it was found. A connection
// whose setting cannot be restored is destroyed rather than returned: a pooled
// connection silently running FULL would make every later write on it slower
// for no stated reason, which is the same class of bug as leaving foreign keys
// off.
func releaseDurable(ctx context.Context, conn *sql.Conn) {
	if conn == nil {
		return
	}
	restore := ctx
	if restore == nil || restore.Err() != nil {
		restore = context.Background()
	}
	if _, err := conn.ExecContext(restore, `PRAGMA synchronous=NORMAL`); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = conn.Close()
}

// rowChanges reads SQLite's own count of rows this connection has changed. It is
// how a commit that changed nothing is told apart from one that did — which
// matters because a transaction that wrote nothing must not wake every
// background loop in the server. It is one call into an in-process library, and
// a transaction that cannot answer reports zero, which errs towards notifying.
func rowChanges(ctx context.Context, tx *sql.Tx) int64 {
	var total int64
	if tx == nil {
		return 0
	}
	if err := tx.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&total); err != nil {
		return 0
	}
	return total
}

// Tx is the live transaction handle. It is nil-safe so a call site that ignored
// the Begin error behaves the way it did before the gate existed.
func (w *Write) Tx() *sql.Tx {
	if w == nil {
		return nil
	}
	return w.tx
}

// Context carries this transaction, so a helper called with it reuses the
// transaction rather than opening a second one behind the same gate.
func (w *Write) Context() context.Context {
	if w == nil || w.ctx == nil {
		return context.Background()
	}
	return w.ctx
}

// Commit ends the transaction and releases the gate. Invalidation, notification
// and cache work belong strictly after this call returns: the gate must never be
// held across work the database does not need.
func (w *Write) Commit() error {
	if w == nil {
		return errors.New("dbwork: commit on a transaction that never opened")
	}
	w.mu.Lock()
	if w.done {
		w.mu.Unlock()
		return nil
	}
	w.done = true
	w.mu.Unlock()
	if w.parent != nil {
		w.parent.mu.Lock()
		w.parent.depth--
		w.parent.mu.Unlock()
		if w.parent.failed() {
			return ErrAborted
		}
		return nil
	}
	defer w.finish()
	if w.owned != nil && w.owned.failed() {
		// A helper that joined this transaction rolled back. Committing now would
		// make durable exactly the work it refused.
		_ = w.tx.Rollback()
		w.finish()
		return ErrAborted
	}
	for _, check := range w.beforeCommit {
		if err := check(); err != nil {
			_ = w.tx.Rollback()
			return err
		}
	}
	changesAtEnd := rowChanges(w.ctx, w.tx)
	changed := changesAtEnd != w.changesAtStart
	if w.readOnly && changed {
		_ = w.tx.Rollback()
		w.finish()
		return ErrSnapshotWrite
	}
	err := w.tx.Commit()
	if err == nil {
		// Retirement must happen after durable acceptance and before the gate
		// admits another sender. A rollback must never retire live authority.
		w.mu.Lock()
		callbacks := w.afterCommit
		w.afterCommit = nil
		w.mu.Unlock()
		for _, callback := range callbacks {
			callback()
		}
	}
	if err == nil && !w.readOnly {
		publications.Add(1)
	}
	w.finish()
	if err == nil && w.owned != nil {
		w.owned.mu.Lock()
		joined := w.owned.authority
		w.owned.mu.Unlock()
		if joined {
			BumpAuthority()
		} else {
			noteCommitted(w.owned.class)
		}
	}
	if err == nil && changed {
		noteCommitProgress(w.ctx)
		noteCommitSite()
		// Something actually changed. Every background loop that was waiting to be
		// told hears it now, instead of asking four times a second whether anything
		// had. A transaction that wrote nothing — the shape a "nothing to do" pass
		// has — says nothing, which is what keeps an idle server idle. See commit.go.
		notifyCommit(w.changes)
	}
	if err == nil && w.meter != nil {
		// Last, with the gate already released: nothing here can hold a writer.
		cost, measured := w.meter.cost(max(changesAtEnd-w.changesAtStart, 0))
		w.mu.Lock()
		plan, accounting := w.plan, w.accountingWAL
		w.mu.Unlock()
		if accounting != 0 {
			noteAccountingWAL(accounting, accountingWALSample{bytes: w.meter.walBytesWritten, frames: w.meter.walFrames, pages: cost.Pages, writtenKnown: w.meter.walWrittenKnown, framesKnown: w.meter.walFramesKnown, pagesKnown: measured && cost.Pages >= 0})
		}
		notePlan(w.owned.class, plan, cost, measured, w.site)
	}
	return err
}

// Rollback discards the transaction and releases the gate. It is a no-op after
// Commit, which is what makes the deferred form safe.
func (w *Write) Rollback() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	if w.done {
		w.mu.Unlock()
		return nil
	}
	w.done = true
	w.mu.Unlock()
	if w.parent != nil {
		w.parent.mu.Lock()
		w.parent.depth--
		w.parent.mu.Unlock()
		w.parent.abort()
		return nil
	}
	err := w.tx.Rollback()
	w.finish()
	return err
}

func (w *Write) finish() {
	w.once.Do(func() {
		if w.parent == nil {
			liveWrites.Delete(w.tx)
		}
		// The connection goes back before the gate does, so the next writer never
		// finds the pool one connection short.
		releaseDurable(w.ctx, w.conn)
		if w.release != nil {
			w.release()
		}
	})
}

// AfterCommit binds a bounded in-memory retirement to this exact accepting
// transaction. It runs only after a successful commit, before writer release.
// Callbacks must not perform IO, acquire the write gate, or start other work.
// An untracked or already-finished transaction is refused, never guessed.
func AfterCommit(tx *sql.Tx, callback func()) error {
	value, ok := liveWrites.Load(tx)
	if !ok || callback == nil {
		return errors.New("dbwork: callback requires a live gated transaction")
	}
	w := value.(*Write)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return errors.New("dbwork: callback transaction already finished")
	}
	w.afterCommit = append(w.afterCommit, callback)
	return nil
}

// BeforeCommit checks an invariant on this transaction immediately before its
// commit. The callback may read this transaction only; it must not acquire the
// gate, perform external work, or register more callbacks. A failed check rolls
// back the complete transaction, including its domain effects.
func BeforeCommit(tx *sql.Tx, check func() error) error {
	value, ok := liveWrites.Load(tx)
	if !ok || check == nil {
		return errors.New("dbwork: check requires a live gated transaction")
	}
	w := value.(*Write)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return errors.New("dbwork: check transaction already finished")
	}
	w.beforeCommit = append(w.beforeCommit, check)
	return nil
}

// WithWriteTx runs fn inside one gated transaction of the given class. This is
// the sanctioned way to write: the gate is acquired, the transaction opens, fn
// runs, the transaction commits, the gate releases, and only then does the
// caller's own follow-up work run.
//
// If ctx already holds a gated transaction, fn joins it. There is no nesting and
// no second gate acquisition, which is what makes a helper safe to call from
// both a handler and the middle of a larger transaction.
func WithWriteTx(ctx context.Context, db *sql.DB, class Class, fn func(*sql.Tx) error) error {
	return WithWriteTxContext(ctx, db, class, func(_ context.Context, tx *sql.Tx) error { return fn(tx) })
}

// WithWriteTxContext is WithWriteTx with the transaction-carrying context handed
// to fn, so anything fn calls reuses the same transaction.
func WithWriteTxContext(ctx context.Context, db *sql.DB, class Class, fn func(context.Context, *sql.Tx) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if parent := carriedWrite(ctx); parent != nil {
		if err := fn(ctx, parent.tx); err != nil {
			parent.abort()
			return err
		}
		return nil
	}
	// One admission is one attempt. The gate has already resolved same-process
	// contention, so a lock error here is an outside opener or a defect, and
	// running fn again would repeat the whole wait instead of reporting it.
	w, err := Begin(ctx, db, class)
	if err != nil {
		return noteAdmittedFailure(class, err)
	}
	defer w.Rollback()
	if err = fn(w.Context(), w.Tx()); err != nil {
		return noteAdmittedFailure(class, err)
	}
	return noteAdmittedFailure(class, w.Commit())
}

// busyLogged rate-limits the log line for a lock error after admission. The
// count is exact; the log only has to say that it is happening.
var busyLogged atomic.Int64

// noteAdmittedFailure counts and reports SQLITE_BUSY or SQLITE_LOCKED met by a
// write that held the gate, and returns err unchanged for the caller to answer.
// Callers see it as Overloaded, so a request is refused as retryable.
func noteAdmittedFailure(class Class, err error) error {
	if err == nil || !Retryable(err) {
		return err
	}
	gate.noteBusyAfterAdmission(class)
	now := time.Now().UnixNano()
	if last := busyLogged.Load(); now-last >= int64(time.Minute) && busyLogged.CompareAndSwap(last, now) {
		log.Printf("dbwork: a %s write met a database lock after taking the write gate (%d so far): %v", class, gate.busy.Load(), err)
	}
	return err
}

// ExecWrite runs one statement through the gate, in one attempt. The gate is
// released before the caller sees the result, so a caller that
// invalidates a cache or publishes an event afterwards does so with no writer
// hold outstanding.
func ExecWrite(ctx context.Context, db *sql.DB, class Class, query string, args ...any) (sql.Result, error) {
	countCall()
	if ctx == nil {
		ctx = context.Background()
	}
	if parent := carriedWrite(ctx); parent != nil {
		if bearsAuthority(class) {
			parent.mu.Lock()
			parent.authority = true
			parent.mu.Unlock()
		}
		return parent.tx.ExecContext(ctx, query, args...)
	}
	site := captureSite(2)
	ctx = walContext(ctx, db, class, query)
	release, err := acquireCatalogWrite(ctx, db, class)
	if err != nil {
		return nil, err
	}
	conn, err := writerHandle(db).Conn(ctx)
	if err != nil {
		release()
		return nil, err
	}
	meter := &writeMeter{}
	meter.wal, _ = ctx.Value(walRequestKey{}).(*walRequest)
	_ = conn.Raw(func(inner any) error { meter.begin(inner); return nil })
	changes := &changeSet{}
	writeCtx := context.WithValue(ctx, changeKey{}, changes)
	var result sql.Result
	var before, after int64
	if err = conn.QueryRowContext(writeCtx, `SELECT total_changes()`).Scan(&before); err == nil {
		// Autocommit has no BeginTx hook to attach this set to the observed
		// connection. Record this statement's supplied write root.
		changes.note(query)
		result, err = conn.ExecContext(writeCtx, query, args...)
	}
	if err == nil {
		if countErr := conn.QueryRowContext(writeCtx, `SELECT total_changes()`).Scan(&after); countErr != nil {
			// The statement already committed. Conservatively wake subscribers;
			// running it again could execute a non-idempotent write twice.
			after, changes = before+1, nil
		}
	}
	_ = conn.Raw(func(inner any) error { meter.end(inner); return nil })
	_ = conn.Close()
	release()
	if err != nil {
		return result, noteAdmittedFailure(class, err)
	}
	cost, measured := meter.cost(max(after-before, 0))
	defer notePlan(class, planFor(ctx, class), cost, measured, site)
	noteCommitted(class)
	if after != before {
		// An autocommit statement that changed rows is a committed gated
		// write like any transaction: caches and response validators keyed on
		// Publications must see it (PERF-12).
		publications.Add(1)
		noteCommitProgress(ctx)
		noteCommitSite()
		notifyCommit(changes)
		// Lane C's direct-write subscribers (event feed) also hear autocommit
		// ExecWrite statements.
		notifyDirectWrite(changes)
	}
	return result, nil
}

// ErrSnapshotWrite prevents accidental row mutations from committing through a reader.
var ErrSnapshotWrite = errors.New("dbwork: write attempted through read snapshot")

// BeginSnapshot opens a read-only transaction in the same shape as Begin, for
// call sites whose transaction only reads. It takes no gate: a WAL reader does
// not compete with the writer, so making it queue would invent contention.
func BeginSnapshot(ctx context.Context, db *sql.DB) (*Write, error) {
	countCall()
	if db == nil {
		return nil, errors.New("dbwork: nil database")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if parent := carriedWrite(ctx); parent != nil {
		// The caller is already inside a gated write. Reading through it is both
		// cheaper and more correct than opening a second snapshot that could not
		// see this request's own uncommitted rows.
		parent.mu.Lock()
		parent.depth++
		parent.mu.Unlock()
		return &Write{tx: parent.tx, ctx: ctx, parent: parent}, nil
	}
	if tx := Snapshot(ctx); tx != nil {
		return &Write{tx: tx, ctx: ctx, parent: &carried{tx: tx}}, nil
	}
	ctx, cancel := snapshotContext(ctx)
	tx, err := ReadHandle(ctx, db).BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		cancel()
		return nil, err
	}
	return &Write{tx: tx, ctx: withSnapshot(ctx, tx), readOnly: true, release: cancel, changesAtStart: rowChanges(ctx, tx)}, nil
}

type writerReservationKey struct{}
type writerReservation struct{ conn *sql.Conn }

// ReserveWriterConn holds the write gate before reserving the dedicated writer
// connection. The caller can set connection-local pragmas before BeginConn and
// restore them before release without another writer inheriting them.
func ReserveWriterConn(ctx context.Context, db *sql.DB, class Class) (context.Context, *sql.Conn, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		return nil, nil, nil, errors.New("dbwork: nil database")
	}
	releaseGate, err := acquireCatalogWrite(ctx, db, class)
	if err != nil {
		return nil, nil, nil, err
	}
	conn, err := writerHandle(db).Conn(ctx)
	if err != nil {
		releaseGate()
		return nil, nil, nil, err
	}
	var once sync.Once
	release := func() { once.Do(func() { _ = conn.Close(); releaseGate() }) }
	ctx = context.WithValue(ctx, reservedWALKey{}, ledgerFor(db))
	return context.WithValue(ctx, writerReservationKey{}, writerReservation{conn: conn}), conn, release, nil
}

// BeginConn starts a transaction on a reserved writer connection. An arbitrary
// read-pool connection is rejected because writing through it can starve every
// writer when foreground reads fill that pool.
func BeginConn(ctx context.Context, conn *sql.Conn, class Class) (*Write, error) {
	countCall()
	if conn == nil {
		return nil, errors.New("dbwork: nil connection")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reserved, ok := ctx.Value(writerReservationKey{}).(writerReservation)
	if !ok || reserved.conn != conn {
		return nil, errors.New("dbwork: BeginConn requires a reserved writer connection")
	}
	site := captureSite(2)
	// The connection reservation may perform many independent transactions.
	// Each BEGIN has its own debit; holding the gate is not a reservation.
	if d, ok := ctx.Value(reservedWALKey{}).(*walLedger); ok {
		ctx = walContextForLedger(ctx, d, class, "")
		r, _ := ctx.Value(walRequestKey{}).(*walRequest)
		if !r.ready() {
			return nil, ErrWriterCapacity
		}
		r.take()
	}
	changes := &changeSet{}
	meter := &writeMeter{}
	ctx = context.WithValue(context.WithValue(ctx, changeKey{}, changes), meterKey{}, meter)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	held := &carried{tx: tx, class: class}
	w := &Write{tx: tx, changes: changes, ctx: context.WithValue(ctx, writeKey{}, held), owned: held, changesAtStart: rowChanges(ctx, tx), plan: planFor(ctx, class), meter: meter, site: site}
	liveWrites.Store(tx, w)
	return w, nil
}
