package writer

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
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
}

func (c *testCustody) check() error { c.checks++; return c.checkErr }
func (c *testCustody) retired() error {
	c.returns++
	if c.closed != nil && !c.closed.Load() {
		return errors.New("retired before physical close")
	}
	return c.returnErr
}
func (c *testCustody) fence() { c.fences++ }

func testExecutor(t *testing.T) (*executor, string, *time.Time) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "writer.sqlite")
	w, err := newEmptyExecutor(t.Context(), path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.close(); err != nil {
			t.Error(err)
		}
	})
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
	driver.Connector
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
}

func (c *faultConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	wrapped := &faultConn{Conn: conn, owner: c}
	c.physical.Store(wrapped)
	return wrapped, nil
}

type faultConn struct {
	driver.Conn
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
	if exec, ok := c.Conn.(driver.ExecerContext); ok {
		result, err = exec.ExecContext(ctx, q, args)
	} else {
		values := make([]driver.Value, len(args))
		for i, arg := range args {
			values[i] = arg.Value
		}
		result, err = c.Conn.(driver.Execer).Exec(q, values)
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
	err := c.Conn.Close()
	c.owner.closes.Add(1)
	c.owner.closed.Store(true)
	return err
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
			w.db = sql.OpenDB(fault)
			w.db.SetMaxOpenConns(1)
			w.db.SetMaxIdleConns(1)
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

func TestTransactionCommittedOverrunNeverBecomesRetryableRollback(t *testing.T) {
	w, path, clock := testExecutor(t)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	fault := &faultConnector{Connector: base, afterNativeCommit: func() { *clock = clock.Add(30 * time.Millisecond) }}
	w.db = sql.OpenDB(fault)
	w.db.SetMaxOpenConns(1)
	w.db.SetMaxIdleConns(1)
	c := &testCustody{}
	tx, err := w.begin(t.Context(), ProgressAccept, testRecipe(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	mutateFixture(t, tx)
	result, err := tx.commit()
	if result.state != committed || !errors.Is(err, ErrOverrun) || result.commit != 30*time.Millisecond || c.fences != 1 || c.returns != 1 {
		t.Fatal(result, err, c)
	}
	if got := readRevision(t, path); got != 2 {
		t.Fatal("committed effect lost", got)
	}
	if _, err := w.begin(t.Context(), ProgressAccept, testRecipe(), &testCustody{}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
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
	*clock = clock.Add(21 * time.Millisecond)
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
	w, path, _ := testExecutor(t)
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
	w.db = sql.OpenDB(fault)
	w.db.SetMaxOpenConns(1)
	w.db.SetMaxIdleConns(1)
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
