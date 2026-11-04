package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// This is a database-producer microbenchmark, not a million-file filesystem
// replay or a million-item derived-catalogue build. Only a real 128-file page
// is observed on disk. The rest is a normalized inventory/asset fixture in the
// complete baseline schema, with distinct assets and all production triggers.
// Construction is excluded. Run the million-object case on GitHub, not a
// shared development host: -run '^$' -bench BenchmarkInventoryUnchangedDatabasePage
// -benchtime=1x -count=1. The smaller case is a hosted rehearsal/control.
func BenchmarkInventoryUnchangedDatabasePage(b *testing.B) {
	for _, objects := range []int{1000, 1_000_000} {
		b.Run(strconv.Itoa(objects), func(b *testing.B) {
			b.StopTimer()
			f := inventoryScaleFixture(b, objects)
			lookup := inventoryScaleLookup(b)
			b.Run("lookup128", func(b *testing.B) {
				ctx, cost := dbwork.Measure(context.Background())
				b.ResetTimer()
				for range b.N {
					g, err := dbwork.BeginSnapshot(ctx, f.c.DB)
					if err != nil {
						b.Fatal(err)
					}
					for _, file := range f.page.Entries {
						var object, asset, revision, seen, identity, incarnation, state string
						var size, modified int64
						err = g.Tx().QueryRowContext(ctx, lookup, f.source.ID, file.Name).Scan(&object, &asset, &revision, &seen, &identity, &incarnation, &state, &size, &modified)
						if err != nil || object == "" || asset == "" || revision == "" || incarnation != f.source.Incarnation || state != "available" || size != file.Size || modified != file.ModifiedNS {
							g.Rollback()
							b.Fatalf("unchanged lookup lost its normalized binding: %v", err)
						}
					}
					g.Rollback()
				}
				b.StopTimer()
				inventoryScaleMetrics(b, len(f.page.Entries), cost(), 2, 40)
			})
			b.Run("page128", func(b *testing.B) {
				ctx, cost := dbwork.Measure(dbwork.WithClass(context.Background(), dbwork.ClassBackgroundMedia))
				b.ResetTimer()
				for range b.N {
					b.StopTimer()
					job := f.job(b)
					b.StartTimer()
					_, err := f.service.CommitInventoryPages(ctx, job, []InventoryPageWrite{{Relative: ".", Page: f.page}})
					b.StopTimer()
					if err != nil {
						b.Fatal(err)
					}
					var seen int
					if err := f.c.DB.QueryRow(`SELECT count(*) FROM inventory_objects WHERE source_id=? AND seen_job=?`, f.source.ID, job).Scan(&seen); err != nil || seen != len(f.page.Entries) {
						b.Fatalf("page did not publish its own observations: %d %v", seen, err)
					}
					f.c.Exec(`UPDATE jobs SET status='complete' WHERE id=?`, job)
				}
				b.StopTimer()
				measured := cost()
				if measured.Transactions != int64(b.N) {
					b.Fatalf("page writer used %d transactions for %d pages", measured.Transactions, b.N)
				}
				inventoryScaleMetrics(b, len(f.page.Entries), measured, 12, 256)
			})
		})
	}
}

// Work ceilings are independent of host speed. The hosted 1k control costs
// 1.008 statements/22.03 counted VM per lookup and 10.13/153.4 per page
// object. A lost point index must fail rather than hiding behind fast hardware.
// Counted VM covers the driver's measured reads, not every triggered write.
func inventoryScaleMetrics(b *testing.B, files int, cost dbwork.Cost, statements, vm int64) {
	b.Helper()
	if cost.Statements > int64(b.N*files)*statements || cost.VMSteps > int64(b.N*files)*vm {
		b.Fatalf("unchanged inventory exceeded its per-object work ceiling: %+v over %d objects (statements<=%d, counted VM<=%d per object)", cost, b.N*files, statements, vm)
	}
	items := float64(b.N * files)
	b.ReportMetric(float64(cost.Statements)/items, "stmts/object")
	b.ReportMetric(float64(cost.VMSteps)/items, "counted-vm/object")
	b.ReportMetric(float64(cost.Transactions)/float64(b.N), "tx/page")
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/items, "ns/object")
}

// Read the actual lookup from observeInventoryTx, so its benchmark cannot
// silently keep measuring a cheaper copied query after the producer changes.
func inventoryScaleLookup(b *testing.B) string {
	b.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "inventory.go", nil, 0)
	if err != nil {
		b.Fatal(err)
	}
	var statements []string
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "observeInventoryTx" {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "QueryRowContext" {
				return true
			}
			literal, ok := call.Args[1].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			statement, err := strconv.Unquote(literal.Value)
			if err == nil && strings.HasPrefix(statement, "SELECT id,asset_id,revision,seen_job,") {
				statements = append(statements, statement)
			}
			return true
		})
	}
	if len(statements) != 1 {
		b.Fatal("the producer's unchanged-object lookup was not uniquely identified")
	}
	return statements[0]
}

type inventoryScaleState struct {
	c       *catalogtest.Catalog
	service *Service
	source  LibrarySource
	page    storage.InventoryPage
	next    int
	policy  int64
}

func (f *inventoryScaleState) job(b *testing.B) string {
	b.Helper()
	f.next++
	job := fmt.Sprintf("inventory-benchmark-%d", f.next)
	f.c.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,'inventory-benchmark','running','now')`, job)
	f.c.Exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision) VALUES(?,?,?,?,?,?)`, job, f.source.ID, f.source.Generation, f.source.Incarnation, f.source.RootIdentity, f.policy)
	f.c.Exec(`INSERT INTO inventory_source_active(source_id,job_id) VALUES(?,?) ON CONFLICT(source_id) DO UPDATE SET job_id=excluded.job_id`, f.source.ID, job)
	f.c.Exec(`INSERT INTO inventory_directories(job_id,relative_path) VALUES(?,'.')`, job)
	return job
}

func inventoryScaleFixture(b *testing.B, objects int) *inventoryScaleState {
	b.Helper()
	started := time.Now()
	c := catalogtest.Open(b)
	root, err := filepath.EvalSymlinks(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	selected := map[int]bool{}
	for i := range 128 {
		n := i * (objects - 1) / 127
		selected[n] = true
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("Film %09d (2000).mkv", n)), nil, 0600); err != nil {
			b.Fatal(err)
		}
	}
	c.Library("inventory-benchmark", "Inventory benchmark", "movie", root)
	c.Exec(`UPDATE library_scan_policies SET tier='file_list_only',operations_json='[]' WHERE library_id='inventory-benchmark'`)
	page, err := storage.LocalInventoryPage(storage.InventoryRequest{Root: root, RelativePath: ".", Limit: 128})
	if err != nil || len(page.Entries) != 128 {
		b.Fatalf("real sample page: %d %v", len(page.Entries), err)
	}
	c.Exec(`UPDATE library_sources SET root_identity=? WHERE library_id='inventory-benchmark'`, page.RootIdentity)
	service := New(c.DB)
	sources, err := service.LibrarySources(context.Background(), "inventory-benchmark")
	if err != nil || len(sources) != 1 {
		b.Fatalf("source: %v", err)
	}
	f := &inventoryScaleState{c: c, service: service, source: sources[0], page: page}
	if err := c.DB.QueryRow(`SELECT revision FROM library_scan_policies WHERE library_id='inventory-benchmark'`).Scan(&f.policy); err != nil {
		b.Fatal(err)
	}
	job := f.job(b)
	if _, err := service.CommitInventoryPages(context.Background(), job, []InventoryPageWrite{{Relative: ".", Page: page}}); err != nil {
		b.Fatal(err)
	}
	c.Exec(`UPDATE jobs SET status='complete' WHERE id=?`, job)

	// Bounded fixture batches retain the actual constraints, indexes, foreign
	// keys and triggers. Every filler observation owns a distinct asset. Their
	// files are intentionally virtual; only the measured page exists on disk.
	plan := dbwork.WritePlan{Name: "inventory-benchmark-fixture", Rows: 65536, Pages: 8192, VMOps: 64_000_000, Why: "isolated benchmark fixture construction, excluded from measured producer work"}
	ctx := dbwork.WithPlan(context.Background(), plan)
	checkpoints, err := dbwork.NewCheckpointSchedule(context.Background(), c.DB)
	if err != nil {
		b.Fatal(err)
	}
	for after := 0; after < objects; after += 512 {
		var batch []int
		for n := after; n < min(after+512, objects); n++ {
			if !selected[n] {
				batch = append(batch, n)
			}
		}
		encoded, err := json.Marshal(batch)
		if err != nil {
			b.Fatal(err)
		}
		err = dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration)
 SELECT printf('inventory-benchmark-asset-%09d',value),?1||printf('Film %09d (2000).mkv',value),1,1,'mkv','','',0,0,0 FROM json_each(?2)`, root+string(filepath.Separator), string(encoded)); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,seen_job)
 SELECT printf('inventory-benchmark-object-%09d',value),?1,printf('inventory-benchmark-asset-%09d',value),?2,printf('Film %09d (2000).mkv',value),'fixture','{}',1,1,'fixture' FROM json_each(?3)`, f.source.ID, f.source.Incarnation, string(encoded))
			return err
		})
		if err != nil {
			b.Fatal(err)
		}
		checkpoints.Tick(context.Background())
	}
	var actual, assets int
	if err := c.DB.QueryRow(`SELECT count(*),count(DISTINCT asset_id) FROM inventory_objects WHERE source_id=?`, f.source.ID).Scan(&actual, &assets); err != nil || actual != objects || assets != objects {
		b.Fatalf("normalized fixture: objects=%d distinct_assets=%d want=%d error=%v", actual, assets, objects, err)
	}
	b.Logf("INVENTORY_FIXTURE objects=%d distinct_assets=%d physical_sample=128 construction_seconds=%.3f (excluded)", actual, assets, time.Since(started).Seconds())
	return f
}
