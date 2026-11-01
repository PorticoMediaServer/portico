//go:build linux

package ingestion

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestLegacyInventoryConsumesRawRejectsAcrossRestart(t *testing.T) {
	f := newLoopFixture(t, "movie")
	c := catalogtest.New(t, f.db)
	lib := c.Handle(f.library)
	// Two full rejected pages precede the eligible legacy rows. Keep the real
	// public tokens: dependent source records already reference those identities.
	type candidate struct{ token, relative string }
	candidates := make([]candidate, 0, 25)
	for i := 0; i < 25; i++ {
		relative := fmt.Sprintf("Film %02d.mkv", i)
		item := c.Movie(lib, filepath.Join(f.root, relative), relative, 2000)
		candidates = append(candidates, candidate{item.Token, relative})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].token < candidates[j].token })
	for i, v := range candidates {
		if i < 16 {
			f.exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
    SELECT ?,id,?,incarnation,?,'fixture','{}',0,0 FROM library_sources WHERE id=?`, fmt.Sprintf("object-%03d", i), v.token, v.relative, f.source)
		}
	}
	job := f.queue()
	source := startInventorySource(t, f, job)
	f.exec(`UPDATE inventory_runs SET phase='legacy_inventory',reconcile_cursor='' WHERE job_id=?`, job.ID)
	dbwork.ResetPlans()
	for page := 0; page < 2; page++ {
		if err := f.s.adoptLegacyPage(context.Background(), job.ID, source); err != nil {
			t.Fatal(err)
		}
		var cursor, phase string
		if err := f.db.QueryRow(`SELECT reconcile_cursor,phase FROM inventory_runs WHERE job_id=?`, job.ID).Scan(&cursor, &phase); err != nil {
			t.Fatal(err)
		}
		if want := candidates[(page+1)*8-1].token; cursor != want || phase != "legacy_inventory" {
			t.Fatalf("rejected page cursor=%q phase=%q, want %q legacy_inventory", cursor, phase, want)
		}
		// Recreate the owner. No process-local cursor can make progress for it.
		f.s = New(f.db, catalog.New(f.db), assets.Probe{})
	}
	for page := 0; page < 4; page++ {
		if err := f.s.adoptLegacyPage(context.Background(), job.ID, source); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.count(`SELECT count(*) FROM inventory_objects WHERE source_id=?`, f.source); got != 25 {
		t.Fatalf("adopted %d objects, want all 25", got)
	}
	var phase string
	if err := f.db.QueryRow(`SELECT phase FROM inventory_runs WHERE job_id=?`, job.ID).Scan(&phase); err != nil {
		t.Fatal(err)
	}
	if phase != "extras" {
		t.Fatalf("phase=%q, want extras after raw EOF", phase)
	}
	if plans := dbwork.Plans(); plans.Overruns != 0 {
		t.Fatalf("legacy page exceeded existing plan: %+v", plans)
	}
}

func TestLegacyInventoryCanceledFenceKeepsCursor(t *testing.T) {
	f := newLoopFixture(t, "movie")
	job := f.queue()
	source := startInventorySource(t, f, job)
	f.exec(`UPDATE inventory_runs SET phase='legacy_inventory',reconcile_cursor='before' WHERE job_id=?`, job.ID)
	f.exec(`UPDATE jobs SET status='canceled' WHERE id=?`, job.ID)
	if err := f.s.adoptLegacyPage(context.Background(), job.ID, source); err == nil {
		t.Fatal("canceled scan published a page")
	}
	var cursor string
	if err := f.db.QueryRow(`SELECT reconcile_cursor FROM inventory_runs WHERE job_id=?`, job.ID).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if cursor != "before" {
		t.Fatalf("canceled scan advanced to %q", cursor)
	}
}
