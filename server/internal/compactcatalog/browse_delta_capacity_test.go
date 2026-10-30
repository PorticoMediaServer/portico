package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestBrowseReDerivationWritesOnlyChangedKeys(t *testing.T) {
	sizes := []int{256}
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release":
		sizes = []int{10000, 200000}
	case "deep":
		sizes = []int{10000, 200000, 2000000}
	}
	for _, n := range sizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			db := cc1OpenDB(t, "delta.sqlite")
			lib := cc1Library(t, db, "m", "Movies", "movie", "/m")
			id := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film:a", Title: "Alpha", Year: 2000, Added: "2026-01-01T00:00:00.000Z"}, nil)
			cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
				asset, _, err := UpsertAssetTx(ctx, tx, Asset{Path: "/m/a.mp4", Size: 1000, Duration: 90})
				if err != nil {
					return err
				}
				return LinkAssetTx(ctx, tx, id, asset, Link{})
			})
			cc1Drain(t, db)
			// Asynchronously retired counted rows have no entity FK. Their volume
			// must not change the point reads used to re-derive an unchanged live row.
			for after := 0; after < n; after += 10000 {
				cc1Exec(t, db, `WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?) INSERT INTO catalog_browse_counted_rows(entity_id,library_id,kind,container,head,year,rating_key,label_key) SELECT i+1000000000,99999,1,0,'x',2000,'','' FROM ids`, after+1, min(n, after+10000))
			}
			ctx, trace := dbwork.TraceQueryPlans(context.Background())
			err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
				var before, after int64
				if err := tx.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
					return err
				}
				if err := deriveBrowseRow(ctx, tx, id); err != nil {
					return err
				}
				if err := tx.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
					return err
				}
				if after != before {
					t.Fatalf("unchanged row made %d database changes", after-before)
				}
				if _, err := tx.Exec(`UPDATE catalog_entities SET title='Beta' WHERE id=?`, id); err != nil {
					return err
				}
				return deriveBrowseRow(ctx, tx, id)
			})
			if err != nil {
				t.Fatal(err)
			}
			writes := 0
			for _, q := range trace() {
				if strings.HasPrefix(q, "INSERT INTO catalog_browse_buckets") || strings.HasPrefix(q, "UPDATE catalog_browse_buckets") || strings.HasPrefix(q, "DELETE FROM catalog_browse_buckets") {
					writes++
				}
			}
			if writes != 3 {
				t.Fatalf("a changed letter moved %d bucket statements", writes)
			}
			if got := cc1Scalar(t, db, `SELECT sum(total) FROM catalog_browse_buckets WHERE library_id=? AND axis=0`, lib); got != 1 {
				t.Fatalf("letter total=%d", got)
			}
			t.Logf("counted rows=%d unchanged changes=0 letter delta statements=%d", n, writes)
		})
	}
}
