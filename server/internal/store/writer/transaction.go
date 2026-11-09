package writer

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

var (
	ErrBusy            = errors.New("writer custody unavailable")
	ErrClosed          = errors.New("writer closed")
	ErrResolveRequired = errors.New("writer commit requires durable resolution")
	ErrOverrun         = errors.New("writer runtime safety hold ceiling exceeded")
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
	check(CommandID, Work) error
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
	mu                 sync.Mutex
	db                 *sql.DB
	config             sqliteconn.Config
	now                func() time.Time
	closed             bool
	ambiguous          bool
	fenced             bool
	physicalFaults     *physicalCloseFaults
	driverPanics       *driverPanicLog
	fileBinding        *scanFileBinding // nil for existing harness frames
	ownerObservation   atomic.Pointer[ownerWindowObservation]
	holdTargetOverruns atomic.Uint64 // bounded telemetry; never admission or authority
	keeper             atomic.Pointer[governor.CheckpointWriterService]
}

// These are the bundled driver's contextual interfaces. Interposing only Close
// must not replace cancellation, reset, validation or execution with fallbacks.
type writerDriverConn interface {
	driver.Conn
	driver.ConnPrepareContext
	driver.ConnBeginTx
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
	SQLiteDQS() (ddl, dml int, err error)
}

type physicalCloseFaults struct {
	mu     sync.Mutex
	failed bool
	err    error
	panics []terminalPanic
}

func (f *physicalCloseFaults) record(phase string, value any) error {
	err := errTerminalCleanup
	if panicErr, ok := value.(error); ok {
		err = errors.Join(err, panicErr)
	}
	f.mu.Lock()
	f.failed = true
	f.err = errors.Join(f.err, err)
	f.panics = append(f.panics, terminalPanic{phase, value})
	f.mu.Unlock()
	return err
}

func (f *physicalCloseFaults) take() ([]terminalPanic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	panics, err := f.panics, f.err
	f.panics, f.err = nil, nil // failed stays sticky; no replacement may open.
	return panics, err
}

func (f *physicalCloseFaults) recordError(err error) {
	f.mu.Lock()
	f.failed = true
	f.err = errors.Join(f.err, err)
	f.mu.Unlock()
}

type writerConnector struct {
	driver.Connector
	faults   *physicalCloseFaults
	panics   *driverPanicLog
	decorate func(driver.Conn) driver.Conn // private fault instrumentation only
	binding  *scanFileBinding
}

func (c *writerConnector) Connect(ctx context.Context) (out driver.Conn, err error) {
	var native *ownedPhysicalConn
	defer func() {
		if value := recover(); value != nil {
			err = c.panics.add("connect", value)
			if native != nil {
				err = errors.Join(err, native.Close())
			}
			out = nil
		}
	}()
	c.faults.mu.Lock()
	failed := c.faults.failed
	c.faults.mu.Unlock()
	if failed {
		return nil, ErrClosed
	}
	if c.binding != nil {
		if err := c.binding.check(); err != nil {
			return nil, err
		}
	}
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	scope := &driverSafety{log: c.panics}
	native = &ownedPhysicalConn{writerDriverConn: conn.(writerDriverConn), faults: c.faults, scope: scope}
	if c.binding != nil {
		if err := c.binding.attest(conn); err != nil {
			return nil, errors.Join(err, native.Close())
		}
	}
	var wrapped driver.Conn = native
	if c.decorate != nil {
		wrapped = c.decorate(native)
	}
	boundary := &physicalCloseBoundary{writerDriverConn: wrapped.(writerDriverConn), native: native, faults: c.faults}
	return &safeWriterConn{writerDriverConn: boundary, scope: scope}, nil
}

func (c *writerConnector) Close() (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = errors.Join(err, c.faults.record("connector close", value))
		}
	}()
	if closer, ok := c.Connector.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

type ownedPhysicalConn struct {
	writerDriverConn
	faults *physicalCloseFaults
	scope  *driverSafety
	once   sync.Once
	err    error
}

func (c *ownedPhysicalConn) Close() error {
	c.once.Do(func() {
		defer func() {
			if value := recover(); value != nil {
				c.err = errors.Join(c.err, c.faults.record("native close", value))
			}
		}()
		c.err = func() (err error) {
			defer func() { err = errors.Join(err, c.writerDriverConn.Close()) }()
			return c.scope.closeAll()
		}()
		if c.err != nil {
			c.faults.recordError(c.err)
		}
	})
	return c.err
}

type physicalCloseBoundary struct {
	writerDriverConn
	native *ownedPhysicalConn
	faults *physicalCloseFaults
}

func (c *physicalCloseBoundary) Close() (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = errors.Join(err, c.faults.record("physical close", value))
		}
		// Native release has independent deferred ownership, even if an
		// interposed close panics before or after delegating to it. Returning
		// an error rather than a panic lets database/sql finish finalClose's
		// accounting and DB.Close's opener-stop/connector-close tail.
		err = errors.Join(err, c.native.Close())
	}()
	return c.writerDriverConn.Close()
}

func (w *executor) openPool(connector driver.Connector, decorate func(driver.Conn) driver.Conn) {
	w.physicalFaults = &physicalCloseFaults{}
	w.driverPanics = &driverPanicLog{}
	w.db = sql.OpenDB(&writerConnector{Connector: connector, faults: w.physicalFaults, panics: w.driverPanics, decorate: decorate, binding: w.fileBinding})
	w.db.SetMaxOpenConns(w.config.Connections)
	w.db.SetMaxIdleConns(w.config.Connections)
}

// newEmptyExecutor is harness-only fresh-file startup. It refuses nonempty
// databases through the canonical bootstrap, never mutates an old layout and
// never exposes a driver handle. Restart opening is a separate receipt-aware
// adapter; it must not treat an empty result as proof of a previous commit.
func newEmptyExecutor(ctx context.Context, path string, cacheKiB int32) (*executor, error) {
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: cacheKiB, Synchronous: sqliteconn.Full}
	connector, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		return nil, err
	}
	w := &executor{config: cfg, now: time.Now}
	w.openPool(connector, nil)
	db := w.db
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
	return w, nil
}

// begin never waits for another writer. Custody validation precedes connection
// acquisition and BEGIN. No command-supplied synchronous/priority override is
// accepted. The recipe is private reviewed qualification data, not caller input.
func (w *executor) begin(ctx context.Context, id CommandID, r turnRecipe, c turnCustody) (tx *transaction, err error) {
	if !w.mu.TryLock() {
		return nil, ErrBusy
	}
	window := ownerWindow{commandID: id, setupEnteredAt: time.Now()}
	// ErrBusy leaves ownership with the private caller. Once the lock is taken,
	// this frame consumes custody, even when admission fails before SQL begins.
	transferred := false
	var t *transaction
	defer func() {
		if transferred {
			return
		}
		primary := recover()
		if t == nil {
			// Admission has no connection or transaction yet, but retirement
			// still needs the same panic-safe, fenced cleanup protocol.
			t = &transaction{writer: w, custody: c, ownerWindow: window}
		}
		t.primaryPanic = primary
		_, err = t.finish(rolledBack, true, err)
		if primary != nil {
			panic(primary)
		}
	}()
	if w.closed || w.fenced || w.ambiguous {
		if w.ambiguous {
			return nil, ErrResolveRequired
		}
		return nil, ErrClosed
	}
	if keeper := w.keeper.Load(); keeper != nil {
		if err := keeper.Check(); err != nil {
			return nil, err
		}
	}
	start := w.now()
	var clockHighWater time.Time
	var compositionClock *time.Time
	if governed, ok := c.(*governedTurn); ok {
		compositionClock = governed.clockHighWater
		if compositionClock != nil {
			clockHighWater = *compositionClock
		}
	}
	if err = observeTime(&clockHighWater, start); err != nil {
		return nil, err
	}
	if compositionClock != nil {
		*compositionClock = clockHighWater
	}
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
		err = c.check(id, r.limit)
	}
	if err != nil {
		return nil, err
	}
	deadline := start.Add(MaxCommandSafetyHold)
	if governed, ok := c.(*governedTurn); ok && governed.expected.Deadline.Before(deadline) {
		deadline = governed.expected.Deadline
	}
	turnCtx, cancel := context.WithDeadline(ctx, deadline)
	t = &transaction{writer: w, ctx: turnCtx, cancel: cancel, custody: c, quantum: q, start: start, clockHighWater: clockHighWater, compositionClock: compositionClock, limit: r.limit.Hold, workLimit: r.limit, descriptor: desc, ownerWindow: window}
	now := w.now()
	if err = t.observeTime(now); err != nil {
		return nil, err
	}
	if err = q.TryStep(r.setup, now); err != nil {
		return nil, err
	}
	if w.fileBinding != nil {
		if err = w.fileBinding.check(); err != nil {
			w.fenced = true
			return nil, err
		}
	}
	conn, err := w.db.Conn(turnCtx)
	t.conn = conn
	t.raiseDriverPanic(err)
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
		t.policyVerified = err == nil
	}
	if governed, ok := c.(*governedTurn); err == nil && ok && governed.inventoryEngine != (sqliteconn.EngineIdentity{}) {
		t.engineIdentity, err = attestInventoryDriverEngine(turnCtx, t, governed.inventoryEngine)
	}
	if err == nil {
		err = c.check(id, r.limit)
	}
	if err == nil {
		_, err = conn.ExecContext(turnCtx, "BEGIN IMMEDIATE")
	}
	t.raiseDriverPanic(err)
	if err != nil {
		return nil, err
	}
	t.begun = true
	t.setupComplete = true
	t.setupReturnedAt = time.Now()
	transferred = true
	return t, nil
}

type transaction struct {
	ownerWindow
	setupComplete  bool
	writer         *executor
	conn           *sql.Conn
	ctx            context.Context
	cancel         context.CancelFunc
	custody        turnCustody
	quantum        *Quantum
	descriptor     Descriptor
	settings       sqliteconn.Settings
	engineIdentity sqliteconn.EngineIdentity // same acquired native connection, before BEGIN
	policyVerified bool
	start          time.Time
	clockHighWater time.Time // same turn: setup, helper reads and statement/terminal admission
	limit          time.Duration
	workLimit      Work
	begun          bool
	done           bool
	// Set before entering the COMMIT driver: no panic/error cleanup may then
	// classify the outcome as rolled back or attempt an ordinary ROLLBACK.
	commitAttempted    bool
	rollbackReturned   bool
	commitElapsed      time.Duration
	commitStartedAt    time.Time
	commitReturnedAt   time.Time
	physicalReturnedAt time.Time
	result             turnResult
	finishErr          error
	// Saved before private SQL-context cleanup cancels ctx. A dispatcher keeps
	// an independent copy of the exact protected deadline through publication.
	lifetimeErr         error
	cleanupComplete     bool
	connectionReturned  bool
	retirementAttempted bool
	retirementComplete  bool
	fenceAttempted      bool
	poolCloseAttempted  bool
	primaryPanic        any
	cleanupPanics       []terminalPanic

	compositionClock  *time.Time                  // real runtime's same mark survives all admission failures
	mpPhysicalRelease *mpPhysicalRelease          // sealed native exit, checked again before COMMIT
	publicReading     *uoPublicReadingReservation // one closed canonical public-reading original
	birthStart        *uoBirthStartLookup         // one closed 700 original; never a callback registry
}

// observeTime records every trusted admission/helper sample, even a helper read
// that consumes no statement work. Compare both elapsed and wall ordering:
// persisted Unix timestamps must not regress during a wall-clock adjustment.
func (t *transaction) observeTime(now time.Time) error {
	floors := [3]time.Time{t.start}
	count := 1
	if t.quantum != nil {
		floors[1] = t.quantum.last
		count++
	}
	if t.compositionClock != nil {
		floors[count] = *t.compositionClock
		count++
	}
	if err := observeTime(&t.clockHighWater, now, floors[:count]...); err != nil {
		return err
	}
	if t.compositionClock != nil {
		*t.compositionClock = t.clockHighWater
	}
	return nil
}

// The same ordering rule covers pre-BEGIN admission and post-return WAL samples.
// Their composition-owned mark transfers into the owned turn, then its terminal
// samples fence the next WAL publication. It grants no work or clock authority.
func observeTime(highWater *time.Time, now time.Time, floors ...time.Time) error {
	if now.Before(*highWater) || now.Round(0).Before(highWater.Round(0)) {
		return ErrQuantum
	}
	for _, floor := range floors {
		if now.Before(floor) || now.Round(0).Before(floor.Round(0)) {
			return ErrQuantum
		}
	}
	*highWater = now
	return nil
}

func (t *transaction) clockTime() (time.Time, error) {
	now := t.writer.now()
	if err := t.observeTime(now); err != nil {
		return time.Time{}, err
	}
	return now, nil
}

// before is used before each next static statement/sub-batch. Domain SQL and
// result receipt effects must ALL be charged; no SQL executor escapes this type.
func (t *transaction) before(work Work) error {
	defer t.guardPanic()
	if t.done {
		return ErrClosed
	}
	if err := t.ctx.Err(); err != nil {
		return err
	}
	if err := t.custody.check(t.descriptor.ID, t.workLimit); err != nil {
		return err
	}
	now := t.writer.now()
	if err := t.observeTime(now); err != nil {
		return err
	}
	return t.quantum.TryStep(work, now)
}

type commitState uint8

const (
	rolledBack commitState = iota + 1
	committed
	commitUnknown
)

// turnResult reports physical transaction custody only, never an invented
// Applied receipt. A committed safety overrun stays committed and fences the driver;
// retrying its effects would be wrong even when an error accompanies it.
type turnResult struct {
	state    commitState
	settings sqliteconn.Settings
	hold     time.Duration
	commit   time.Duration
}

func (t *transaction) commit() (turnResult, error) {
	if t.done {
		return t.saved(nil)
	}
	defer t.guardPanic()
	t.enterTerminal()
	err := t.ctx.Err()
	if err == nil {
		err = t.custody.check(t.descriptor.ID, t.workLimit)
	}
	if err == nil {
		err = t.checkUOPublicReadingCommit()
	}
	if err == nil {
		err = t.checkUOBirthStartCommit()
	}
	if err == nil {
		err = t.checkPublicationCommit()
	}
	if err == nil {
		err = t.checkMPPhysicalCommit()
	}
	if err == nil {
		now := t.writer.now()
		err = t.observeTime(now)
		if err == nil {
			err = t.quantum.TryFinish(now)
		}
	}
	if err != nil {
		return t.rollback(err)
	}
	start := t.writer.now()
	if err := t.observeTime(start); err != nil {
		return t.rollback(err)
	}
	t.commitStartedAt = start
	t.commitAttempted = true
	_, err = t.conn.ExecContext(t.ctx, "COMMIT")
	t.raiseDriverPanic(err)
	t.commitReturnedAt = t.writer.now()
	t.commitElapsed = t.commitReturnedAt.Sub(start)
	if err != nil {
		return t.unknown(err)
	}
	t.begun = false
	return t.finish(committed, false, nil)
}

// guardPanic captures the primary panic BEFORE invoking any fallible cleanup.
// Cleanup records secondary panics without replacing the supervisor's primary.
func (t *transaction) guardPanic() {
	if primary := recover(); primary != nil {
		var secondary error
		if t.primaryPanic == nil {
			t.primaryPanic = primary
		} else if !t.done {
			t.cleanupPanics = append(t.cleanupPanics, terminalPanic{"unwinding rollback", primary})
			secondary = errTerminalCleanup
			if err, ok := primary.(error); ok {
				secondary = errors.Join(secondary, err)
			}
		}
		if !t.done {
			if t.commitAttempted {
				_, _ = t.unknown(secondary)
			} else {
				_, _ = t.finish(rolledBack, true, secondary)
			}
		}
		panic(t.primaryPanic)
	}
}

func (t *transaction) saved(cause error) (turnResult, error) {
	err := errors.Join(t.finishErr, cause)
	if !t.cleanupComplete {
		err = errors.Join(err, errTerminalCleanup)
	} else if err == nil {
		err = ErrClosed
	}
	return t.result, err
}

func (t *transaction) unknown(cause error) (turnResult, error) {
	t.writer.ambiguous = true
	return t.finish(commitUnknown, true, errors.Join(ErrResolveRequired, cause))
}

// rollback can be deferred directly by the private command driver. Capture an
// already-unwinding caller panic BEFORE invoking any fallible rollback work.
func (t *transaction) rollback(cause error) (turnResult, error) {
	if primary := recover(); primary != nil {
		if t.primaryPanic == nil {
			t.primaryPanic = primary
		}
		defer func() { panic(primary) }()
	}
	return t.rollbackOwned(cause)
}

func (t *transaction) rollbackOwned(cause error) (turnResult, error) {
	if t.done {
		return t.saved(cause)
	}
	defer t.guardPanic()
	t.enterTerminal()
	if t.commitAttempted {
		return t.unknown(cause)
	}
	discard := false
	if t.begun {
		// Producer cancellation must not suppress rollback. Native return/close,
		// not context expiry, is the physical retirement boundary.
		cleanup, cancel := context.WithTimeout(context.Background(), MaxCommandSafetyHold)
		defer cancel()
		t.rollbackStartedAt = time.Now()
		_, err := t.conn.ExecContext(cleanup, "ROLLBACK")
		t.rollbackReturnedAt = time.Now()
		cancel()
		if t.primaryPanic == nil {
			t.raiseDriverPanic(err)
		} else {
			for _, fault := range t.writer.driverPanics.take() {
				t.cleanupPanics = append(t.cleanupPanics, terminalPanic{fault.phase, fault.value})
			}
		}
		if err != nil {
			cause = errors.Join(cause, err)
			discard = true
		}
		t.rollbackReturned = err == nil
		t.begun = false
	}
	return t.finish(rolledBack, discard, cause)
}

var errTerminalCleanup = errors.New("writer terminal cleanup incomplete")

type terminalPanic struct {
	phase string
	value any
}

// cleanupCall contains only private fixed cleanup operations. Each panic is
// retained as a diagnostic value and, when it is an error, in the saved error.
func (t *transaction) cleanupCall(phase string, call func() error) (err error) {
	returned := false
	defer func() {
		if !returned {
			value := recover()
			t.cleanupPanics = append(t.cleanupPanics, terminalPanic{phase, value})
			err = errTerminalCleanup
			if panicErr, ok := value.(error); ok {
				err = errors.Join(err, panicErr)
			}
		}
		if t.writer.physicalFaults != nil {
			panics, closeErr := t.writer.physicalFaults.take()
			t.cleanupPanics = append(t.cleanupPanics, panics...)
			err = errors.Join(err, closeErr)
		}
		for _, fault := range t.writer.driverPanics.take() {
			t.cleanupPanics = append(t.cleanupPanics, terminalPanic{fault.phase, fault.value})
			err = errors.Join(err, errTerminalCleanup, fault)
		}
	}()
	err = call()
	returned = true
	return err
}

func (t *transaction) closePool() error {
	if t.poolCloseAttempted {
		return nil
	}
	t.poolCloseAttempted = true
	t.poolCloseStartedAt = time.Now()
	err := t.cleanupCall("quarantine pool", t.writer.db.Close)
	t.poolCloseReturnedAt = time.Now()
	if err == nil && t.conn != nil && t.connectionReturned {
		t.nativeReturnedAt = t.poolCloseReturnedAt
	} else {
		t.nativeReturnedAt = time.Time{}
	}
	return err
}

func (t *transaction) signalFence() error {
	if t.fenceAttempted || t.custody == nil {
		return nil
	}
	t.fenceAttempted = true
	return t.cleanupCall("fence", func() error { t.custody.fence(); return nil })
}

func (t *transaction) finish(state commitState, discard bool, cause error) (turnResult, error) {
	if t.done {
		return t.saved(cause)
	}
	t.enterTerminal()
	w := t.writer
	wasFenced := w.fenced
	// Terminal outcome and quarantine precede ALL callbacks. done means that
	// this outcome is frozen, not that release or trusted retirement finished.
	t.done = true
	t.result = turnResult{state: state, settings: t.settings, commit: t.commitElapsed}
	t.finishErr = cause
	w.fenced = true
	defer w.mu.Unlock()
	defer t.observeTerminalReturn()
	defer func() {
		if !t.cleanupComplete {
			// Also covers an unexpected unwind/Goexit inside private cleanup.
			w.fenced = true
			t.finishErr = errors.Join(t.finishErr, errTerminalCleanup, t.closePool())
		}
	}()
	failed := false
	add := func(err error) {
		if err != nil {
			failed = true
			t.finishErr = errors.Join(t.finishErr, err)
		}
	}
	if t.cancel != nil {
		if t.ctx != nil {
			t.lifetimeErr = t.ctx.Err()
		}
		add(t.cleanupCall("cancel", func() error { t.cancel(); return nil }))
	}
	t.connectionReturned = t.conn == nil
	if t.conn != nil {
		var discardErr error
		if discard {
			discardErr = t.cleanupCall("discard connection", func() error {
				err := t.conn.Raw(func(any) error { return driver.ErrBadConn })
				if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
					return nil
				}
				return err
			})
			add(discardErr)
		}
		closeErr := t.cleanupCall("return connection", func() error {
			if err := t.conn.Close(); !errors.Is(err, sql.ErrConnDone) {
				return err
			}
			return nil
		})
		add(closeErr)
		t.connectionReturned = discardErr == nil && closeErr == nil
		if t.connectionReturned {
			t.connectionReturnedAt = time.Now()
			t.nativeReturnedAt = t.connectionReturnedAt
		}
	}
	measure := func() error {
		if !t.start.IsZero() {
			t.result.hold = w.now().Sub(t.start)
			if t.conn != nil && t.connectionReturned && t.retirementComplete && t.result.hold >= 0 {
				t.physicalReturnedAt = t.start.Add(t.result.hold)
			}
		}
		return nil
	}
	add(t.cleanupCall("hold clock", measure))
	overrun := t.limit > 0 && (t.result.hold > MaxCommandSafetyHold || t.result.hold < 0)
	if overrun {
		t.finishErr = errors.Join(t.finishErr, ErrOverrun)
	}
	mustFence := wasFenced || state == commitUnknown || overrun
	if mustFence || failed {
		add(t.signalFence())
	}
	// A failed physical release NEVER authorizes trusted retirement. Otherwise
	// attempt it once; a partial refund or panic must not cause a second call.
	t.retirementComplete = t.custody == nil
	if t.connectionReturned && t.custody != nil {
		t.retirementAttempted = true
		t.custodyRetirementStartedAt = time.Now()
		err := t.cleanupCall("retire", t.custody.retired)
		t.custodyRetirementReturnedAt = time.Now()
		t.retirementComplete = err == nil
		if t.retirementComplete {
			t.custodyRetiredAt = t.custodyRetirementReturnedAt
		}
		add(err)
	}
	add(t.cleanupCall("complete hold clock", measure))
	if t.limit > 0 && (t.result.hold > MaxCommandSafetyHold || t.result.hold < 0) {
		mustFence = true
		t.finishErr = errors.Join(t.finishErr, ErrOverrun)
	}
	if mustFence || failed || !t.retirementComplete || !t.connectionReturned {
		add(t.signalFence())
		// The successful path can keep its idle connection. A failed retirement
		// closes the private pool while the mutex is still held, so that idle
		// connection can never be borrowed by another writer turn.
		add(t.closePool())
	}
	add(t.cleanupCall("final hold clock", measure))
	if t.limit > 0 && (t.result.hold > MaxCommandSafetyHold || t.result.hold < 0) {
		mustFence = true
		t.finishErr = errors.Join(t.finishErr, ErrOverrun)
	}
	// A late clock/fence callback failure cannot leave an idle connection in
	// the pool. closePool/signalFence remain individually at-most-once.
	if (mustFence || failed || !t.retirementComplete || !t.connectionReturned) && !t.poolCloseAttempted {
		add(t.signalFence())
		add(t.closePool())
		add(t.cleanupCall("quarantined hold clock", measure))
	}
	w.fenced = mustFence || failed || !t.retirementComplete || !t.connectionReturned
	if t.limit > 0 && t.result.hold > t.limit {
		w.holdTargetOverruns.Add(1)
	}
	t.cleanupComplete = true
	if t.primaryPanic == nil && len(t.cleanupPanics) != 0 {
		panic(t.cleanupPanics[0].value)
	}
	return t.result, t.finishErr
}

func (w *executor) close() error {
	if keeper := w.keeper.Load(); keeper != nil {
		keeper.Stop() // deny new admission even if the old turn is still busy
	}
	if !w.mu.TryLock() {
		return ErrBusy
	}
	defer w.mu.Unlock()
	w.closed = true
	err := w.db.Close()
	if keeper := w.keeper.Load(); keeper != nil {
		keeper.Stop()
		w.physicalFaults.mu.Lock()
		failed := w.physicalFaults.failed
		w.physicalFaults.mu.Unlock()
		stats := w.db.Stats()
		if err != nil || failed || stats.InUse != 0 || stats.OpenConnections != 0 {
			return errors.Join(err, ErrResolveRequired)
		}
		err = keeper.Retired()
	}
	return err
}
