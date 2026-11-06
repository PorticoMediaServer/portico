package governor

import (
	"database/sql/driver"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

var snapshotBoundRegistration sync.Once
var snapshotBoundRegistrationError error

func snapshotBoundAttempts(t *testing.T, kind stmt.Type) func(bool) {
	t.Helper()
	snapshotBoundRegistration.Do(func() {
		snapshotBoundRegistrationError = sqlite.RegisterDeterministicScalarFunction("governor_bound_panic", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			switch x := args[0].(type) {
			case string:
				if x[0] == '!' {
					panic(snapshotNativePanic)
				}
			case []byte:
				if x[0] == '!' {
					panic(snapshotNativePanic)
				}
			}
			return int64(1), nil
		})
	})
	if snapshotBoundRegistrationError != nil {
		t.Fatal(snapshotBoundRegistrationError)
	}
	path, _ := snapshotFixture(t)
	var witness any = "warm"
	if kind == stmt.Blob {
		witness = []byte("warm")
	}
	registry, err := stmttest.New(t, []stmt.Definition{{ID: "bound", SQL: "SELECT governor_bound_panic(?)", Kind: stmt.Read, Owner: "test", CallSites: []string{"bound-panic"}, Params: []stmt.Param{{Name: "value", Type: kind}}, Cohorts: []stmt.Cohort{{Name: "bound", Bindings: []any{witness}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return func(panics bool) {
		t.Helper()
		g, a := snapshotGovernor(t)
		cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 16384, Columns: 16}}
		connector, err := sqliteconn.Connector(path, cfg)
		if err != nil {
			t.Fatal(err)
		}
		s, err := NewSnapshots(connector, cfg, g, a, registry)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		text := strings.Repeat("a", 12000)
		if panics {
			text = "!" + text[1:]
		}
		var arg any = text
		if kind == stmt.Blob {
			arg = []byte(text)
		}
		var caught any
		runtime.LockOSThread()
		func() {
			defer func() { caught = recover() }()
			_, err = s.Read(t.Context(), snapshotRequest(), time.Second, func(scope *ReadScope) error {
				values, err := scope.Query("bound", arg)
				if panics {
					var typed *snapshotDriverPanic
					if !errors.As(err, &typed) {
						t.Error("first-step panic was not contained", err)
					}
				}
				if !panics && err == nil && (len(values) != 1 || values[0][0] != int64(1)) {
					t.Error("normal bound control returned wrong value")
				}
				return err
			})
		}()
		runtime.UnlockOSThread()
		if panics && caught != snapshotNativePanic || !panics && (caught != nil || err != nil) {
			t.Fatal("bound control outcome", caught, err)
		}
		if err := s.Close(); err != nil || a.Usage() != [4]Amounts{} || len(s.slots) != 0 || s.db.Stats().InUse != 0 {
			t.Fatal("bound control cleanup", err, a.Usage(), len(s.slots))
		}
	}
}

func TestSnapshotBoundFirstStepPanicCleanup(t *testing.T) {
	for _, kind := range []stmt.Type{stmt.Text, stmt.Blob} {
		t.Run(string(kind), func(t *testing.T) {
			attempt := snapshotBoundAttempts(t, kind)
			attempt(false)
			attempt(true)
			attempt(false)
		})
	}
}
