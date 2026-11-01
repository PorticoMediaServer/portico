package enrichment

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A real release gives a work tens of tags and up to fifteen similar titles.
// Importing and purging a library of such works stays inside the background
// write plan, a transaction at a time, so the single writer is never held
// for long (found by the B2 R7 end-to-end test: one batch was 442 rows).
func TestImportAndPurgeStayWithinTheWritePlan(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}
	// 341 tags: the real screen vocabulary's size (vocabulary/tags-v2.json).
	const works, tags, similar, vocabulary = 300, 60, 15, 341
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < works; i++ {
		f := c.Movie(films, fmt.Sprintf("/m/f%03d.mkv", i), fmt.Sprintf("Film %03d", i), 1900+i%100)
		c.Exec(`INSERT INTO catalog_external_ids VALUES(?,'tmdb','movie',?)`, f.ID, fmt.Sprint(1000+i))
	}
	c.Drain()
	p := newPublisher(t)
	p.domains["screen"] = ManifestDomain{DatasetVersion: "1", VocabularyVersion: 2, Full: p.build("screen", "full", "1", func(t *testing.T, db *sql.DB) {
		for v := 1; v <= vocabulary; v++ {
			mustExec(t, db, `INSERT INTO vocabulary VALUES(?,?,'theme',?,'d','["movie","show"]',2,NULL)`, v, fmt.Sprintf("theme:t%03d", v), fmt.Sprintf("Theme %03d", v))
		}
		for i := 0; i < works; i++ {
			qid := 900000 + i
			mustExec(t, db, `INSERT INTO works VALUES(?,1,0,0,1,2,1990,10,?)`, qid, fmt.Sprintf("Film %03d", i))
			mustExec(t, db, `INSERT INTO work_ids VALUES(1,?,?)`, 1000+i, qid)
			mustExec(t, db, `INSERT INTO work_sources VALUES(?,3,'www',1)`, qid)
			for v := 1; v <= tags; v++ {
				mustExec(t, db, `INSERT INTO work_tags VALUES(?,?,?)`, qid, v, 1+(i+v)%3)
			}
			for r := 1; r <= similar; r++ {
				mustExec(t, db, `INSERT INTO work_similar VALUES(?,?,?,?)`, qid, r, 900000+(i+r)%works, 1+r%5)
			}
		}
	})}
	s := publisherService(c.DB, p, t.TempDir())
	overruns := func(step string) {
		t.Helper()
		for _, o := range dbwork.Plans().Recent {
			if strings.Contains(o.Site, "/enrichment.") {
				t.Fatalf("%s: a dataset write went over its plan: %+v", step, o)
			}
		}
	}
	dbwork.ResetPlans()
	if _, err := s.Step(context.Background(), 500); err != nil {
		t.Fatal(err)
	}
	overruns("import")
	var n int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_dataset_tags`).Scan(&n); err != nil || n != works*tags {
		t.Fatalf("imported %d tags, want %d (%v)", n, works*tags, err)
	}
	c.Exec(`INSERT INTO server_settings(singleton,updated_ms,"recommendations.titleDataset") VALUES(1,0,0) ON CONFLICT(singleton) DO UPDATE SET "recommendations.titleDataset"=0`)
	dbwork.ResetPlans()
	for more := true; more; {
		var err error
		if more, err = s.Step(context.Background(), 500); err != nil {
			t.Fatal(err)
		}
	}
	overruns("purge")
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_dataset_tags`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d tags left after the purge (%v)", n, err)
	}
}
