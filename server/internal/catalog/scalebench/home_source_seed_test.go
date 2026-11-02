package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Extend the existing narrow reference seeder with a focused serving-index
// fixture. No full derivation, search vocabulary, Reading publication or scan
// is claimed. All production triggers stay installed. The exact memberships,
// availability and recent metrics needed by Home's source SQL are populated
// set-wise, like the existing narrow serving-index capacity fixtures.
func TestHomeSourceSeedMeasurement(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_HOME_SOURCE_ITEMS"))
	if n == 0 {
		t.Skip("hosted Home source fixture")
	}
	root := os.Getenv("PORTICO_SCALE_SEEDS")
	if root == "" {
		t.Fatal("explicit seed directory required")
	}
	filmOnly := os.Getenv("PORTICO_HOME_SOURCE_FILMS") == "1"
	prefix := "home-sources"
	if filmOnly {
		prefix = "new-server-films"
	}
	seed := filepath.Join(root, fmt.Sprintf("%s-%d.sqlite", prefix, n))
	if _, err := os.Stat(seed); err == nil {
		return
	}
	start := time.Now()
	db, err := persistence.Open(seed + ".building")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	if filmOnly {
		_, _, err = seedBulkFactsShape(db, n, true, referenceShape{movies: n, shows: 2, albums: 2, people: max(24, n/100)}, true)
	} else {
		_, _, err = seedBulkFactsDensity(db, n, true)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !filmOnly {
		exec(`INSERT INTO libraries(id,name,kind,root) VALUES('reading','Reading','book','/fixture/reading')`)
		var next int
		if err = db.QueryRow(`SELECT COALESCE(max(id),0)+1 FROM catalog_entities`).Scan(&next); err != nil {
			t.Fatal(err)
		}
		for off := 0; off < n; off += 1000 {
			exec(`WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?)
 INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head)
 SELECT ?+i,randomblob(16),(SELECT id FROM catalog_libraries WHERE library_id='reading'),20,printf('Publication %09d',i),printf('publication %09d',i),'p' FROM ids`, off, min(n, off+1000)-1, next)
			exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
		}
	}
	var maximum int
	if err = db.QueryRow(`SELECT max(id) FROM catalog_entities`).Scan(&maximum); err != nil {
		t.Fatal(err)
	}
	for after := 0; after < maximum; after += 256 {
		end := min(maximum, after+256)
		if filmOnly {
			exec(`INSERT INTO metadata_details(item_id,provider,provider_id,source_url,observed_at)
 SELECT id,'tmdb',printf('fixture-film-%09d',id),'','' FROM catalog_entities WHERE kind=1 AND id>? AND id<=?`, after, end)
		}
		exec(`INSERT INTO catalog_item_availability(entity_id,available,retired)
 SELECT e.id,1,e.retired FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.id>? AND e.id<=? AND k.playable=1`, after, end)
		exec(`INSERT INTO catalog_browse_memberships(entity_id,item_id,source)
 SELECT e.id,e.id,0 FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.id>? AND e.id<=? AND k.playable=1`, after, end)
		exec(`INSERT INTO catalog_browse_memberships(entity_id,item_id,source)
 SELECT album_id,entity_id,1 FROM catalog_songs WHERE entity_id>? AND entity_id<=?`, after, end)
		exec(`INSERT INTO catalog_browse_memberships(entity_id,item_id,source)
 SELECT show_id,entity_id,1 FROM catalog_episodes WHERE entity_id>? AND entity_id<=?
 UNION ALL SELECT season_id,entity_id,1 FROM catalog_episodes WHERE entity_id>? AND entity_id<=?`, after, end, after, end)
		exec(`INSERT INTO catalog_browse_memberships(entity_id,item_id,source)
 SELECT a.artist_id,s.entity_id,1 FROM catalog_songs s JOIN catalog_albums a ON a.entity_id=s.album_id WHERE s.entity_id>? AND s.entity_id<=?`, after, end)
		exec(`INSERT INTO catalog_browse_member_metrics(entity_id,item_id,source,available_links,duration,added)
 SELECT m.entity_id,m.item_id,m.source,1,3600,d.added_text FROM catalog_browse_memberships m
 JOIN catalog_item_details d ON d.entity_id=m.item_id WHERE m.item_id>? AND m.item_id<=?`, after, end)
		if after%4096 == 0 {
			exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
		}
	}
	for after := 0; after < maximum; after += 256 {
		exec(`INSERT INTO catalog_browse_rows(entity_id,library_id,kind,title,sort_key,head,year,added_text,item_id,rating_key,label_key,available,recent,backdrop,recent_text,availability_state)
 SELECT e.id,e.library_id,e.kind,e.title,e.sort_key,e.sort_head,COALESCE(e.year,0),d.added_text,
 CASE WHEN k.playable=1 THEN e.id END,'','',CASE WHEN k.playable=1 OR EXISTS(SELECT 1 FROM catalog_browse_memberships m WHERE m.entity_id=e.id) THEN 1 ELSE 0 END,1,0,
 CASE WHEN e.kind IN(1,2,6) THEN (SELECT max(added) FROM catalog_browse_member_metrics m WHERE m.entity_id=e.id) END,
 CASE WHEN k.playable=1 OR EXISTS(SELECT 1 FROM catalog_browse_memberships m WHERE m.entity_id=e.id) THEN 'available' ELSE 'unavailable' END
 FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind LEFT JOIN catalog_item_details d ON d.entity_id=e.id WHERE e.id>? AND e.id<=?`, after, min(maximum, after+256))
		if after%4096 == 0 {
			exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
		}
	}
	if err = persistence.RefreshPlannerStatistics(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(seed+".building", seed); err != nil {
		t.Fatal(err)
	}
	result := map[string]any{"playableFacts": n, "filmOnly": filmOnly, "filmProviderIdentities": filmOnly, "seconds": time.Since(start).Seconds(), "boundary": "existing narrow scalebench facts plus focused Home serving projections; no full derive/scan/Reading publication claim"}
	b, _ := json.MarshalIndent(result, "", "  ")
	t.Log(string(b))
	if report := os.Getenv("PORTICO_SCALE_REPORT"); report != "" {
		if err = os.WriteFile(filepath.Join(report, "seed.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
