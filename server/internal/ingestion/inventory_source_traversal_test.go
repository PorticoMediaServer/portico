//go:build linux

package ingestion

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Compare the former real 128-name traversal with the bounded producer over
// the same source. Reopen the database and recreate the service after EVERY
// small-page commit: only durable cursors and source evidence can carry it on.
// A rescan must preserve the exact public/object/asset identities and all
// catalogue/Reading facts, not merely the number of discovered files.
func TestInventorySourceTraversalRestartParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"movie", "book"} {
		t.Run(kind, func(t *testing.T) {
			ext := "mkv"
			if kind == "book" {
				ext = "epub"
			}
			files := []string{
				"Äuthor/Series/01 - First." + ext, "Äuthor/Series/02 - Second." + ext,
				"Äuthor/Series/03 - Third." + ext, "日本/Other." + ext, "Loose." + ext,
				"Loose.en.srt", "Loose.fr.srt", "Ignored/.hidden"}
			f := newLoopFixture(t, kind, files...)
			var dbPath string
			var seq int
			var name string
			if err := f.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &dbPath); err != nil {
				t.Fatal(err)
			}
			old := f.queue()
			source := startInventorySource(t, f, old)
			for turn := 0; turn < 100; turn++ {
				var relative, cursor, revision string
				err := f.db.QueryRow(`SELECT relative_path,cursor,revision FROM inventory_directories WHERE job_id=? AND state='pending' ORDER BY relative_path LIMIT 1`, old.ID).Scan(&relative, &cursor, &revision)
				if err == sql.ErrNoRows {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				page, err := storage.LocalInventoryPage(storage.InventoryRequest{Root: f.root, RelativePath: relative, RootIdentity: source.RootIdentity, DirectoryRevision: revision, Cursor: cursor, Limit: 128})
				if err != nil {
					t.Fatal(err)
				}
				disc, err := f.s.discRootObservation(context.Background(), source, relative, page)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.s.catalog.CommitInventoryPages(context.Background(), old.ID, []catalog.InventoryPageWrite{{Relative: relative, Cursor: cursor, Page: page, DiscRoot: disc}}); err != nil {
					t.Fatal(err)
				}
			}
			f.finish(old)
			fresh := newLoopFixture(t, kind, files...)
			freshJob := fresh.queue()
			fresh.finish(freshJob)
			oldContent, freshContent := inventoryContentSnapshot(t, f, old.ID), inventoryContentSnapshot(t, fresh, freshJob.ID)
			if !reflect.DeepEqual(oldContent, freshContent) {
				t.Fatalf("first-import large/small page contents differ:\nold %v\nnew %v", oldContent, freshContent)
			}

			before := inventorySemanticSnapshot(t, f, old.ID)
			next := f.queue()
			source = startInventorySource(t, f, next)
			t.Cleanup(func() { f.db.Close() })
			turns := 0
			for ; turns < 100; turns++ {
				if f.count(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND state='pending'`, next.ID) == 0 {
					break
				}
				if err := f.s.inventoryPage(context.Background(), next.ID, source); err != nil {
					t.Fatal(err)
				}
				if err := f.db.Close(); err != nil {
					t.Fatal(err)
				}
				db, err := persistence.Open(dbPath)
				if err != nil {
					t.Fatal(err)
				}
				f.db = db
				f.s = New(db, catalog.New(db), assets.Probe{})
				source = f.librarySource()
			}
			if turns == 100 {
				t.Fatal("durable traversal failed to terminate")
			}
			f.finish(next)
			after := inventorySemanticSnapshot(t, f, next.ID)
			for name, want := range before {
				if !reflect.DeepEqual(after[name], want) {
					t.Errorf("%s differs after restart traversal:\nold %v\nnew %v", name, want, after[name])
				}
			}
			if len(after["objects"]) != 5 {
				t.Fatalf("lost media: %v", after["objects"])
			}
			if kind == "book" && len(after["publications"]) != 5 {
				t.Fatalf("lost Reading publications: %v", after["publications"])
			}
			t.Logf("reopened after %d bounded producer commits", turns)
		})
	}
}

func inventorySemanticSnapshot(t *testing.T, f *loopFixture, job string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for name, q := range map[string]string{
		"objects":        `SELECT id,asset_id,object_identity,revision,relative_path,state,retired FROM inventory_objects ORDER BY relative_path`,
		"entities":       `SELECT id,hex(public_id),library_id,kind,parent_id,title,sort_title,year,retired FROM catalog_entities ORDER BY id`,
		"identities":     `SELECT root,source_key,hex(public_id),kind FROM catalog_identities ORDER BY root,source_key`,
		"assets":         `SELECT * FROM catalog_assets ORDER BY id`,
		"links":          `SELECT * FROM catalog_asset_links ORDER BY entity_id,asset_id`,
		"publications":   `SELECT * FROM reading_publications ORDER BY item_id,asset_id`,
		"reading-values": `SELECT * FROM reading_source_values ORDER BY item_id,asset_id,field,source`,
		"reading-types":  `SELECT * FROM reading_publication_types ORDER BY asset_id`,
		"folders":        `SELECT relative_path,identity,revision FROM inventory_directory_cache ORDER BY relative_path`,
		"issues":         `SELECT relative_path,reason,line,protect,warning FROM inventory_scan_issues WHERE job_id=? ORDER BY relative_path,reason,line`,
	} {
		var rows *sql.Rows
		var err error
		if name == "issues" {
			rows, err = f.db.Query(q, job)
		} else {
			rows, err = f.db.Query(q)
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err = rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for i, v := range values {
				if b, ok := v.([]byte); ok {
					values[i] = string(b)
				}
			}
			out[name] = append(out[name], fmt.Sprint(values))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// Fresh imports allocate unrelated IDs. Compare the complete object-to-asset-
// entity graph by source-relative locator, typed facts and retained identity
// cardinality; the rescan above separately requires exact identity equality.
func inventoryContentSnapshot(t *testing.T, f *loopFixture, job string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	queries := map[string]string{
		"objects":        `SELECT o.relative_path,o.state,o.retired,a.size,a.container,a.available,e.kind,e.title,parent.kind,parent.title,l.part_index,l.available,l.start_seconds,l.end_seconds,l.edition FROM inventory_objects o LEFT JOIN catalog_assets a ON a.token=o.asset_id LEFT JOIN catalog_asset_links l ON l.asset_id=a.id LEFT JOIN catalog_entities e ON e.id=l.entity_id LEFT JOIN catalog_entities parent ON parent.id=e.parent_id ORDER BY o.relative_path,e.kind,e.title`,
		"reading":        `SELECT o.relative_path,p.format FROM reading_publications p JOIN catalog_assets a ON a.id=p.asset_id JOIN inventory_objects o ON o.asset_id=a.token ORDER BY o.relative_path`,
		"values":         `SELECT o.relative_path,v.field,v.text_value,v.number_value,v.source FROM reading_source_values v JOIN inventory_objects o ON o.id=v.inventory_object_id ORDER BY o.relative_path,v.field,v.source`,
		"identity-graph": `SELECT e.kind,e.title,count(DISTINCT i.public_id),count(*) FROM catalog_identities i JOIN catalog_entities e ON e.public_id=i.public_id GROUP BY e.kind,e.title ORDER BY e.kind,e.title`,
		"folders":        `SELECT relative_path FROM inventory_directory_cache ORDER BY relative_path`,
		"issues":         `SELECT relative_path,reason,line,protect,warning FROM inventory_scan_issues WHERE job_id=? ORDER BY relative_path,reason,line`,
		"managed":        `SELECT path,name FROM subtitle_inventory_entries WHERE job_id=? ORDER BY path`,
	}
	for name, q := range queries {
		var rows *sql.Rows
		var err error
		if name == "issues" || name == "managed" {
			rows, err = f.db.Query(q, job)
		} else {
			rows, err = f.db.Query(q)
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err = rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for i, v := range values {
				if b, ok := v.([]byte); ok {
					values[i] = string(b)
				}
			}
			out[name] = append(out[name], strings.ReplaceAll(fmt.Sprint(values), f.root, "<root>"))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}
