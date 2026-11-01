package compactcatalog

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestPacedSearchReplacementKeepsOneAtomicDocument(t *testing.T) {
	db := cc1OpenDB(t, "search-unit.sqlite")
	lib := cc1Library(t, db, "films", "Films", "movie", "/films")
	for i := 0; i < 2; i++ {
		title := "Amber Birch Cypress Dogwood Elm Fir Garden Hazel Iris Juniper Kiwi Laurel Maple Nettle Olive Pine Quince Rose Spruce Tulip"
		cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: fmt.Sprintf("film-%d", i), Title: title}, nil)
	}
	w := NewWorker(db)
	w.PaceToHold(BatchHoldTarget)
	if err := w.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	cc1Exec(t, db, `UPDATE catalog_derivations SET rebuilding=0 WHERE domain<>17`)
	cc1Exec(t, db, `DELETE FROM catalog_dirty WHERE domain<>17`)
	visibilityFanoutReset(t, db, 1)
	w.observeBatch(DomainSearch, 2, 2*time.Nanosecond, 2)
	dbwork.ResetPlans()
	if n, err := w.Step(context.Background(), 500); err != nil || n != 1 {
		t.Fatalf("paced document turn=%d: %v", n, err)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_search_documents`); got != 1 {
		t.Fatalf("published %d documents in one turn, want 1", got)
	}
	if plans := dbwork.Plans(); plans.Overruns != 0 || plans.Largest.Rows > derivationUnitPlan.Rows {
		t.Fatalf("single document exceeded its existing unit plan: %+v", plans)
	}
}

func TestSearchCleanupPagesLeavesBeforeDeletingDocuments(t *testing.T) {
	for _, slot := range []int{0, 1} {
		t.Run(fmt.Sprint(slot), func(t *testing.T) {
			db := cc1OpenDB(t, "cleanup.sqlite")
			lib := cc1Library(t, db, "films", "Films", "movie", "/films")
			entity := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film", Title: "Film"}, nil)
			w := NewWorker(db)
			w.PaceToHold(BatchHoldTarget)
			if err := w.prepare(context.Background()); err != nil {
				t.Fatal(err)
			}
			// Both search generations have real native FTS entries. Only the
			// inactive generation may be pruned during rebuilding.
			for _, generation := range []int{0, 1} {
				cc1Exec(t, db, searchTables(`INSERT INTO catalog_search_documents(entity_id,title,title_fold,entity_pid,search,normalized,kind) VALUES(?,'Film','film','film','film','film','k1')`, generation), entity)
			}
			for i := 0; i < 49; i++ {
				cc1Exec(t, db, `INSERT INTO catalog_search_prefix_terms(id,prefix) VALUES(?,?)`, i+1, fmt.Sprintf("prefix-%03d", i))
				cc1Exec(t, db, searchTables(`INSERT INTO catalog_search_prefix_rows(term_id,entity_id,kind,library_id,tier,container,rating_key,label_key) VALUES(?,?,1,?,0,0,'','')`, 1-slot), i+1, entity, lib)
			}
			cc1Exec(t, db, `UPDATE catalog_derivations SET rebuilding=0`)
			cc1Exec(t, db, `UPDATE catalog_derivations SET active_slot=?,rebuilding=1,cleaning=1 WHERE domain=17`, slot)
			cc1Exec(t, db, `DELETE FROM catalog_dirty`)
			visibilityFanoutReset(t, db, 1)
			// An inexpensive document history must not size the cleanup phase.
			w.observeBatch(DomainSearch, 2, 2*time.Nanosecond, 2)
			dbwork.ResetPlans()
			if _, err := w.Step(context.Background(), 500); err != nil {
				t.Fatal(err)
			}
			if got := cc1Scalar(t, db, searchTables(`SELECT count(*) FROM catalog_search_prefix_rows`, 1-slot)); got != 25 {
				t.Fatalf("first page left %d leaves, want 25", got)
			}
			if got := cc1Scalar(t, db, searchTables(`SELECT count(*) FROM catalog_search_documents`, 1-slot)); got != 1 {
				t.Fatal("parent deleted before leaf exhaustion")
			}
			for turn := 0; turn < 15; turn++ {
				// A new owner must resume from durable remaining leaves, even
				// when the previous page committed with just one raw leaf.
				w = NewWorker(db)
				w.PaceToHold(BatchHoldTarget)
				if _, err := w.Step(context.Background(), 500); err != nil {
					t.Fatal(err)
				}
				if cc1Scalar(t, db, `SELECT cleaning FROM catalog_derivations WHERE domain=17`) == 0 {
					break
				}
			}
			if got := cc1Scalar(t, db, `SELECT cleaning FROM catalog_derivations WHERE domain=17`); got != 0 {
				t.Fatal("cleanup did not reach committed EOF")
			}
			if got := cc1Scalar(t, db, searchTables(`SELECT count(*) FROM catalog_search_documents`, 1-slot)); got != 0 {
				t.Fatal("inactive document survived cleanup")
			}
			if got := cc1Scalar(t, db, searchTables(`SELECT count(*) FROM catalog_search_prefix_counts`, 1-slot)); got != 0 {
				t.Fatalf("%d empty count rows survived", got)
			}
			if got := cc1Scalar(t, db, searchTables(`SELECT count(*) FROM catalog_search_titles WHERE catalog_search_titles MATCH 'film'`, slot)); got != 1 {
				t.Fatal("active FTS generation changed")
			}
			plans := dbwork.Plans()
			if plans.Overruns != 0 || plans.Largest.Rows > dbwork.DefaultPlan(dbwork.ClassMaintenance).Rows {
				t.Fatalf("cleanup exceeded the ordinary plan: %+v", plans)
			}
		})
	}
}
