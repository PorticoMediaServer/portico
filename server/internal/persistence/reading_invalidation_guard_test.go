package persistence

import (
	"context"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestReadingInventoryInvalidationGuardCoversWatchedColumns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var statement string
	if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='reading_attribution_inventory_objects_update'`).Scan(&statement); err != nil {
		t.Fatal(err)
	}
	parts := regexp.MustCompile(`(?is)BEFORE UPDATE OF (.*?) ON inventory_objects\s+WHEN (.*?) BEGIN`).FindStringSubmatch(statement)
	if len(parts) != 3 {
		t.Fatalf("missing NULL-safe input guard: %s", statement)
	}
	watched := strings.Split(parts[1], ",")
	for i := range watched {
		watched[i] = strings.TrimSpace(watched[i])
	}
	slices.Sort(watched)
	pairs := regexp.MustCompile(`(?i)OLD\.(\w+)\s+IS NOT\s+NEW\.(\w+)`).FindAllStringSubmatch(parts[2], -1)
	guarded := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		if pair[1] != pair[2] {
			t.Fatalf("mismatched guard pair %v", pair)
		}
		guarded = append(guarded, pair[1])
	}
	slices.Sort(guarded)
	if !slices.Equal(watched, guarded) {
		t.Fatalf("UPDATE OF %v differs from NULL-safe WHEN %v", watched, guarded)
	}
	for _, event := range []string{"insert", "delete"} {
		if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name=?`, "reading_attribution_inventory_objects_"+event).Scan(&statement); err != nil {
			t.Fatal(err)
		}
		prefix := strings.SplitN(strings.ToUpper(statement), "BEGIN", 2)[0]
		if strings.Contains(prefix, "WHEN") {
			t.Fatalf("%s invalidation became conditional", event)
		}
	}
}

func TestReadingInventoryInvalidatesOnlyEffectiveChangesAndKeepsInsertDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	library := persistenceLibrary(t, db, "read", "Read", "book", "/read")
	other := persistenceLibrary(t, db, "other", "Other", "book", "/other")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES('asset','/read/book.pdf',1,1,'pdf','','',0,0,0),('second-asset','/read/other.pdf',1,1,'pdf','','',0,0,0)`)
	exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('object','read','asset','root','book.pdf','rev','{}',1,1)`)
	changes := map[string]string{"id": "'renamed'", "source_id": "'other'", "asset_id": "'second-asset'", "root_incarnation": "'new-root'", "relative_path": "'other.pdf'", "revision": "'new-rev'", "state": "'missing'", "retired": "1", "unsupported_reason": "'unsupported'"}
	for column, value := range changes {
		t.Run(column, func(t *testing.T) {
			w, err := dbwork.Begin(context.Background(), db, dbwork.ClassMaintenance)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Rollback()
			tx := w.Tx()
			generation := func(lib int64) int64 {
				t.Helper()
				var g int64
				if err := tx.QueryRow(`SELECT input_generation FROM reading_attribution_state WHERE library_id=?`, lib).Scan(&g); err != nil {
					t.Fatal(err)
				}
				return g
			}
			before, otherBefore := generation(library), generation(other)
			if _, err := tx.Exec(`UPDATE inventory_objects SET ` + column + `=` + column + `,seen_job='new-job' WHERE id='object'`); err != nil {
				t.Fatal(err)
			}
			if got := generation(library); got != before {
				t.Fatalf("no-op %s invalidated: %d -> %d", column, before, got)
			}
			if _, err := tx.Exec(`UPDATE inventory_objects SET ` + column + `=` + value + ` WHERE id='object'`); err != nil {
				t.Fatal(err)
			}
			if got := generation(library); got <= before {
				t.Fatalf("real %s change did not invalidate", column)
			}
			if column == "source_id" && generation(other) <= otherBefore {
				t.Fatal("move failed to invalidate new library")
			}
		})
	}
	w, err := dbwork.Begin(context.Background(), db, dbwork.ClassMaintenance)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()
	tx := w.Tx()
	generation := func() int64 {
		var g int64
		if err := tx.QueryRow(`SELECT input_generation FROM reading_attribution_state WHERE library_id=?`, library).Scan(&g); err != nil {
			t.Fatal(err)
		}
		return g
	}
	before := generation()
	if _, err := tx.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('object','read','asset','root','book.pdf','rev','{}',1,1) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision`); err != nil {
		t.Fatal(err)
	}
	if generation() <= before {
		t.Fatal("attempted UPSERT INSERT hazard was hidden, rather than removed from producer")
	}
	before = generation()
	if _, err := tx.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('new','read','asset','root','new.pdf','rev','{}',1,1)`); err != nil {
		t.Fatal(err)
	}
	if generation() <= before {
		t.Fatal("genuine INSERT did not invalidate")
	}
	before = generation()
	if _, err := tx.Exec(`DELETE FROM inventory_objects WHERE id='new'`); err != nil {
		t.Fatal(err)
	}
	if generation() <= before {
		t.Fatal("genuine DELETE did not invalidate")
	}
}
