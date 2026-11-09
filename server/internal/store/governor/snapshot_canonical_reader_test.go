package governor

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Exercise canonical-schema reparse and physical replacement through GV's
// existing shared-opener fault/observation hooks. No new connector or raw
// opening seam is needed for this fixed Reader lifecycle control.
func TestSnapshotCanonicalReader129Lifecycle(t *testing.T) {
	path, writer := snapshotFixture(t)
	if _, err := writer.ExecContext(t.Context(), "DROP TABLE source"); err != nil {
		t.Fatal(err)
	}
	if err := schema.InitializeEmpty(t.Context(), writer); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(t.Context(), "INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')"); err != nil {
		t.Fatal(err)
	}
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 65536, Columns: 129}}
	readerDB, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer readerDB.Close()
	reader, err := readerDB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	definitions := []stmt.Definition{{ID: "canonical.identity", SQL: "SELECT authority_generation FROM identity_state WHERE singleton=?", Owner: "canonical-reader-lifecycle-test", CallSites: []string{"snapshot_canonical_reader_test.go"}, Kind: stmt.Read, Params: []stmt.Param{{Name: "singleton", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "missing", Bindings: []any{int64(2)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"identity_state"}, Seeks: []string{"rowid=?"}}}}
	catalogue, err := stmt.Describe(definitions)
	if err != nil {
		t.Fatal(err)
	}
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	capture := func(statistics plan.Statistics) *stmt.Registry {
		t.Helper()
		golden, err := plan.Capture(t.Context(), reader, catalogue, plan.Fixture{ConnectionPolicy: cfg, SHA256: schema.SHA256, Statistics: statistics, Implementations: "canonical schema plus identity singleton; builtin functions/collations; GV replacement fault control"}, policy)
		if err != nil {
			t.Fatal(err)
		}
		registry, err := stmt.New(definitions, golden.Receipt())
		if err != nil || len(golden.Plans) != 2 {
			t.Fatal("actual Reader Capture/receipt", err, len(golden.Plans))
		}
		return registry
	}
	registry := capture(plan.Unanalyzed)
	gc := Config{Now: time.Now, MaxGrantsPerClass: 2}
	gc.Partitions[Foreground] = Amounts{Reader: 1, MemoryBytes: 16 << 20}
	g, a, err := New(gc)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	fault := &snapshotConnector{Connector: connector}
	nativeReservation := int64(cfg.ReadLimits.Columns) * int64(cfg.ReadLimits.LengthBytes)
	const goReservation int64 = 4 << 20
	var unreserved atomic.Int64
	observed := &snapshotObservedConnector{Connector: fault, observe: func() {
		if a.Usage()[Foreground][MemoryBytes] < nativeReservation+goReservation {
			unreserved.Add(1)
		}
	}}
	s, err := NewSnapshots(observed, cfg, g, a, registry)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	check := func(phase string, opens, closes int64, wantErr error) {
		t.Helper()
		r := Request{Key: JobKey{Domain: 71, ID: "canonical-reader129"}, Owner: 1, Class: Foreground, Need: Amounts{Reader: 1, MemoryBytes: nativeReservation + goReservation}, Budget: UnitBudget{RawRows: 8, VMOps: 10000, Pages: 128}, Deadline: time.Now().Add(time.Second)}
		out, err := s.Read(t.Context(), r, time.Second, func(scope *ReadScope) error {
			values, err := scope.Query("canonical.identity", int64(1))
			if err == nil && (len(values) != 1 || len(values[0]) != 1 || values[0][0] != int64(1)) {
				return errors.New("unexpected canonical authority value")
			}
			return err
		})
		if !errors.Is(err, wantErr) || out.Refusal != nil || out.Settings.ReadLimits != cfg.ReadLimits || out.Settings.QueryOnly != 1 || out.NativeReservedBytes != nativeReservation || out.GoReservedBytes != goReservation {
			t.Fatalf("%s: fixed Reader evidence: %+v err=%v want=%v", phase, out, err, wantErr)
		}
		if fault.opened.Load() != opens || fault.closed.Load() != closes || unreserved.Load() != 0 || a.Usage() != [4]Amounts{} || len(s.slots) != 0 || s.db.Stats().InUse != 0 {
			t.Fatalf("%s: opens=%d closes=%d unreserved=%d usage=%v", phase, fault.opened.Load(), fault.closed.Load(), unreserved.Load(), a.Usage())
		}
		t.Logf("%s: Reader C129/L65536; native=%d Go=%d; opens=%d closes=%d", phase, nativeReservation, goReservation, opens, closes)
	}
	check("fresh open", 1, 0, nil)
	// Explicit fixture maintenance on the separate writer, outside snapshots.
	if _, err := writer.ExecContext(t.Context(), "ANALYZE"); err != nil {
		t.Fatal(err)
	}
	capture(plan.Analyzed)
	check("same physical Reader after ANALYZE/reparse", 1, 0, nil)
	fault.failRollback.Store(true)
	check("failed rollback physically discards Reader", 1, 1, snapshotRollbackError)
	fault.failRollback.Store(false)
	check("replacement Reader on analyzed schema", 2, 1, nil)
	if err := s.Close(); err != nil || fault.closed.Load() != 2 || a.Usage() != [4]Amounts{} {
		t.Fatal("replacement Reader cleanup", err, fault.closed.Load(), a.Usage())
	}
}
