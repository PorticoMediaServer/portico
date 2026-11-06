package governor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// These are the existing Q WAL recipe's qualification inputs, NOT installed
// production defaults. The 64 MiB cell reaches its debt trigger without changing
// the hard cap, reserve, CP settings, spacing, or 25 ms whole-hold forecast.
var dirtyPrefixWALPolicy = WALPolicy{
	HardBytes: 192 << 20, ReserveBytes: 16 << 20,
	SoftDebtBytes: 64 << 20, ActivePressureBytes: 96 << 20,
	MaxSampleAge: time.Second,
}

// TestDiagnosticPassiveDirtyPrefix is opt-in component measurement, never a
// default CI timing assertion. Run one fresh cell/process, for example:
//
//	PORTICO_GV_NATIVE_TIMING=1 go test -json -count=1 -timeout=2m \
//	  -run '^TestDiagnosticPassiveDirtyPrefix/64MiB$' ./internal/store/governor
//
// Each GV_DIRTY_PREFIX_V1 log payload is JSON. Retain failed steps and unoffered
// second steps; do not retry to fill a cohort. CP pool first-use and already-
// backfilled second-use are separate populations. This indexed, noncanonical
// fixture is not a production writer, concurrent-writer, reset or service test.
func TestDiagnosticPassiveDirtyPrefix(t *testing.T) {
	if os.Getenv("PORTICO_GV_NATIVE_TIMING") != "1" {
		t.Skip("opt-in physical timing: PORTICO_GV_NATIVE_TIMING=1")
	}
	for _, cell := range []struct {
		name  string
		bytes int64
	}{{"1MiB", 1 << 20}, {"64MiB", 64 << 20}} {
		t.Run(cell.name, func(t *testing.T) {
			path, rows := dirtyPrefixFixture(t, cell.bytes)
			g, a, cp, advance := dirtyPrefixCheckpointer(t, path, true)
			for _, phase := range []string{"dirty", "backfilled"} {
				sample, err := measureDirtyPrefix(t.Context(), path, cell.name, phase, cell.bytes, rows, "real", g, a, cp)
				logDirtyPrefix(t, sample)
				if err != nil || sample.Result.Refusal != nil || !sample.Result.PhysicalSafe {
					if phase == "dirty" {
						logDirtyPrefix(t, map[string]any{"Kind": "unoffered", "Cell": cell.name, "Phase": "backfilled", "Reason": "dirty step failed; no retry", "Qualification": false})
					}
					t.Error("retain failed checkpoint sample", err, sample.Result.Refusal)
					return
				}
				if phase == "dirty" {
					advance(sample.Result.CheckpointResult)
				}
			}
		})
	}
}

type dirtyPrefixWAL struct {
	Started, Returned time.Time
	State             *WALState
	Error             string
	AllocatedBytes    *int64 // OS stat blocks*512 where available, not RSS
	AllocationError   string
}

// Explicit shadows ensure failed Scan fields are null, never misleading zeros.
// Actual DQS zeros are serialized even though Settings normally omits them.
type dirtyPrefixResult struct {
	CheckpointResult
	Busy, Frames, Backfilled *int
	Settings                 *struct {
		sqliteconn.Settings
		DQSDDL, DQSDML int
	}
}

type dirtyPrefixSample struct {
	Kind, Cell, Phase, Clock                           string
	Qualification, ConcurrentWriter                    bool
	TargetDirtyBytes                                   int64
	Rows                                               int
	WALPolicy                                          WALPolicy
	Request                                            Request
	Before, After                                      dirtyPrefixWAL // observers outside HOLD
	Result                                             dirtyPrefixResult
	HoldNS, NativeNS, PreNativeNS, PostNativeNS        *int64
	Error                                              string
	DeadlineExceeded, Overrun, FenceBefore, FenceAfter bool
	UsageAfter                                         [4]Amounts
}

func diagnosticPrefixResult(out CheckpointResult, err error) dirtyPrefixResult {
	r := dirtyPrefixResult{CheckpointResult: out}
	if err == nil && out.Refusal == nil && !out.NativeEnded.IsZero() {
		r.Busy, r.Frames, r.Backfilled = &out.Busy, &out.Frames, &out.Backfilled
	}
	if out.Settings.Engine != "" {
		r.Settings = &struct {
			sqliteconn.Settings
			DQSDDL, DQSDML int
		}{out.Settings, out.Settings.DQSDDL, out.Settings.DQSDML}
	}
	return r
}

func prefixInterval(start, end time.Time) *int64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return nil
	}
	n := end.Sub(start).Nanoseconds()
	return &n
}

func observeDirtyPrefix(path string) (out dirtyPrefixWAL) {
	out.Started = time.Now()
	defer func() { out.Returned = time.Now() }()
	state, err := ReadWAL(path)
	if err != nil {
		out.Error = err.Error()
	} else {
		out.State = &state
	}
	info, err := os.Stat(path + "-wal")
	if err != nil {
		out.AllocationError = err.Error()
		return out
	}
	stat := reflect.ValueOf(info.Sys())
	if stat.Kind() == reflect.Pointer && !stat.IsNil() && stat.Elem().Kind() == reflect.Struct {
		blocks := stat.Elem().FieldByName("Blocks")
		if blocks.IsValid() && blocks.CanInt() && blocks.Int() >= 0 && blocks.Int() <= (1<<63-1)/512 {
			n := blocks.Int() * 512
			out.AllocatedBytes = &n
		}
	}
	if out.AllocatedBytes == nil {
		out.AllocationError = "OS stat allocated bytes unavailable"
	}
	return out
}

func measureDirtyPrefix(ctx context.Context, path, cell, phase string, target int64, rows int, clock string, g Governor, a *Authority, cp *Checkpointer) (dirtyPrefixSample, error) {
	r := Request{Key: JobKey{Domain: 1, ID: "dirty-prefix-" + phase}, Owner: 1, Class: Recovery,
		Need:    Amounts{Reader: 1, MemoryBytes: 64 << 10, CPUMilli: 1000, IOBytes: 2 * dirtyPrefixWALPolicy.HardBytes},
		Budget:  UnitBudget{RawRows: 1, VMOps: 10000, Pages: uint32(2*dirtyPrefixWALPolicy.HardBytes/4096 + 1)},
		MaxHold: 25 * time.Millisecond, Deadline: g.(*ledger).config.Now().Add(time.Second)}
	sample := dirtyPrefixSample{Kind: "step", Cell: cell, Phase: phase, Clock: clock,
		TargetDirtyBytes: target, Rows: rows, WALPolicy: dirtyPrefixWALPolicy, Request: r,
		Before: observeDirtyPrefix(path), FenceBefore: g.(*ledger).state.Load()&1 != 0}
	out, err := cp.Step(ctx, r) // real serialized Step; no timing/preemption wrapper
	sample.Result = diagnosticPrefixResult(out, err)
	sample.HoldNS, sample.NativeNS = prefixInterval(out.Started, out.PhysicalReturned), prefixInterval(out.NativeStarted, out.NativeEnded)
	sample.PreNativeNS, sample.PostNativeNS = prefixInterval(out.Started, out.NativeStarted), prefixInterval(out.NativeEnded, out.PhysicalReturned)
	sample.After = observeDirtyPrefix(path) // independent even after an errored Scan
	sample.FenceAfter, sample.UsageAfter = g.(*ledger).state.Load()&1 != 0, a.Usage()
	sample.DeadlineExceeded, sample.Overrun = errors.Is(err, context.DeadlineExceeded), errors.Is(err, ErrCheckpointOverrun)
	if err != nil {
		sample.Error = err.Error()
	}
	return sample, err
}

func logDirtyPrefix(t *testing.T, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("GV_DIRTY_PREFIX_V1 %s", raw)
}

func prefixError(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

func dirtyPrefixFixture(t *testing.T, target int64) (string, int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dirty-prefix.sqlite")
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, settings, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		start := time.Now()
		err := db.Close()
		logDirtyPrefix(t, map[string]any{"Kind": "fixture_pool_close", "ElapsedNS": time.Since(start).Nanoseconds(), "Error": prefixError(err), "Qualification": false})
		if err != nil {
			t.Error(err)
		}
	})
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	settings, err = sqliteconn.Assert(t.Context(), conn, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Engine != "3.53.4" || settings.AutoCheckpoint != 0 || settings.TempStore != 1 {
		t.Fatal("fixture policy drift", settings)
	}
	// Indexed page-producing metadata, not repeated updates to one hot page.
	// Preparation is ungoverned fixture work, outside the measured Step.
	if _, err := conn.ExecContext(t.Context(), `CREATE TABLE checkpoint_source(id INTEGER PRIMARY KEY,library_id INTEGER NOT NULL,title TEXT NOT NULL,revision INTEGER NOT NULL,payload BLOB NOT NULL);
CREATE INDEX checkpoint_library ON checkpoint_source(library_id,id);
CREATE INDEX checkpoint_title ON checkpoint_source(title,id)`); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 3072)
	rows := 0
	for rows < 32768 { // finite, below local 100k-resident rule even in large cells
		if err := fillDirtyPrefixBatch(t.Context(), conn, rows, payload); err != nil {
			t.Fatal(err)
		}
		rows += 32
		state, err := ReadWAL(path)
		if err != nil {
			t.Fatal(err)
		}
		if state.UncheckpointedBytes >= target {
			if state.Backfill != 0 || state.UncheckpointedBytes >= target+(1<<20) || state.PhysicalBytes > dirtyPrefixWALPolicy.HardBytes {
				t.Fatal("dirty fixture outside requested envelope", state)
			}
			var actual int
			if err := conn.QueryRowContext(t.Context(), "SELECT count(*) FROM checkpoint_source").Scan(&actual); err != nil || actual != rows {
				t.Fatal("fixture row count", actual, rows, err)
			}
			logDirtyPrefix(t, map[string]any{"Kind": "fixture_ready", "Rows": actual, "TargetDirtyBytes": target, "WAL": observeDirtyPrefix(path), "WriterSettings": diagnosticPrefixResult(CheckpointResult{Settings: settings}, nil).Settings, "Qualification": false})
			return path, rows
		}
	}
	t.Fatal("finite fixture could not reach dirty prefix", target)
	return "", 0
}

func fillDirtyPrefixBatch(ctx context.Context, conn *sql.Conn, first int, payload []byte) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO checkpoint_source VALUES(?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for row := first + 1; row <= first+32; row++ {
		for i := range payload {
			payload[i] = byte((i*31 + row*17) % 251)
		}
		if _, err := stmt.ExecContext(ctx, row, row%16, fmt.Sprintf("title-%08d", row), 1, payload); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func dirtyPrefixCheckpointer(t *testing.T, path string, realClock bool) (Governor, *Authority, *Checkpointer, func(CheckpointResult)) {
	t.Helper()
	wal, err := NewWALGate(dirtyPrefixWALPolicy)
	if err != nil {
		t.Fatal(err)
	}
	clock := &testClock{}
	clock.ms.Store(time.Now().UnixMilli())
	now := clock.now
	if realClock {
		now = time.Now
	}
	cfg := Config{Now: now, MaxGrantsPerClass: 8, WAL: wal}
	for i := range cfg.Partitions {
		cfg.Partitions[i] = Amounts{Reader: 2, MemoryBytes: 4 << 20, CPUMilli: 1000, IOBytes: 2 * dirtyPrefixWALPolicy.HardBytes}
	}
	g, a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	connection := sqliteconn.Config{Role: sqliteconn.Checkpointer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	connector, err := sqliteconn.Connector(path, connection)
	if err != nil {
		t.Fatal(err)
	}
	policy := CheckpointPolicy{DatabasePath: path, Interval: 50 * time.Millisecond, MaxHold: 25 * time.Millisecond, MaxWait: 50 * time.Millisecond}
	var cp *Checkpointer
	if realClock {
		cp, err = NewCheckpointer(connector, connection, g, a, policy)
	} else {
		cp, err = functionalCheckpointer(connector, connection, g, a, policy)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		start := time.Now()
		err := cp.Close()
		logDirtyPrefix(t, map[string]any{"Kind": "checkpoint_close", "ElapsedNS": time.Since(start).Nanoseconds(), "Error": prefixError(err), "Usage": a.Usage(), "Qualification": false})
		if err != nil {
			t.Error(err)
		}
	})
	return g, a, cp, func(out CheckpointResult) {
		if realClock {
			time.Sleep(max(0, time.Until(out.Requested.Add(out.EligibleInterval)))) // outside HOLD, unchanged spacing
		} else {
			clock.ms.Add(out.EligibleInterval.Milliseconds())
		}
	}
}

func TestDirtyPrefixMeasurement(t *testing.T) {
	// Real indexed pages/Step/cleanup, virtual elapsed/deadlines: no noisy-host
	// physical speed requirement in the default functional suite.
	path, rows := dirtyPrefixFixture(t, 256<<10)
	g, a, cp, advance := dirtyPrefixCheckpointer(t, path, false)
	for _, phase := range []string{"dirty", "backfilled"} {
		sample, err := measureDirtyPrefix(t.Context(), path, "functional", phase, 256<<10, rows, "virtual-functional", g, a, cp)
		logDirtyPrefix(t, sample)
		if err != nil || sample.Result.Refusal != nil || !sample.Result.PhysicalSafe || sample.FenceAfter || sample.UsageAfter != [4]Amounts{} {
			t.Fatal("functional Step/retirement", sample, err)
		}
		before, after := sample.Before.State, sample.After.State
		if before == nil || after == nil || before.Frames == 0 || before.PhysicalBytes != after.PhysicalBytes || before.Salt != after.Salt || before.Frames != after.Frames || after.Backfill != after.Frames {
			t.Fatal("PASSIVE must backfill without truncating/resetting", sample)
		}
		if phase == "dirty" && before.UncheckpointedBytes < 256<<10 || phase == "backfilled" && before.UncheckpointedBytes != 0 {
			t.Fatal("different dirty/backfilled populations not established", sample)
		}
		if sample.HoldNS == nil || sample.NativeNS == nil || sample.Result.Settings == nil || sample.Result.Settings.DQSDDL != 0 || sample.Result.Settings.DQSDML != 0 {
			t.Fatal("missing durations or actual settings", sample)
		}
		advance(sample.Result.CheckpointResult)
	}
	unknown := diagnosticPrefixResult(CheckpointResult{}, context.DeadlineExceeded)
	raw, err := json.Marshal(unknown)
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &fields) != nil || string(fields["Frames"]) != "null" || string(fields["Backfilled"]) != "null" || string(fields["Busy"]) != "null" || string(fields["Settings"]) != "null" {
		t.Fatal("failed native Scan/settings must be null", string(raw), err)
	}
}
