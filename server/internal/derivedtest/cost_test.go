package derivedtest

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	sqlite "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// Run on each PR parent and candidate with the same Go/SQLite versions:
// go test -run '^TestDerivedMaintenanceCost$' -count=3 -v -p 1 ./internal/derivedtest
// Schema bytes are SQLite's own per-connection schema heap, not RSS. Writer
// time includes the accepting transaction and commit; drain time is separate.
// This is a real catalogue write fixture, not an end-to-end scanner benchmark.
func TestDerivedMaintenanceCost(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("films", "Films", "movie", "/films")
	c.Drain()
	conn, err := c.DB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var objects int
	if err = conn.QueryRowContext(context.Background(), `SELECT count(*) FROM sqlite_schema`).Scan(&objects); err != nil {
		t.Fatal(err)
	}
	var heap int
	err = conn.Raw(func(raw any) error {
		n, _, err := dbwork.RawConn(raw).(sqlite.DBStatus).Status(sqlite.DBStatusSchemaUsed, false)
		heap = n
		return err
	})
	conn.Close()
	if err != nil || heap <= 0 {
		t.Fatal(heap, err)
	}
	const files = 12
	var rows, vm, pages int64
	var elapsed time.Duration
	for i := 0; i < files; i++ {
		dbwork.ResetPlans()
		start := time.Now()
		err = dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			ctx := context.Background()
			id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: lib, Kind: compactcatalog.Movie, Key: fmt.Sprint("cost:", i), Title: fmt.Sprint("Film ", i), Year: 2026, Added: "2026-01-01T00:00:00Z"})
			if err != nil {
				return err
			}
			asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: fmt.Sprintf("/films/%03d.mkv", i), Size: 1000, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 5400})
			if err != nil {
				return err
			}
			return compactcatalog.LinkAssetTx(ctx, tx, id, asset, compactcatalog.Link{})
		})
		elapsed += time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		stats := dbwork.Plans()
		if stats.Measured != 1 {
			t.Fatalf("accepting transaction unmeasured: %+v", stats)
		}
		rows += stats.Largest.Rows
		vm += stats.Largest.VMOps
		pages += stats.Largest.Pages
	}
	start := time.Now()
	c.Drain()
	drain := time.Since(start)
	var triggers int
	if err = c.DB.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type='trigger'`).Scan(&triggers); err != nil {
		t.Fatal(err)
	}
	t.Logf("schema_bytes_per_connection=%d triggers=%d files=%d writer_ms_per_file=%.3f changed_rows_per_file=%.2f vmops_per_file=%.2f cache_page_writes_per_file=%.2f drain_ms_per_file=%.3f", heap, triggers, files, float64(elapsed.Microseconds())/1000/files, float64(rows)/files, float64(vm)/files, float64(pages)/files, float64(drain.Microseconds())/1000/files)
}
