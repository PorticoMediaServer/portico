package governor

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

var checkpointClocks sync.Map // test-only clock custody; removed with each fixture

// Native SQL remains real; only elapsed time and deadline delivery are virtual.
// Parent cancellation is still real and drives the native cleanup controls.
func functionalCheckpointer(connector driver.Connector, cfg sqliteconn.Config, g Governor, a *Authority, policy CheckpointPolicy) (*Checkpointer, error) {
	cp, err := NewCheckpointer(connector, cfg, g, a, policy)
	if err == nil {
		cp.deadlineContext = func(parent context.Context, _ time.Time) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		}
	}
	return cp, err
}

func checkpointClockAdvance(g Governor, d time.Duration) {
	// Every functional governor below owns this test clock through Config.Now.
	// Store its pointer separately from the implementation's clock seam.
	clock, _ := checkpointClocks.Load(g)
	clock.(*testClock).ms.Add(d.Milliseconds())
}

func nativeCheckpointGovernor(t *testing.T) (Governor, *Authority, *WALGate) {
	t.Helper()
	wal := gate(t)
	clock := &testClock{}
	clock.ms.Store(time.Now().UnixMilli())
	cfg := Config{Now: clock.now, MaxGrantsPerClass: 8, WAL: wal}
	for i := range cfg.Partitions {
		cfg.Partitions[i] = Amounts{Reader: 2, MemoryBytes: 4 << 20, CPUMilli: 1000, IOBytes: 4 << 20, WALBytes: 4 << 20}
	}
	g, a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	checkpointClocks.Store(g, clock)
	t.Cleanup(func() { checkpointClocks.Delete(g) })
	return g, a, wal
}
func nativeCheckpointRequest(g Governor) Request {
	return Request{Key: JobKey{Domain: 1, ID: "native-checkpoint"}, Owner: 1, Class: Recovery, Need: Amounts{Reader: 1, MemoryBytes: 64 << 10, CPUMilli: 1000, IOBytes: 2 << 20}, Budget: UnitBudget{RawRows: 1, VMOps: 10000, Pages: 512}, MaxHold: 25 * time.Millisecond, Deadline: g.(*ledger).config.Now().Add(time.Second)}
}
func nativeCheckpointer(t *testing.T, path string, g Governor, a *Authority) *Checkpointer {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Checkpointer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	connector, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := functionalCheckpointer(connector, cfg, g, a, CheckpointPolicy{DatabasePath: path, Interval: 50 * time.Millisecond, MaxHold: 25 * time.Millisecond, MaxWait: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cp.Close(); err != nil {
			t.Error(err)
		}
	})
	return cp
}

func TestNativePassiveBoundedReaderAndObservedFixtureReuse(t *testing.T) {
	path, writer := snapshotFixture(t)
	g, a, wal := nativeCheckpointGovernor(t)
	snapshots, _, _ := snapshotReaderDriver(t, path, g, a)
	entered, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, err := snapshots.Read(ctx, snapshotRequest(), time.Second, func(scope *ReadScope) error {
			if err := snapshotQueryOne(scope); err != nil {
				return err
			}
			close(entered)
			<-release
			return nil
		})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("reader did not pin")
	}
	if _, err := writer.ExecContext(t.Context(), "UPDATE source SET value='after'"); err != nil {
		t.Fatal(err)
	}
	cp := nativeCheckpointer(t, path, g, a)
	first, err := cp.Step(t.Context(), nativeCheckpointRequest(g))
	if err != nil || first.Refusal != nil || first.Busy != 0 || first.Frames <= first.Backfilled || !first.DatabaseMatched || !first.PhysicalSafe {
		t.Fatal("pinned PASSIVE", first, err)
	}
	if first.Settings.Engine != "3.53.4" || first.Settings.TempStore != 1 || first.NativeStarted.IsZero() || first.NativeEnded.Before(first.NativeStarted) || first.PhysicalReturned.Before(first.NativeEnded) || first.RetiredAt.Before(first.PhysicalReturned) || first.SampleStarted.Before(first.RetiredAt) {
		t.Fatal("physical/native/sample evidence", first)
	}
	if _, err := cp.Step(t.Context(), nativeCheckpointRequest(g)); !errors.Is(err, ErrCheckpointCadence) {
		t.Fatal("pressure bypassed shared start spacing", err)
	}
	close(release)
	released = true
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	checkpointClockAdvance(g, first.EligibleInterval)
	second, err := cp.Step(t.Context(), nativeCheckpointRequest(g))
	if err != nil || second.Frames != second.Backfilled || second.NativeStarted.Sub(first.NativeStarted) != first.EligibleInterval {
		t.Fatal("unpinned PASSIVE", second, err)
	}
	if wal.ResetCount() != 0 {
		t.Fatal("copy mislabeled reset")
	}
	// A governed real fixture mutation, NOT a production maintenance command.
	// No scheduled-reset experiment or borrowed domain effect is implied.
	r := nativeCheckpointRequest(g)
	r.Class = Foreground
	r.Writer = true
	r.Need[WALBytes] = 32 << 10
	p := grant(t, g, r)
	if err := a.Check(p, r); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(t.Context(), "UPDATE source SET value='new-cycle'"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	retire(t, a, p)
	token := wal.Observation()
	after, err := ReadWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = wal.Publish(token, after, g.(*ledger).config.Now()); err != nil {
		t.Fatal(err)
	}
	if after.Salt == second.After.Salt || after.Frames >= second.After.Frames || wal.ResetCount() != 1 {
		t.Fatal("actual salt/frame reuse missing", second.After, after, wal.ResetCount())
	}
	if a.Usage() != [4]Amounts{} {
		t.Fatal("native custody leaked", a.Usage())
	}
}

func TestNativePassiveRejectsWrongFileAndIncompleteForecast(t *testing.T) {
	path, _ := snapshotFixture(t)
	other, _ := snapshotFixture(t)
	g, a, wal := nativeCheckpointGovernor(t)
	cp := nativeCheckpointer(t, path, g, a)
	for _, pages := range []bool{false, true} {
		small := nativeCheckpointRequest(g)
		budgetG, budgetA, _ := nativeCheckpointGovernor(t)
		budgetCP := nativeCheckpointer(t, path, budgetG, budgetA)
		if pages {
			small.Budget.Pages = 1
		} else {
			small.Need[IOBytes] = 1
		}
		out, err := budgetCP.Step(t.Context(), small)
		if !errors.Is(err, ErrCheckpointBudget) || !out.NativeStarted.IsZero() || budgetA.Usage() != [4]Amounts{} {
			t.Fatal("incomplete native forecast", pages, out, err, a.Usage())
		}
	}
	cfg := sqliteconn.Config{Role: sqliteconn.Checkpointer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	connector, err := sqliteconn.Connector(other, cfg)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := functionalCheckpointer(connector, cfg, g, a, cp.policy)
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.Close()
	out, err := wrong.Step(t.Context(), nativeCheckpointRequest(g))
	if !errors.Is(err, sqliteconn.ErrPolicy) || out.DatabaseMatched || !out.SampleStarted.IsZero() || !out.NativeStarted.IsZero() {
		t.Fatal("other DB qualified this WAL prefix", out, err)
	}
	r := nativeCheckpointRequest(g)
	r.Class = Foreground
	r.Writer = true
	r.Need[WALBytes] = 32
	refused(t, g, r, FailSafe)
	if wal.ResetCount() != 0 {
		t.Fatal("failed checkpoint invented reset")
	}
}

var checkpointInjectedPanic = errors.New("injected native checkpoint panic")

var checkpointInjectedError = errors.New("injected checkpoint result error")

type checkpointFaultStmt struct {
	driver.Stmt
	owner *snapshotConnector
}

func (s *checkpointFaultStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if s.owner.checkpointEntered != nil {
		close(s.owner.checkpointEntered)
		if s.owner.checkpointRelease != nil {
			<-s.owner.checkpointRelease
		}
	}
	if s.owner.checkpointPanic.Load() {
		panic(checkpointInjectedPanic)
	}
	rows, err := s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
	if err != nil {
		return rows, err
	}
	if s.owner.checkpointCancelEntered != nil {
		close(s.owner.checkpointCancelEntered)
		<-ctx.Done()
		return rows, ctx.Err() // Actual native CP ran; returned rows still require disposal.
	}
	if s.owner.checkpointError.Load() {
		return rows, checkpointInjectedError
	}
	return rows, nil
}
func (c *snapshotConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	st, err := c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
	if err == nil && (q == "PRAGMA wal_checkpoint(PASSIVE)" || q == "PRAGMA wal_checkpoint(TRUNCATE)") {
		st = &checkpointFaultStmt{Stmt: st, owner: c.owner}
	}
	return st, err
}
func TestNativePassivePanicFailedDiscardRetainsTurn(t *testing.T) {
	path, _ := snapshotFixture(t)
	g, a, _ := nativeCheckpointGovernor(t)
	cfg := sqliteconn.Config{Role: sqliteconn.Checkpointer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	connector, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	counted := &snapshotConnector{Connector: connector}
	counted.checkpointPanic.Store(true)
	counted.failClose.Store(true)
	cp, err := functionalCheckpointer(counted, cfg, g, a, CheckpointPolicy{DatabasePath: path, Interval: 50 * time.Millisecond, MaxHold: 25 * time.Millisecond, MaxWait: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { counted.failClose.Store(false); cp.Close() }()
	r := nativeCheckpointRequest(g)
	var caught any
	func() { defer func() { caught = recover() }(); _, _ = cp.Step(t.Context(), r) }()
	if caught != checkpointInjectedPanic || cp.pending == nil || a.Usage()[Recovery] != r.Need {
		t.Fatal("panic lost native custody", caught, cp.pending, a.Usage())
	}
	writer := nativeCheckpointRequest(g)
	writer.Class = Foreground
	writer.Writer = true
	writer.Need[WALBytes] = 32
	refused(t, g, writer, Capacity)
	if err := cp.Close(); err == nil || a.Usage()[Recovery] != r.Need {
		t.Fatal("failed close erased custody", err, a.Usage())
	}
	counted.failClose.Store(false)
	if err := cp.Close(); err != nil || a.Usage() != [4]Amounts{} || cp.pending != nil {
		t.Fatal("owned retry failed", err, a.Usage())
	}
	refused(t, g, writer, FailSafe)
}

func TestNativePassiveOverrunFencesAndRefusesReuse(t *testing.T) {
	path, _ := snapshotFixture(t)
	g, a, _ := nativeCheckpointGovernor(t)
	cp := nativeCheckpointer(t, path, g, a)
	r := nativeCheckpointRequest(g)
	// Connection acquisition is part of the physical hold; inject its duration.
	acquire := cp.pool.acquire
	cp.pool.acquire = func(ctx context.Context) (*sql.Conn, error) {
		conn, err := acquire(ctx)
		checkpointClockAdvance(g, 26*time.Millisecond)
		return conn, err
	}
	out, err := cp.Step(t.Context(), r)
	if !errors.Is(err, ErrCheckpointOverrun) || !out.PhysicalSafe || out.PhysicalReturned.IsZero() || out.NativeStarted.IsZero() || a.Usage() != [4]Amounts{} {
		t.Fatal("over-budget physical attempt was hidden/refunded without return", out, err, a.Usage())
	}
	if _, err = cp.Step(t.Context(), nativeCheckpointRequest(g)); !errors.Is(err, ErrCheckpointOverrun) {
		t.Fatal("unqualified checkpoint reused", err)
	}
	if g.(*ledger).state.Load()&1 == 0 {
		t.Fatal("overrun did not fence")
	}
}

func TestNativePassiveErrorAndCancelCleanup(t *testing.T) {
	for _, cancelCase := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "cancel"}[cancelCase], func(t *testing.T) {
			path, _ := snapshotFixture(t)
			g, a, _ := nativeCheckpointGovernor(t)
			cfg := sqliteconn.Config{Role: sqliteconn.Checkpointer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
			connector, err := sqliteconn.Connector(path, cfg)
			if err != nil {
				t.Fatal(err)
			}
			counted := &snapshotConnector{Connector: connector}
			if cancelCase {
				counted.checkpointCancelEntered = make(chan struct{})
				counted.closeEntered = make(chan struct{})
				counted.releaseClose = make(chan struct{})
			} else {
				counted.checkpointError.Store(true)
			}
			cp, err := functionalCheckpointer(counted, cfg, g, a, CheckpointPolicy{DatabasePath: path, Interval: 50 * time.Millisecond, MaxHold: 25 * time.Millisecond, MaxWait: 50 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			released := !cancelCase
			defer func() {
				if !released {
					close(counted.releaseClose)
				}
				cp.Close()
			}()
			r := nativeCheckpointRequest(g)
			type result struct {
				out CheckpointResult
				err error
			}
			done := make(chan result, 1)
			go func() { out, err := cp.Step(ctx, r); done <- result{out, err} }()
			if cancelCase {
				select {
				case <-counted.checkpointCancelEntered:
				case <-time.After(time.Second):
					t.Fatal("native checkpoint did not execute")
				}
				cancel()
				select {
				case <-counted.closeEntered:
				case <-time.After(time.Second):
					t.Fatal("cancel did not reach physical discard")
				}
				if a.Usage()[Recovery] != r.Need {
					t.Fatal("cancel refunded CP before physical close", a.Usage())
				}
				writer := nativeCheckpointRequest(g)
				writer.Class = Foreground
				writer.Writer = true
				writer.Need[WALBytes] = 32
				refused(t, g, writer, Capacity)
				select {
				case <-done:
					t.Fatal("cancel returned before physical cleanup")
				default:
				}
				close(counted.releaseClose)
				released = true
			}
			var got result
			select {
			case got = <-done:
			case <-time.After(time.Second):
				t.Fatal("native cleanup did not complete")
			}
			want := checkpointInjectedError
			if cancelCase {
				want = context.Canceled
			}
			if !errors.Is(got.err, want) || !got.out.PhysicalSafe || got.out.RetiredAt.IsZero() || counted.closed.Load() != 1 || cp.pending != nil || a.Usage() != [4]Amounts{} || cp.pool.Stats().InUse != 0 {
				t.Fatal("native error/cancel cleanup", got.out, got.err, counted.closed.Load(), a.Usage())
			}
			if err := cp.Close(); err != nil || counted.closed.Load() != 1 {
				t.Fatal("native cleanup repeated", err, counted.closed.Load())
			}
		})
	}
}
