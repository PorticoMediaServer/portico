package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

type recWorld struct {
	c              *catalogtest.Catalog
	scifi, romance []catalogtest.Item
	dramas         []catalogtest.Item
	others         []catalogtest.Item
	s              *Service
	history        int
}

// A small library with two clear tastes (a sci-fi director, a romance
// director) and a block of higher-rated dramas that quality alone would rank
// first.
func newRecWorld(t *testing.T) *recWorld {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	w := &recWorld{c: c}
	for i := 0; i < 12; i++ {
		f := c.Movie(films, fmt.Sprintf("/m/s%02d.mkv", i), fmt.Sprintf("Star %02d", i), 2000+i)
		c.Genres(f.ID, "tmdb", "Science Fiction")
		compactCredits(c, f.ID, personCredit("tmdb:dv", "dv", "Dana Villeneuve", "Director", "Directing", 0))
		w.scifi = append(w.scifi, f)
		g := c.Movie(films, fmt.Sprintf("/m/r%02d.mkv", i), fmt.Sprintf("Love %02d", i), 2000+i)
		c.Genres(g.ID, "tmdb", "Romance")
		compactCredits(c, g.ID, personCredit("tmdb:rd", "rd", "Rita Director", "Director", "Directing", 0))
		w.romance = append(w.romance, g)
	}
	// Sci-fi by other directors: they share the genre, not the director.
	for i := 0; i < 10; i++ {
		o := c.Movie(films, fmt.Sprintf("/m/o%02d.mkv", i), fmt.Sprintf("Orbit %02d", i), 2010+i)
		c.Genres(o.ID, "tmdb", "Science Fiction")
		compactCredits(c, o.ID, personCredit(fmt.Sprintf("tmdb:o%d", i), fmt.Sprintf("o%d", i), fmt.Sprintf("Other Director %d", i), "Director", "Directing", 0))
		w.others = append(w.others, o)
	}
	for i := 0; i < 30; i++ {
		d := c.Movie(films, fmt.Sprintf("/m/d%02d.mkv", i), fmt.Sprintf("Drama %02d", i), 1990+i)
		c.Genres(d.ID, "tmdb", "Drama")
		c.Exec(`INSERT INTO metadata_ratings(item_id,provider,value,scale,votes,source_url,observed_at) VALUES(?,'tmdb',8.6,10,20000,'','2026-01-01T00:00:00Z')`, d.ID)
		w.dramas = append(w.dramas, d)
	}
	c.Drain()
	w.s = New(c.DB)
	return w
}

func (w *recWorld) watch(profile string, item catalogtest.Item) {
	w.history++
	at := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	w.c.Exec(`INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed) VALUES(?,?,?,?,?,1,5400000,0,1)`, fmt.Sprintf("h%d", w.history), profile, item.ID, at, at)
}

func (w *recWorld) personal(profile string, item catalogtest.Item, column string, value any) {
	w.c.Exec(`INSERT INTO personal_items(profile_id,item_id,revision,`+column+`) VALUES(?,?,1,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET `+column+`=excluded.`+column+`,revision=revision+1`, profile, item.ID, value)
}

func (w *recWorld) rank(t *testing.T, profile string) []recCandidate {
	t.Helper()
	ranked, err := w.s.recRank(HomeRequest{Viewer: Viewer{Profile: profile, Fence: profile, Libraries: []string{"m"}}, ServerID: "s", Profile: profile, ViewerFence: profile, Libraries: []string{"m"}, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return ranked
}

func (w *recWorld) check(t *testing.T) {
	t.Helper()
	if err := compactcatalog.CheckRecPostings(context.Background(), w.c.DB); err != nil {
		t.Fatal(err)
	}
	if err := compactcatalog.CheckRecTaste(context.Background(), w.c.DB); err != nil {
		t.Fatal(err)
	}
}

func publicSet(items []catalogtest.Item) map[string]bool {
	out := map[string]bool{}
	for _, it := range items {
		out[it.Public] = true
	}
	return out
}

func TestRecommendedFollowsTasteNotJustQuality(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	for _, f := range w.scifi[:3] {
		w.watch("p", f)
	}
	w.c.Drain()
	w.check(t)
	ranked := w.rank(t, "p")
	scifi, watched := publicSet(w.scifi[3:]), publicSet(w.scifi[:3])
	if len(ranked) < 9 {
		t.Fatalf("ranked %d works", len(ranked))
	}
	// Taste leads, and a page of 12 holds at most three films of one director:
	// the other six sci-fi films lead the next page.
	if !scifi[ranked[0].ID] {
		t.Fatalf("the first recommendation isn't an unwatched sci-fi film: %v", ranked[:12])
	}
	count := func(from, to int) int {
		n := 0
		for _, c := range ranked[from:to] {
			if scifi[c.ID] {
				n++
			}
		}
		return n
	}
	if len(ranked) < 19 {
		t.Fatalf("ranked %d works, want the 9 films of the director and the 10 other sci-fi films", len(ranked))
	}
	if n := count(0, 12); n != 3 {
		t.Fatalf("the first page holds %d films of one director, want 3: %v", n, ranked[:12])
	}
	if n := count(0, len(ranked)); n != 9 {
		t.Fatalf("%d of the director's 9 unwatched films are recommended", n)
	}
	// Only titles that match the taste are recommended: neither romance nor
	// the well-rated dramas (a shared decade isn't a match).
	romance, dramas := publicSet(w.romance), publicSet(w.dramas)
	for _, c := range ranked {
		if romance[c.ID] || dramas[c.ID] {
			t.Fatalf("a title with no taste match is recommended: %s", c.ID)
		}
	}
	for _, c := range ranked {
		if watched[c.ID] {
			t.Fatalf("a watched film is recommended: %s", c.ID)
		}
	}
	// A profile with no history gets the best-rated titles.
	cold := w.rank(t, "q")
	if len(cold) == 0 || !publicSet(w.dramas)[cold[0].ID] {
		t.Fatalf("a new profile's first recommendation should be a well-rated drama: %v", cold[:min(5, len(cold))])
	}
}

// A watch or a "not interested" shows on the very next load, before the taste
// worker has run: pending jobs are overlaid with the worker's computation.
func TestRecommendedIsFreshBeforeTheWorkerRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	for _, f := range w.scifi[:3] {
		w.watch("p", f)
	}
	w.c.Drain()
	before := w.rank(t, "p")
	if !rankedHas(before, w.scifi[3].Public) || !rankedHas(before, w.scifi[4].Public) {
		t.Fatal("setup: the next sci-fi films should be recommended")
	}
	w.watch("p", w.scifi[3])
	w.personal("p", w.scifi[4], "not_interested", 1)
	after := w.rank(t, "p")
	if rankedHas(after, w.scifi[3].Public) {
		t.Fatal("a film watched a moment ago is still recommended")
	}
	if rankedHas(after, w.scifi[4].Public) {
		t.Fatal("a film marked not interested is still recommended")
	}
	w.c.Drain()
	w.check(t)
	settled := w.rank(t, "p")
	if rankedHas(settled, w.scifi[3].Public) || rankedHas(settled, w.scifi[4].Public) {
		t.Fatal("the worker's result differs from the overlay")
	}
}

// A bulk backlog (an imported history, say) isn't replayed on every request:
// past the overlay bound its works are held out of recommendations until the
// worker takes them in, so a title marked not interested in the middle of an
// import is still never recommended.
func TestRecBulkBacklogIsHeldOutNotReplayed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	w.watch("p", w.scifi[0])
	w.c.Drain()
	w.personal("p", w.scifi[3], "not_interested", 1)
	// Fill the backlog past the bound with works that exist nowhere else.
	for i := 0; i <= recOverlayJobs; i++ {
		if _, err := w.c.DB.Exec(`INSERT OR IGNORE INTO rec_profile_jobs(profile_id,work_id) VALUES('p',?)`, 900000000+i); err != nil {
			t.Fatal(err)
		}
	}
	taste, err := New(w.c.DB).recLoadTaste("p", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.s.recLoadSignals("p", &taste, []int64{w.scifi[3].ID, 900000000}); err != nil {
		t.Fatal(err)
	}
	if !taste.pending || !taste.hidden[w.scifi[3].ID] || !taste.hidden[900000000] {
		t.Fatalf("bulk backlog: pending %v, not-interested held out %v", taste.pending, taste.hidden[w.scifi[3].ID])
	}
	if rankedHas(w.rank(t, "p"), w.scifi[3].Public) {
		t.Fatal("a film marked not interested during a bulk backlog is recommended")
	}
}

// Taste moves by exact differences: a favorite taken back leaves the taste as
// it was, and a watched film's changed genre moves its watchers' taste.
func TestRecTasteIsExactUnderRetractionAndRefacet(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	w.watch("p", w.scifi[0])
	w.c.Drain()
	w.personal("p", w.romance[0], "favorite", 1)
	w.c.Drain()
	w.check(t)
	w.personal("p", w.romance[0], "favorite", 0)
	w.c.Drain()
	w.check(t)
	var romance float64
	if err := w.c.DB.QueryRow(`SELECT COALESCE(sum(long),0) FROM rec_profile_taste WHERE profile_id='p' AND facet='g:romance'`).Scan(&romance); err != nil {
		t.Fatal(err)
	}
	if romance > 1e-9 || romance < -1e-9 {
		t.Fatalf("an undone favorite left %v of romance taste", romance)
	}
	w.c.Genres(w.scifi[0].ID, "tmdb", "Western")
	w.c.Drain()
	w.check(t)
}

func rankedHas(ranked []recCandidate, id string) bool {
	for _, c := range ranked {
		if c.ID == id {
			return true
		}
	}
	return false
}

// Home expands "Picks for you" into the profile's strongest personal rows:
// named for their genre or person, each title in one row only and none from
// Recommended's first page; the next film of a started franchise; each row
// pages on its own.
func TestPicksForYouRows(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	films := w.c.Handle("m")
	// Enough sci-fi that each row still has titles after Recommended and the
	// rows before it have taken theirs.
	for i := 0; i < 40; i++ {
		f := w.c.Movie(films, fmt.Sprintf("/m/x%02d.mkv", i), fmt.Sprintf("Nebula %02d", i), 1980+i)
		w.c.Genres(f.ID, "tmdb", "Science Fiction")
		compactCredits(w.c, f.ID, personCredit(fmt.Sprintf("tmdb:x%d", i), fmt.Sprintf("x%d", i), fmt.Sprintf("Director X%d", i), "Director", "Directing", 0))
	}
	w.c.Collection(films, "Star Saga", w.scifi[0], w.scifi[1], w.scifi[2], w.scifi[3])
	w.c.Collection(films, "Orbit Saga", w.others[0], w.others[1], w.others[2])
	w.c.Drain()
	for _, f := range []catalogtest.Item{w.scifi[0], w.scifi[1], w.others[0]} {
		w.watch("p", f)
	}
	w.c.Drain()
	r := HomeRequest{Viewer: Viewer{Profile: "p", Fence: "p", Libraries: []string{"m"}}, ServerID: "s", Profile: "p", ViewerFence: "p", Libraries: []string{"m"}, Now: time.Now(), RowOrder: []string{"recommended", "for_you"}}
	doc, err := w.s.HomeRows(r)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	var recommended []string
	personal := map[string]HomeRow{}
	for _, row := range doc.Rows {
		if row.ID == "recommended" {
			for _, e := range row.Entries {
				recommended = append(recommended, e.ID)
			}
		}
		if row.Family != recFamily {
			continue
		}
		personal[row.ID] = row
		for _, e := range row.Entries {
			if other, ok := seen[e.ID]; ok {
				t.Fatalf("%s is in both %s and %s", e.ID, other, row.ID)
			}
			seen[e.ID] = row.ID
		}
	}
	for _, id := range recommended {
		if row, ok := seen[id]; ok && row != "for_you:franchise" {
			t.Fatalf("%s is on Recommended's first page and in %s", id, row)
		}
	}
	genre, ok := personal["for_you:genre:science fiction"]
	if !ok || genre.TitleText.Code != "home.row.genreForYou" || genre.TitleText.Params["genre"] != "Science Fiction" {
		t.Fatalf("no Science Fiction row: %v", keys(personal))
	}
	franchise, ok := personal["for_you:franchise"]
	if !ok || len(franchise.Entries) != 2 || !publicSet([]catalogtest.Item{w.scifi[2], w.others[1]})[franchise.Entries[0].ID] || !publicSet([]catalogtest.Item{w.scifi[2], w.others[1]})[franchise.Entries[1].ID] {
		t.Fatalf("the next films of the two started franchises should be Star 02 and Orbit 01: %+v", franchise.Entries)
	}
	directed := 0
	for id, row := range personal {
		if strings.HasPrefix(id, "for_you:person:") && row.TitleText.Params["name"] == "Dana Villeneuve" {
			directed++
		}
	}
	if directed != 1 {
		t.Fatalf("want one row for the director, got %d: %v", directed, keys(personal))
	}
	// A personal row pages on its own, continuing where Home's page ended.
	page, err := w.s.HomeSingleRow(r, genre.ID, HomeRowPage{Limit: 2, Cursor: ""})
	if err != nil || len(page.Entries) == 0 || page.Entries[0].ID != genre.Entries[0].ID {
		t.Fatalf("paging %s: %+v %v", genre.ID, page.Entries, err)
	}
	// Hiding the family hides every personal row.
	r.HiddenRowIDs = []string{recFamily}
	doc, err = w.s.HomeRows(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range doc.Rows {
		if row.Family == recFamily {
			t.Fatalf("hidden family still shows %s", row.ID)
		}
	}
}

func keys(m map[string]HomeRow) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A library's Discover view carries the personal rows scoped to it, after
// Recommended, with parameterised headings, plus the kind's own rows
// (films under 100 minutes).
func TestDiscoverCarriesPicksForYou(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	films := w.c.Handle("m")
	for i := 0; i < 40; i++ {
		f := w.c.Movie(films, fmt.Sprintf("/m/x%02d.mkv", i), fmt.Sprintf("Nebula %02d", i), 1980+i)
		w.c.Genres(f.ID, "tmdb", "Science Fiction")
		// Feature length, so these are the genre's own and never short picks.
		// Rows of equal strength take turns by the day, and a title shows in
		// one row only: without titles of its own the genre row would be on
		// Discover some days and not others, and so would this test.
		w.c.File(f.ID, fmt.Sprintf("/m/x%02d.long.mkv", i), 130*60)
	}
	w.c.Drain()
	for _, f := range w.scifi[:3] {
		w.watch("p", f)
	}
	w.c.Drain()
	out, err := w.s.Content(ContentRequest{Viewer: Viewer{Profile: "p", Fence: "p", Libraries: []string{"m"}}, ServerID: "s", Library: "m", Profile: "p", ViewerFence: "p", View: "discover", Limit: 12, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	var genre *ContentSection
	for i, section := range out.Sections {
		ids = append(ids, section.ID)
		if section.ID == "for_you:genre:science fiction" {
			genre = &out.Sections[i]
		}
	}
	if genre == nil || genre.Heading.Key != "home.row.genreForYou" || genre.Heading.Params["genre"] != "Science Fiction" {
		t.Fatalf("no Science Fiction for you section with its heading: %v", ids)
	}
	// Its See all is a browse of the genre, For you: run it as a client would.
	if genre.SeeAll == nil || genre.SeeAll.Pivot != "movies" || genre.SeeAll.Query == nil || genre.SeeAll.Query.Field != "genre" || genre.SeeAll.Sort[0].Field != "forYou" {
		t.Fatalf("See all: %+v", genre.SeeAll)
	}
	page, err := w.s.BrowseEntities(Viewer{Profile: "p", Fence: "p", Libraries: []string{"m"}}, BrowseRequest{Library: "m", Profile: "p", ViewerFence: "p", Pivot: genre.SeeAll.Pivot, Query: genre.SeeAll.Query, Sort: genre.SeeAll.Sort, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if page.PageInfo.Total != len(w.scifi)+len(w.others)+40 {
		t.Fatalf("See all lists %d titles, the genre holds %d", page.PageInfo.Total, len(w.scifi)+len(w.others)+40)
	}
	if !publicSet(w.scifi[3:])[page.Entries[0].ID] {
		t.Fatalf("See all should open with an unwatched film of the director: %v", page.Entries[0].ID)
	}
	rec, first := -1, -1
	for i, id := range ids {
		if id == "recommended" {
			rec = i
		}
		if strings.HasPrefix(id, recFamily+":") && first < 0 {
			first = i
		}
	}
	if rec < 0 || first != rec+1 {
		t.Fatalf("personal rows should follow Recommended: %v", ids)
	}
}

// Browse "For you": the viewer's ranking of the pivot and filter first
// (unwatched before watched), then everything else in title order; complete
// and stable across pages; it orders on its own.
func TestBrowseForYou(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	for _, f := range w.scifi[:3] {
		w.watch("p", f)
	}
	w.personal("p", w.romance[0], "not_interested", 1)
	w.c.Drain()
	viewer := Viewer{Profile: "p", Fence: "p", Libraries: []string{"m"}}
	request := func(start, limit int) BrowseRequest {
		return BrowseRequest{Library: "m", Profile: "p", ViewerFence: "p", Pivot: "movies", Sort: []BrowseSortSelection{{Field: "forYou", Direction: "desc"}}, Limit: limit, Range: &BrowseRange{Start: start}}
	}
	var all []string
	for start := 0; ; start += 20 {
		page, err := w.s.BrowseEntities(viewer, request(start, 20))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page.Entries {
			all = append(all, e.ID)
		}
		if !page.PageInfo.HasMore {
			break
		}
	}
	total := len(w.scifi) + len(w.romance) + len(w.dramas) + len(w.others)
	if len(all) != total {
		t.Fatalf("For you listed %d titles, the library holds %d", len(all), total)
	}
	seen := map[string]bool{}
	for _, id := range all {
		if seen[id] {
			t.Fatalf("%s listed twice", id)
		}
		seen[id] = true
	}
	if !publicSet(w.scifi[3:])[all[0]] {
		t.Fatalf("For you should open with an unwatched film of the director: %v", all[:5])
	}
	position := map[string]int{}
	for i, id := range all {
		position[id] = i
	}
	for _, f := range w.scifi[3:] {
		for _, g := range w.scifi[:3] {
			if position[f.Public] > position[g.Public] {
				t.Fatalf("a watched film ranks before an unwatched one")
			}
		}
	}
	if _, err := w.s.BrowseEntities(viewer, BrowseRequest{Library: "m", Profile: "p", ViewerFence: "p", Pivot: "movies", Sort: []BrowseSortSelection{{Field: "forYou", Direction: "desc"}, {Field: "title", Direction: "asc"}}, Limit: 5}); err == nil {
		t.Fatal("For you combined with another sort should be refused")
	}
}

// A saved view placed in the Home layout is a Home row: the view's browse,
// under its name, in its own order; a view the viewer can't read is ignored
// and doesn't block saving the layout.
func TestSavedViewOnHome(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	actor := ResourceActor{Authority: "local", AccountID: "a", ProfileID: "q"}
	name := "Sci-fi"
	receipt, err := w.s.MutateSavedResource(actor, "", "create", SavedResourceMutation{OperationID: "op-view-1", Kind: "view", Name: &name,
		Definition: &SavedViewDefinition{LibraryID: "m", Pivot: "movies", Query: &BrowseNode{Field: "genre", Operator: "contains", Value: "Science Fiction"}, Sort: []BrowseSortSelection{{Field: "title", Direction: "asc"}}, Presentation: "grid"}}, func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	profile := actorKey(actor)
	r := HomeRequest{Viewer: Viewer{Profile: profile, Fence: "f", Libraries: []string{"m"}}, ServerID: "s", Profile: profile, ViewerFence: "f", Libraries: []string{"m"}, Now: time.Now(),
		RowOrder: []string{"view:" + receipt.ResourceID}}
	doc, err := w.s.HomeRows(r)
	if err != nil {
		t.Fatal(err)
	}
	var row *HomeRow
	for i := range doc.Rows {
		if doc.Rows[i].ID == "view:"+receipt.ResourceID {
			row = &doc.Rows[i]
		}
	}
	if row == nil || row.Title != "Sci-fi" || len(row.Entries) == 0 || row.Entries[0].ID != w.others[0].Public {
		t.Fatalf("saved view row: %+v", row)
	}
	if err := w.s.ValidateHomeLayout(r, []string{"view:" + receipt.ResourceID, "view:gone"}, nil); err != nil {
		t.Fatal("a layout naming an unreadable view must still save:", err)
	}
}

// A film started, under a quarter watched and untouched for three weeks
// counts mildly against what it's like; one watched to its end doesn't.
func TestRecAbandonmentCountsAgainst(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	w.watch("p", w.scifi[0])
	w.c.Drain()
	film := w.others[0]
	old := time.Now().AddDate(0, 0, -30).UTC().Format("2006-01-02T15:04:05.000Z")
	w.c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES('p',?,60000,0,0,'pb1')`, film.ID)
	w.c.Exec(`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES('p','m',?,?,'paused')`, film.ID, old)
	w.c.Drain()
	taste, err := w.s.recLoadTaste("p", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if v := taste.values["cd:directing:other director 0"]; v >= 0 {
		t.Fatalf("an abandoned film's director should count against it: %v", v)
	}
	if err := w.s.recLoadSignals("p", &taste, []int64{film.ID}); err != nil {
		t.Fatal(err)
	}
	if !taste.engaged[film.ID] {
		t.Fatal("an abandoned film is still engaged (never recommended)")
	}
}

// With the title dataset, a profile's strongest mood becomes a personal row,
// named by the dataset's label.
func TestMoodRowFromTheTitleDataset(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	w.c.Exec(`INSERT INTO catalog_dataset_vocabulary(tag,family,label) VALUES('tone:gritty','tone','Gritty')`)
	films := w.c.Handle("m")
	for i := 0; i < 30; i++ {
		f := w.c.Movie(films, fmt.Sprintf("/m/g%02d.mkv", i), fmt.Sprintf("Grit %02d", i), 1970+i)
		w.c.Exec(`INSERT INTO catalog_dataset_tags(entity_id,tag,strength) VALUES(?,'tone:gritty',3)`, f.ID)
	}
	for _, f := range w.romance[:3] {
		w.c.Exec(`INSERT INTO catalog_dataset_tags(entity_id,tag,strength) VALUES(?,'tone:gritty',3)`, f.ID)
		w.watch("p", f)
	}
	w.c.Drain()
	r := HomeRequest{Viewer: Viewer{Profile: "p", Fence: "p", Libraries: []string{"m"}}, ServerID: "s", Profile: "p", ViewerFence: "p", Libraries: []string{"m"}, Now: time.Now(), RowOrder: []string{"recommended", "for_you"}}
	doc, err := w.s.HomeRows(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range doc.Rows {
		if row.ID == "for_you:tag:tone:gritty" {
			if row.TitleText.Code != "home.row.moodForYou" || row.TitleText.Params["mood"] != "Gritty" || len(row.Entries) < recRowMinimum {
				t.Fatalf("mood row: %+v", row.TitleText)
			}
			return
		}
	}
	t.Fatal("no Gritty picks row")
}

// Taste stays exact when a job crosses the epoch rebase, and when a finished
// watch is taken back (its history entry deleted): the signal retracts, and
// the film can be recommended again.
func TestRecTasteExactAcrossRebaseAndRetraction(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	w.watch("p", w.scifi[0])
	w.watch("p", w.scifi[1])
	w.c.Drain()
	// Put the epoch far behind, as years of use would.
	w.c.Exec(`UPDATE rec_profile_revisions SET epoch=epoch-3000 WHERE profile_id='p'`)
	w.c.Exec(`UPDATE rec_profile_signals SET long=long*?,short=short*? WHERE profile_id='p'`, math.Exp2(3000/compactcatalog.RecLongHalfLife), math.Exp2(3000/compactcatalog.RecShortHalfLife))
	w.c.Exec(`UPDATE rec_profile_taste SET long=long*?,short=short*? WHERE profile_id='p'`, math.Exp2(3000/compactcatalog.RecLongHalfLife), math.Exp2(3000/compactcatalog.RecShortHalfLife))
	w.check(t)
	w.personal("p", w.scifi[0], "favorite", 1)
	w.c.Drain()
	w.check(t)
	var epochAge float64
	if err := w.c.DB.QueryRow(`SELECT julianday('now')-2440587.5-epoch FROM rec_profile_revisions WHERE profile_id='p'`).Scan(&epochAge); err != nil || epochAge > 1500 {
		t.Fatalf("the epoch should have moved forward: %v days behind (%v)", epochAge, err)
	}
	w.c.Exec(`DELETE FROM personal_history WHERE profile_id='p' AND item_id=?`, w.scifi[1].ID)
	w.c.Drain()
	w.check(t)
	var engaged int
	w.c.DB.QueryRow(`SELECT count(*) FROM rec_profile_signals WHERE profile_id='p' AND work_id=? AND engaged=1`, w.scifi[1].ID).Scan(&engaged)
	if engaged != 0 {
		t.Fatal("a watch taken back still counts as watched")
	}
}

// Short picks fills in a movie library's Discover view (its duration is read
// by eligibility, before the row's condition applies).
func TestShortPicksFill(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	for _, f := range w.scifi[:3] {
		w.watch("p", f)
	}
	w.c.Drain()
	x, err := w.s.recSession(HomeRequest{Viewer: Viewer{Profile: "p", Fence: "p", Libraries: []string{"m"}}, Profile: "p", ViewerFence: "p", Libraries: []string{"m"}, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	x.discover = "movie"
	rows, err := x.recGenerators()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.spec.ID == "for_you:short" {
			if len(row.items) == 0 {
				t.Fatal("Short picks is empty although every film runs 90 minutes")
			}
			return
		}
	}
	t.Fatal("no Short picks row generated")
}
