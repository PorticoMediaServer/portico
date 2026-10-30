package catalog

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestFilteredPagesHydrateOnlySelectedRows(t *testing.T) {
	sizes := []int{256}
	if os.Getenv("PORTICO_PERFORMANCE_TIER") == "release" {
		sizes = []int{10000, 200000}
	}
	if os.Getenv("PORTICO_PERFORMANCE_TIER") == "deep" {
		sizes = []int{10000, 200000, 1000000}
	}
	for _, n := range sizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			c := catalogtest.Open(t)
			lib := c.Library("m", "Movies", "movie", "/m")
			item := c.Movie(lib, "/m/a.mkv", "Title", 2000)
			c.Drain()
			// Copies of the narrow browse row isolate selection from library seeding.
			// Foreign keys are disabled only on this fixture's reserved writer.
			ctx, conn, release, err := dbwork.ReserveWriterConn(context.Background(), c.DB, dbwork.ClassMaintenance)
			if err != nil {
				t.Fatal(err)
			}
			_, err = conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`)
			for off := 0; err == nil && off < n; off += 10000 {
				_, err = conn.ExecContext(ctx, `WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?)
 INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,year) SELECT i+1000000,randomblob(16),?,1,'Title','Title','T',2000 FROM ids`, off+1, min(n, off+10000), lib)
				if err == nil {
					_, err = conn.ExecContext(ctx, `INSERT INTO catalog_browse_rows(entity_id,library_id,kind,title,sort_key,head,year,rating_key,label_key,available,recent,backdrop,random_key,availability_state) SELECT id,library_id,kind,title,sort_key,'T',year,'','',1,0,0,id,'available' FROM catalog_entities WHERE id>? AND id<=?`, 1000000+off, 1000000+min(n, off+10000))
				}
			}
			_, restore := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
			release()
			if err != nil || restore != nil {
				t.Fatalf("seed=%v restore=%v", err, restore)
			}
			c.Exec(`ANALYZE`)
			start := min(n-40, 50000)
			shape := pageShape{request: BrowseRequest{Library: "m", Query: &BrowseNode{Field: "availability", Operator: "equals", Value: "available"}}, sorts: []BrowseSortSelection{{Field: "title", Direction: "asc"}}, where: `e.library_id=? AND e.kind=1 AND e.availability_state=?`, args: []any{lib, "available"}, start: start, limit: 40, total: n, kind: 1}
			compiler := browseCompiler{}
			clause, err := compiler.compile(shape.request.Query)
			if err != nil {
				t.Fatal(err)
			}
			shape.where = "e.library_id=? AND e.kind=1 AND " + clause
			shape.args = append([]any{lib}, compiler.args...)
			plan := seekCapacityPlan(t, c.DB, `SELECT e.entity_id FROM catalog_browse_rows e WHERE `+shape.where, shape.args...)
			if !strings.Contains(plan, "availability_state=?") {
				t.Fatal("availability filter lost its seek: " + plan)
			}
			t.Logf("availability plan=%s", plan)
			for _, field := range []string{"title", "random", "fileSize"} {
				shape.sorts[0].Field = field
				shape.sorts[0].Seed = 1000000
				ctx, trace := dbwork.TraceQueryPlans(context.Background())
				svc := New(c.DB).WithContext(ctx)
				began := time.Now()
				var rows []browseRow
				err := dbwork.WithReadSnapshot(ctx, c.DB, func(ctx context.Context) error {
					svc = svc.WithContext(ctx)
					var e error
					if field == "random" {
						d, _ := browseSortByID(field)
						rows, e = svc.seededRandomRange(shape, d, false)
					} else {
						rows, e = svc.filteredRangePage(shape)
					}
					return e
				})
				elapsed := time.Since(began)
				if err != nil || len(rows) != 40 || rows[0].entity == item.ID {
					t.Fatalf("%s rows=%d err=%v", field, len(rows), err)
				}
				for _, q := range trace() {
					if strings.Contains(q, "WITH page AS MATERIALIZED") {
						if strings.Contains(q, "row_number()") || !strings.Contains(q, "LIMIT ? OFFSET ?") {
							t.Fatalf("unbounded page: %s", q)
						}
						t.Logf("%s query=%s", field, q)
					}
				}
				t.Logf("rows=%d sort=%s deep=%d elapsed=%s", n, field, start, elapsed)
				if elapsed > time.Second {
					t.Fatalf("deep page took %s", elapsed)
				}
			}
			// Isolate the class's random read model from its other order axes.
			triggers, err := c.DB.Query(`SELECT name FROM sqlite_master WHERE type='trigger' AND name LIKE 'compact_visibility_blocks_%' AND name NOT LIKE '%_random_%'`)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for triggers.Next() {
				var name string
				if err := triggers.Scan(&name); err != nil {
					t.Fatal(err)
				}
				names = append(names, name)
			}
			triggers.Close()
			for _, name := range names {
				c.Exec(`DROP TRIGGER ` + name)
			}
			c.Exec(`INSERT INTO compact_visibility_classes(id,class_key,library_id,policy_json,active_generation,catalog_revision,requested_ms,built_ms) VALUES(1,'fixture',?,'{}',1,0,0,0)`, lib)
			// A published visibility projection contains only these identities.
			c.Exec(`INSERT INTO compact_visibility_rows(class_id,generation,entity_id,library_id,kind,sort_key,head,decade,random_key,availability_state) SELECT 1,1,entity_id,library_id,kind,sort_key,head,2000,random_key,availability_state FROM catalog_browse_rows WHERE entity_id>=1000000 AND entity_id%2=0`)
			for _, restricted := range []bool{false, true} {
				p := shape
				p.request.Query, p.where, p.args = nil, `e.library_id=? AND e.kind=1`, []any{lib}
				p.total = n + 1
				p.summary = browseSummary{exact: true, total: p.total}
				from, key, id := `catalog_browse_rows WHERE library_id=? AND kind=1`, "random_key", "entity_id"
				if restricted {
					p.total = n / 2
					p.summary = browseSummary{exact: true, total: p.total, classKey: "fixture", classID: 1, generation: 1}
					from = `compact_visibility_rows WHERE class_id=1 AND generation=1 AND library_id=? AND kind=1`
				}
				p.start = min(50000, p.total-40)
				p.sorts = []BrowseSortSelection{{Field: "random", Seed: uint32(1000000 + n/2)}}
				for _, desc := range []bool{false, true} {
					op, direction := ">=", "ASC"
					if desc {
						op, direction = "<=", "DESC"
					}
					reference, err := c.DB.Query(`SELECT `+id+` FROM `+from+` ORDER BY CASE WHEN `+key+op+`? THEN 0 ELSE 1 END,`+key+` `+direction+`,`+id+` `+direction+` LIMIT 40 OFFSET ?`, lib, p.sorts[0].Seed, p.start)
					if err != nil {
						t.Fatal(err)
					}
					var want []int64
					for reference.Next() {
						var id int64
						if err := reference.Scan(&id); err != nil {
							t.Fatal(err)
						}
						want = append(want, id)
					}
					reference.Close()
					var durations []time.Duration
					for sample := 0; sample < 3; sample++ {
						before, began := dbwork.Reads().Statements, time.Now()
						var rows []browseRow
						err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
							var err error
							rows, err = New(c.DB).WithContext(ctx).positionRandomPage(p, desc)
							return err
						})
						durations = append(durations, time.Since(began))
						var got []int64
						for _, row := range rows {
							got = append(got, row.entity)
						}
						if err != nil || !reflect.DeepEqual(got, want) || len(got) != 40 || dbwork.Reads().Statements-before > 20 {
							t.Fatalf("counted random: restricted=%v desc=%v got=%v want=%v err=%v", restricted, desc, got, want, err)
						}
					}
					sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
					t.Logf("rows=%d counted-random restricted=%v desc=%v median=%s worst=%s", n, restricted, desc, durations[1], durations[2])
					if durations[1] > 100*time.Millisecond || durations[2] > time.Second {
						t.Fatal(durations)
					}
				}
				if restricted {
					p.sorts = []BrowseSortSelection{{Field: "fileSize", Direction: "asc"}}
					// The class selects positions; a restriction newer than the
					// class (simulated here) is checked on the selected rows and
					// the page is refilled from the rows after them.
					p.unrestricted, p.unrestrictedArgs = p.where, p.args
					p.live, p.liveArgs = `e.entity_id%4=0`, nil
					p.where += ` AND e.entity_id%4=0`
					p.start = min(50000, n/4-40)
					ctx, trace := dbwork.TraceQueryPlans(context.Background())
					began := time.Now()
					var rows []browseRow
					if err := dbwork.WithReadSnapshot(ctx, c.DB, func(ctx context.Context) error {
						var err error
						rows, err = New(c.DB).WithContext(ctx).filteredRangePage(p)
						return err
					}); err != nil {
						t.Fatal(err)
					}
					elapsed := time.Since(began)
					if len(rows) != 40 || elapsed > time.Second {
						t.Fatalf("class fact range rows=%d elapsed=%s", len(rows), elapsed)
					}
					for _, row := range rows {
						if row.entity%4 != 0 {
							t.Fatal("class range dropped its live predicate")
						}
					}
					bounded := false
					for _, query := range trace() {
						if strings.Contains(query, "WITH page AS MATERIALIZED") {
							bounded = strings.Contains(query, "FROM compact_visibility_rows v CROSS JOIN catalog_browse_rows e") && strings.Contains(query, "e.entity_id%4=0") && strings.Contains(query, "LIMIT ? OFFSET ?")
						}
					}
					if !bounded {
						t.Fatal("fact range did not start from the class with its live predicate")
					}
					t.Logf("rows=%d class-fact-range elapsed=%s", n, elapsed)
				}
			}
		})
	}
}
