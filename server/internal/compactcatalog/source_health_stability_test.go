package compactcatalog_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// Health is a source fact. A transport disconnect neither changes the last
// accepted catalogue image nor creates one dirty key per stored title.
func TestSourceHealthFlapsKeepCatalogueEvidenceAndVerificationCursor(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("health", "Health", "movie", "/health")
	item := c.Movie(library, "/health/One.mkv", "One", 2020)
	c.Drain()
	type image struct {
		revision, predicate, source, projection int64
		link, available, retired                int
	}
	read := func() image {
		t.Helper()
		var v image
		err := c.DB.QueryRow(`SELECT r.revision,p.revision,s.source_epoch,s.projection_epoch,l.available,a.available,a.retired
 FROM library_revisions r CROSS JOIN catalog_browse_predicate_epoch p CROSS JOIN catalog_facet_source_state s
 JOIN catalog_asset_links l ON l.entity_id=? JOIN catalog_item_availability a ON a.entity_id=l.entity_id
 WHERE r.library_id='health' AND p.singleton=1 AND s.singleton=1`, item.ID).Scan(&v.revision, &v.predicate, &v.source, &v.projection, &v.link, &v.available, &v.retired)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := read()
	c.Exec(`UPDATE library_sources SET health='offline' WHERE id='health'`)
	if got := read(); got != before {
		t.Fatalf("offline changed catalogue facts: before=%+v after=%+v", before, got)
	}
	c.Exec(`UPDATE library_sources SET health='healthy' WHERE id='health'`)
	c.Exec(`UPDATE catalog_source_verification SET started=1,after_object='kept-cursor',fence_object='finite-upper' WHERE source_id='health'`)
	for i := 0; i < 5; i++ {
		c.Exec(`UPDATE library_sources SET health='offline' WHERE id='health'`)
		c.Exec(`UPDATE library_sources SET health='healthy' WHERE id='health'`)
	}
	if got := read(); got != before {
		t.Fatalf("flaps changed catalogue facts: before=%+v after=%+v", before, got)
	}
	var rows, pending, started, dirty int
	var after, upper string
	if err := c.DB.QueryRow(`SELECT (SELECT count(*) FROM catalog_source_verification),pending,started,after_object,fence_object,
 (SELECT count(*) FROM catalog_dirty WHERE domain=28) FROM catalog_source_verification WHERE source_id='health'`).Scan(&rows, &pending, &started, &after, &upper, &dirty); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || pending != 1 || started != 1 || after != "kept-cursor" || upper != "finite-upper" || dirty != 0 {
		t.Fatalf("flaps reset or duplicated work: rows=%d pending=%d started=%d cursor=%q upper=%q dirty28=%d", rows, pending, started, after, upper, dirty)
	}
	var live bool
	if err := c.DB.QueryRow(`SELECT available FROM inventory_item_availability WHERE item_id=?`, item.ID).Scan(&live); err != nil || !live {
		t.Fatalf("retained availability=%v error=%v", live, err)
	}
	// A new configuration is a new walk, unlike another health transition.
	c.Exec(`UPDATE library_sources SET generation=generation+1 WHERE id='health'`)
	if err := c.DB.QueryRow(`SELECT started,after_object,fence_object FROM catalog_source_verification WHERE source_id='health'`).Scan(&started, &after, &upper); err != nil {
		t.Fatal(err)
	}
	if started != 0 || after != "" || upper != "" {
		t.Fatal("configuration change retained an old proof cursor")
	}
	// Structural refusal changes the predicate immediately, while the retained
	// per-item facts wait for their ordinary bounded maintenance.
	before = read()
	c.Exec(`UPDATE library_sources SET health='root_changed' WHERE id='health'`)
	structural := read()
	if structural.predicate != before.predicate+1 || structural.link != before.link || structural.available != before.available {
		t.Fatalf("structural health did not fence predicates without item fanout: before=%+v after=%+v", before, structural)
	}
	c.Exec(`UPDATE library_sources SET health='root_changed' WHERE id='health'`)
	if got := read(); got != structural {
		t.Fatal("structural no-op invalidated predicates")
	}
	c.Exec(`UPDATE library_sources SET health='healthy' WHERE id='health'`)
	if got := read(); got.predicate != structural.predicate+1 {
		t.Fatal("leaving structural refusal did not fence predicates")
	}

}
