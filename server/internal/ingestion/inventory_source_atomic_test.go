//go:build linux

package ingestion

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// These are actual bounded source pages, with their genuine opaque cursors.
// The group is still one atomic commit, even when the last page fails after
// the first page's subtitle/object/Reading writes have run.
func TestInventorySourceGroupAtomicReplayAndFences(t *testing.T) {
	f := newLoopFixture(t, "book", "A/First.epub", "B/Second.epub")
	job := f.queue()
	source := startInventorySource(t, f, job)
	if err := f.s.readDirectoryPage(context.Background(), job.ID, source, ".", inventorySourceNames); err != nil {
		t.Fatal(err)
	}
	var pages []catalog.InventoryPageWrite
	for _, relative := range []string{"A", "B"} {
		var revision string
		if err := f.db.QueryRow(`SELECT revision FROM inventory_directories WHERE job_id=? AND relative_path=?`, job.ID, relative).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		page, err := storage.LocalInventoryPage(storage.InventoryRequest{Root: f.root, RelativePath: relative, RootIdentity: source.RootIdentity, DirectoryRevision: revision, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, catalog.InventoryPageWrite{Relative: relative, Page: page})
	}
	commit := func(pages []catalog.InventoryPageWrite) error {
		return f.s.catalog.CommitInventoryPages(context.Background(), job.ID, pages)
	}
	f.exec(`CREATE TRIGGER refuse_second_page BEFORE INSERT ON inventory_objects WHEN NEW.relative_path='B/Second.epub' BEGIN SELECT RAISE(ABORT,'injected page failure'); END`)
	if err := commit(pages); err == nil {
		t.Fatal("injected error ignored")
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects`); n != 0 {
		t.Fatalf("partial objects=%d", n)
	}
	if n := f.count(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND relative_path<>'.' AND pages>0`, job.ID); n != 0 {
		t.Fatalf("partial cursors=%d", n)
	}
	if n := f.count(`SELECT count(*) FROM subtitle_inventory_entries WHERE job_id=? AND name LIKE '%.epub'`, job.ID); n != 0 {
		t.Fatalf("partial subtitles=%d", n)
	}
	if n := f.count(`SELECT count(*) FROM reading_publications`); n != 0 {
		t.Fatalf("partial Reading facts=%d", n)
	}
	f.exec(`DROP TRIGGER refuse_second_page`)
	if err := commit(pages); err != nil {
		t.Fatal(err)
	}
	before := inventorySemanticSnapshot(t, f, job.ID)
	discovered := f.count(`SELECT discovered FROM inventory_runs WHERE job_id=?`, job.ID)
	if err := commit(pages); err != nil {
		t.Fatal(err)
	}
	if after := inventorySemanticSnapshot(t, f, job.ID); !reflect.DeepEqual(before, after) {
		t.Fatal("replay changed catalogue/Reading facts")
	}
	if n := f.count(`SELECT discovered FROM inventory_runs WHERE job_id=?`, job.ID); n != discovered {
		t.Fatal("replay changed discovered counter")
	}
	if n := f.count(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND relative_path<>'.' AND pages=1`, job.ID); n != 2 {
		t.Fatal("replay advanced cursors")
	}
	// A stale cursor for one member invalidates the complete group.
	stale := append([]catalog.InventoryPageWrite(nil), pages...)
	stale[1].Cursor = "stale"
	stale[1].Page.NextCursor = "different"
	if err := commit(stale); !errors.Is(err, catalog.ErrStaleContinuation) {
		t.Fatalf("stale cursor: %v", err)
	}
	if after := inventorySemanticSnapshot(t, f, job.ID); !reflect.DeepEqual(before, after) {
		t.Fatal("stale member changed group")
	}
	// The same durable run/source fences still guard this exact source page group.
	for _, tc := range []struct{ name, change, restore string }{
		{"paused", `UPDATE jobs SET status='paused' WHERE id=?`, `UPDATE jobs SET status='running' WHERE id=?`},
		{"generation", `UPDATE library_sources SET generation=generation+1 WHERE id=?`, `UPDATE library_sources SET generation=generation-1 WHERE id=?`},
		{"incarnation", `UPDATE library_sources SET incarnation=incarnation||':changed' WHERE id=?`, `UPDATE library_sources SET incarnation=substr(incarnation,1,length(incarnation)-8) WHERE id=?`},
		{"root", `UPDATE library_sources SET root_identity=root_identity||':changed' WHERE id=?`, `UPDATE library_sources SET root_identity=substr(root_identity,1,length(root_identity)-8) WHERE id=?`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := f.source
			if tc.name == "paused" {
				id = job.ID
			}
			f.exec(tc.change, id)
			if err := commit(pages); !errors.Is(err, context.Canceled) {
				t.Fatalf("fence: %v", err)
			}
			f.exec(tc.restore, id)
			if after := inventorySemanticSnapshot(t, f, job.ID); !reflect.DeepEqual(before, after) {
				t.Fatal("fenced group changed catalogue")
			}
		})
	}
}
