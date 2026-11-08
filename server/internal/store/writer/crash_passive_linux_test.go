package writer

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

type passiveCrashProof struct {
	Before                                    governor.WALState
	ReadMark                                  uint32
	Pinned                                    bool
	Native                                    []int64 // actual native PRAGMA row, never CheckpointResult
	PoolValidated, GrantRetired, NativeClosed bool
}

type passiveCrashTrace struct {
	t                           *testing.T
	witness                     *crashWitness
	proof                       *passiveCrashProof
	authority                   *governor.Authority
	armed, nativeSeen, returned atomic.Bool
	clocks                      atomic.Int32
}

func (s *passiveCrashTrace) point(phase string) {
	if s.armed.Load() {
		crashPoint(s.t, s.witness, phase)
	}
}

// Clock values never depend on a hook: only the declared fixture clock is
// returned. Identify the private step defer's post-pool observations, without
// inspecting a ledger while its own clock callback holds the ledger lock.
func (s *passiveCrashTrace) observeClock() {
	if !s.armed.Load() || !s.returned.Load() {
		return
	}
	pc, _, _, ok := runtime.Caller(2)
	if !ok {
		s.t.Fatal("checkpoint clock caller missing")
	}
	fn := runtime.FuncForPC(pc)
	if fn == nil || !strings.Contains(fn.Name(), "governor.(*Checkpointer).step.func") {
		return
	}
	switch s.clocks.Add(1) {
	case 1:
		if s.authority.Usage()[governor.Recovery] == (governor.Amounts{}) {
			s.t.Fatal("checkpoint refunded custody before pool return")
		}
		s.point("pool_returned")
	case 2:
		if s.authority.Usage() != ([4]governor.Amounts{}) {
			s.t.Fatal("checkpoint grant did not retire after pool return")
		}
		s.proof.GrantRetired = true
		s.point("grant_retired")
	case 3:
		s.point("before_sample")
	case 4: // observation time passed to the actual WALGate.Publish
	case 5:
		s.point("sample_returned")
	default:
		s.t.Fatal("checkpoint clock finish chain changed")
	}
}

type passiveCrashConn struct {
	writerDriverConn
	trace *passiveCrashTrace
}

func (c *passiveCrashConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.writerDriverConn.QueryContext(ctx, q, args)
	if err != nil || q != "PRAGMA wal_checkpoint(PASSIVE)" {
		return rows, err
	}
	return &passiveCrashRows{Rows: rows, trace: c.trace}, nil
}

func (c *passiveCrashConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	statement, err := c.writerDriverConn.PrepareContext(ctx, q)
	if err != nil || q != "PRAGMA wal_checkpoint(PASSIVE)" {
		return statement, err
	}
	return &passiveCrashStmt{Stmt: statement, trace: c.trace}, nil
}

type passiveCrashStmt struct {
	driver.Stmt
	trace *passiveCrashTrace
}

func (s *passiveCrashStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
	if err != nil {
		return nil, err
	}
	return &passiveCrashRows{Rows: rows, trace: s.trace}, nil
}
func (s *passiveCrashStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.Stmt.(driver.StmtExecContext).ExecContext(ctx, args)
}

type passiveCrashRows struct {
	driver.Rows
	trace *passiveCrashTrace
}

func (r *passiveCrashRows) Next(values []driver.Value) error {
	err := r.Rows.Next(values)
	if err == nil {
		if len(values) != 3 || r.trace.nativeSeen.Swap(true) {
			r.trace.t.Fatal("unexpected native PASSIVE result shape")
		}
		r.trace.proof.Native = make([]int64, 3)
		for i, v := range values {
			n, ok := v.(int64)
			if !ok {
				r.trace.t.Fatal("PASSIVE returned non-integer native value", v)
			}
			r.trace.proof.Native[i] = n
		}
	}
	return err
}

func (c *passiveCrashConn) IsValid() bool {
	valid := c.writerDriverConn.IsValid()
	if c.trace.nativeSeen.Load() {
		if !valid {
			c.trace.t.Fatal("checkpoint pool refused physical return")
		}
		c.trace.proof.PoolValidated = true
		c.trace.point("before_pool_return")
		c.trace.returned.Store(true)
	}
	return valid
}

func (c *passiveCrashConn) Close() error {
	c.trace.point("before_native_close")
	err := c.writerDriverConn.Close()
	if err == nil {
		c.trace.proof.NativeClosed = true
		c.trace.point("after_native_close")
	}
	return err
}

// This pinned native reader is a test input/sentinel, not a foreground service
// or a read-admission/memory qualification. It has no domain write authority.
func pinPassiveCrashReader(t *testing.T, path string) func() {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	connector, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	e := &executor{config: cfg, now: func() time.Time { return crashClock }}
	e.openPool(connector, nil)
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqliteconn.Assert(t.Context(), conn, cfg); err != nil {
		t.Fatal(err)
	}
	if err := schema.AssertExisting(t.Context(), conn); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var floor int64
	if err := tx.QueryRowContext(t.Context(), "SELECT acknowledged_command_sequence FROM metadata_provider_operations WHERE id='operation-one'").Scan(&floor); err != nil || floor != 0 {
		t.Fatal("sentinel did not see original pre-ACK snapshot", floor, err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			if err := tx.Rollback(); err != nil {
				t.Error(err)
			}
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
			if err := e.close(); err != nil {
				t.Error(err)
			}
			if e.db.Stats().OpenConnections != 0 {
				t.Error("sentinel native reader did not join")
			}
		})
	}
	t.Cleanup(release)
	return release
}

func passiveCrashBackfill(proof passiveCrashProof) uint32 {
	if proof.Pinned {
		return proof.ReadMark
	}
	return proof.Before.Frames
}

// Read only the nonempty, stable fixture format directly. Expected bytes must
// not be supplied by the production ReadWAL parser used by Checkpointer.Step.
// Empty/restarted/torn formats are outside these cells and fail explicitly.
func readPassiveJournalWAL(path string) (governor.WALState, error) {
	read := func(name string, count int) ([]byte, int64, error) {
		file, err := os.Open(name)
		if err != nil {
			return nil, 0, err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return nil, 0, err
		}
		header := make([]byte, count)
		_, err = file.ReadAt(header, 0)
		return header, info.Size(), err
	}
	db, _, err := read(path, 100)
	if err != nil {
		return governor.WALState{}, err
	}
	wal, size, err := read(path+"-wal", 32)
	if err != nil {
		return governor.WALState{}, err
	}
	index, _, err := read(path+"-shm", 100)
	if err != nil {
		return governor.WALState{}, err
	}
	stable, _, err := read(path+"-shm", 100)
	if err != nil {
		return governor.WALState{}, err
	}
	page := uint32(binary.BigEndian.Uint16(db[16:18]))
	if page == 1 {
		page = 65536
	}
	indexPage := uint32(binary.NativeEndian.Uint16(index[14:16]))
	if indexPage == 1 {
		indexPage = 65536
	}
	magic := binary.BigEndian.Uint32(wal[:4])
	if string(db[:16]) != "SQLite format 3\x00" || db[18] != 2 || db[19] != 2 ||
		page < 512 || page > 65536 || page&(page-1) != 0 || indexPage != page ||
		(magic != 0x377f0682 && magic != 0x377f0683) || binary.BigEndian.Uint32(wal[4:8]) != 3007000 ||
		binary.BigEndian.Uint32(wal[8:12]) != page || binary.NativeEndian.Uint32(index[:4]) != 3007000 || index[12] != 1 ||
		!bytes.Equal(index, stable) || !bytes.Equal(index[:48], index[48:96]) || !bytes.Equal(wal[16:24], index[32:40]) {
		return governor.WALState{}, errors.New("unsupported original PASSIVE WAL/index format")
	}
	state := governor.WALState{PhysicalBytes: size, PageSize: page,
		Frames: binary.NativeEndian.Uint32(index[16:20]), Backfill: binary.NativeEndian.Uint32(index[96:100])}
	copy(state.Salt[:], wal[16:24])
	state.ActiveBytes = 32 + int64(state.Frames)*(int64(page)+24)
	state.UncheckpointedBytes = int64(state.Frames-state.Backfill) * (int64(page) + 24)
	if state.Frames == 0 || state.Backfill > state.Frames || state.ActiveBytes > size {
		return governor.WALState{}, errors.New("unsupported original PASSIVE WAL prefix")
	}
	return state, nil
}

func passiveExpectedWAL(before governor.WALState, backfill uint32) (governor.WALState, error) {
	if before.Frames == 0 || before.Backfill > backfill || backfill > before.Frames {
		return governor.WALState{}, errors.New("unsupported PASSIVE prefix")
	}
	before.Backfill = backfill
	before.UncheckpointedBytes = int64(before.Frames-backfill) * (int64(before.PageSize) + 24)
	return before, nil
}

func comparePassiveResult(proof passiveCrashProof, request governor.Request, sequence uint64, at time.Time, got governor.CheckpointResult) error {
	backfill := passiveCrashBackfill(proof)
	after, err := passiveExpectedWAL(proof.Before, backfill)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(proof.Native, []int64{0, int64(proof.Before.Frames), int64(backfill)}) {
		return fmt.Errorf("native PASSIVE row differs from original prefix/read mark: %+v", proof)
	}
	want := governor.CheckpointResult{
		Requested: at, EligibleInterval: 1500 * time.Millisecond, Sequence: sequence, Key: request.Key, Budget: request.Budget, MaxHold: request.MaxHold,
		IOBytes: request.Need[governor.IOBytes], PrefixLimit: 1 << 29,
		Started: at, NativeStarted: at, NativeEnded: at, PhysicalReturned: at, RetiredAt: at, SampleStarted: at, SampleEnded: at,
		Busy: 0, Frames: int(proof.Before.Frames), Backfilled: int(backfill), Before: proof.Before, After: after,
		Settings:        sqliteconn.Settings{Engine: schema.NativeSQLiteVersion, JournalMode: "wal", ForeignKeys: 1, Synchronous: 1, CacheSize: -1024, TempStore: 1},
		DatabaseMatched: true, PhysicalSafe: true,
	}
	if !reflect.DeepEqual(want, got) {
		return fmt.Errorf("complete PASSIVE result changed: got %+v, want %+v", got, want)
	}
	return nil
}

var passiveCrashPhases = []string{"before_checkpoint", "after_checkpoint", "before_pool_return", "pool_returned", "grant_retired", "before_sample", "sample_returned", "checkpoint_return", "before_native_close", "after_native_close", "close_return"}

func passiveCaseValid(c crashCase) bool {
	if c.Domain != "passive_native" || c.CommitFault != "" || c.Operation != "free" && c.Operation != "pinned" && c.Operation != "resumed" {
		return false
	}
	if c.Operation != "free" {
		return c.Boundary == "checkpoint_return"
	}
	for _, phase := range passiveCrashPhases {
		if c.Boundary == phase {
			return true
		}
	}
	return false
}

func passiveCrashRequest(at time.Time) governor.Request {
	r := governor.Request{Key: governor.JobKey{Domain: 1, ID: "passive-crash"}, Owner: 1, Class: governor.Recovery,
		Budget: governor.UnitBudget{RawRows: 1, VMOps: 10000, Pages: 32768}, MaxHold: 25 * time.Millisecond, Deadline: at.Add(time.Second)}
	r.Need[governor.Reader], r.Need[governor.MemoryBytes], r.Need[governor.CPUMilli], r.Need[governor.IOBytes] = 1, 64<<10, 100, 64<<20
	return r
}

func crashPassiveFixture(t *testing.T, witness *crashWitness) (*effectiveFirstLightFixture, governor.CheckpointResult) {
	t.Helper()
	if witness.Case.Boundary != "trace" && !passiveCaseValid(witness.Case) {
		t.Fatal("unsupported PASSIVE cell", witness.Case)
	}
	source := crashWitness{Case: crashCase{"effective_steps", "stage", "trace", ""}}
	f, _ := crashEffectiveStepsFixture(t, &source)
	witness.Path, witness.Reference, witness.Effective = f.path, source.Reference, source.Effective
	mark, err := readPassiveJournalWAL(f.path)
	if err != nil || mark.Frames == 0 || mark.Backfill != 0 {
		t.Fatal("fixture lacks original dirty WAL", mark, err)
	}
	var release func()
	if witness.Case.Operation != "free" {
		release = pinPassiveCrashReader(t, f.path)
	}
	ack, err := f.writer.AckEffective(t.Context(), f.disclosure, effectiveReference(f.command))
	if err != nil || ack.AcknowledgedThrough != 1 {
		t.Fatal(ack, err)
	}
	want, _, err := effectiveStepExpectation(f.command, source.Effective, true, true)
	if err != nil {
		t.Fatal(err)
	}
	current, err := readEffectiveJournal(t.Context(), f.executor.db, effectiveJournalTables)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareEffectiveJournal(want, current); err != nil {
		t.Fatal(err)
	}
	proof := &passiveCrashProof{ReadMark: mark.Frames, Pinned: witness.Case.Operation != "free"}
	witness.Passive = proof
	clock := atomic.Int64{}
	clock.Store(crashClock.UnixNano())
	trace := &passiveCrashTrace{t: t, witness: witness, proof: proof}
	f.executor.now = func() time.Time { trace.observeClock(); return time.Unix(0, clock.Load()).UTC() }
	g, authority, wal := crashGovernor(t, f.executor, f.writer.effective.plan)
	trace.authority = authority
	state, err := readPassiveJournalWAL(f.path)
	if err != nil || state.Frames <= mark.Frames || state.Salt != mark.Salt {
		t.Fatal("real ACK did not advance original WAL", state, mark, err)
	}
	if err := wal.Publish(wal.Observation(), state, f.executor.now()); err != nil {
		t.Fatal(err)
	}
	cfg := sqliteconn.Config{Role: sqliteconn.Checkpointer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	connector, err := sqliteconn.Connector(f.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The reviewed wrapper still only delegates to this exact shared Connector.
	nativeWitness := *witness
	nativeWitness.Case.Boundary = "disabled_setup"
	cp, err := governor.NewCheckpointer(crashCheckpointConnector{Connector: connector, t: t, witness: &nativeWitness,
		wrap: func(conn writerDriverConn) writerDriverConn {
			return &passiveCrashConn{writerDriverConn: conn, trace: trace}
		}}, cfg, g, authority,
		governor.CheckpointPolicy{DatabasePath: f.path, Interval: 50 * time.Millisecond, MaxHold: 25 * time.Millisecond, MaxWait: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	request := func() governor.Request { return passiveCrashRequest(f.executor.now()) }
	sequence := uint64(1)
	if witness.Case.Operation == "resumed" {
		proof.Before = state
		firstRequest := request()
		first, err := cp.Step(t.Context(), firstRequest)
		if err != nil {
			t.Fatal(err)
		}
		if err := comparePassiveResult(*proof, firstRequest, 1, crashClock, first); err != nil {
			t.Fatal(err)
		}
		if first.Backfilled >= first.Frames {
			t.Fatal("sentinel failed to hold a real read mark")
		}
		release()
		proof.Pinned, proof.Native, proof.PoolValidated = false, nil, false
		trace.nativeSeen.Store(false)
		trace.returned.Store(false)
		trace.clocks.Store(0)
		clock.Add(int64(1500 * time.Millisecond)) // declared cadence, no sleep or production policy change
		sequence = 2
	}
	proof.Before, err = readPassiveJournalWAL(f.path)
	if err != nil {
		t.Fatal(err)
	}
	nativeWitness.Case = witness.Case
	trace.armed.Store(true)
	r := request()
	out, err := cp.Step(t.Context(), r)
	if err != nil {
		t.Fatal(out, err)
	}
	if err := comparePassiveResult(*proof, r, sequence, f.executor.now(), out); err != nil {
		t.Fatal(err)
	}
	if trace.clocks.Load() != 5 || !proof.PoolValidated || !proof.GrantRetired || authority.Usage() != ([4]governor.Amounts{}) || wal.ResetCount() != 0 {
		t.Fatal("PASSIVE finish/custody/reset witness missing", trace.clocks.Load(), *proof)
	}
	crashPoint(t, witness, "checkpoint_return")
	if err := cp.Close(); err != nil {
		t.Fatal(err)
	}
	if !proof.NativeClosed {
		t.Fatal("pool close did not join the actual native connection")
	}
	crashPoint(t, witness, "close_return")
	if witness.Case.Boundary != "trace" {
		t.Fatal("PASSIVE boundary not reached", witness.Case)
	}
	return f, out
}

func verifyPassiveCrash(t *testing.T, command *effectiveStageBeginCommand, witness crashWitness) {
	t.Helper()
	if !passiveCaseValid(witness.Case) || witness.Passive == nil || witness.Reference != effectiveReference(command).Receipt {
		t.Fatal("unsupported/missing PASSIVE witness", witness)
	}
	proof := *witness.Passive
	pool, retired, closed := false, false, false
	switch witness.Case.Boundary {
	case "before_checkpoint", "after_checkpoint":
	case "before_pool_return", "pool_returned":
		pool = true
	case "grant_retired", "before_sample", "sample_returned", "checkpoint_return", "before_native_close":
		pool, retired = true, true
	case "after_native_close", "close_return":
		pool, retired, closed = true, true, true
	default:
		t.Fatal("unsupported PASSIVE custody phase")
	}
	if proof.PoolValidated != pool || proof.GrantRetired != retired || proof.NativeClosed != closed {
		t.Fatal("PASSIVE custody witness does not match actual boundary", witness.Case, proof)
	}
	wantBackfill := passiveCrashBackfill(proof)
	if witness.Case.Boundary == "before_checkpoint" {
		wantBackfill = proof.Before.Backfill
	}
	wantWAL, err := passiveExpectedWAL(proof.Before, wantBackfill)
	if err != nil {
		t.Fatal(err)
	}
	// Observe BEFORE any new SQLite opener can recover/checkpoint the survivor.
	gotWAL, err := readPassiveJournalWAL(witness.Path)
	if err != nil || gotWAL != wantWAL {
		t.Fatal("native PASSIVE progress changed", gotWAL, wantWAL, err)
	}
	if witness.Case.Boundary != "before_checkpoint" && !reflect.DeepEqual(proof.Native, []int64{0, int64(proof.Before.Frames), int64(passiveCrashBackfill(proof))}) {
		t.Fatal("native PASSIVE row witness missing", proof)
	}
	e := reopenCrashExecutor(t, witness.Path)
	want, _, err := effectiveStepExpectation(command, witness.Effective, true, true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readEffectiveJournal(t.Context(), e.db, effectiveJournalTables)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareEffectiveJournal(want, got); err != nil {
		t.Fatal(err)
	}
	w, cap, _, _, a := bindEffectiveFixtureRuntime(t, e, command, e.config)
	if cap.scope.fixture != nil {
		t.Fatal("PASSIVE reopen gained source permission")
	}
	out, err := w.ResolveEffective(t.Context(), cap, effectiveReference(command))
	if err != nil {
		t.Fatal(err)
	}
	out.Outcome.Evidence = nil
	if out != (EffectiveStageResult{Outcome: ReceiptOutcome{State: ResultRetired, Reference: witness.Reference}}) || a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("PASSIVE recovery changed retained history/custody", out)
	}
}

func TestCrashPassiveRejectsCorruption(t *testing.T) {
	witness := crashWitness{Case: crashCase{"passive_native", "free", "trace", ""}}
	f, result := crashPassiveFixture(t, &witness)
	defer func() {
		if joined, err := closeEffectiveFirstLightFixture(f); err != nil || !joined {
			t.Error(joined, err)
		}
	}()
	request := passiveCrashRequest(crashClock)
	for _, mutation := range []struct {
		name  string
		apply func(*governor.CheckpointResult)
	}{
		{"backfill", func(v *governor.CheckpointResult) { v.Backfilled = 0 }},
		{"frames", func(v *governor.CheckpointResult) { v.Frames++ }},
		{"busy", func(v *governor.CheckpointResult) { v.Busy = 1 }},
		{"sequence", func(v *governor.CheckpointResult) { v.Sequence++ }},
		{"physical", func(v *governor.CheckpointResult) { v.PhysicalSafe = false }},
		{"database", func(v *governor.CheckpointResult) { v.DatabaseMatched = false }},
		{"key", func(v *governor.CheckpointResult) { v.Key.ID = "WRONG-CHECKPOINT" }},
		{"prefix", func(v *governor.CheckpointResult) { v.PrefixLimit-- }},
		{"policy", func(v *governor.CheckpointResult) { v.Settings.ForeignKeys = 0 }},
		{"retirement", func(v *governor.CheckpointResult) { v.RetiredAt = time.Time{} }},
		{"sample", func(v *governor.CheckpointResult) { v.SampleEnded = time.Time{} }},
		{"salt_reset", func(v *governor.CheckpointResult) { v.After.Salt[0] ^= 1 }},
		{"debt", func(v *governor.CheckpointResult) { v.After.UncheckpointedBytes++ }},
		{"physical_bytes", func(v *governor.CheckpointResult) { v.After.PhysicalBytes-- }},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			got := result
			mutation.apply(&got)
			if err := comparePassiveResult(*witness.Passive, request, 1, crashClock, got); err == nil {
				t.Fatal("PASSIVE oracle accepted changed output")
			}
		})
	}
	want, _, err := effectiveStepExpectation(f.command, witness.Effective, true, true)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := f.executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	policy := f.executor.config
	policy.Synchronous = sqliteconn.Normal
	if _, err := sqliteconn.Assert(t.Context(), conn, policy); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []struct{ name, sql string }{
		{"native_receipt", `UPDATE enrichment_command_receipts SET result_digest=zeroblob(32)`},
		{"native_image", `UPDATE catalog_credit_images SET reclaim_state='ready'`},
		{"native_owner", `UPDATE metadata_provider_operations SET after_item='WRONG-CP-OWNER'`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			if _, err := conn.ExecContext(t.Context(), "SAVEPOINT corrupt_passive"); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := conn.ExecContext(t.Context(), "ROLLBACK TO corrupt_passive"); err != nil {
					t.Error(err)
				}
				if _, err := conn.ExecContext(t.Context(), "RELEASE corrupt_passive"); err != nil {
					t.Error(err)
				}
			}()
			if _, err := conn.ExecContext(t.Context(), mutation.sql); err != nil {
				t.Fatal("mutation failed before oracle", err)
			}
			got, err := readEffectiveJournal(t.Context(), conn, effectiveJournalTables)
			if err != nil {
				t.Fatal(err)
			}
			if err := compareEffectiveJournal(want, got); err == nil {
				t.Fatal("PASSIVE oracle accepted native data corruption")
			}
		})
	}
	for _, bad := range []crashCase{
		{"passive_native", "future_reset", "checkpoint_return", ""}, {"passive_native", "free", "inside_fsync", ""},
		{"passive_native", "pinned", "checkpoint_return", "after"},
	} {
		if passiveCaseValid(bad) {
			t.Fatal("unsupported PASSIVE semantics became a passing cell", bad)
		}
	}
}

func TestCrashReplayGovernedPassive(t *testing.T) {
	if raceDetector {
		t.Skip("crash replay is covered without -race; race child matrix exceeds the shard budget")
	}
	for _, operation := range []string{"free", "pinned", "resumed"} {
		t.Run(operation, func(t *testing.T) {
			witness := crashWitness{Case: crashCase{"passive_native", operation, "trace", ""}}
			f, _ := crashPassiveFixture(t, &witness)
			command := f.command
			if joined, err := closeEffectiveFirstLightFixture(f); err != nil || !joined {
				t.Fatal(joined, err)
			}
			phases := passiveCrashPhases
			if operation != "free" {
				// Pool/close mechanics are identical; exercise those once. Only
				// the distinct pinned/resumed progress needs another crash.
				phases = []string{"checkpoint_return"}
			}
			for _, phase := range phases {
				t.Run(phase, func(t *testing.T) {
					verifyPassiveCrash(t, command, runCrashChild(t, crashCase{"passive_native", operation, phase, ""}))
				})
			}
		})
	}
}
