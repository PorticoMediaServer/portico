package enrichment

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// A weekly delta re-matches only the library works it touches: one whose work
// it changes, one whose work it removes, and one it newly holds an id for.
// A matched work it doesn't name is left alone (B2 R7: a delta re-matching the
// whole library cost as much as a full import).
func TestADeltaRematchesOnlyTheWorksItTouches(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	heat := c.Movie(films, "/m/heat.mkv", "Heat", 1995)
	thief := c.Movie(films, "/m/thief.mkv", "Thief", 1981)
	collateral := c.Movie(films, "/m/collateral.mkv", "Collateral", 2004)
	ronin := c.Movie(films, "/m/ronin.mkv", "Ronin", 1998)
	c.Exec(`INSERT INTO catalog_external_ids VALUES(?,'tmdb','movie','949'),(?,'tmdb','movie','11524'),(?,'tmdb','movie','1538'),(?,'tmdb','movie','8195')`, heat.ID, thief.ID, collateral.ID, ronin.ID)
	c.Drain()
	p := newPublisher(t)
	full := p.build("screen-1", "full", "1", screenV1)
	p.domains["screen"] = ManifestDomain{DatasetVersion: "1", VocabularyVersion: 1, Full: full}
	s := publisherService(c.DB, p, t.TempDir())
	if _, err := s.Step(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	matched := func() []int64 {
		var out []int64
		rows, _ := c.DB.Query(`SELECT entity_id FROM catalog_dataset_works ORDER BY entity_id`)
		for rows.Next() {
			var id int64
			rows.Scan(&id)
			out = append(out, id)
		}
		rows.Close()
		return out
	}
	if got := matched(); !slices.Equal(got, []int64{heat.ID, thief.ID, collateral.ID}) {
		t.Fatalf("before the delta %v", got)
	}
	// The installed file has the similar-title lookup's index (a work's ids by qid).
	installed, err := openDataset(s.path("screen"), true)
	if err != nil {
		t.Fatal(err)
	}
	var indexed int
	installed.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='work_ids_by_qid'`).Scan(&indexed)
	var plan string
	installed.QueryRow(`EXPLAIN QUERY PLAN SELECT provider,provider_id FROM work_ids WHERE qid=1`).Scan(new(int), new(int), new(int), &plan)
	installed.Close()
	if indexed != 1 {
		t.Fatal("the installed file has no work_ids_by_qid index")
	}
	if !strings.Contains(plan, "work_ids_by_qid") {
		t.Fatalf("a similar title's lookup still scans: %s", plan)
	}

	// The delta: Heat's tags change, Collateral is removed, and Ronin arrives.
	delta := p.build("screen-2", "delta", "2", func(t *testing.T, db *sql.DB) {
		mustExec(t, db, `INSERT INTO vocabulary VALUES(1,'tone:gritty','tone','Gritty','Hard-edged.','["movie","show"]',1,NULL,NULL),(2,'theme:heist','theme','Heists','A robbery drives the story.','["movie","show"]',1,NULL,NULL),(3,'pacing:slow-burn','pacing','Slow burn','Takes its time.','["movie","show"]',1,NULL,NULL)`)
		mustExec(t, db, `INSERT INTO works VALUES(47703,1,0,0,1,2,1995,71,'Heat'),(999123,1,0,0,1,2,1998,40,'Ronin')`)
		mustExec(t, db, `INSERT INTO work_ids VALUES(1,949,47703),(1,8195,999123)`)
		mustExec(t, db, `INSERT INTO work_tags VALUES(47703,3,3),(999123,1,3)`)
		mustExec(t, db, `INSERT INTO removed VALUES(223596)`)
	})
	delta.Version, delta.BaseVersion = "2", "1"
	p.domains["screen"] = ManifestDomain{DatasetVersion: "2", VocabularyVersion: 1, Full: full, Deltas: []ManifestFile{delta}}
	c.Exec(`UPDATE enrichment_domains SET checked_at='2000-01-01T00:00:00Z'`)
	var mu sync.Mutex
	rematched := 0
	importTrace = func(stage string, _ time.Duration) {
		if stage == "lookup: library ids" {
			mu.Lock()
			rematched++
			mu.Unlock()
		}
	}
	t.Cleanup(func() { importTrace = nil })
	if _, err := s.Step(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	importTrace = nil
	if rematched != 3 {
		t.Fatalf("the delta re-matched %d works, want Heat, Collateral and Ronin only", rematched)
	}
	if got := matched(); !slices.Equal(got, []int64{heat.ID, thief.ID, ronin.ID}) {
		t.Fatalf("after the delta %v (Collateral removed, Ronin matched, Thief kept)", got)
	}
	var tags []string
	rows, _ := c.DB.Query(`SELECT tag FROM catalog_dataset_tags WHERE entity_id=? ORDER BY tag`, heat.ID)
	for rows.Next() {
		var v string
		rows.Scan(&v)
		tags = append(tags, v)
	}
	rows.Close()
	if !slices.Equal(tags, []string{"pacing:slow-burn"}) {
		t.Fatalf("Heat's tags after the delta %v", tags)
	}
	var thiefTags int
	c.DB.QueryRow(`SELECT count(*) FROM catalog_dataset_tags WHERE entity_id=?`, thief.ID).Scan(&thiefTags)
	if thiefTags != 1 {
		t.Fatalf("Thief, untouched, has %d tags", thiefTags)
	}
}
