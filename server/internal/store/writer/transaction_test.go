package writer

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

type testCustody struct {
	checks, returns, fences int
	checkErr, returnErr     error
	closed                  *atomic.Bool
	retireHook              func()
	checkHook               func()
}

func (c *testCustody) check(CommandID, Work) error {
	c.checks++
	if c.checkHook != nil {
		c.checkHook()
	}
	return c.checkErr
}
func (c *testCustody) retired() error {
	c.returns++
	if c.retireHook != nil {
		c.retireHook()
	}
	if c.closed != nil && !c.closed.Load() {
		return errors.New("retired before physical close")
	}
	return c.returnErr
}
func (c *testCustody) fence() { c.fences++ }

func testExecutor(t *testing.T, decorate ...func(driver.Conn) driver.Conn) (*executor, string, *time.Time) {
	t.Helper()
	w, path := testCanonicalExecutor(t, decorate...)
	// Control protocol transitions with a virtual clock. This is not latency
	// qualification on a shared CI VM; actual SQL/cancellation is tested below.
	now := time.Now().Add(time.Hour)
	w.now = func() time.Time { return now }
	if _, err := w.db.ExecContext(t.Context(), "INSERT INTO search_revision(singleton,revision) VALUES(1,1)"); err != nil {
		t.Fatal(err)
	}
	return w, path, &now
}

func testRecipe() turnRecipe {
	return turnRecipe{limit: Work{100, 10000, 100, 100000, 20 * time.Millisecond}, setup: Work{10, 100, 10, 100, 2 * time.Millisecond}, terminal: Work{10, 100, 10, 100, 2 * time.Millisecond}}
}

func mutateFixture(t *testing.T, tx *transaction) {
	t.Helper()
	if err := tx.before(Work{1, 100, 1, 100, time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	// Synthetic single-row effect on the canonical layout. This is NOT a
	// registered domain command or an Applied receipt/scan/search acceptance.
	if _, err := tx.conn.ExecContext(tx.ctx, "UPDATE search_revision SET revision=revision+1 WHERE singleton=1"); err != nil {
		t.Fatal(err)
	}
}

func readRevision(t *testing.T, path string) int {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sqliteconn.Assert(t.Context(), conn, cfg); err != nil {
		t.Fatal(err)
	}
	var revision int
	if err := conn.QueryRowContext(t.Context(), "SELECT revision FROM search_revision WHERE singleton=1").Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func TestTransactionPrivateCustodyVisibilityAndDurability(t *testing.T) {
	w, path, _ := testExecutor(t)
	for _, id := range []CommandID{ProgressAccept, SecurityAccept, ProgressAccept} {
		c := &testCustody{}
		tx, err := w.begin(t.Context(), id, testRecipe(), c)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.rollback(nil)
		want := 1
		if id == SecurityAccept {
			want = 2
		}
		if tx.settings.Synchronous != want || tx.settings.ForeignKeys != 1 || tx.settings.TempStore != 1 {
			t.Fatal(tx.settings)
		}
		old := readRevision(t, path)
		mutateFixture(t, tx)
		if got := readRevision(t, path); got != old {
			t.Fatal("uncommitted source became visible", got)
		}
		busy := &testCustody{}
		if _, err := w.begin(t.Context(), id, testRecipe(), busy); !errors.Is(err, ErrBusy) {
			t.Fatal(err)
		}
		if busy.returns != 0 {
			t.Fatal("busy frame took custody")
		}
		if err := w.close(); !errors.Is(err, ErrBusy) {
			t.Fatal(err)
		}
		result, err := tx.commit()
		if err != nil || result.state != committed {
			t.Fatal(result, err)
		}
		if got := readRevision(t, path); got != old+1 {
			t.Fatal(got)
		}
		if c.checks < 3 || c.returns != 1 || c.fences != 0 {
			t.Fatal(c)
		}
		if _, err := tx.commit(); !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
		if err := tx.before(Work{Hold: time.Nanosecond}); !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	}
	if w.db.Stats().OpenConnections != 1 {
		t.Fatal(w.db.Stats())
	}
}

func TestTransactionCancellationAndRevocationRollback(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "revoke"}[revoke], func(t *testing.T) {
			w, path, _ := testExecutor(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			c := &testCustody{}
			tx, err := w.begin(ctx, ProgressAccept, testRecipe(), c)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.rollback(nil)
			mutateFixture(t, tx)
			if revoke {
				c.checkErr = ErrInvalid
			} else {
				cancel()
			}
			result, err := tx.commit()
			if err == nil || result.state != rolledBack {
				t.Fatal(result, err)
			}
			if got := readRevision(t, path); got != 1 {
				t.Fatal("rollback lost", got)
			}
			if c.returns != 1 {
				t.Fatal(c)
			}
		})
	}
}

type faultConnector struct {
	connectObserved func(driver.Conn, time.Time, time.Time, error)
	observeNative   func(driver.Conn) error // test-only read-only attestation; success returns actual conn unchanged
	driver.Connector
	beforeConnect     func() error
	afterConnect      func() error
	afterCommit       bool
	closed            atomic.Bool
	closes            atomic.Int64
	commits           atomic.Int64
	rollbacks         atomic.Int64
	physical          atomic.Pointer[faultConn]
	replyError        bool
	afterNativeCommit func()
	beforeNative      func(string, context.Context)
	afterNative       func(string, context.Context)
	beforeClose       func()
	afterClose        func()
	nativeCloses      atomic.Int64
	nativeClosed      atomic.Bool
}

func (c *faultConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if c.beforeConnect != nil {
		if err := c.beforeConnect(); err != nil {
			return nil, err
		}
	}
	var started time.Time
	if c.connectObserved != nil {
		started = time.Now()
	}
	conn, err := c.Connector.Connect(ctx)
	if c.connectObserved != nil {
		c.connectObserved(conn, started, time.Now(), err)
	}
	if c.afterConnect != nil {
		if restoreErr := c.afterConnect(); restoreErr != nil {
			if conn != nil {
				restoreErr = errors.Join(restoreErr, conn.Close())
			}
			return nil, errors.Join(err, restoreErr)
		}
	}
	if err != nil {
		return nil, err
	}
	if c.observeNative != nil {
		if observationErr := c.observeNative(conn); observationErr != nil {
			// Return a refusing handle to the existing snapshot owner so it retains
			// native custody through its real Close, including any Close failure.
			return &unattestedTestConn{testDriverConn: conn.(testDriverConn), err: observationErr}, nil
		}
		return conn, nil
	}
	return c.wrap(conn), nil
}

type unattestedTestConn struct {
	testDriverConn
	err error
}

func (c *unattestedTestConn) SQLiteDQS() (int, int, error) { return 0, 0, c.err }
func (c *unattestedTestConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, c.err
}
func (c *unattestedTestConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return nil, c.err
}
func (c *unattestedTestConn) ReadBudgetSupported() bool {
	native, ok := c.testDriverConn.(interface{ ReadBudgetSupported() bool })
	return ok && native.ReadBudgetSupported()
}
func (c *unattestedTestConn) SQLiteLimit(id, value int) (int, error) {
	native, ok := c.testDriverConn.(interface{ SQLiteLimit(int, int) (int, error) })
	if !ok {
		return 0, c.err
	}
	return native.SQLiteLimit(id, value)
}

func (c *faultConn) SQLiteMainFileIdentity() (uint64, uint64, error) {
	getter, ok := c.testDriverConn.(interface {
		SQLiteMainFileIdentity() (uint64, uint64, error)
	})
	if !ok {
		return 0, 0, ErrInvalid
	}
	return getter.SQLiteMainFileIdentity()
}

func (c *faultConnector) wrap(conn driver.Conn) driver.Conn {
	wrapped := &faultConn{testDriverConn: conn.(testDriverConn), owner: c}
	c.physical.Store(wrapped)
	return wrapped
}

// Preserve the bundled driver's contextual operations when interposing faults.
type testDriverConn interface {
	SQLiteDQS() (ddl, dml int, err error)
	driver.Conn
	driver.ConnPrepareContext
	driver.ConnBeginTx
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}

type faultConn struct {
	testDriverConn
	owner *faultConnector
}

var errCommitReply = errors.New("injected commit response failure")

func (c *faultConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if q == "COMMIT" {
		c.owner.commits.Add(1)
	}
	if q == "ROLLBACK" {
		c.owner.rollbacks.Add(1)
	}
	if c.owner.beforeNative != nil {
		c.owner.beforeNative(q, ctx)
	}
	if q == "COMMIT" && c.owner.replyError && !c.owner.afterCommit {
		return nil, errCommitReply
	}
	var result driver.Result
	var err error
	if exec, ok := any(c.testDriverConn).(driver.ExecerContext); ok {
		result, err = exec.ExecContext(ctx, q, args)
	} else {
		values := make([]driver.Value, len(args))
		for i, arg := range args {
			values[i] = arg.Value
		}
		result, err = any(c.testDriverConn).(driver.Execer).Exec(q, values)
	}
	if err == nil && c.owner.afterNative != nil {
		c.owner.afterNative(q, ctx)
	}
	if q == "COMMIT" && err == nil {
		if c.owner.afterNativeCommit != nil {
			c.owner.afterNativeCommit()
		}
		if c.owner.replyError {
			return result, errCommitReply
		}
	}
	return result, err
}
func (c *faultConn) Close() error {
	if c.owner.beforeClose != nil {
		c.owner.beforeClose()
	}
	err := c.testDriverConn.Close()
	c.owner.closes.Add(1)
	c.owner.closed.Store(true)
	if c.owner.afterClose != nil {
		c.owner.afterClose()
	}
	return err
}

type trackedNativeConnector struct {
	driver.Connector
	owner *faultConnector
}

func (c trackedNativeConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &trackedNativeConn{testDriverConn: conn.(testDriverConn), owner: c.owner}, nil
}

type trackedNativeConn struct {
	testDriverConn
	owner *faultConnector
}

func (c *trackedNativeConn) Close() error {
	c.owner.nativeCloses.Add(1)
	err := c.testDriverConn.Close()
	if err == nil {
		c.owner.nativeClosed.Store(true)
	}
	return err
}

func installFaultPool(w *executor, fault *faultConnector) {
	fault.Connector = trackedNativeConnector{Connector: fault.Connector, owner: fault}
	// The held-source overlay lacks the new private physical-close boundary.
	// Both branches inject identical faults; old source uses its original pool.
	if guarded, ok := any(w).(interface {
		openPool(driver.Connector, func(driver.Conn) driver.Conn)
	}); ok {
		guarded.openPool(fault.Connector, fault.wrap)
		return
	}
	w.db = sql.OpenDB(fault)
	w.db.SetMaxOpenConns(1)
	w.db.SetMaxIdleConns(1)
}

func TestTransactionAmbiguousCommitClosesBeforeRetirementAndBlocksNextTurn(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_commit", true: "after_commit"}[after], func(t *testing.T) {
			w, path, _ := testExecutor(t)
			if err := w.db.Close(); err != nil {
				t.Fatal(err)
			}
			base, err := sqliteconn.Connector(path, w.config)
			if err != nil {
				t.Fatal(err)
			}
			fault := &faultConnector{Connector: base, afterCommit: after, replyError: true}
			installFaultPool(w, fault)
			c := &testCustody{closed: &fault.closed}
			tx, err := w.begin(t.Context(), ProgressAccept, testRecipe(), c)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.rollback(nil)
			mutateFixture(t, tx)
			result, err := tx.commit()
			if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCommitReply) {
				t.Fatal(result, err)
			}
			if !fault.closed.Load() || c.returns != 1 {
				t.Fatal("physical close/retirement missing", c)
			}
			want := 1
			if after {
				want = 2
			}
			if got := readRevision(t, path); got != want {
				t.Fatalf("SQLite state %d want%d", got, want)
			}
			if _, err := w.begin(t.Context(), SecurityAccept, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("advanced past ambiguity", err)
			}
		})
	}
}

func TestTransactionCommittedHoldTargetAndSafetyCeiling(t *testing.T) {
	for _, elapsed := range []time.Duration{30 * time.Millisecond, MaxCommandSafetyHold + time.Millisecond} {
		t.Run(elapsed.String(), func(t *testing.T) {
			w, path, clock := testExecutor(t)
			if err := w.db.Close(); err != nil {
				t.Fatal(err)
			}
			base, err := sqliteconn.Connector(path, w.config)
			if err != nil {
				t.Fatal(err)
			}
			installFaultPool(w, &faultConnector{Connector: base, afterNativeCommit: func() { *clock = clock.Add(elapsed) }})
			c := &testCustody{}
			tx, err := w.begin(t.Context(), ProgressAccept, testRecipe(), c)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.rollback(nil)
			mutateFixture(t, tx)
			result, err := tx.commit()
			if result.state != committed || result.commit != elapsed || c.returns != 1 || w.holdTargetOverruns.Load() != 1 {
				t.Fatal(result, err, c)
			}
			evidence := frameEvidence(tx, &governedTurn{})
			if !evidence.HoldTargetExceeded || evidence.HoldTarget != testRecipe().limit.Hold || evidence.HoldSafetyCeiling != MaxCommandSafetyHold {
				t.Fatal(evidence)
			}
			if got := readRevision(t, path); got != 2 {
				t.Fatal("committed effect lost", got)
			}
			if elapsed > MaxCommandSafetyHold {
				if !errors.Is(err, ErrOverrun) || c.fences != 1 || !w.fenced {
					t.Fatal(result, err, c)
				}
				if _, err := w.begin(t.Context(), ProgressAccept, testRecipe(), &testCustody{}); !errors.Is(err, ErrClosed) {
					t.Fatal(err)
				}
			} else {
				if err != nil || c.fences != 0 || w.fenced {
					t.Fatal(result, err, c)
				}
				next, err := w.begin(t.Context(), ProgressAccept, testRecipe(), &testCustody{})
				if err != nil {
					t.Fatal("target miss disabled later work", err)
				}
				if _, err := next.rollback(nil); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestTransactionAdmissionAndCompleteOverrunFailClosed(t *testing.T) {
	w, path, clock := testExecutor(t)
	for _, tc := range []struct {
		id   CommandID
		r    turnRecipe
		c    *testCustody
		want error
	}{
		{ProgressAccept, turnRecipe{}, &testCustody{}, ErrQuantum},
		{0, testRecipe(), &testCustody{}, ErrInvalid},
		{ProgressAccept, testRecipe(), &testCustody{checkErr: ErrInvalid}, ErrInvalid},
	} {
		if _, err := w.begin(t.Context(), tc.id, tc.r, tc.c); !errors.Is(err, tc.want) {
			t.Fatal(err)
		}
		if tc.c.returns != 1 {
			t.Fatal("refusal leaked consumed custody", tc.c)
		}
	}
	c := &testCustody{}
	tx, err := w.begin(t.Context(), ProgressAccept, testRecipe(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	mutateFixture(t, tx)
	*clock = clock.Add(MaxCommandSafetyHold + time.Millisecond)
	result, err := tx.commit()
	if result.state != rolledBack || !errors.Is(err, ErrQuantum) || !errors.Is(err, ErrOverrun) {
		t.Fatal(result, err)
	}
	if c.fences != 1 || c.returns != 1 {
		t.Fatal(c)
	}
	if got := readRevision(t, path); got != 1 {
		t.Fatal(got)
	}
	if _, err := w.begin(t.Context(), ProgressAccept, testRecipe(), &testCustody{}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestEmptyExecutorRefusesExistingLayoutAndRetirementFailure(t *testing.T) {
	// This is a genuine fresh-startup/refusal control, not a copied fixture.
	path := filepath.Join(t.TempDir(), "fresh-writer.sqlite")
	w, err := newEmptyExecutor(t.Context(), path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.close(); err != nil {
			t.Error(err)
		}
	})
	now := time.Now().Add(time.Hour)
	w.now = func() time.Time { return now }
	if other, err := newEmptyExecutor(t.Context(), path, 1024); err == nil {
		other.close()
		t.Fatal("bootstrap reused nonempty layout")
	}
	c := &testCustody{returnErr: ErrInvalid}
	tx, err := w.begin(t.Context(), ProgressAccept, testRecipe(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	result, err := tx.rollback(nil)
	if result.state != rolledBack || !errors.Is(err, ErrInvalid) || c.fences != 1 {
		t.Fatal(result, err, c)
	}
}

func TestTransactionConcurrentAdmissionRefusesBeforeTakingCustody(t *testing.T) {
	w, _, _ := testExecutor(t)
	tx, err := w.begin(t.Context(), ProgressAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	var group sync.WaitGroup
	results := make(chan error, 16)
	for range 16 {
		group.Go(func() {
			custody := &testCustody{}
			_, err := w.begin(t.Context(), ProgressAccept, testRecipe(), custody)
			if !errors.Is(err, ErrBusy) || custody.checks != 0 || custody.returns != 0 {
				results <- errors.New("contending turn entered custody")
				return
			}
			if err := w.close(); !errors.Is(err, ErrBusy) {
				results <- errors.New("contending close acquired custody")
				return
			}
			results <- nil
		})
	}
	done := make(chan struct{})
	go func() { group.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("admission waited for held writer instead of refusing")
	}
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

var errDriverPanic = errors.New("injected native transaction reply panic")

func panicDriver(t *testing.T, w *executor, path string) *faultConnector {
	t.Helper()
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	fault := &faultConnector{Connector: base}
	installFaultPool(w, fault)
	return fault
}

func TestTransactionBeginPanicReleasesUnreturnedCustody(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_native_begin", true: "after_native_begin"}[after], func(t *testing.T) {
			w, path, _ := testExecutor(t)
			fault := panicDriver(t, w, path)
			var beginCtx context.Context
			hook := func(q string, ctx context.Context) {
				if q == "BEGIN IMMEDIATE" {
					beginCtx = ctx
					panic(errDriverPanic)
				}
			}
			if after {
				fault.afterNative = hook
			} else {
				fault.beforeNative = hook
			}
			c := &testCustody{closed: &fault.closed}
			var caught any
			func() {
				defer func() { caught = recover() }()
				_, _ = w.begin(t.Context(), ProgressAccept, testRecipe(), c)
			}()
			unlocked := w.mu.TryLock()
			if unlocked {
				w.mu.Unlock()
			}
			cancelled := beginCtx != nil && beginCtx.Err() != nil
			if caught != errDriverPanic || !unlocked || !cancelled || !fault.closed.Load() || fault.closes.Load() != 1 || c.returns != 1 || w.db.Stats().InUse != 0 {
				t.Errorf("BEGIN panic=%v unlocked=%v cancelled=%v physicalClosed=%v closes=%d retired=%d inUse=%d", caught, unlocked, cancelled, fault.closed.Load(), fault.closes.Load(), c.returns, w.db.Stats().InUse)
				// Keep the deliberate old-frame negative from leaking its native
				// connection/mutex after recording the failed ownership assertions.
				if !unlocked {
					w.mu.Unlock()
				}
				if !fault.closed.Load() {
					_ = fault.physical.Load().Close()
				}
				return
			}
			fault.beforeNative, fault.afterNative = nil, nil
			if w.ambiguous || w.fenced || readRevision(t, path) != 1 {
				t.Fatal("BEGIN panic invented an outcome or retained an effect")
			}
			tx, err := w.begin(t.Context(), SecurityAccept, testRecipe(), &testCustody{})
			if err != nil {
				t.Fatal("replacement turn refused after joined BEGIN cleanup", err)
			}
			defer tx.rollback(nil)
			if tx.settings.Synchronous != 2 || tx.settings.ForeignKeys != 1 || tx.settings.TempStore != 1 {
				t.Fatal(tx.settings)
			}
			if _, err := tx.rollback(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTransactionCommitPanicPreservesUnknownOutcome(t *testing.T) {
	for _, after := range []bool{false, true} {
		for _, callerCleanup := range []bool{false, true} {
			name := map[bool]string{false: "inside_driver_before_native_commit", true: "after_native_commit_before_reply"}[after]
			if callerCleanup {
				name += "/caller_cleanup"
			}
			t.Run(name, func(t *testing.T) {
				w, path, _ := testExecutor(t)
				fault := panicDriver(t, w, path)
				hook := func(q string, _ context.Context) {
					if q == "COMMIT" {
						panic(errDriverPanic)
					}
				}
				if after {
					fault.afterNative = hook
				} else {
					fault.beforeNative = hook
				}
				c := &testCustody{closed: &fault.closed}
				tx, err := w.begin(t.Context(), ProgressAccept, testRecipe(), c)
				if err != nil {
					t.Fatal(err)
				}
				mutateFixture(t, tx)
				var caught any
				var cleanup turnResult
				var cleanupErr error
				func() {
					defer func() { caught = recover() }()
					if callerCleanup {
						defer func() { cleanup, cleanupErr = tx.rollback(nil) }()
					}
					_, _ = tx.commit()
				}()
				// With no caller defer, the frame must already have joined cleanup.
				joined := fault.closed.Load() && c.returns == 1 && w.db.Stats().InUse == 0
				if !callerCleanup {
					cleanup, cleanupErr = tx.rollback(nil)
				}
				if caught != errDriverPanic || !joined || cleanup.state != commitUnknown || !errors.Is(cleanupErr, ErrResolveRequired) || !w.ambiguous || fault.closes.Load() != 1 || c.returns != 1 || fault.commits.Load() != 1 || fault.rollbacks.Load() != 0 {
					t.Errorf("COMMIT panic=%v joined=%v cleanupState=%v cleanupErr=%v ambiguous=%v closes=%d retired=%d commits=%d rollbacks=%d", caught, joined, cleanup.state, cleanupErr, w.ambiguous, fault.closes.Load(), c.returns, fault.commits.Load(), fault.rollbacks.Load())
				}
				want := 1
				if after {
					want = 2
				}
				if got := readRevision(t, path); got != want {
					t.Errorf("native outcome=%d want%d", got, want)
				}
				next, err := w.begin(t.Context(), SecurityAccept, testRecipe(), &testCustody{})
				if !errors.Is(err, ErrResolveRequired) {
					t.Errorf("advanced past panic ambiguity: %v", err)
				}
				if next != nil {
					_, _ = next.rollback(nil)
				}
			})
		}
	}
}

var errRetirementPanic = errors.New("injected retirement panic")

func TestTransactionRetirementPanicCannotEraseTerminalOutcome(t *testing.T) {
	for _, outcome := range []string{"committed", "unknown", "rolled_back"} {
		for _, callerCleanup := range []bool{false, true} {
			name := outcome
			if callerCleanup {
				name += "/caller_cleanup"
			}
			t.Run(name, func(t *testing.T) {
				w, path, _ := testExecutor(t)
				fault := panicDriver(t, w, path)
				if outcome == "unknown" {
					fault.afterNativeCommit = func() { panic(errDriverPanic) }
				}
				closedAtRetirement := false
				fencedAtRetirement := false
				var tx *transaction
				var err error
				var during turnResult
				var duringErr error
				c := &testCustody{retireHook: func() {
					closedAtRetirement = fault.closed.Load()
					fencedAtRetirement = w.fenced
					during, duringErr = tx.rollback(nil)
					panic(errRetirementPanic)
				}}
				tx, err = w.begin(t.Context(), ProgressAccept, testRecipe(), c)
				if err != nil {
					t.Fatal(err)
				}
				mutateFixture(t, tx)
				var caught any
				var fromDefer turnResult
				var deferErr error
				func() {
					defer func() { caught = recover() }()
					if callerCleanup {
						defer func() { fromDefer, deferErr = tx.rollback(nil) }()
					}
					if outcome == "rolled_back" {
						_, _ = tx.rollback(nil)
					} else {
						_, _ = tx.commit()
					}
				}()
				wantPanic, wantState, wantRevision := errRetirementPanic, committed, 2
				if outcome == "unknown" {
					wantPanic, wantState = errDriverPanic, commitUnknown
				} else if outcome == "rolled_back" {
					wantState, wantRevision = rolledBack, 1
				}
				if caught != wantPanic {
					t.Errorf("primary panic=%v want%v", caught, wantPanic)
				}
				if !fencedAtRetirement || during.state != wantState || duringErr == nil {
					t.Errorf("callback entered before saved terminal/quarantine: fenced=%v state=%v err=%v", fencedAtRetirement, during.state, duringErr)
				}
				if !w.fenced || !fault.closed.Load() || fault.closes.Load() != 1 || c.returns != 1 || w.db.Stats().InUse != 0 {
					t.Errorf("incomplete retirement fenced=%v closed=%v closes=%d attempts=%d inUse=%d", w.fenced, fault.closed.Load(), fault.closes.Load(), c.returns, w.db.Stats().InUse)
				}
				if outcome == "unknown" && !closedAtRetirement {
					t.Error("uncertain connection not closed before retirement")
				}
				check := func(result turnResult, err error) {
					t.Helper()
					if result.state != wantState || !errors.Is(err, errRetirementPanic) {
						t.Errorf("saved outcome=%v err=%v wantstate=%v and secondary panic", result.state, err, wantState)
					}
					if outcome == "unknown" && !errors.Is(err, ErrResolveRequired) {
						t.Error("lost resolve-required", err)
					}
				}
				if callerCleanup {
					check(fromDefer, deferErr)
				}
				for range 3 {
					result, err := tx.rollback(nil)
					check(result, err)
					result, err = tx.commit()
					check(result, err)
				}
				if fault.closes.Load() != 1 || c.returns != 1 {
					t.Errorf("repeated cleanup closes=%d retirement attempts=%d", fault.closes.Load(), c.returns)
				}
				if got := readRevision(t, path); got != wantRevision {
					t.Errorf("actual SQLite outcome=%d want%d", got, wantRevision)
				}
				next, err := w.begin(t.Context(), SecurityAccept, testRecipe(), &testCustody{})
				if err == nil || errors.Is(err, ErrBusy) || outcome == "unknown" && !errors.Is(err, ErrResolveRequired) {
					t.Errorf("retirement fence bypassed: %v", err)
				}
				if next != nil {
					_, _ = next.rollback(nil)
				}
			})
		}
	}
}

func TestTransactionBeginAndRetirementPanicsPreservePrimary(t *testing.T) {
	w, path, _ := testExecutor(t)
	fault := panicDriver(t, w, path)
	fault.afterNative = func(query string, _ context.Context) {
		if query == "BEGIN IMMEDIATE" {
			panic(errDriverPanic)
		}
	}
	c := &testCustody{closed: &fault.closed, retireHook: func() { panic(errRetirementPanic) }}
	var caught any
	func() {
		defer func() { caught = recover() }()
		_, _ = w.begin(t.Context(), ProgressAccept, testRecipe(), c)
	}()
	if caught != errDriverPanic || !w.fenced || !fault.closed.Load() || fault.closes.Load() != 1 || c.returns != 1 || w.db.Stats().InUse != 0 {
		t.Fatalf("BEGIN/retire panic=%v fenced=%v closed=%v closes=%d returns=%d inUse=%d", caught, w.fenced, fault.closed.Load(), fault.closes.Load(), c.returns, w.db.Stats().InUse)
	}
	if _, err := w.begin(t.Context(), SecurityAccept, testRecipe(), &testCustody{}); !errors.Is(err, ErrClosed) {
		t.Fatal("incomplete BEGIN retirement reused frame", err)
	}
}

var errPhysicalClosePanic = errors.New("injected physical-close panic")
var errCallerPanic = errors.New("injected caller-body panic")
var errCancelPanic = errors.New("injected cancel panic")

func openerCount() int {
	stack := make([]byte, 1<<20)
	n := runtime.Stack(stack, true)
	return bytes.Count(stack[:n], []byte("database/sql.(*DB).connectionOpener("))
}

func TestTransactionPhysicalClosePanicReleasesSQLPool(t *testing.T) {
	for _, discard := range []bool{false, true} {
		for _, after := range []bool{false, true} {
			name := map[bool]string{false: "idle_pool", true: "discard"}[discard] + "/" + map[bool]string{false: "before_native_close", true: "after_native_close"}[after]
			t.Run(name, func(t *testing.T) {
				baseline := openerCount()
				w, path, _ := testExecutor(t)
				fault := panicDriver(t, w, path)
				hook := func() { panic(errPhysicalClosePanic) }
				if after {
					fault.afterClose = hook
				} else {
					fault.beforeClose = hook
				}
				c := &testCustody{}
				wantPanic, wantState := errRetirementPanic, committed
				if discard {
					fault.afterNativeCommit = func() { panic(errDriverPanic) }
					wantPanic, wantState = errDriverPanic, commitUnknown
				} else {
					c.retireHook = func() { panic(errRetirementPanic) }
				}
				tx, err := w.begin(t.Context(), ProgressAccept, testRecipe(), c)
				if err != nil {
					t.Fatal(err)
				}
				mutateFixture(t, tx)
				var caught any
				func() {
					defer func() { caught = recover() }()
					defer tx.rollback(nil)
					_, _ = tx.commit()
				}()
				result, err := tx.rollback(nil)
				if caught != wantPanic || result.state != wantState || !errors.Is(err, errPhysicalClosePanic) || !w.fenced {
					t.Errorf("close fault panic=%v state=%v err=%v fenced=%v", caught, result.state, err, w.fenced)
				}
				if !fault.nativeClosed.Load() || fault.nativeCloses.Load() != 1 {
					t.Errorf("native release closed=%v calls=%d", fault.nativeClosed.Load(), fault.nativeCloses.Load())
				}
				if err := w.close(); err != nil && !errors.Is(err, errPhysicalClosePanic) {
					t.Error(err)
				}
				stats := w.db.Stats()
				deadline := time.Now().Add(time.Second)
				for openerCount() > baseline && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if stats.OpenConnections != 0 || stats.InUse != 0 || openerCount() > baseline {
					t.Errorf("pool leaked: open=%d inUse=%d openers=%d baseline=%d", stats.OpenConnections, stats.InUse, openerCount(), baseline)
				}
				// Release only the native handle stranded by the deliberate held-
				// source negative, after recording all failed resource assertions.
				if !fault.nativeClosed.Load() {
					fault.beforeClose, fault.afterClose = nil, nil
					_ = fault.physical.Load().Close()
				}
			})
		}
	}
}

func TestTransactionDeferredRollbackPreservesCallerPanic(t *testing.T) {
	for _, kind := range []string{"retirement", "cancel", "before_check"} {
		t.Run(kind, func(t *testing.T) {
			w, path, _ := testExecutor(t)
			fault := panicDriver(t, w, path)
			c := &testCustody{}
			tx, err := w.begin(t.Context(), ProgressAccept, testRecipe(), c)
			if err != nil {
				t.Fatal(err)
			}
			mutateFixture(t, tx)
			secondary := errRetirementPanic
			if kind == "cancel" {
				secondary = errCancelPanic
				cancel := tx.cancel
				tx.cancel = func() { cancel(); panic(errCancelPanic) }
			} else {
				c.retireHook = func() { panic(errRetirementPanic) }
			}
			if kind == "before_check" {
				c.checkHook = func() { panic(errCallerPanic) }
			}
			var caught any
			func() {
				defer func() { caught = recover() }()
				defer tx.rollback(nil)
				if kind == "before_check" {
					_ = tx.before(Work{Hold: time.Nanosecond})
				} else {
					panic(errCallerPanic)
				}
			}()
			result, err := tx.rollback(nil)
			if caught != errCallerPanic || tx.primaryPanic != errCallerPanic || result.state != rolledBack || !errors.Is(err, secondary) {
				t.Errorf("caller panic=%v savedPrimary=%v state=%v err=%v", caught, tx.primaryPanic, result.state, err)
			}
			if !w.fenced || !fault.nativeClosed.Load() || fault.nativeCloses.Load() != 1 || c.returns != 1 || w.db.Stats().InUse != 0 || readRevision(t, path) != 1 {
				t.Error("deferred cleanup did not preserve release/rollback/fence", w.db.Stats(), c)
			}
		})
	}
}

func TestTransactionSafetyDeadlinePreservesOriginalCaps(t *testing.T) {
	for _, mode := range []string{"safety", "caller", "grant"} {
		t.Run(mode, func(t *testing.T) {
			w, path, clock := testExecutor(t)
			ctx := t.Context()
			expected := clock.Add(MaxCommandSafetyHold)
			var c turnCustody = &testCustody{}
			switch mode {
			case "caller":
				expected = clock.Add(100 * time.Millisecond)
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, expected)
				defer cancel()
			case "grant":
				g, a, wal, request := governedFixture(t, w.now)
				if err := refreshWriterWAL(path, wal, w.now); err != nil {
					t.Fatal(err)
				}
				d := g.Try(request)
				if d.Grant == nil {
					t.Fatal(d.Refusal)
				}
				governed, err := bindGovernedTurn(InventoryAccept, testRecipe().limit, g, a, wal, d.Grant, request)
				if err != nil {
					t.Fatal(err)
				}
				c = governed
				expected = request.Deadline
			}
			tx, err := w.begin(ctx, InventoryAccept, testRecipe(), c)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.rollback(nil)
			if got, ok := tx.ctx.Deadline(); !ok || !got.Equal(expected) {
				t.Fatal("original deadline changed", got, expected)
			}
		})
	}
}
