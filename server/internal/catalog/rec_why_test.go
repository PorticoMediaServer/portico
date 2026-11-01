package catalog

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// datasetVocabulary inserts the Portico title dataset's tag families the rows
// are named from.
func datasetVocabulary(w *recWorld, entries ...[3]string) {
	for _, e := range entries {
		w.c.Exec(`INSERT INTO catalog_dataset_vocabulary(tag,family,label) VALUES(?,?,?)`, e[0], e[1], e[2])
	}
}

// tagWorks tags every work with one dataset tag at one strength.
func tagWorks(w *recWorld, strength int, tag string, works ...int64) {
	for _, work := range works {
		w.c.Exec(`INSERT INTO catalog_dataset_tags(entity_id,tag,strength) VALUES(?,?,?)`, work, tag, strength)
	}
}

func itemIDs(items []catalogtest.Item) []int64 {
	out := make([]int64, len(items))
	for i, item := range items {
		out[i] = item.ID
	}
	return out
}

// TestTagRowsForSubgenreAndAppeal checks the title dataset's subgenre and
// appeal families get their own personal rows, named by the dataset's labels,
// with their own limits; origin, audience and intensity still never do.
func TestTagRowsForSubgenreAndAppeal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	datasetVocabulary(w,
		[3]string{"mood:gritty", "mood", "Gritty"},
		[3]string{"tone:hopeful", "tone", "Hopeful"},
		[3]string{"theme:heist", "theme", "Heist"},
		[3]string{"subgenre:cyberpunk", "subgenre", "Cyberpunk"},
		[3]string{"subgenre:noir", "subgenre", "Noir"},
		[3]string{"subgenre:space opera", "subgenre", "Space Opera"},
		[3]string{"appeal:gritty", "appeal", "Gritty Appeal"},
		[3]string{"appeal:sweeping", "appeal", "Sweeping"},
		[3]string{"origin:portico", "origin", "Portico"},
		[3]string{"audience:adults", "audience", "Adults"},
		[3]string{"intensity:high", "intensity", "High"},
	)
	works := append(itemIDs(w.dramas[:12]), itemIDs(w.scifi[:3])...)
	for _, f := range w.scifi[:3] {
		w.watch("p", f)
	}
	for _, tag := range []string{"subgenre:cyberpunk", "subgenre:noir", "subgenre:space opera", "appeal:gritty", "appeal:sweeping", "mood:gritty", "theme:heist", "tone:hopeful", "origin:portico", "audience:adults", "intensity:high"} {
		tagWorks(w, 2, tag, works...)
	}
	w.c.Drain()
	x, err := w.s.recSession(HomeRequest{Viewer: Viewer{Profile: "p", Fence: "p", Libraries: []string{"m"}}, ServerID: "s", Profile: "p", ViewerFence: "p", Libraries: []string{"m"}, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := x.recGenerators()
	if err != nil {
		t.Fatal(err)
	}
	var subgenres, appeals, moods int
	var cyberpunk, appeal *recRow
	for i := range rows {
		row := &rows[i]
		switch row.spec.TitleCode {
		case "home.row.subgenreForYou":
			subgenres++
		case "home.row.appealForYou":
			appeals++
		case "home.row.moodForYou", "home.row.themeForYou":
			moods++
		}
		switch row.spec.ID {
		case "for_you:tag:subgenre:cyberpunk":
			cyberpunk = row
		case "for_you:tag:appeal:gritty":
			appeal = row
		}
		for _, family := range []string{"origin", "audience", "intensity"} {
			if strings.HasPrefix(row.spec.ID, "for_you:tag:"+family+":") {
				t.Fatalf("%s made a personal row", family)
			}
		}
	}
	if subgenres != recSubgenreRows || appeals != recAppealRows || moods != recGenreRows {
		t.Fatalf("subgenre/appeal/mood rows = %d/%d/%d, want %d/%d/%d", subgenres, appeals, moods, recSubgenreRows, recAppealRows, recGenreRows)
	}
	if cyberpunk == nil || cyberpunk.spec.Title != "Cyberpunk for you" || cyberpunk.spec.TitleParams["subgenre"] != "Cyberpunk" {
		t.Fatalf("subgenre row: %+v", cyberpunk)
	}
	if appeal == nil || appeal.spec.Title != "Gritty Appeal for you" || appeal.spec.TitleParams["appeal"] != "Gritty Appeal" {
		t.Fatalf("appeal row: %+v", appeal)
	}
	for _, row := range []*recRow{cyberpunk, appeal} {
		if len(row.items) < recRowMinimum {
			t.Fatalf("%s ranked %d titles, want at least %d", row.spec.ID, len(row.items), recRowMinimum)
		}
		if err := x.recWhy(row); err != nil {
			t.Fatal(err)
		}
		if row.spec.Why == nil || row.spec.Why.Code != recWhyFacet {
			t.Fatalf("%s why: %+v", row.spec.ID, row.spec.Why)
		}
		if len(row.spec.WhyTitles) != 3 {
			t.Fatalf("%s names %d of the profile's titles", row.spec.ID, len(row.spec.WhyTitles))
		}
		watched := publicSet(w.scifi[:3])
		for _, ref := range row.spec.WhyTitles {
			if !watched[ref.ID] {
				t.Fatalf("%s names %s, which the profile has not watched", row.spec.ID, ref.ID)
			}
		}
	}
	if cyberpunk.spec.Why.Params["label"] != "Cyberpunk" || appeal.spec.Why.Params["label"] != "Gritty Appeal" {
		t.Fatalf("why labels: %+v %+v", cyberpunk.spec.Why.Params, appeal.spec.Why.Params)
	}
}

// homePersonalRows is Home's personal family by id.
func homePersonalRows(t *testing.T, s *Service, r HomeRequest) map[string]HomeRow {
	t.Helper()
	doc, err := s.HomeRows(r)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]HomeRow{}
	for _, row := range doc.Rows {
		if row.Family == recFamily {
			out[row.ID] = row
		}
	}
	if len(out) == 0 {
		t.Fatal("no personal rows on Home")
	}
	return out
}

// TestPersonalRowsCarryWhyOnHomePagesAndDiscover checks every personal row
// explains itself with the agreed copy on Home, on its own page and in a
// library's Discover, and that the titles behind it are at most three.
func TestPersonalRowsCarryWhyOnHomePagesAndDiscover(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	datasetVocabulary(w,
		[3]string{"tone:gritty", "tone", "Gritty"},
		[3]string{"theme:heist", "theme", "Heist"},
		[3]string{"subgenre:cyberpunk", "subgenre", "Cyberpunk"},
		[3]string{"appeal:sweeping", "appeal", "Sweeping"},
	)
	films := w.c.Handle("m")
	tags := []string{"tone:gritty", "theme:heist", "subgenre:cyberpunk", "appeal:sweeping"}
	for i := 0; i < 30; i++ {
		f := w.c.Movie(films, fmt.Sprintf("/m/g%02d.mkv", i), fmt.Sprintf("Grit %02d", i), 1970+i)
		for _, tag := range tags {
			tagWorks(w, 3, tag, f.ID)
		}
	}
	w.c.Collection(films, "Star Saga", w.scifi[0], w.scifi[1], w.scifi[2], w.scifi[3], w.scifi[4])
	w.c.Drain()
	for _, f := range w.scifi[:3] {
		tagWorks(w, 3, "tone:gritty", f.ID)
		w.watch("p", f)
	}
	w.c.Drain()
	r := HomeRequest{Viewer: Viewer{Profile: "p", Fence: "p", Libraries: []string{"m"}}, ServerID: "s", Profile: "p", ViewerFence: "p", Libraries: []string{"m"}, Now: time.Now(), RowOrder: []string{"recommended", "for_you"}}
	personal := homePersonalRows(t, w.s, r)
	codes := map[string]bool{recWhyFacet: true, recWhyPerson: true, recWhyNext: true, recWhyLikes: true, recWhyRediscover: true}
	for id, row := range personal {
		if row.Why == nil || !codes[row.Why.Code] {
			t.Fatalf("%s has no agreed Why: %+v", id, row.Why)
		}
		if len(row.WhyTitles) > 3 {
			t.Fatalf("%s names %d titles", id, len(row.WhyTitles))
		}
		page, err := w.s.HomeSingleRow(r, id, HomeRowPage{Limit: 2})
		if err != nil {
			t.Fatalf("paging %s: %v", id, err)
		}
		if !reflect.DeepEqual(page.Why, row.Why) || !reflect.DeepEqual(page.WhyTitles, row.WhyTitles) {
			t.Fatalf("%s explains itself differently on its own page: %+v %+v vs %+v %+v", id, page.Why, page.WhyTitles, row.Why, row.WhyTitles)
		}
	}
	// A personal row Home did not choose today still explains itself.
	x, err := w.s.recSession(r)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := x.recGenerators()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range generated {
		if _, onHome := personal[row.spec.ID]; onHome {
			continue
		}
		page, err := w.s.HomeSingleRow(r, row.spec.ID, HomeRowPage{Limit: 2})
		if err != nil {
			t.Fatalf("%s: %v", row.spec.ID, err)
		}
		if page.Why == nil || !codes[page.Why.Code] {
			t.Fatalf("%s has no Why off Home: %+v", row.spec.ID, page.Why)
		}
		break
	}
	// The franchise row continues the finished films of its collection.
	franchise, err := w.s.recPersonalRow(r, "for_you:franchise")
	if err != nil {
		t.Fatal(err)
	}
	if franchise.spec.Why == nil || franchise.spec.Why.Code != recWhyNext || franchise.spec.Why.Fallback != "Next after what you've finished." {
		t.Fatalf("franchise why: %+v", franchise.spec.Why)
	}
	if len(franchise.spec.WhyTitles) != 3 {
		t.Fatalf("franchise names the finished films it continues, got %+v", franchise.spec.WhyTitles)
	}
	finished := publicSet(w.scifi[:3])
	for _, ref := range franchise.spec.WhyTitles {
		if !finished[ref.ID] {
			t.Fatalf("franchise names %s, not a finished film", ref.ID)
		}
	}
	// Discover carries the same why for the same personal row.
	discovery, err := w.s.DiscoverWithRestrictions("m", "p", identity.ContentRestrictions{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, section := range discovery.Sections {
		if !strings.HasPrefix(section.ID, recFamily+":") {
			continue
		}
		seen++
		if section.Why == nil || !codes[section.Why.Code] {
			t.Fatalf("%s has no Why in Discover: %+v", section.ID, section.Why)
		}
		if row, ok := personal[section.ID]; ok && (!reflect.DeepEqual(section.Why, row.Why) || !reflect.DeepEqual(section.WhyTitles, row.WhyTitles)) {
			t.Fatalf("%s explains itself differently in Discover: %+v vs %+v", section.ID, section.Why, row.Why)
		}
	}
	if seen == 0 {
		t.Fatal("Discover has no personal sections")
	}
	// Top rated is liked, not continued, and Rediscover explains itself without
	// naming a title.
	top, err := w.s.recPersonalRow(r, "for_you:top")
	if err != nil {
		t.Fatal(err)
	}
	if top.spec.Why == nil || top.spec.Why.Code != recWhyLikes || top.spec.Why.Fallback != "Based on what you like." {
		t.Fatalf("top rated why: %+v", top.spec.Why)
	}
	if len(top.spec.WhyTitles) == 0 || len(top.spec.WhyTitles) > 3 {
		t.Fatalf("top rated names %d titles", len(top.spec.WhyTitles))
	}
	for _, f := range w.romance[:3] {
		w.personal("p", f, "favorite", 1)
	}
	w.c.Drain()
	old := time.Now().AddDate(-2, 0, 0).UTC().Format("2006-01-02T15:04:05.000Z")
	w.c.Exec(`UPDATE rec_profile_signals SET at=? WHERE profile_id='p' AND work_id IN (?,?,?)`, old, w.romance[0].ID, w.romance[1].ID, w.romance[2].ID)
	rediscover, err := w.s.recPersonalRow(r, "for_you:rediscover")
	if err != nil {
		t.Fatal(err)
	}
	if rediscover.spec.Why == nil || rediscover.spec.Why.Code != recWhyRediscover || rediscover.spec.Why.Fallback != "Favorites you haven't watched in a while." || len(rediscover.spec.WhyTitles) != 0 {
		t.Fatalf("rediscover why: %+v %+v", rediscover.spec.Why, rediscover.spec.WhyTitles)
	}
}

// TestWhySeriesRowContinuesFinishedBook checks the series row names the finished
// book it continues.
func TestWhySeriesRowContinuesFinishedBook(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	library := w.c.Library("books", "Books", "audiobook", "/books")
	var books []catalogtest.Item
	for i := 0; i < 4; i++ {
		book := w.c.Book(library, fmt.Sprintf("Book %d", i), "Author")
		part := w.c.BookFile(book, 1, fmt.Sprintf("/books/b%d.mp3", i))
		var token string
		if err := w.c.DB.QueryRow(`SELECT a.token FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=?`, part.ID).Scan(&token); err != nil {
			t.Fatal(err)
		}
		w.c.Exec(`INSERT INTO audio_tag_evidence(library_id,asset_id,field,source,value) VALUES('books',?,'series','embedded','Story'),('books',?,'series_index','embedded',?)`, token, token, fmt.Sprint(i+1))
		books = append(books, book)
	}
	w.watch("p", books[0])
	w.c.Drain()
	r := HomeRequest{Viewer: Viewer{Profile: "p", Fence: "p", Libraries: []string{"books"}}, ServerID: "s", Profile: "p", ViewerFence: "p", Libraries: []string{"books"}, Now: time.Now()}
	out, err := w.s.recPersonalRow(r, "for_you:series")
	if err != nil {
		t.Fatal(err)
	}
	if out.spec.Why == nil || out.spec.Why.Code != recWhyNext || len(out.spec.WhyTitles) != 1 || out.spec.WhyTitles[0].ID != books[0].Public {
		t.Fatalf("series why: %+v %+v", out.spec.Why, out.spec.WhyTitles)
	}
}

// TestWhyTitlesAreTheRequestingProfilesOwn checks a row's titles behind it come
// from that profile's history, never another profile's, and are capped at
// three.
func TestWhyTitlesAreTheRequestingProfilesOwn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	for _, f := range w.scifi[:4] {
		w.watch("p", f)
	}
	for _, f := range w.romance[:4] {
		w.watch("q", f)
	}
	w.c.Drain()
	mine := publicSet(w.scifi[:4])
	theirs := publicSet(w.romance[:4])
	row := func(profile, id string) recRow {
		out, err := w.s.recPersonalRow(HomeRequest{Viewer: Viewer{Profile: profile, Fence: profile, Libraries: []string{"m"}}, ServerID: "s", Profile: profile, ViewerFence: profile, Libraries: []string{"m"}, Now: time.Now()}, id)
		if err != nil {
			t.Fatalf("%s for %s: %v", id, profile, err)
		}
		return out
	}
	for _, check := range []struct {
		profile, id string
		own, other  map[string]bool
	}{
		{"p", "for_you:genre:science fiction", mine, theirs},
		{"q", "for_you:genre:romance", theirs, mine},
	} {
		out := row(check.profile, check.id)
		if len(out.spec.WhyTitles) == 0 || len(out.spec.WhyTitles) > 3 {
			t.Fatalf("%s names %d titles", check.id, len(out.spec.WhyTitles))
		}
		for _, ref := range out.spec.WhyTitles {
			if !check.own[ref.ID] {
				t.Fatalf("%s for %s names %s, which it has not watched", check.id, check.profile, ref.ID)
			}
			if check.other[ref.ID] {
				t.Fatalf("%s names another profile's title %s", check.id, ref.ID)
			}
		}
	}
}

// TestWhyPersonCountAndPlural checks the person explanation counts the
// profile's own titles with that person and pluralises its English fallback.
func TestWhyPersonCountAndPlural(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	w.watch("one", w.scifi[0])
	for _, f := range w.scifi[1:6] {
		w.watch("many", f)
	}
	w.c.Drain()
	row := func(profile string) recRow {
		out, err := w.s.recPersonalRow(HomeRequest{Viewer: Viewer{Profile: profile, Fence: profile, Libraries: []string{"m"}}, ServerID: "s", Profile: profile, ViewerFence: profile, Libraries: []string{"m"}, Now: time.Now()}, "for_you:person:cd:directing:dana villeneuve")
		if err != nil {
			t.Fatalf("%s: %v", profile, err)
		}
		return out
	}
	one := row("one")
	if one.spec.Why == nil || one.spec.Why.Code != recWhyPerson || one.spec.Why.Params["count"] != "1" || one.spec.Why.Params["name"] != "Dana Villeneuve" ||
		one.spec.Why.Fallback != "Because you've watched 1 title with Dana Villeneuve." {
		t.Fatalf("one watched title: %+v", one.spec.Why)
	}
	if len(one.spec.WhyTitles) != 1 || one.spec.WhyTitles[0].ID != w.scifi[0].Public {
		t.Fatalf("one watched title names %+v", one.spec.WhyTitles)
	}
	many := row("many")
	if many.spec.Why == nil || many.spec.Why.Code != recWhyPerson || many.spec.Why.Params["count"] != "5" ||
		many.spec.Why.Fallback != "Because you've watched 5 titles with Dana Villeneuve." {
		t.Fatalf("five watched titles: %+v", many.spec.Why)
	}
	if len(many.spec.WhyTitles) != 3 {
		t.Fatalf("five watched titles names %d, want 3", len(many.spec.WhyTitles))
	}
}
