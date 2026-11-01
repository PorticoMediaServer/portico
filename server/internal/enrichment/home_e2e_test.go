package enrichment

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// B2 R7, end to end in one process: a signed release goes through the real
// verification and import, the profile's taste picks up the dataset's facets,
// and Home composes the subgenre and appeal rows with their reasons. Switched
// off, everything the dataset brought is removed and Home is exactly what it
// was before the file arrived.
func TestTheDatasetReachesHomeAndSwitchingOffRestoresIt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	// 40 folk horror films, 40 comfort watches, 40 others: every one known to
	// the dataset by its TMDB id (enough that Recommended's first page leaves
	// each tag row its minimum).
	var folk, comfort, other []catalogtest.Item
	for i := 0; i < 120; i++ {
		f := c.Movie(films, fmt.Sprintf("/m/f%03d.mkv", i), fmt.Sprintf("Film %03d", i), 1900+i)
		c.Genres(f.ID, "tmdb", []string{"Drama", "Comedy", "Thriller"}[i%3])
		c.Exec(`INSERT INTO catalog_external_ids VALUES(?,'tmdb','movie',?)`, f.ID, fmt.Sprint(1000+i))
		switch i / 40 {
		case 0:
			folk = append(folk, f)
		case 1:
			comfort = append(comfort, f)
		default:
			other = append(other, f)
		}
	}
	c.Drain()
	// The profile watched four folk horror films and two comfort watches.
	history := 0
	watch := func(item catalogtest.Item) {
		history++
		at := time.Now().UTC().Add(-time.Duration(history) * time.Hour).Format("2006-01-02T15:04:05.000Z")
		c.Exec(`INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed) VALUES(?,?,?,?,?,1,5400000,0,1)`, fmt.Sprintf("h%d", history), "p", item.ID, at, at)
	}
	for _, f := range folk[:4] {
		watch(f)
	}
	for _, f := range comfort[:2] {
		watch(f)
	}
	c.Drain()
	home := catalog.New(c.DB)
	request := catalog.HomeRequest{Viewer: catalog.Viewer{Profile: "p", Fence: "p", Libraries: []string{"m"}}, ServerID: "s", Profile: "p", ViewerFence: "p", Libraries: []string{"m"}, Now: time.Now(),
		RowOrder: []string{"recommended", "for_you"}}
	personal := func() map[string]catalog.HomeRow {
		t.Helper()
		doc, err := home.HomeRows(request)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]catalog.HomeRow{}
		for _, row := range doc.Rows {
			if row.Family == "for_you" {
				out[row.ID] = row
			}
		}
		return out
	}
	ids := func(rows map[string]catalog.HomeRow) []string {
		out := make([]string, 0, len(rows))
		for id := range rows {
			out = append(out, id)
		}
		slices.Sort(out)
		return out
	}
	before := ids(personal())
	if len(before) == 0 {
		t.Fatal("no personal rows before the dataset: the fixture has no taste")
	}
	for _, id := range before {
		if strings.HasPrefix(id, "for_you:tag:") {
			t.Fatal("a dataset row before any dataset:", id)
		}
	}

	// The release: folk horror (a subgenre) and comfort (an appeal) on their films.
	p := newPublisher(t)
	p.domains["screen"] = ManifestDomain{DatasetVersion: "2026.45", VocabularyVersion: 2, Full: p.build("screen", "full", "2026.45", func(t *testing.T, db *sql.DB) {
		mustExec(t, db, `INSERT INTO vocabulary VALUES(1,'subgenre:folk-horror','subgenre','Folk Horror','d','["movie","show"]',2,NULL,'folk horror'),(2,'appeal:comfort','appeal','Comfort Watch','d','["movie","show"]',2,NULL,'comfort watches'),(3,'origin:remake','origin','Remakes','d','["movie","show"]',2,NULL,NULL)`)
		for i := 0; i < 120; i++ {
			qid := 500000 + i
			mustExec(t, db, `INSERT INTO works VALUES(?,1,0,0,1,2,?,10,?)`, qid, 1900+i, fmt.Sprintf("Film %03d", i))
			mustExec(t, db, `INSERT INTO work_ids VALUES(1,?,?)`, 1000+i, qid)
			mustExec(t, db, `INSERT INTO work_sources VALUES(?,3,'www',1)`, qid)
			switch i / 40 {
			case 0:
				mustExec(t, db, `INSERT INTO work_tags VALUES(?,1,3),(?,3,3)`, qid, qid)
			case 1:
				mustExec(t, db, `INSERT INTO work_tags VALUES(?,2,3)`, qid)
			default:
				mustExec(t, db, `INSERT INTO work_tags VALUES(?,3,2)`, qid)
			}
		}
	})}
	dir := t.TempDir()
	s := publisherService(c.DB, p, dir)
	if _, err := s.Step(context.Background(), 500); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	with := personal()
	folkRow, ok := with["for_you:tag:subgenre:folk-horror"]
	if !ok {
		t.Fatalf("no folk horror row: %v", ids(with))
	}
	if folkRow.Title != "Folk horror for you" || folkRow.Why == nil || !strings.HasPrefix(folkRow.Why.Code, "next.home.whyFacet") || folkRow.Why.Params["label"] != "folk horror" {
		t.Fatalf("folk horror row: %q %+v", folkRow.Title, folkRow.Why)
	}
	watched := map[string]bool{}
	for _, f := range folk[:4] {
		watched[c.Public(f.ID)] = true
	}
	for _, ref := range folkRow.WhyTitles {
		if !watched[ref.ID] {
			t.Fatalf("the folk horror reason names %s, which the profile hasn't watched", ref.Name)
		}
	}
	for _, entry := range folkRow.Entries {
		if watched[entry.ID] {
			t.Fatal("the row recommends a film the profile finished:", entry.Title)
		}
	}
	if _, ok := with["for_you:tag:origin:remake"]; ok {
		t.Fatal("an origin tag made a row")
	}

	// Off: no requests, everything removed, and Home is as it was.
	requests := 0
	for _, n := range p.requests {
		requests += n
	}
	c.Exec(`INSERT INTO server_settings(singleton,updated_ms,"recommendations.titleDataset") VALUES(1,0,0) ON CONFLICT(singleton) DO UPDATE SET "recommendations.titleDataset"=0`)
	for more := true; more; {
		var err error
		if more, err = s.Step(context.Background(), 500); err != nil {
			t.Fatal(err)
		}
	}
	c.Drain()
	after := 0
	for _, n := range p.requests {
		after += n
	}
	if after != requests {
		t.Fatal("switching off made requests")
	}
	if got := ids(personal()); !slices.Equal(got, before) {
		t.Fatalf("Home after switching off %v, before the dataset %v", got, before)
	}
}
