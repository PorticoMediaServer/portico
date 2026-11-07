package read

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/schematest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Native GV fixture using explicitly UNQUALIFIED test-only registration. This
// proves component SQL/byte accounting, never current principal publication or
// HN Capture/1M qualification. No production handle or callback is introduced.
func uoReadTestFixture(t *testing.T, defs []stmt.Definition) (*governor.Snapshots, func(string, ...any)) {
	t.Helper()
	path := schematest.Clone(t)
	db, _, e := sqliteconn.Open(t.Context(), path, sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = db.Close() })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.ExecContext(t.Context(), q, args...); e != nil {
			t.Fatal(q, e)
		}
	}
	registry, e := stmttest.New(t, defs)
	if e != nil {
		t.Fatal(e)
	}
	cfg := browseReaderConfig()
	connector, e := sqliteconn.Connector(path, cfg)
	if e != nil {
		t.Fatal(e)
	}
	gc := governor.Config{Now: time.Now, MaxGrantsPerClass: 2}
	gc.Partitions[governor.Foreground] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 20 << 20}
	g, a, e := governor.New(gc)
	if e != nil {
		t.Fatal(e)
	}
	pool, e := governor.NewSnapshots(connector, cfg, g, a, registry)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := pool.Close(); e != nil {
			t.Error(e)
		}
	})
	return pool, exec
}
func uoReadTestRequest() governor.Request {
	r := foregroundRequest()
	r.Budget.RawRows = 4096
	r.Budget.VMOps = 2000000
	r.Budget.Pages = 4096
	return r
}
func TestUOPreferenceSnapshotDefaultsScopeAndLists(t *testing.T) {
	pool, exec := uoReadTestFixture(t, UOPreferenceDefinitions())
	a := UOPreferenceAddress{"local", "account", "profile", UOProfileServer, ""}
	exec(`INSERT INTO preference_scopes VALUES('local','account','profile','profile-server','',2,1000)`)
	exec(`INSERT INTO preference_values VALUES('local','account','profile','profile-server','','home.rowOrder','string-list',NULL,NULL,NULL)`)
	exec(`INSERT INTO preference_list_items VALUES('local','account','profile','profile-server','','home.rowOrder',0,'海')`)
	exec(`INSERT INTO preference_list_items VALUES('local','account','profile','profile-server','','home.rowOrder',1,'two')`)
	read := func(address UOPreferenceAddress) uoPreferenceDocument {
		t.Helper()
		var out uoPreferenceDocument
		_, e := pool.ReadForecasted(t.Context(), uoReadTestRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 8192, MaxQueries: 256}, func(s *governor.ReadScope) error {
			var e error
			out, e = uoReadPreferenceScope(t.Context(), s, address, []UOPreferenceKey{UOPrefMusicGapless, UOPrefHomeRowOrder})
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	out := read(a)
	if out.Revision != 2 || len(out.Points) != 2 || out.Points[0].Override || !out.Points[0].Value.Bool || !out.Points[1].Override || !reflect.DeepEqual(out.Points[1].Value.List, []string{"海", "two"}) {
		t.Fatal(out)
	}
	a.Account = "other"
	out = read(a)
	if out.Revision != 1 || out.Points[1].Override || len(out.Points[1].Value.List) != 0 {
		t.Fatal("cross-account scope leaked", out)
	}
	// A dangling sparse ordinal is corruption, not a truncated successful list.
	exec(`DELETE FROM preference_list_items WHERE position=0`)
	a.Account = "account"
	_, e := pool.ReadForecasted(t.Context(), uoReadTestRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 8192, MaxQueries: 256}, func(s *governor.ReadScope) error {
		v, e := uoReadPreferenceScope(t.Context(), s, a, []UOPreferenceKey{UOPrefHomeRowOrder})
		if !reflect.DeepEqual(v, uoPreferenceDocument{}) {
			t.Fatal("partial result", v)
		}
		return e
	})
	if !errors.Is(e, ErrUnqualified) {
		t.Fatal(e)
	}
}
func TestUOPreferenceSnapshotRefusesUnknownScopeAndCancellation(t *testing.T) {
	pool, _ := uoReadTestFixture(t, UOPreferenceDefinitions())
	a := UOPreferenceAddress{"local", "a", "p", UOProfileServer, ""}
	for _, keys := range [][]UOPreferenceKey{{"unknown"}, {UOPrefAppearanceMotion}, {UOPrefMusicGapless, UOPrefMusicGapless}} {
		_, e := pool.ReadForecasted(t.Context(), uoReadTestRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 8192, MaxQueries: 256}, func(s *governor.ReadScope) error {
			out, e := uoReadPreferenceScope(t.Context(), s, a, keys)
			if !reflect.DeepEqual(out, uoPreferenceDocument{}) {
				t.Fatal(out)
			}
			return e
		})
		if !errors.Is(e, ErrUnqualified) {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out, e := uoReadPreferenceScope(ctx, nil, a, nil)
	if e == nil || !reflect.DeepEqual(out, uoPreferenceDocument{}) {
		t.Fatal(out, e)
	}
}
func TestUOPreferenceValueRejectsMalformedArms(t *testing.T) {
	for _, row := range [][]any{{"boolean", int64(2), nil, nil}, {"integer", nil, "wrong", nil}, {"string", nil, nil, int64(1)}, {"string-list", nil, nil, "wrong"}, {"unknown", nil, nil, nil}} {
		if _, e := uoPreferenceValue(row); e == nil {
			t.Fatal(row)
		}
	}
}
