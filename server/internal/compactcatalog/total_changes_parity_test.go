package compactcatalog

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Hide only the optional native-counter interface. All SQL still goes through
// the production observed driver, with the same transaction and query contexts.
type sqlCounterParityConn struct{ driver.Conn }

func (c sqlCounterParityConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, o)
}
func (c sqlCounterParityConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c sqlCounterParityConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}
func (c sqlCounterParityConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
}

func (c sqlCounterParityConn) Ping(ctx context.Context) error {
	return c.Conn.(driver.Pinger).Ping(ctx)
}
func (c sqlCounterParityConn) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}
func (c sqlCounterParityConn) IsValid() bool {
	return c.Conn.(driver.Validator).IsValid()
}

type counterParityDriver struct{ driver.Driver }
type counterParityConnector struct {
	base    driver.Driver
	dsn     string
	sqlOnly bool
}

func (c counterParityConnector) Driver() driver.Driver { return counterParityDriver{c.base} }
func (c counterParityConnector) Connect(context.Context) (driver.Conn, error) {
	inner, err := c.base.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	if c.sqlOnly {
		return sqlCounterParityConn{inner}, nil
	}
	return inner, nil
}

func TestWorkerNativeCounterMatchesSQLRowsAndCursors(t *testing.T) {
	queries := []string{
		`SELECT domain,version,rebuilding,rebuild_cursor,active_slot,cleaning,first_build FROM catalog_derivations ORDER BY domain`,
		`SELECT * FROM catalog_visibility_sort_state ORDER BY id`,
		`SELECT * FROM catalog_personal_sort_state ORDER BY id`,
		`SELECT entity_id,library_id,kind,title,sort_key,year,added_text,available,recent_text,duration_max,episode_count,file_size,resolution_key,availability_state FROM catalog_browse_rows ORDER BY entity_id`,
		`SELECT entity_id,item_id,source FROM catalog_browse_memberships ORDER BY 1,2,3`,
		`SELECT entity_id,field,value FROM catalog_facet_members ORDER BY 1,2,3`,
		`SELECT entity_id,search FROM catalog_search_documents ORDER BY entity_id`,
		`SELECT class_id,generation,entity_id,library_id,kind,sort_key,head,decade FROM compact_visibility_rows ORDER BY 1,2,3`,
	}
	build := func(sqlOnly bool) (state string, meters int) {
		seed := cc1OpenDB(t, fmt.Sprint("meters-", sqlOnly))
		path := dbwork.DatabaseFile(context.Background(), seed)
		base := seed.Driver()
		if err := seed.Close(); err != nil {
			t.Fatal(err)
		}
		dsn, err := dbwork.DefaultPolicy().WriterDSN(path)
		if err != nil {
			t.Fatal(err)
		}
		db := sql.OpenDB(counterParityConnector{base, dsn, sqlOnly})
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { db.Close() })
		lib := cc1Library(t, db, "films", "Films", "movies", "/films")
		class := cc1Class(t, db, lib, "counter-parity")
		for i := 0; i < 4; i++ {
			item := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: fmt.Sprint("film:", i), Title: fmt.Sprint("Film ", i), Year: 2000 + i, Added: "2026-01-01T00:00:00.000Z"}, nil)
			cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
				asset, _, err := UpsertAssetTx(ctx, tx, Asset{Path: fmt.Sprint("/films/", i), Size: int64(100 + i), Height: 1080, Duration: 120})
				if err != nil {
					return err
				}
				return LinkAssetTx(ctx, tx, item, asset, Link{})
			})
			cc1Visible(t, db, class, item, int(Movie))
		}
		// Domain fairness uses wall-clock served times, so compare the settled
		// projections and durable cursors, rather than incidental turn order.
		w := NewWorker(db)
		ctx, statements := dbwork.TraceQueryPlans(context.Background())
		for turn := 0; turn < 1000; turn++ {
			n, err := w.Step(ctx, 4)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				break
			}
			if turn == 999 {
				t.Fatal("worker did not drain")
			}
		}
		var snapshot strings.Builder
		for _, q := range queries {
			snapshot.WriteString(groupRows(t, db, q))
			snapshot.WriteByte('\n')
		}
		state = snapshot.String()

		if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_rows`); got != 4 {
			t.Fatalf("fixture did not derive its rows: %d", got)
		}
		for _, q := range statements() {
			if strings.Contains(strings.ToLower(q), "total_changes(") {
				meters++
			}
		}
		return state, meters
	}
	sqlRows, sqlMeters := build(true)
	nativeRows, nativeMeters := build(false)
	if sqlRows != nativeRows {
		t.Fatalf("settled worker rows/cursors differ\nSQL: %s\nnative: %s", sqlRows, nativeRows)
	}
	if sqlMeters == 0 || nativeMeters != 0 {
		t.Fatalf("SQL meters=%d native meters=%d", sqlMeters, nativeMeters)
	}
	t.Logf("identical settled rows/cursors; SQL meters=%d native meters=%d", sqlMeters, nativeMeters)
}
