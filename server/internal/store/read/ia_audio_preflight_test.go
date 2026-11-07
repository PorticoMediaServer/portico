package read

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Observe actual native query dispatch, independently of production counters.
// Forward all native policy/identity evidence; no execution or decoder is mocked.
// The driver's later materialization refusal must not hide an early payload query.
type iaAudioPointTrace struct{ lengths, payload atomic.Int64 }
type iaAudioPointConnector struct {
	driver.Connector
	trace *iaAudioPointTrace
}
type iaAudioPointNative interface {
	driver.Conn
	driver.QueryerContext
	driver.ExecerContext
	ReadBudgetSupported() bool
	SQLiteMainFileIdentity() (uint64, uint64, error)
	SQLiteLimit(int, int) (int, error)
	SQLiteDQS() (int, int, error)
}
type iaAudioPointConnection struct {
	iaAudioPointNative
	trace *iaAudioPointTrace
}

func (c *iaAudioPointConnector) Connect(ctx context.Context) (driver.Conn, error) {
	raw, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	native, ok := raw.(iaAudioPointNative)
	if !ok {
		_ = raw.Close()
		return nil, errors.New("native point observation lacks policy evidence")
	}
	return &iaAudioPointConnection{iaAudioPointNative: native, trace: c.trace}, nil
}
func (t *iaAudioPointTrace) query(sql string) {
	switch sql {
	case iaAudioEntityOctetsSQL, iaAudioLinkOctetsSQL:
		t.lengths.Add(1)
	case iaAudioEntitySQL, iaAudioLinkSQL:
		t.payload.Add(1)
	}
}
func (c *iaAudioPointConnection) QueryContext(ctx context.Context, sql string, args []driver.NamedValue) (driver.Rows, error) {
	c.trace.query(sql)
	return c.iaAudioPointNative.QueryContext(ctx, sql, args)
}

type iaAudioPointStatement struct {
	driver.Stmt
	sql   string
	trace *iaAudioPointTrace
}

func (c *iaAudioPointConnection) Prepare(sql string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), sql)
}
func (c *iaAudioPointConnection) PrepareContext(ctx context.Context, sql string) (driver.Stmt, error) {
	var st driver.Stmt
	var err error
	if native, ok := c.iaAudioPointNative.(driver.ConnPrepareContext); ok {
		st, err = native.PrepareContext(ctx, sql)
	} else {
		st, err = c.iaAudioPointNative.Prepare(sql)
	}
	if err != nil {
		return nil, err
	}
	if _, ok := st.(driver.StmtQueryContext); !ok {
		_ = st.Close()
		return nil, errors.New("native point statement lacks contextual reads")
	}
	return &iaAudioPointStatement{Stmt: st, sql: sql, trace: c.trace}, nil
}
func (s *iaAudioPointStatement) Query(args []driver.Value) (driver.Rows, error) {
	s.trace.query(s.sql)
	return s.Stmt.Query(args)
}
func (s *iaAudioPointStatement) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	s.trace.query(s.sql)
	return s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
}
func iaAudioPointFixture(t *testing.T) (*browseFixture, *iaAudioPointTrace) {
	t.Helper()
	f := newIAAudioSource(t)
	definitions := append(browseDefinitions(), IAAudioHarnessDefinitions()...)
	golden := captureBrowse(t, f, definitions, plan.Unanalyzed, iaAudioHash(t))
	registry, err := stmt.New(definitions, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	cfg := browseReaderConfig()
	connector, err := sqliteconn.Connector(f.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	g, a, err := governor.New(readfixture.LiveConfig(time.Now))
	if err != nil {
		t.Fatal(err)
	}
	trace := &iaAudioPointTrace{}
	pool, err := governor.NewSnapshots(&iaAudioPointConnector{Connector: connector, trace: trace}, cfg, g, a, registry)
	if err != nil {
		t.Fatal(err)
	}
	f.governor, f.authority, f.snapshots = g, a, pool
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Error(err)
		}
	})
	return f, trace
}

// Exercise the production entity/link point adapter with the real snapshot
// pool, grants, byte points and payload decoders. Keep Go-copy custody generous
// while independently varying the native variable-row forecast.
func TestIAAudioPreflightBeforePayload(t *testing.T) {
	for _, link := range []bool{false, true} {
		for _, bound := range []int64{1024, 16 << 10} {
			name := "entity"
			if link {
				name = "link"
			}
			if bound == 1024 {
				name += "/refuse"
			} else {
				name += "/decode"
			}
			t.Run(name, func(t *testing.T) {
				f, trace := iaAudioPointFixture(t)
				payload := strings.Repeat("é", 1024) // 2048 actual UTF-8 octets, not rune count
				point, columns, args := "entity", []int{1, 2, 3, 14, 18}, []any{int64(2002), int64(1)}
				if link {
					f.exec(t, `UPDATE catalog_asset_links SET edition=? WHERE entity_id=2003 AND asset_id=1`, payload)
					point, columns, args = "link", []int{2, 7}, []any{int64(2003), int64(1), int64(1)}
				} else {
					f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=2002`, payload)
				}
				_, request, _ := liveRequests("ia-audio-preflight")
				request.Need[governor.MemoryBytes] = 12 << 20
				var row []any
				_, err := f.snapshots.ReadForecasted(t.Context(), request, time.Second, governor.SnapshotForecast{NativeRowBytes: bound, GoCopyBytes: 2 << 20, MaxQueries: 2}, func(scope *governor.ReadScope) error {
					s := &iaAudioSnapshots{scope: scope, extra: []iaAudioCalls{{}}, copyLimit: 2 << 20}
					var e error
					row, e = s.point(point, columns, args...)
					return e
				})
				if bound == 1024 {
					if !errors.Is(err, governor.ErrSnapshotBudget) || row != nil || trace.lengths.Load() != 1 || trace.payload.Load() != 0 {
						t.Fatal("payload query dispatched before byte preflight refused", trace.lengths.Load(), trace.payload.Load(), err)
					}
					return
				}
				if err != nil || trace.lengths.Load() != 1 || trace.payload.Load() != 1 {
					t.Fatal("legal point pair refused", trace.lengths.Load(), trace.payload.Load(), err)
				}
				column := 3 // entity title
				if link {
					column = 2 // link edition
				}
				if row[column] != payload {
					t.Fatal("legal payload was truncated")
				}
			})
		}
	}
}
