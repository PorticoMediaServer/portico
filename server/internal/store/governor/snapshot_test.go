package governor

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func snapshotFixture(t *testing.T) (string, *sql.Conn) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "governor.sqlite")
	c := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, settings, err := sqliteconn.Open(t.Context(), path, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if settings.Engine != "3.53.4" || settings.TempStore != 1 {
		t.Fatalf("invalid control settings %+v", settings)
	}
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err = sqliteconn.Assert(t.Context(), conn, c); err != nil {
		t.Fatal(err)
	}
	// Tiny test-only source, not a change to INT's canonical DDL.
	if _, err = conn.ExecContext(t.Context(), "CREATE TABLE source(id INTEGER PRIMARY KEY,value TEXT); INSERT INTO source VALUES(1,'first')"); err != nil {
		t.Fatal(err)
	}
	return path, conn
}

func snapshotGovernor(t *testing.T) (Governor, *Authority) {
	t.Helper()
	c := Config{Now: time.Now, MaxGrantsPerClass: 8}
	for i := range c.Partitions {
		c.Partitions[i] = Amounts{Reader: 1, MemoryBytes: 4 << 20, CPUMilli: 1000, IOBytes: 4 << 20, WALBytes: 4 << 20}
	}
	g, a, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return g, a
}

func snapshotRequest() Request {
	return Request{Key: JobKey{Domain: 1, ID: "page"}, Owner: 1, Class: Foreground, Need: Amounts{Reader: 1, MemoryBytes: (16 * 16384) + (64 << 10)}, Budget: UnitBudget{RawRows: 32, VMOps: 10000, Pages: 128}, Deadline: time.Now().Add(2 * time.Second)}
}

func snapshotRegistry(t *testing.T) *stmt.Registry {
	t.Helper()
	var definitions []stmt.Definition
	for id, query := range map[stmt.ID]string{"source": "SELECT value FROM source ORDER BY id LIMIT 1", "blob": "SELECT CAST(value AS BLOB) FROM source ORDER BY id LIMIT 1", "slow": "SELECT sum(x) FROM (WITH RECURSIVE seq(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM seq) SELECT x FROM seq)", "two": "SELECT id FROM source UNION ALL SELECT id FROM source", "large": "SELECT zeroblob(1024)", "all": "SELECT value FROM source ORDER BY id"} {
		definitions = append(definitions, stmt.Definition{ID: id, SQL: query, Owner: "read-test", CallSites: []string{"snapshot-test"}, Kind: stmt.Read, Cohorts: []stmt.Cohort{{Name: "plain"}}})
	}
	definitions = append(definitions, stmt.Definition{ID: "arg", SQL: "SELECT ?", Owner: "read-test", CallSites: []string{"snapshot-test"}, Kind: stmt.Read, Params: []stmt.Param{{Name: "value", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "plain", Bindings: []any{"text"}}}})
	r, err := stmttest.New(t, definitions)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type snapshotConnector struct {
	driver.Connector
	opened, closed, rollbacks atomic.Int64
	failRollback              atomic.Bool
	rollbackBadConn           atomic.Bool
	failClose                 atomic.Bool
	checkpointPanic           atomic.Bool
	checkpointError           atomic.Bool
	checkpointTuple           *[3]int64
	checkpointCancelEntered   chan struct{}
	checkpointEntered         chan struct{}
	checkpointRelease         chan struct{}
	closeEntered              chan struct{}
	mainIdentityError         error
	releaseClose              chan struct{}
}
type snapshotConn struct {
	driver.Conn
	owner *snapshotConnector
}

func (c *snapshotConn) SQLiteMainFileIdentity() (uint64, uint64, error) {
	if c.owner.mainIdentityError != nil {
		return 0, 0, c.owner.mainIdentityError
	}
	getter, ok := c.Conn.(interface {
		SQLiteMainFileIdentity() (uint64, uint64, error)
	})
	if !ok {
		return 0, 0, errors.New("native identity unavailable")
	}
	return getter.SQLiteMainFileIdentity()
}

func (c *snapshotConnector) Connect(ctx context.Context) (driver.Conn, error) {
	raw, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	c.opened.Add(1)
	return &snapshotConn{Conn: raw, owner: c}, nil
}
func (c *snapshotConn) ReadBudgetSupported() bool {
	native, ok := c.Conn.(interface{ ReadBudgetSupported() bool })
	return ok && native.ReadBudgetSupported()
}
func (c *snapshotConn) SQLiteDQS() (int, int, error) {
	native, ok := c.Conn.(interface{ SQLiteDQS() (int, int, error) })
	if !ok {
		return 0, 0, errors.New("test native DQS getter unsupported")
	}
	return native.SQLiteDQS()
}
func (c *snapshotConn) SQLiteLimit(id int, newVal int) (int, error) {
	return c.Conn.(interface{ SQLiteLimit(int, int) (int, error) }).SQLiteLimit(id, newVal)
}
func (c *snapshotConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}

var snapshotRollbackError = errors.New("injected rollback failed before ending transaction")

func (c *snapshotConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if strings.EqualFold(q, "ROLLBACK") {
		c.owner.rollbacks.Add(1)
		if c.owner.rollbackBadConn.Load() {
			return nil, driver.ErrBadConn
		}
		if c.owner.failRollback.Load() {
			return nil, snapshotRollbackError
		}
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c *snapshotConn) Close() error {
	n := c.owner.closed.Add(1)
	if n == 1 && c.owner.closeEntered != nil {
		close(c.owner.closeEntered)
		<-c.owner.releaseClose
	}
	if c.owner.failClose.Load() {
		return errors.New("injected physical close failure")
	}
	return c.Conn.Close()
}
func snapshotReaderDriver(t *testing.T, path string, g Governor, a *Authority) (*Snapshots, snapshotPool, *snapshotConnector) {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 16384, Columns: 16}}
	connector, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	counted := &snapshotConnector{Connector: connector}
	s, err := NewSnapshots(counted, cfg, g, a, snapshotRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, s.db, counted
}
func snapshotQueryOne(s *ReadScope) error {
	rows, err := s.Query("source")
	if err != nil {
		return err
	}
	if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != "first" {
		return errors.New("unexpected copied values")
	}
	return nil
}
func snapshotCheckpoint(t *testing.T, path string) (int, int) {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Checkpointer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
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
	if _, err = sqliteconn.Assert(t.Context(), conn, cfg); err != nil {
		t.Fatal(err)
	}
	var busy, frames, copied int
	if err = conn.QueryRowContext(t.Context(), "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &frames, &copied); err != nil || busy != 0 {
		t.Fatal(busy, frames, copied, err)
	}
	return frames, copied
}
func waitSnapshotPhysical(t *testing.T, s *Snapshots) {
	t.Helper()
	deadline := time.NewTimer(1500 * time.Millisecond)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		s.mu.Lock()
		n := len(s.running)
		s.mu.Unlock()
		if n == 0 {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("consumer prevented physical snapshot cleanup")
		case <-tick.C:
		}
	}
}
func TestSnapshotCopiesAndRollsBackEveryWindow(t *testing.T) {
	path, _ := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	s, db, c := snapshotReaderDriver(t, path, g, a)
	var retained *ReadScope
	for i := 0; i < 2; i++ {
		out, err := s.Read(t.Context(), snapshotRequest(), time.Second, func(scope *ReadScope) error { retained = scope; return snapshotQueryOne(scope) })
		if err != nil || out.Refusal != nil || out.Settings.QueryOnly != 1 || out.Settings.TempStore != 1 {
			t.Fatal(out, err)
		}
	}
	if _, err := retained.Query("source"); err != ErrSnapshotClosed {
		t.Fatal(err)
	}
	if c.rollbacks.Load() != 2 || c.opened.Load() != 1 || c.closed.Load() != 0 {
		t.Fatal("explicit native rollback/reuse missing", c.rollbacks.Load(), c.opened.Load(), c.closed.Load())
	}
	if a.Usage() != [4]Amounts{} || db.Stats().InUse != 0 {
		t.Fatal("read custody leaked")
	}
}

type snapshotBlockedScanner struct {
	entered, release chan struct{}
	panicNow         bool
}

func (s snapshotBlockedScanner) Scan(any) error {
	close(s.entered)
	if s.panicNow {
		panic("consumer scanner panic")
	}
	<-s.release
	return nil
}
func TestSnapshotRawBytesAndBlockedScannerCannotPinPastDeadline(t *testing.T) {
	for _, mode := range []string{"raw", "scanner"} {
		t.Run(mode, func(t *testing.T) {
			path, writer := snapshotFixture(t)
			g, a := snapshotGovernor(t)
			s, db, _ := snapshotReaderDriver(t, path, g, a)
			entered, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			done := make(chan error, 1)
			go func() {
				_, err := s.Read(context.Background(), snapshotRequest(), 10*time.Second, func(scope *ReadScope) error {
					rows, err := scope.Query("blob")
					if err != nil {
						return err
					}
					raw := sql.RawBytes(rows[0][0].([]byte))
					if mode == "scanner" {
						return (snapshotBlockedScanner{entered: entered, release: release}).Scan(raw)
					}
					close(entered)
					<-release
					return scope.Context().Err()
				})
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("consumer not reached")
			}
			if _, err := writer.ExecContext(t.Context(), "UPDATE source SET value='after' WHERE id=1"); err != nil {
				t.Fatal(err)
			}
			n, k := snapshotCheckpoint(t, path)
			if k >= n {
				t.Fatal("control did not establish pinned snapshot", n, k)
			}
			waitSnapshotPhysical(t, s)
			n, k = snapshotCheckpoint(t, path)
			if n == 0 || k != n {
				t.Fatal("expired consumer still pins WAL", n, k)
			}
			if db.Stats().InUse != 0 || a.Usage()[Foreground][MemoryBytes] != (16*16384)+(64<<10) {
				t.Fatal("physical disposal or retained-buffer custody incorrect", db.Stats(), a.Usage())
			}
			closed := make(chan struct{})
			go func() { s.Close(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(100 * time.Millisecond):
				t.Fatal("Close waited on consumer")
			}
			// Release exactly once before waiting for Read/retirement.
			close(release)
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("consumer did not join")
			}
			if a.Usage() != [4]Amounts{} {
				t.Fatal("consumer custody leaked after actual return")
			}
		})
	}
}
func TestSnapshotScannerPanicCleansWithoutSQLLocks(t *testing.T) {
	path, writer := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	s, db, _ := snapshotReaderDriver(t, path, g, a)
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		_, _ = s.Read(t.Context(), snapshotRequest(), time.Second, func(scope *ReadScope) error {
			values, err := scope.Query("source")
			if err != nil {
				return err
			}
			if _, err = writer.ExecContext(t.Context(), "UPDATE source SET value='after'"); err != nil {
				return err
			}
			return (snapshotBlockedScanner{entered: make(chan struct{}), panicNow: true}).Scan(values[0][0])
		})
	}()
	select {
	case p := <-done:
		if p != "consumer scanner panic" {
			t.Fatal(p)
		}
	case <-time.After(time.Second):
		t.Fatal("scanner panic deadlocked cleanup")
	}
	n, k := snapshotCheckpoint(t, path)
	if n != k || a.Usage() != [4]Amounts{} || db.Stats().InUse != 0 || len(s.slots) != 0 {
		t.Fatal(n, k, a.Usage(), db.Stats())
	}
}
func TestSnapshotFailedRollbackDiscardsBeforeRefund(t *testing.T) {
	path, writer := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	s, db, c := snapshotReaderDriver(t, path, g, a)
	c.failRollback.Store(true)
	c.closeEntered = make(chan struct{})
	c.releaseClose = make(chan struct{})
	read, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := s.Read(t.Context(), snapshotRequest(), time.Second, func(scope *ReadScope) error {
			if err := snapshotQueryOne(scope); err != nil {
				return err
			}
			close(read)
			<-release
			return nil
		})
		done <- err
	}()
	<-read
	if _, err := writer.ExecContext(t.Context(), "UPDATE source SET value='after'"); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-c.closeEntered:
	case <-time.After(time.Second):
		t.Fatal("rollback uncertainty did not discard")
	}
	if a.Usage()[Foreground][Reader] != 1 {
		t.Fatal("refund preceded physical discard")
	}
	n, k := snapshotCheckpoint(t, path)
	if k >= n {
		t.Fatal("discard delay control did not retain native transaction")
	}
	close(c.releaseClose)
	if err := <-done; !errors.Is(err, snapshotRollbackError) {
		t.Fatal(err)
	}
	if c.rollbacks.Load() != 1 || c.closed.Load() == 0 || a.Usage() != [4]Amounts{} || db.Stats().InUse != 0 {
		t.Fatal("failed rollback custody", c.rollbacks.Load(), c.closed.Load(), a.Usage(), db.Stats())
	}
	n, k = snapshotCheckpoint(t, path)
	if n != k {
		t.Fatal("discard failed to release WAL", n, k)
	}
	c.failRollback.Store(false)
	out, err := s.Read(t.Context(), snapshotRequest(), time.Second, func(scope *ReadScope) error { _, err := scope.Query("source"); return err })
	if err != nil || out.Settings.QueryOnly != 1 || c.opened.Load() != 2 {
		t.Fatal(out, err, c.opened.Load())
	}
}
func TestSnapshotClosedStatementsAndOutputBudgets(t *testing.T) {
	path, _ := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	s, db, c := snapshotReaderDriver(t, path, g, a)
	for _, sqlText := range []string{"PRAGMA query_only=0", "ATTACH 'other' AS extra", "CREATE TEMP TABLE x(v)", "INSERT INTO temp.x VALUES(1)", "WITH x AS(SELECT 1) DELETE FROM source", "SELECT 1; PRAGMA query_only=0; CREATE TEMP TABLE x(v)"} {
		reg, err := stmttest.New(t, []stmt.Definition{{ID: "unsafe", SQL: sqlText, Kind: stmt.Read, Owner: "test", CallSites: []string{"test"}, Cohorts: []stmt.Cohort{{Name: "unsafe"}}}})
		if err == nil {
			if _, err = NewSnapshots(c, s.config, g, a, reg); err == nil {
				t.Fatal("policy changing statement accepted", sqlText)
			}
		}
	}
	_, err := s.Read(t.Context(), snapshotRequest(), time.Second, func(scope *ReadScope) error {
		if _, err := scope.Query(stmt.ID("PRAGMA query_only=0; CREATE TEMP TABLE x(v); INSERT INTO x VALUES(1)")); !errors.Is(err, sqliteconn.ErrPolicy) {
			return errors.New("unregistered SQL accepted")
		}
		return snapshotQueryOne(scope)
	})
	if err != nil {
		t.Fatal(err)
	}
	r := snapshotRequest()
	r.Budget.RawRows = 1
	if _, err = s.Read(t.Context(), r, time.Second, func(scope *ReadScope) error { _, err := scope.Query("two"); return err }); !errors.Is(err, ErrSnapshotBudget) {
		t.Fatal("row overflow accepted", err)
	}
	r = snapshotRequest()
	r.Need[MemoryBytes] = s.NativeRowReservation() + 512
	if _, err = s.Read(t.Context(), r, time.Second, func(scope *ReadScope) error { _, err := scope.Query("large"); return err }); !errors.Is(err, ErrSnapshotBudget) {
		t.Fatal("byte overflow accepted", err)
	}
	r = snapshotRequest()
	r.Need[MemoryBytes] = 5 << 20
	before := c.opened.Load()
	out, err := s.Read(t.Context(), r, time.Second, snapshotQueryOne)
	if err != nil || out.Refusal == nil || c.opened.Load() != before {
		t.Fatal("allocation before admission", out, err)
	}
	if a.Usage() != [4]Amounts{} || db.Stats().InUse != 0 {
		t.Fatal("budget refusal leaked custody")
	}
}
func TestSnapshotNativeCancellationAndBoundaryPolicy(t *testing.T) {
	path, _ := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	s, db, c := snapshotReaderDriver(t, path, g, a)
	_, err := s.Read(t.Context(), snapshotRequest(), 30*time.Millisecond, func(scope *ReadScope) error {
		if err := snapshotQueryOne(scope); err != nil {
			return err
		}
		_, err := scope.Query("slow")
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	n, k := snapshotCheckpoint(t, path)
	if n != k {
		t.Fatal("native cancellation left snapshot", n, k)
	}
	// Drift is injected only through the test's privately owned physical pool.
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(t.Context(), "PRAGMA query_only=0"); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	before := c.opened.Load()
	callbackReached := false
	if _, err = s.Read(t.Context(), snapshotRequest(), time.Second, func(scope *ReadScope) error { callbackReached = true; return snapshotQueryOne(scope) }); !errors.Is(err, sqliteconn.ErrPolicy) || callbackReached {
		t.Fatal("drift crossed acquisition boundary", err, callbackReached)
	}
	out, err := s.Read(t.Context(), snapshotRequest(), time.Second, snapshotQueryOne)
	if err != nil || out.Settings.QueryOnly != 1 || c.opened.Load() <= before {
		t.Fatal(out, err, c.opened.Load())
	}
	wrong := s.config
	wrong.Role = sqliteconn.Writer
	if _, err = NewSnapshots(c, wrong, g, a, snapshotRegistry(t)); err == nil {
		t.Fatal("writer connector policy accepted")
	}
	_, foreign := snapshotGovernor(t)
	if _, err = NewSnapshots(c, s.config, g, foreign, snapshotRegistry(t)); err == nil {
		t.Fatal("foreign authority accepted")
	}
	if a.Usage() != [4]Amounts{} || db.Stats().InUse != 0 {
		t.Fatal("cancel/policy custody leaked")
	}
}

func TestSnapshotUnprovenDiscardRetainsOwnershipAndCustody(t *testing.T) {
	path, writer := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	s, db, c := snapshotReaderDriver(t, path, g, a)
	c.failRollback.Store(true)
	c.failClose.Store(true)
	defer c.failClose.Store(false)
	_, err := s.Read(t.Context(), snapshotRequest(), time.Second, func(scope *ReadScope) error {
		if err := snapshotQueryOne(scope); err != nil {
			return err
		}
		_, err := writer.ExecContext(t.Context(), "UPDATE source SET value='after'")
		return err
	})
	if err == nil || len(s.slots) != 1 || a.Usage()[Foreground][Reader] != 1 || a.Usage()[Foreground][MemoryBytes] != (16*16384)+(64<<10) {
		t.Fatal("failed disposal refunded physical/output custody", err, a.Usage())
	}
	n, k := snapshotCheckpoint(t, path)
	if k >= n {
		t.Fatal("fault control did not retain transaction")
	}
	if err = s.Close(); err == nil {
		t.Fatal("Close reported quiescence with unknown native state")
	}
	if a.Usage()[Foreground][Reader] != 1 {
		t.Fatal("failed Close erased custody")
	}
	c.failClose.Store(false)
	if err = s.Close(); err != nil {
		t.Fatal("retained raw ownership cannot finish disposal", err)
	}
	n, k = snapshotCheckpoint(t, path)
	if n != k || a.Usage() != [4]Amounts{} || db.Stats().InUse != 0 {
		t.Fatal(n, k, a.Usage(), db.Stats())
	}
}
func TestSnapshotReassertsPolicyBeforeReturningPoolConnection(t *testing.T) {
	path, _ := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	s, _, c := snapshotReaderDriver(t, path, g, a)
	_, err := s.Read(t.Context(), snapshotRequest(), time.Second, func(scope *ReadScope) error {
		if err := snapshotQueryOne(scope); err != nil {
			return err
		}
		// Test-only physical drift injection; this handle never reaches a consumer.
		_, err := scope.conn.ExecContext(t.Context(), "PRAGMA query_only=0")
		return err
	})
	if !errors.Is(err, sqliteconn.ErrPolicy) || c.closed.Load() == 0 {
		t.Fatal("exit policy was not checked/disposed", err, c.closed.Load())
	}
	out, err := s.Read(t.Context(), snapshotRequest(), time.Second, snapshotQueryOne)
	if err != nil || out.Settings.QueryOnly != 1 || c.opened.Load() != 2 {
		t.Fatal(out, err, c.opened.Load())
	}
}

type snapshotForbiddenValuer struct{ calls *atomic.Int64 }

func (v snapshotForbiddenValuer) Value() (driver.Value, error) {
	v.calls.Add(1)
	panic("untrusted Valuer ran")
}
func TestSnapshotPrimitiveBindingsAndCallBound(t *testing.T) {
	path, _ := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	s, _, _ := snapshotReaderDriver(t, path, g, a)
	_, err := s.Read(t.Context(), snapshotRequest(), time.Second, func(scope *ReadScope) error {
		var calls atomic.Int64
		if _, err := scope.Query("arg", snapshotForbiddenValuer{calls: &calls}); err == nil || calls.Load() != 0 {
			return errors.New("custom Valuer entered SQL")
		}
		for i := 0; i < 7; i++ {
			rows, err := scope.Query("arg", "safe")
			if err != nil || rows[0][0] != "safe" {
				return errors.New("plain registered binding failed")
			}
		}
		if _, err := scope.Query("source"); err != ErrBusy {
			return errors.New("unbounded native query count")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestSnapshotSupersedeClosesNativeBeforeConsumerReturns(t *testing.T) {
	path, writer := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	s, db, _ := snapshotReaderDriver(t, path, g, a)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, err := s.Read(ctx, snapshotRequest(), time.Second, func(scope *ReadScope) error {
			if err := snapshotQueryOne(scope); err != nil {
				return err
			}
			close(entered)
			<-release
			return nil
		})
		done <- err
	}()
	<-entered
	if _, err := writer.ExecContext(t.Context(), "UPDATE source SET value='new'"); err != nil {
		t.Fatal(err)
	}
	n, k := snapshotCheckpoint(t, path)
	if k >= n {
		t.Fatal("snapshot was not pinned")
	}
	cancel()
	waitSnapshotPhysical(t, s)
	n, k = snapshotCheckpoint(t, path)
	if k != n || db.Stats().InUse != 0 || a.Usage()[Foreground][MemoryBytes] != (16*16384)+(64<<10) {
		t.Fatal("supersede native/output custody", n, k, db.Stats(), a.Usage())
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if a.Usage() != [4]Amounts{} {
		t.Fatal("retained consumer debt did not retire")
	}
}

func TestSnapshotOversizedStoredValuesRefuseBeforeAllocation(t *testing.T) {
	for _, kind := range []string{"blob", "text", "excess-row"} {
		t.Run(kind, func(t *testing.T) {
			path, writer := snapshotFixture(t)
			statement := "UPDATE source SET value=zeroblob(8388608) WHERE id=1"
			if kind == "text" {
				statement = "UPDATE source SET value=CAST(zeroblob(8388608) AS TEXT) WHERE id=1"
			}
			if kind == "excess-row" {
				statement = "INSERT INTO source VALUES(2,zeroblob(8388608))"
			}
			if _, err := writer.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			g, a := snapshotGovernor(t)
			s, _, _ := snapshotReaderDriver(t, path, g, a)
			r := snapshotRequest()
			if kind == "excess-row" {
				r.Budget.RawRows = 1
			}
			_, err := s.Read(t.Context(), r, time.Second, func(scope *ReadScope) error {
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				_, err := scope.Query("all")
				runtime.ReadMemStats(&after)
				allocated := after.TotalAlloc - before.TotalAlloc
				t.Logf("%s grant=%d allocated=%d", kind, r.Need[MemoryBytes], allocated)
				if allocated > uint64(r.Need[MemoryBytes]-s.NativeRowReservation()) {
					return errors.New("oversized value allocated beyond grant before rejection")
				}
				if !errors.Is(err, ErrSnapshotBudget) {
					return errors.New("oversized value was not refused by native budget")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if a.Usage() != [4]Amounts{} {
				t.Fatal("oversized refusal leaked custody")
			}
		})
	}
}
func TestSnapshotBadConnRollbackKeepsDisposalOwnership(t *testing.T) {
	path, writer := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	s, _, c := snapshotReaderDriver(t, path, g, a)
	c.rollbackBadConn.Store(true)
	c.failClose.Store(true)
	defer c.failClose.Store(false)
	_, err := s.Read(t.Context(), snapshotRequest(), time.Second, func(scope *ReadScope) error {
		if err := snapshotQueryOne(scope); err != nil {
			return err
		}
		_, err := writer.ExecContext(t.Context(), "UPDATE source SET value='after'")
		return err
	})
	if err == nil || a.Usage()[Foreground][Reader] != 1 {
		t.Fatal("implicit disposal erased custody", err, a.Usage())
	}
	n, k := snapshotCheckpoint(t, path)
	if k >= n {
		t.Fatal("fault did not keep read transaction alive")
	}
	before := c.closed.Load()
	c.failClose.Store(false)
	if err = s.Close(); err != nil {
		t.Fatal("automatic ErrBadConn discard lost owned raw handle", err)
	}
	if c.closed.Load() <= before {
		t.Fatal("Close did not retry owned handle")
	}
	n, k = snapshotCheckpoint(t, path)
	if n != k || a.Usage() != [4]Amounts{} {
		t.Fatal("retried disposal failed", n, k, a.Usage())
	}
}

func TestSnapshotNativeReservationPrecedesConnectionAndSQL(t *testing.T) {
	snapshotNativeReservation(t, false)
}

// The full native aggregate is an opt-in timing diagnostic, not a default
// functional/race gate. Product snapshot lifetime remains capped at one second.
func TestDiagnosticSnapshotNativeAggregateDeadline(t *testing.T) {
	if os.Getenv("PORTICO_GV_NATIVE_TIMING") != "1" {
		t.Skip("opt-in native aggregate timing: PORTICO_GV_NATIVE_TIMING=1")
	}
	snapshotNativeReservation(t, true)
}

func snapshotNativeReservation(t *testing.T, fullAggregate bool) {
	t.Helper()
	for _, columns := range []int32{16, 128} {
		t.Run(fmt.Sprint(columns), func(t *testing.T) {
			path, _ := snapshotFixture(t)
			g, a := snapshotGovernor(t)
			cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 16384, Columns: columns}}
			connector, err := sqliteconn.Connector(path, cfg)
			if err != nil {
				t.Fatal(err)
			}
			counted := &snapshotConnector{Connector: connector}
			// Configured columns still determine C×L admission. Four values suffice
			// to exceed the 64KiB Go-copy grant in both native/Go controls without
			// requiring a 128-value PRNG aggregate to meet a wall-clock deadline.
			queryColumns := int(columns)
			if !fullAggregate {
				queryColumns = 4
			}
			expression := "SELECT " + strings.TrimSuffix(strings.Repeat("randomblob(12000),", queryColumns), ",")
			registry, err := stmttest.New(t, []stmt.Definition{
				{ID: "aggregate", SQL: expression, Kind: stmt.Read, Owner: "test", CallSites: []string{"native-reserve"}, Cohorts: []stmt.Cohort{{Name: "all"}}},
				{ID: "go-aggregate", SQL: strings.ReplaceAll(expression, "12000", "6000"), Kind: stmt.Read, Owner: "test", CallSites: []string{"copy-reserve"}, Cohorts: []stmt.Cohort{{Name: "all"}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			s, err := NewSnapshots(counted, cfg, g, a, registry)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := snapshotRequest()
			r.Need[MemoryBytes] = 64 << 10
			called := false
			out, err := s.Read(t.Context(), r, time.Second, func(scope *ReadScope) error { called = true; _, err := scope.Query("aggregate"); return err })
			if !errors.Is(err, ErrSnapshotBudget) || called || counted.opened.Load() != 0 || a.Usage() != [4]Amounts{} {
				t.Fatal("native floor was not reserved first", out, err, called, counted.opened.Load(), a.Usage())
			}
			native := int64(columns) * 16384
			r.Need[MemoryBytes] = native + (64 << 10)
			out, err = s.Read(t.Context(), r, time.Second, func(scope *ReadScope) error {
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				_, err := scope.Query("aggregate")
				runtime.ReadMemStats(&after)
				if after.TotalAlloc-before.TotalAlloc > 64<<10 {
					return errors.New("Go aggregate over-allocated")
				}
				return err
			})
			if !errors.Is(err, ErrSnapshotBudget) || out.NativeReservedBytes != native || out.GoReservedBytes != 64<<10 {
				t.Fatal(out, err)
			}
			out, err = s.Read(t.Context(), r, time.Second, func(scope *ReadScope) error {
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				_, err := scope.Query("go-aggregate")
				runtime.ReadMemStats(&after)
				if after.TotalAlloc-before.TotalAlloc > 64<<10 {
					return errors.New("native reservation was reused for Go copies")
				}
				return err
			})
			if !errors.Is(err, ErrSnapshotBudget) || out.GoReservedBytes != 64<<10 {
				t.Fatal("Go copies consumed native reservation", out, err)
			}
			if a.Usage() != [4]Amounts{} {
				t.Fatal("native reservation leaked")
			}
		})
	}
}
