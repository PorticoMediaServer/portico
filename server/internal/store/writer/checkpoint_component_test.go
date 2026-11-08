package writer

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/stateownership"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Measurement ONLY: canonical disposable file, actual W physical frame and SAME
// ledger, but synthetic source mutation/forecasts, not an authorized production
// domain adapter or qualified profile. No semantic maintenance effect escapes.
// Run fresh cells; no retries/filtering. The operator MUST declare sandbox IO.
// PORTICO_GV_NATIVE_TIMING=1 PORTICO_SANDBOX_IO_BYTES_PER_SEC=134217728 \
// go test -json -count=1 -timeout=3m -run '^TestDiagnosticConcurrentKeeper/64MiB$' ./internal/store/writer
func TestDiagnosticConcurrentKeeper(t *testing.T) {
	if os.Getenv("PORTICO_GV_NATIVE_TIMING") != "1" {
		t.Skip("opt-in native component timing")
	}
	ioCap := os.Getenv("PORTICO_SANDBOX_IO_BYTES_PER_SEC")
	if ioCap != "unavailable" && ioCap != "unrestricted" {
		if n, e := strconv.ParseInt(ioCap, 10, 64); e != nil || n <= 0 {
			t.Fatal("declare actual sandbox IO bytes/s, unrestricted, or unavailable")
		}
	}
	for _, cell := range []struct {
		name   string
		target int64
	}{{"1MiB", 1 << 20}, {"8MiB", 8 << 20}, {"64MiB", 64 << 20}, {"HardCap", 192 << 20}} {
		t.Run(cell.name, func(t *testing.T) {
			f := newCheckpointComponent(t, true, true)
			before := f.fill(t, cell.target)
			beforeAllocated := checkpointAllocated(f.path)

			type completion struct {
				out governor.CheckpointResult
				err error
			}
			done := make(chan completion, 1)
			go func() { out, err := f.cp.Step(t.Context(), f.cpRequest()); done <- completion{out, err} }()
			var finish completion
			select {
			case <-f.signal.entered:
			case finish = <-done:
				logCheckpointComponent(t, map[string]any{"Kind": "early_cp_failure", "Cell": cell.name, "DatabasePath": f.path, "OriginUTC": f.origin, "Checkpoint": measuredCheckpoint(finish.out, finish.err), "Error": checkpointError(finish.err)})
				t.Fatal("CP did not enter native", finish.err, finish.out.Refusal)
			}
			writer := f.turn(t, false, 256<<10)
			// Reader is bounded, returns owned scalar and closes before recovery.
			readStarted := time.Now()
			var revision int
			readRequest := governor.Request{Key: governor.JobKey{Domain: 1, ID: "checkpoint-reader"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 12 << 20, governor.CPUMilli: 1000, governor.IOBytes: 64 << 10}, Budget: governor.UnitBudget{RawRows: 1, VMOps: 10000, Pages: 16}, Deadline: f.now().Add(time.Second)}
			readRequest.IO = []governor.DeviceIO{{DeviceID: fmt.Sprintf("filesystem-device:%d", f.opening.binding.device), Class: governor.Seeking, Outstanding: 1, Bytes: 64 << 10}}
			readerResult, readerErr := f.reader.Read(t.Context(), readRequest, time.Second, func(scope *governor.ReadScope) error {
				rows, e := scope.Query("cp-revision")
				if e != nil {
					return e
				}
				if len(rows) != 1 {
					return ErrConflict
				}
				v, ok := rows[0][0].(int64)
				if !ok {
					return ErrInvalid
				}
				revision = int(v)
				return nil
			})
			readReturned := time.Now()
			finish = <-done
			record := map[string]any{"Kind": "concurrent", "FULLAvailable": false, "FULLUnavailableReason": "This synthetic frame fixture confers no authorized security effect; real FULL cadence belongs to the paired command adapter", "GapToHardBytes": f.policy.HardBytes - before.PhysicalBytes, "Cell": cell.name, "Qualification": false, "OriginUTC": f.origin, "DatabasePath": f.path, "PID": os.Getpid(), "BeforeAllocatedBytes": beforeAllocated, "SandboxIOBytesPerSec": ioCap, "Before": before, "Policy": f.policy, "Checkpoint": measuredCheckpoint(finish.out, finish.err), "CheckpointHoldNS": checkpointInterval(finish.out.Started, finish.out.PhysicalReturned), "CheckpointNativeNS": checkpointInterval(finish.out.NativeStarted, finish.out.NativeEnded), "CheckpointError": checkpointError(finish.err), "Writer": writer, "Reader": map[string]any{"Started": readStarted, "Returned": readReturned, "Error": checkpointError(readerErr), "Result": readerResult, "Revision": revision, "Scope": "real bounded Snapshots.Read scalar, separate fixed-limit Reader pool; NOT RD route qualification"}, "SyncCopy": nil, "SyncCopyReason": "external syscall trace required; no inferred VFS durations"}
			if writer.Window != nil {
				record["WriterNativeOverlap"] = writer.Window.SetupEntered.Before(finish.out.NativeEnded) && writer.Window.NativeReturned.After(finish.out.NativeStarted)
			}
			if cell.name == "HardCap" {
				if writer.HoldNS != nil || writer.Refusal == nil {
					t.Error("hard pressure must first refuse without W HOLD", writer)
				}
				if e := errors.Join(f.reader.Close(), f.a.Retired(f.readerGrant)); e != nil {
					t.Fatal(e)
				}
				f.reader = nil
				recovery, err := f.cp.RecoverWAL(t.Context(), f.cpRequest())
				resumed := f.turn(t, false, 256<<10)
				var latency *int64
				if resumed.HoldNS != nil && resumed.Error == "" {
					n := resumed.Accepted.Sub(writer.RefusedAt).Nanoseconds()
					latency = &n
				}
				record["Recovery"] = measuredCheckpoint(recovery, err)
				record["RecoveryError"] = checkpointError(err)
				record["ResumedWriter"] = resumed
				record["FirstRefusalToAcceptedNS"] = latency
				if err != nil || recovery.Refusal != nil || latency == nil {
					t.Error("recovery did not resume actual writes", err, recovery.Refusal, resumed.Error)
				}
			}
			after, e := governor.ReadWAL(f.path)
			record["After"] = after
			record["AfterError"] = checkpointError(e)
			logCheckpointComponent(t, record)
			if finish.err != nil || finish.out.Refusal != nil || !finish.out.PhysicalSafe {
				t.Error("retain failed CP", finish.err, finish.out.Refusal)
			}
		})
	}
}

type checkpointComponent struct {
	owner       *stateownership.Lock
	readerGrant *governor.Grant
	reader      *governor.Snapshots
	w           *executor
	opening     *scanOpening
	path        string
	origin      time.Time
	g           governor.Governor
	a           *governor.Authority
	wal         *governor.WALGate
	cp          *governor.Checkpointer
	keeper      *governor.CheckpointKeeper
	resources   governor.KeeperResources
	policy      governor.WALPolicy
	trace       *n1Trace
	signal      *checkpointSignalConnector
	now         func() time.Time
}

type checkpointFixtureMode uint8

const checkpointScreeningReader checkpointFixtureMode = 1
const checkpointBatchWriter checkpointFixtureMode = 2 // explicit full-batch resource unit, default fixtures unchanged

func newCheckpointComponent(t *testing.T, keep, real bool, selected ...checkpointFixtureMode) *checkpointComponent {
	t.Helper()
	seed, path, _ := testExecutor(t)
	if err := seed.close(); err != nil {
		t.Fatal(err)
	}
	return newCheckpointComponentAt(t, path, keep, real, selected...)
}

// newCheckpointComponentAt reuses an EXISTING canonical file. It never seeds,
// resets a floor or replays initialization SQL.
func newCheckpointComponentAt(t *testing.T, path string, keep, real bool, selected ...checkpointFixtureMode) *checkpointComponent {
	if len(selected) > 1 || len(selected) == 1 && selected[0] != checkpointScreeningReader && selected[0] != checkpointBatchWriter {
		t.Fatal("unknown closed checkpoint fixture selector")
	}
	screening := len(selected) == 1 && selected[0] == checkpointScreeningReader
	batch := len(selected) == 1 && selected[0] == checkpointBatchWriter
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("native file identity unsupported")
	}
	owner := scanOpeningOwner(t, path)
	opening, err := openScanStore(t.Context(), owner, path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	f := &checkpointComponent{owner: owner, w: opening.executor, opening: opening, path: path, origin: time.Now()}
	frozen := time.Now().Add(time.Hour)
	f.now = func() time.Time { return frozen }
	if real {
		f.now = time.Now
	}
	f.w.now = f.now
	// Rebind test observer outside all measured HOLD/startup. Same shared opener
	// and native file-attestation wrapper, no new raw connector exception.
	if err = f.w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, f.w.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	f.trace = newN1Trace(f.origin, false)
	f.w.openPool(connector, f.trace.decorate)
	if err = f.w.db.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.policy = governor.WALPolicy{HardBytes: 192 << 20, ReserveBytes: 16 << 20, SoftDebtBytes: 64 << 20, ActivePressureBytes: 96 << 20, MaxSampleAge: time.Second, ConcurrentPassive: keep, CheckpointMaxHold: 5 * time.Second}
	if batch || screening && os.Getenv("PORTICO_EC4_QUALIFICATION") == "1" {
		// Formal qualification uses the ONE adopted phase 1a policy; the 96 MiB
		// value above is the labelled screening stress fixture, not a profile.
		f.policy = governor.Phase1aWALPolicy()
	}
	if !keep {
		f.policy.CheckpointMaxHold = 0
	}
	f.wal, err = governor.NewWALGate(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	cfg := governor.Config{Now: f.now, MaxGrantsPerClass: 8, WAL: f.wal}
	for i := range cfg.Partitions {
		cfg.Partitions[i] = governor.Amounts{governor.Reader: 4, governor.MemoryBytes: 32 << 20, governor.CPUMilli: 4000, governor.IOBytes: 4 * f.policy.HardBytes, governor.WALBytes: 64 << 20}
	}
	if batch {
		for i := range cfg.Partitions {
			cfg.Partitions[i][governor.WALBytes] = inventoryBatchWALBytes
			cfg.Partitions[i][governor.IOBytes] = inventoryBatchIOBytes
		}
	}
	if screening {
		live := readfixture.LiveConfig(f.now)
		cfg.Foreground = live.Foreground
		cfg.MaxGrantsPerClass = live.MaxGrantsPerClass // existing LIVE bookkeeping, not worker slots
		cfg.Partitions[governor.Foreground] = live.Partitions[governor.Foreground]
	}
	f.g, f.a, err = governor.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if keep {
		cpCfg := sqliteconn.Config{Role: sqliteconn.Checkpointer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
		base, err := sqliteconn.Connector(path, cpCfg, sqliteconn.ExistingFile)
		if err != nil {
			t.Fatal(err)
		}
		f.signal = &checkpointSignalConnector{Connector: base, entered: make(chan struct{})}
		f.cp, err = governor.NewCheckpointer(f.signal, cpCfg, f.g, f.a, governor.CheckpointPolicy{DatabasePath: path, Interval: 50 * time.Millisecond, MaxHold: 5 * time.Second, MaxWait: 50 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		lifetime := time.Hour
		if screening {
			lifetime = 4 * time.Hour
		}
		f.resources.LifetimeRequest = governor.Request{Key: governor.JobKey{Domain: 1, ID: "cp-life"}, Owner: 1, Class: governor.Recovery, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 4 << 20}, Deadline: f.now().Add(lifetime)}
		f.resources.StartupRequest = f.shutdownRequest("cp-start")
		f.resources.Lifetime = f.mustGrant(t, f.resources.LifetimeRequest)
		f.resources.Startup = f.mustGrant(t, f.resources.StartupRequest)
		f.keeper, err = f.cp.Keep(t.Context(), f.resources)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.a.Retired(f.resources.Startup); err != nil {
			t.Fatal(err)
		}
		if err = opening.bindCheckpointKeeper(f.keeper, f.g, f.a, f.wal); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if f.reader != nil {
			if e := errors.Join(f.reader.Close(), f.a.Retired(f.readerGrant)); e != nil {
				t.Error(e)
			}
		}
		err := opening.close()
		if keep {
			if err != nil && !errors.Is(err, ErrBusy) {
				t.Error(err)
			}
			request := f.shutdownRequest("cp-shutdown")
			decision := f.keeper.AdmitShutdown(request)
			if decision.Grant == nil {
				t.Error("shutdown", decision.Refusal)
				return
			}
			if e := f.keeper.Close(decision.Grant, request); e != nil {
				t.Error(e)
				return
			}
			if e := errors.Join(f.a.Retired(decision.Grant), f.a.Retired(f.resources.Lifetime), opening.close()); e != nil {
				t.Error(e)
			}
		} else if err != nil {
			t.Error(err)
		}
	})
	if keep && !screening {
		readerLife := governor.Request{Key: governor.JobKey{Domain: 1, ID: "measurement-reader"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 4 << 20, governor.CPUMilli: 1000, governor.IOBytes: 4 << 20}, Deadline: f.now().Add(time.Hour)}
		f.readerGrant = f.mustGrant(t, readerLife)
		readCfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 65536, Columns: 128}}
		readConnector, e := sqliteconn.Connector(path, readCfg, sqliteconn.ExistingFile)
		if e != nil {
			t.Fatal(e)
		}
		registry, e := stmttest.New(t, []stmt.Definition{{ID: "cp-revision", SQL: "SELECT revision FROM search_revision WHERE singleton=1", Kind: stmt.Read, Owner: "checkpoint-component", CallSites: []string{"checkpoint_component_test.go"}, Cohorts: []stmt.Cohort{{Name: "scalar"}}}})
		if e != nil {
			t.Fatal(e)
		}
		f.reader, err = governor.NewSnapshots(readConnector, readCfg, f.g, f.a, registry)
		if err != nil {
			t.Fatal(err)
		}
	}
	return f
}
func (f *checkpointComponent) mustGrant(t *testing.T, r governor.Request) *governor.Grant {
	t.Helper()
	d := f.g.Try(r)
	if d.Grant == nil {
		t.Fatal(d.Refusal)
	}
	return d.Grant
}
func (f *checkpointComponent) shutdownRequest(key string) governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 1, ID: key}, Owner: 1, Class: governor.Recovery, Need: governor.Amounts{governor.MemoryBytes: 64 << 10, governor.CPUMilli: 1000, governor.IOBytes: 2 * f.policy.HardBytes}, Deadline: f.now().Add(time.Hour)}
}
func (f *checkpointComponent) cpRequest() governor.Request {
	r := f.shutdownRequest("cp-step")
	r.Need[governor.Reader] = 1
	r.IO = []governor.DeviceIO{{DeviceID: fmt.Sprintf("filesystem-device:%d", f.opening.binding.device), Class: governor.Sequential, Outstanding: 1, Bytes: r.Need[governor.IOBytes]}}
	r.MaxHold = 5 * time.Second
	r.Budget = governor.UnitBudget{RawRows: 1, VMOps: 10000000, Pages: uint32(2*(f.policy.HardBytes/4120) + 1)}
	return r
}
func (f *checkpointComponent) fill(t *testing.T, target int64) governor.WALState {
	t.Helper()
	hardCell := target == f.policy.HardBytes
	if hardCell {
		target -= 256 << 10
	} // explicit bounded gap; never overshoot hard allocation
	payload := strings.Repeat("x", 3072)
	for first := 0; first < 60000; first += 32 {
		tx, err := f.w.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 32; j++ {
			if _, err = tx.ExecContext(t.Context(), "INSERT INTO admin_documents VALUES(?,1,?,0)", fmt.Sprintf("cp-%08d", first+j), payload); err != nil {
				t.Fatal(err)
			}
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		state, err := governor.ReadWAL(f.path)
		if err != nil {
			t.Fatal(err)
		}
		if state.PhysicalBytes > f.policy.HardBytes {
			t.Fatal("fixture overshot hard cap", state)
		}
		if state.UncheckpointedBytes >= target {
			if hardCell {
				// Fixture-only hot-page writes fill the FINAL frame slots after
				// the realistic distributed prefix; never cross the unchanged cap.
				maxFrames := (f.policy.HardBytes - 32) / 4120
				for step := 0; int64(state.Frames) < maxFrames && step < 128; step++ {
					if _, err = f.w.db.ExecContext(t.Context(), "UPDATE search_revision SET revision=revision+1 WHERE singleton=1"); err != nil {
						t.Fatal(err)
					}
					state, err = governor.ReadWAL(f.path)
					if err != nil {
						t.Fatal(err)
					}
					if state.PhysicalBytes > f.policy.HardBytes {
						t.Fatal("final frame overshot cap", state)
					}
				}
				if f.policy.HardBytes-state.PhysicalBytes >= 4120 {
					t.Fatal("hard-cap fixture did not fill final frame slots", state)
				}
			}
			return state
		}
	}
	t.Fatal("finite fixture did not reach prefix")
	return governor.WALState{}
}

type checkpointTurnSample struct {
	Request                                governor.Request
	State                                  commitState
	ChangedRows                            *int64
	Started, Returned, Accepted, RefusedAt time.Time
	HoldNS                                 *int64
	Refusal                                *governor.Refusal
	Error                                  string
	Window                                 *checkpointOwnerWindow
	Closes                                 []n1NativeClose
	Discard                                bool
	PayloadBytes                           int
}

// The turn's 25 ms is the MaxHold TARGET carried by the recipe. The caller
// deadline is the 3 s runtime safety ceiling: a slow COMMIT after a reset (host
// writeback stall) must not be cancelled mid-COMMIT into an uncertain outcome.
const checkpointTurnCallerDeadline = 3 * time.Second

func (f *checkpointComponent) turn(t *testing.T, discard bool, payload int) (out checkpointTurnSample) {
	t.Helper()
	out.Started = time.Now()
	out.Discard = discard
	out.PayloadBytes = payload
	defer func() {
		out.Returned = time.Now()
		if view, ok := f.w.lastOwnerWindow(); ok && !view.setupEnteredAt.Before(out.Started) {
			out.Window = &checkpointOwnerWindow{view.setupEnteredAt, view.terminalEnteredAt, view.terminalReturnedAt, view.nativeReturnedAt, view.custodyRetiredAt, view.cleanupComplete, view.retirementComplete}
			n := view.terminalReturnedAt.Sub(view.setupEnteredAt).Nanoseconds()
			out.HoldNS = &n
		}
		out.Closes = append([]n1NativeClose(nil), f.trace.closes[:f.trace.closeCount]...)
	}()
	if err := refreshWriterWAL(f.path, f.wal, f.now); err != nil {
		out.Error = err.Error()
		return
	}
	risk, err := f.wal.RestartRisk(f.now())
	if err != nil {
		out.Error = err.Error()
		return
	}
	recipe := turnRecipe{limit: Work{4096, 1000000, 16384, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, 8 * time.Millisecond}}
	r := governor.Request{Key: governor.JobKey{Domain: uint16(ProgressAccept), ID: "cp-overlap"}, Owner: 1, Class: governor.Foreground, Writer: true, Need: governor.Amounts{governor.MemoryBytes: int64(4<<20) + 3*int64(payload), governor.CPUMilli: 1000, governor.IOBytes: int64(recipe.limit.WALBytes) * 2, governor.WALBytes: int64(recipe.limit.WALBytes)}, Budget: governor.UnitBudget{RawRows: 4096, VMOps: 1000000, Pages: 16384}, MaxHold: recipe.limit.Hold, Deadline: f.now().Add(checkpointTurnCallerDeadline), RestartRisk: risk}
	r.IO = []governor.DeviceIO{{DeviceID: fmt.Sprintf("filesystem-device:%d", f.opening.binding.device), Class: governor.Seeking, Outstanding: 1, Bytes: r.Need[governor.IOBytes]}}
	out.Request = r
	decision := f.g.Try(r)
	if decision.Grant == nil {
		out.RefusedAt = time.Now()
		out.Refusal = decision.Refusal
		return
	}
	out.Accepted = time.Now()
	custody, err := bindGovernedTurn(ProgressAccept, recipe.limit, f.g, f.a, f.wal, decision.Grant, r)
	if err != nil {
		out.Error = errors.Join(err, f.a.Retired(decision.Grant)).Error()
		return
	}
	tx, err := f.w.begin(t.Context(), ProgressAccept, recipe, custody)
	if err != nil {
		out.Error = err.Error()
		if errors.Is(err, ErrBusy) {
			_ = custody.retired()
		}
		return
	}
	f.trace.armed = true
	if discard {
		f.trace.failExec = "ROLLBACK"
	}
	if err = tx.before(Work{1, 100000, uint64(payload/4096 + 32), uint64(payload*2 + 65536), time.Millisecond}); err == nil {
		result, execErr := tx.conn.ExecContext(tx.ctx, "UPDATE admin_documents SET body=? WHERE scope='cp-00000000'", strings.Repeat("y", payload))
		err = execErr
		if result != nil {
			n, e := result.RowsAffected()
			err = errors.Join(err, e)
			out.ChangedRows = &n
			if n != 1 {
				err = errors.Join(err, ErrConflict)
			}
		}
	}
	if discard || err != nil {
		physical, finishErr := tx.rollback(err)
		out.State = physical.state
		err = finishErr
	} else {
		physical, finishErr := tx.commit()
		out.State = physical.state
		err = finishErr
	}
	f.trace.armed = false
	f.trace.failExec = ""
	out.Error = checkpointError(err)
	return
}
func checkpointError(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
func logCheckpointComponent(t *testing.T, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("GV_KEEPER_CONCURRENT_V1 %s", b)
}

// Signal only, never delays physical IO. Actual returned intervals decide
// whether a writer overlapped a native checkpoint, not channel ordering alone.
type checkpointSignalConnector struct {
	driver.Connector
	entered chan struct{}
}

func (c *checkpointSignalConnector) Connect(ctx context.Context) (driver.Conn, error) {
	raw, e := c.Connector.Connect(ctx)
	if e != nil {
		return nil, e
	}
	return &checkpointSignalConn{Conn: raw, owner: c}, nil
}

type checkpointSignalConn struct {
	driver.Conn
	owner *checkpointSignalConnector
}

func (c *checkpointSignalConn) SQLiteDQS() (int, int, error) {
	return c.Conn.(interface{ SQLiteDQS() (int, int, error) }).SQLiteDQS()
}
func (c *checkpointSignalConn) SQLiteMainFileIdentity() (uint64, uint64, error) {
	return c.Conn.(interface {
		SQLiteMainFileIdentity() (uint64, uint64, error)
	}).SQLiteMainFileIdentity()
}
func (c *checkpointSignalConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	s, e := c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
	if e == nil && q == "PRAGMA wal_checkpoint(PASSIVE)" {
		return &checkpointSignalStmt{Stmt: s, owner: c.owner}, nil
	}
	return s, e
}

type checkpointSignalStmt struct {
	driver.Stmt
	owner *checkpointSignalConnector
}

func (s *checkpointSignalStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	select {
	case <-s.owner.entered:
	default:
		close(s.owner.entered)
	}
	return s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
}

type checkpointOwnerWindow struct {
	SetupEntered, TerminalEntered, TerminalReturned, NativeReturned, CustodyRetired time.Time
	CleanupComplete, RetirementComplete                                             bool
}

// Failed PRAGMA Scan values are unknown, never invented zeros.
type checkpointMeasuredResult struct {
	governor.CheckpointResult
	Busy, Frames, Backfilled *int
}

func measuredCheckpoint(out governor.CheckpointResult, err error) checkpointMeasuredResult {
	r := checkpointMeasuredResult{CheckpointResult: out}
	if err == nil && out.Refusal == nil && !out.NativeEnded.IsZero() {
		r.Busy = &out.Busy
		r.Frames = &out.Frames
		r.Backfilled = &out.Backfilled
	}
	return r
}
func checkpointInterval(start, end time.Time) *int64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return nil
	}
	n := end.Sub(start).Nanoseconds()
	return &n
}

func TestKeeperCompositionDiscardKeepsRealConnectionAndFileLease(t *testing.T) {
	f := newCheckpointComponent(t, true, false)
	f.fill(t, 1<<20)
	sample := f.turn(t, true, 16<<10)
	if !n1OnlyExpectedDiscard(sample.Error) || sample.HoldNS == nil || sample.Window == nil || !sample.Window.CleanupComplete || len(sample.Closes) != 1 {
		t.Fatal(sample)
	}
	state, err := governor.ReadWAL(f.path)
	if err != nil || state.PhysicalBytes == 0 {
		t.Fatal("discard became last-close", state, err)
	}
	if err = f.opening.close(); !errors.Is(err, ErrBusy) {
		t.Fatal("file ownership released before keeper", err)
	}
	if f.opening.closed {
		t.Fatal("file lease lost before keeper shutdown")
	}
}

// Matched whole-frame discard comparison; no real-time assertion in default CI.
// The8ms synthetic terminal reserve is NOT a bound. Every overrun is retained.
func TestDiagnosticKeeperDiscard(t *testing.T) {
	if os.Getenv("PORTICO_GV_NATIVE_TIMING") != "1" {
		t.Skip("opt-in native component timing")
	}
	ioCap := os.Getenv("PORTICO_SANDBOX_IO_BYTES_PER_SEC")
	if ioCap == "" {
		t.Fatal("declare sandbox IO cap or unavailable")
	}
	for _, cell := range []struct {
		name    string
		prefix  int64
		payload int
	}{{"1MiB", 1 << 20, 256 << 10}, {"64MiB", 64 << 20, 8 << 20}, {"MaxAdmittedPrefix", 159 << 20, 8 << 20}} {
		t.Run(cell.name, func(t *testing.T) {
			for _, keep := range []bool{false, true} {
				name := "last_close"
				if keep {
					name = "keeper"
				}
				t.Run(name, func(t *testing.T) {
					f := newCheckpointComponent(t, keep, true)
					before := f.fill(t, cell.prefix)
					beforeAllocated := checkpointAllocated(f.path)
					sample := f.turn(t, true, cell.payload)
					after, afterErr := governor.ReadWAL(f.path)
					logCheckpointComponent(t, map[string]any{"Kind": "discard", "Cell": cell.name, "Keeper": keep, "Qualification": false, "OriginUTC": f.origin, "DatabasePath": f.path, "PID": os.Getpid(), "BeforeAllocatedBytes": beforeAllocated, "SandboxIOBytesPerSec": ioCap, "Before": before, "Writer": sample, "After": after, "AfterError": checkpointError(afterErr), "SyncCopy": nil, "SyncCopyReason": "external syscall trace required; native Close interval present, no invented xSync/xWrite attribution"})
					if !n1OnlyExpectedDiscard(sample.Error) || sample.HoldNS == nil {
						t.Error("retain failed/overrun discard", sample.Error, sample.Refusal)
					}
				})
			}
		})
	}
}

func checkpointAllocated(path string) *int64 {
	info, err := os.Stat(path + "-wal")
	if err != nil {
		return nil
	}
	v := reflect.ValueOf(info.Sys())
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return nil
	}
	blocks := v.Elem().FieldByName("Blocks")
	if !blocks.IsValid() || !blocks.CanInt() || blocks.Int() < 0 || blocks.Int() > (1<<63-1)/512 {
		return nil
	}
	n := blocks.Int() * 512
	return &n // null explicitly means OS allocation unavailable, never RSS
}
