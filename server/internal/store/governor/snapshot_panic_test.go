package governor

import (
	"database/sql/driver"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

var snapshotNativePanic = errors.New("snapshot native scalar panic")
var snapshotPanicRegistration sync.Once
var snapshotPanicRegistrationError error

func TestSnapshotNativePanicReleasesBorrowBeforeRethrow(t *testing.T) {
	snapshotPanicRegistration.Do(func() {
		snapshotPanicRegistrationError = sqlite.RegisterDeterministicScalarFunction("governor_snapshot_panic", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if args[0] == "boom" {
				panic(snapshotNativePanic)
			}
			return args[0], nil
		})
	})
	if snapshotPanicRegistrationError != nil {
		t.Fatal(snapshotPanicRegistrationError)
	}
	for _, phase := range []string{"statement query", "next"} {
		t.Run(phase, func(t *testing.T) {
			path, writer := snapshotFixture(t)
			query := "UPDATE source SET value='boom'"
			if phase == "next" {
				query = "INSERT INTO source VALUES(2,'boom')"
			}
			if _, err := writer.ExecContext(t.Context(), query); err != nil {
				t.Fatal(err)
			}
			g, a := snapshotGovernor(t)
			cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 16384, Columns: 16}}
			connector, err := sqliteconn.Connector(path, cfg)
			if err != nil {
				t.Fatal(err)
			}
			c := &snapshotConnector{Connector: connector}
			registry, err := stmt.New([]stmt.Definition{{ID: "panic", SQL: "SELECT randomblob(12000),governor_snapshot_panic(value) FROM source", Kind: stmt.Read, Owner: "test", CallSites: []string{"native-panic"}, Cohorts: []stmt.Cohort{{Name: "panic"}}}})
			if err != nil {
				t.Fatal(err)
			}
			s, err := NewSnapshots(c, cfg, g, a, registry)
			if err != nil {
				t.Fatal(err)
			}
			type result struct {
				value any
				fault *snapshotDriverPanic
				owned *snapshotOwnedConn
			}
			done := make(chan result, 1)
			go func() {
				runtime.LockOSThread()
				defer runtime.UnlockOSThread()
				var out result
				defer func() { out.value = recover(); done <- out }()
				_, err := s.Read(t.Context(), snapshotRequest(), time.Second, func(scope *ReadScope) error {
					out.owned = scope.owned
					_, err := scope.Query("panic")
					errors.As(err, &out.fault)
					return err
				})
				if err != nil {
					out.value = err
				}
			}()
			var out result
			select {
			case out = <-done:
			case <-time.After(1500 * time.Millisecond):
				// Do not join the broken cleanup on a regression: fail by assertion, so the
				// held implementation is distinguished from the test runner's timeout.
				t.Fatal("native panic retained database/sql borrow and blocked cleanup")
			}
			defer s.Close()
			if out.value != snapshotNativePanic || out.fault == nil || out.fault.phase != phase {
				t.Fatal("panic did not cross SQL as a typed error then rethrow", out.value, out.fault)
			}
			out.owned.safetyMu.Lock()
			remaining := len(out.owned.resources)
			out.owned.safetyMu.Unlock()
			if remaining != 0 || c.closed.Load() != 1 || s.db.Stats().InUse != 0 || len(s.slots) != 0 || a.Usage() != [4]Amounts{} {
				t.Fatal("native ownership not released exactly once", remaining, c.closed.Load(), s.db.Stats(), len(s.slots), a.Usage())
			}
			if _, err := writer.ExecContext(t.Context(), "UPDATE source SET value='after'"); err != nil {
				t.Fatal(err)
			}
			frames, copied := snapshotCheckpoint(t, path)
			if copied != frames {
				t.Fatal("panic left a native statement pinning WAL", frames, copied)
			}
			if g.(*ledger).state.Load()&1 == 0 {
				t.Fatal("native panic did not fence uncertain connection")
			}
			if err := s.Close(); err != nil || c.closed.Load() != 1 {
				t.Fatal("Close repeated native retirement", err, c.closed.Load())
			}
		})
	}
}
