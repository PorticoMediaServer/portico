package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"testing"
	"time"
)

func TestDerivedAcknowledgmentSeeksOnlyItsBatch(t *testing.T) {
	sizes := []int{256}
	if os.Getenv("PORTICO_PERFORMANCE_TIER") == "release" {
		sizes = []int{10000, 200000}
	}
	if os.Getenv("PORTICO_PERFORMANCE_TIER") == "deep" {
		sizes = []int{10000, 200000, 1000000}
	}
	for _, n := range sizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			db, err := dbwork.OpenHandle(filepath.Join(t.TempDir(), "queue.sqlite"), dbwork.DefaultPolicy())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, q := range []string{`CREATE TABLE catalog_dirty(domain INTEGER,entity_id INTEGER,revision INTEGER,PRIMARY KEY(domain,entity_id)) WITHOUT ROWID`, `CREATE TABLE catalog_derivation_failures(domain INTEGER,entity_id INTEGER,PRIMARY KEY(domain,entity_id)) WITHOUT ROWID`, fmt.Sprintf(`WITH RECURSIVE ids(i) AS(SELECT 1 UNION ALL SELECT i+1 FROM ids WHERE i<%d) INSERT INTO catalog_dirty SELECT 17,i,1 FROM ids`, n), `INSERT INTO catalog_derivation_failures SELECT domain,entity_id FROM catalog_dirty`, `ANALYZE`} {
				if _, err = db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			for _, q := range []string{`DELETE FROM catalog_dirty WHERE domain=? AND (entity_id,revision) IN(SELECT json_extract(value,'$[0]'),json_extract(value,'$[1]') FROM json_each(?))`, `DELETE FROM catalog_dirty WHERE domain=? AND entity_id=? AND revision=?`, `DELETE FROM catalog_derivation_failures WHERE domain=? AND entity_id=?`} {
				args := []any{17, n}
				before := strings.Contains(q, "json_each")
				if before {
					args[1] = fmt.Sprintf("[[%d,1]]", n)
				} else if strings.Contains(q, "revision=?") {
					args = append(args, 1)
				}
				rows, err := db.Query("EXPLAIN QUERY PLAN "+q, args...)
				if err != nil {
					t.Fatal(err)
				}
				var plan strings.Builder
				for rows.Next() {
					var a, b, c int
					var d string
					if err = rows.Scan(&a, &b, &c, &d); err != nil {
						t.Fatal(err)
					}
					plan.WriteString(d)
				}
				rows.Close()
				t.Logf("rows=%d before=%v plan=%s", n, before, plan.String())
				if !before && !strings.Contains(plan.String(), "domain=? AND entity_id=?") {
					t.Fatal(plan.String())
				}
			}
			started := time.Now()
			err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
				for id := int64(n - 15); id <= int64(n); id++ {
					if err := acknowledgeDerived(context.Background(), tx, 17, id, 1); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			elapsed := time.Since(started)
			t.Logf("rows=%d batch=16 elapsed=%s", n, elapsed)
			if elapsed > 250*time.Millisecond {
				t.Fatal(elapsed)
			}
			var left int
			db.QueryRow(`SELECT count(*) FROM catalog_dirty`).Scan(&left)
			if left != n-16 {
				t.Fatal(left)
			}
			// A newer edit survives acknowledgement of the old revision.
			db.Exec(`UPDATE catalog_dirty SET revision=2 WHERE domain=17 AND entity_id=1`)
			err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error { return acknowledgeDerived(context.Background(), tx, 17, 1, 1) })
			if err != nil {
				t.Fatal(err)
			}
			db.QueryRow(`SELECT revision FROM catalog_dirty WHERE domain=17 AND entity_id=1`).Scan(&left)
			if left != 2 {
				t.Fatal(left)
			}
		})
	}
}
