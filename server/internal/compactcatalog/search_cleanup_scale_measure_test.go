package compactcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestSearchCleanupGrowthMeasurement(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_RATING_SCALE_ITEMS"))
	if n == 0 {
		t.Skip("hosted cleanup probe")
	}
	// The companion rating probe builds/checkpoints this facts-only seed first.
	seed := filepath.Join(os.Getenv("PORTICO_SCALE_SEEDS"), fmt.Sprintf("rating-%d.sqlite", n))
	data, err := os.ReadFile(seed)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cleanup.sqlite")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	data = nil
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w := NewWorker(db)
	w.PaceToHold(BatchHoldTarget)
	if err = w.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	for entity := 1; entity <= 2; entity++ {
		cc1Exec(t, db, `INSERT INTO catalog_search_documents_next(entity_id,title,title_fold,entity_pid,search,normalized,kind) VALUES(?,'Title','title','title','title','title','k1')`, entity)
	}
	for term := 1; term <= 64; term++ {
		cc1Exec(t, db, `INSERT INTO catalog_search_prefix_terms(id,prefix) VALUES(?,?)`, term, fmt.Sprintf("cleanup-prefix-%03d", term))
		for entity := 1; entity <= 2; entity++ {
			cc1Exec(t, db, `INSERT INTO catalog_search_prefix_rows_next(term_id,entity_id,kind,library_id,tier,container,rating_key,label_key) VALUES(?,?,1,1,0,0,'','')`, term, entity)
		}
	}
	cc1Exec(t, db, `UPDATE catalog_derivations SET rebuilding=0`)
	cc1Exec(t, db, `UPDATE catalog_derivations SET active_slot=0,rebuilding=1,cleaning=1 WHERE domain=17`)
	cc1Exec(t, db, `DELETE FROM catalog_dirty`)
	visibilityFanoutReset(t, db, 1)
	w.observeBatch(DomainSearch, 2, 2*time.Nanosecond, 2)
	dbwork.ResetPlans()
	start := time.Now()
	var failure any
	func() { defer func() { failure = recover() }(); _, err = w.Step(context.Background(), 500) }()
	result := map[string]any{"items": n, "cleanupDocuments": 2, "cleanupLeavesPerDocument": 64, "elapsedMs": float64(time.Since(start).Microseconds()) / 1000, "plans": dbwork.Plans(), "panic": fmt.Sprint(failure), "error": fmt.Sprint(err)}
	if (failure != nil || err != nil) && os.Getenv("PORTICO_EXPECT_CLEANUP_FAILURE") == "" {
		t.Errorf("panic=%v error=%v", failure, err)
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	t.Log(string(b))
	if err = os.WriteFile(filepath.Join(os.Getenv("PORTICO_SCALE_REPORT"), "cleanup.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSearchReplacementGrowthMeasurement(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_RATING_SCALE_ITEMS"))
	if n == 0 {
		t.Skip("hosted search-unit probe")
	}
	seed := filepath.Join(os.Getenv("PORTICO_SCALE_SEEDS"), fmt.Sprintf("rating-%d.sqlite", n))
	data, err := os.ReadFile(seed)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "replace.sqlite")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	data = nil
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w := NewWorker(db)
	w.PaceToHold(BatchHoldTarget)
	if err = w.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	cc1Exec(t, db, `UPDATE catalog_entities SET title='Amber Birch Cypress Dogwood Elm Fir Garden Hazel Iris Juniper Kiwi Laurel Maple Nettle Olive Pine Quince Rose Spruce Tulip' WHERE id IN(1,2)`)
	cc1Exec(t, db, `UPDATE catalog_derivations SET rebuilding=0`)
	cc1Exec(t, db, `UPDATE catalog_derivations SET active_slot=0,first_build=1,cleaning=0 WHERE domain=17`)
	cc1Exec(t, db, `DELETE FROM catalog_dirty`)
	cc1Exec(t, db, `INSERT INTO catalog_dirty VALUES(17,1,1),(17,2,1)`)
	visibilityFanoutReset(t, db, 1)
	w.observeBatch(DomainSearch, 2, 2*time.Nanosecond, 2)
	dbwork.ResetPlans()
	start := time.Now()
	var failure any
	units := 0
	func() { defer func() { failure = recover() }(); units, err = w.Step(context.Background(), 500) }()
	result := map[string]any{"items": n, "queuedDocuments": 2, "units": units, "publishedDocuments": cc1Scalar(t, db, `SELECT count(*) FROM catalog_search_documents`), "elapsedMs": float64(time.Since(start).Microseconds()) / 1000, "plans": dbwork.Plans(), "panic": fmt.Sprint(failure), "error": fmt.Sprint(err)}
	if (failure != nil || err != nil) && os.Getenv("PORTICO_EXPECT_CLEANUP_FAILURE") == "" {
		t.Errorf("panic=%v error=%v", failure, err)
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	t.Log(string(b))
	if err = os.WriteFile(filepath.Join(os.Getenv("PORTICO_SCALE_REPORT"), "replacement.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}
