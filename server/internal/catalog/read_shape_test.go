package catalog

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// readSteps runs one read in its own snapshot and returns the SQLite
// virtual-machine steps it took. Steps measure work independently of the
// machine, so these tests assert the shape of a read's cost, not a time.
func readSteps(t *testing.T, c *catalogtest.Catalog, read func(*Service) error) int64 {
	t.Helper()
	ctx, cost := dbwork.Measure(context.Background())
	if err := dbwork.WithReadSnapshot(ctx, c.DB, func(ctx context.Context) error { return read(New(c.DB).WithContext(ctx)) }); err != nil {
		t.Fatal(err)
	}
	return cost().VMSteps
}

// A filtered grouping counts the entities that hold the grouped field. Songs
// hold genres; their album and artist do not, and evaluating a song predicate
// on those containers walks every song a second and third time.
func TestFilteredGroupingEvaluatesOnlyFieldHolders(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	lib := c.Library("mu", "Music", "music", "/mu")
	artist := c.Artist(lib, "Someone")
	for a := 0; a < 3; a++ {
		album := c.Album(artist, fmt.Sprintf("Album %d", a), 2000+a)
		for i := 0; i < 60; i++ {
			song := c.Song(album, i+1, fmt.Sprintf("/mu/%d/%d.flac", a, i), fmt.Sprintf("Song %d %d", a, i))
			c.Genres(song.ID, "fixture", fmt.Sprintf("Genre %d", i%4))
		}
	}
	c.Drain()
	c.Exec(`ANALYZE`)
	v := Viewer{Profile: "p", Fence: "shape", Libraries: []string{"mu"}}
	for _, query := range []*BrowseNode{
		// Version counts have no value-side index, so each title is tested.
		{Field: "versions", Operator: "equals", Value: float64(9)},
		{Field: "versions", Operator: "equals", Value: float64(1)},
		// The profile has no favourites: the grouping must still start from
		// its personal state rather than test every song.
		{Field: "favorite", Operator: "equals", Value: true},
	} {
		var groups BrowseResult
		grouped := readSteps(t, c, func(s *Service) error {
			var err error
			groups, err = s.BrowseEntities(v, BrowseRequest{Library: "mu", Pivot: "genres", Query: query, Limit: 40})
			return err
		})
		// The same predicate counted over the songs alone.
		var songs int
		counted := readSteps(t, c, func(s *Service) error {
			compiler := browseCompiler{profile: v.Profile}
			clause, err := compiler.compile(query)
			if err != nil {
				return err
			}
			return s.read().QueryRow(`SELECT count(*) FROM catalog_browse_rows e WHERE e.library_id=(SELECT id FROM catalog_libraries WHERE library_id='mu') AND e.kind=? AND `+clause, append([]any{int(compactcatalog.Track)}, compiler.args...)...).Scan(&songs)
		})
		total := 0
		for _, entry := range groups.Entries {
			total += *entry.Count
		}
		if total != songs {
			t.Fatalf("%v: grouped counts %d, songs %d", query.Value, total, songs)
		}
		t.Logf("%v: grouping %d steps, songs count %d steps", query.Value, grouped, counted)
		// With no match the grouping's work is the predicate's evaluation; the
		// former shape took about 2.5 times the songs' count here.
		if songs == 0 && grouped > counted*2+500 {
			t.Fatalf("%v: grouping took %d steps, more than twice the %d of counting the songs", query.Value, grouped, counted)
		}
	}
}

// A selective filter is answered from its values: artists, attribute terms and
// the ordered ratings index name the few matching titles, so the first page
// and its exact count cost the same in a four times larger library.
func TestSelectiveFiltersStayFlatAsTheLibraryGrows(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	queries := map[string]*BrowseNode{
		"artist":          {Field: "artist", Operator: "equals", Value: "Nobody"},
		"album":           {Field: "album", Operator: "starts-with", Value: "Rare"},
		"tag":             {Field: "tag", Operator: "equals", Value: "Rare"},
		"communityRating": {Field: "communityRating", Operator: "greater-than", Value: float64(9)},
	}
	steps := map[string][2]int64{}
	for size, songs := range []int{80, 320} {
		c := catalogtest.Open(t)
		lib := c.Library("mu", "Music", "music", "/mu")
		// The two matching songs belong to their own small artist and album,
		// so a container page reads the same small container at both sizes.
		rareArtist := c.Artist(lib, "Rarity")
		rarity := c.Album(rareArtist, "Rare Album", 2000)
		someone := c.Artist(lib, "Someone")
		var album catalogtest.Item
		for i := 0; i < songs; i++ {
			on := rarity
			if i >= 2 {
				if (i-2)%20 == 0 {
					album = c.Album(someone, fmt.Sprintf("Album %d", i/20), 2000)
				}
				on = album
			}
			song := c.Song(on, i%20+1, fmt.Sprintf("/mu/%d.flac", i), fmt.Sprintf("Song %d", i))
			tag := "Common"
			if i < 2 {
				tag = "Rare"
			}
			c.Attributes(song.ID, "tag", tag)
			rating := 5.0
			if i < 2 {
				rating = 9.5
			}
			c.Exec(`INSERT INTO metadata_ratings(item_id,provider,value,scale,votes,source_url,observed_at) VALUES(?,'tmdb',?,10,1,'','2026-01-01')`, song.ID, rating)
		}
		c.Drain()
		c.Exec(`ANALYZE`)
		v := Viewer{Profile: "p", Fence: "shape", Libraries: []string{"mu"}}
		for name, query := range queries {
			for _, pivot := range []string{"songs", "albums", "artists"} {
				var page BrowseResult
				n := readSteps(t, c, func(s *Service) error {
					var err error
					page, err = s.BrowseEntities(v, BrowseRequest{Library: "mu", Pivot: pivot, Query: query, Limit: 40})
					return err
				})
				if pivot == "songs" {
					want := map[string]int{"artist": 0, "album": 2, "tag": 2, "communityRating": 2}[name]
					if page.PageInfo.Total != want {
						t.Fatalf("%s: %d songs, want %d", name, page.PageInfo.Total, want)
					}
				} else {
					want := 1
					if name == "artist" {
						want = 0
					}
					if page.PageInfo.Total != want || len(page.Entries) != want {
						t.Fatalf("%s/%s: wrong selective containers %+v", name, pivot, page)
					}
					if want == 1 {
						id := rarity.Public
						if pivot == "artists" {
							id = rareArtist.Public
						}
						if page.Entries[0].ID != id {
							t.Fatalf("%s/%s returned another container: %+v", name, pivot, page.Entries)
						}
					}
				}
				pair := steps[name+"/"+pivot]
				pair[size] = n
				steps[name+"/"+pivot] = pair
			}
		}
	}
	for key, pair := range steps {
		t.Logf("%s: %d steps at 80 songs, %d at 320", key, pair[0], pair[1])
		if pair[1] > pair[0]*3/2+500 {
			t.Errorf("%s grew from %d to %d steps with the library", key, pair[0], pair[1])
		}
	}
}

// Attribute filters test a term's key before reading its titles. The key is
// the value's lower-case trimmed text and must fold exactly as the value does.
func TestAttributeTermKeysFoldLikeTheirValues(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	tags := [][]string{{"  Rock "}, {"RÖCK"}, {"Rock and Roll"}, {"Jazz"}, nil, {"ǅungla"}, {"STRASSE"}, {"Straße"}}
	ids := make([]string, len(tags))
	for i, values := range tags {
		item := c.Movie(lib, fmt.Sprintf("/m/%d.mkv", i), fmt.Sprintf("Film %d", i), 2000)
		ids[i] = item.Public
		if values != nil {
			c.Attributes(item.ID, "tag", values...)
		}
	}
	c.Drain()
	v := Viewer{Profile: "p", Fence: "fold", Libraries: []string{"m"}}
	for _, check := range []struct {
		op    string
		value any
		want  []int
	}{
		{"equals", "rock", []int{0, 1}},
		{"contains", "rock", []int{0, 1}},
		{"starts-with", "ro", []int{0, 1, 2}},
		{"not-equals", "rock", []int{2, 3, 4, 5, 6, 7}},
		{"in", []any{"jazz", "ǄUNGLA"}, []int{3, 5}},
		{"equals", "strasse", []int{6, 7}},
		{"is-missing", nil, []int{4}},
	} {
		page, err := New(c.DB).BrowseEntities(v, BrowseRequest{Library: "m", Pivot: "movies", Query: &BrowseNode{Field: "tag", Operator: check.op, Value: check.value}, Limit: 40})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, entry := range page.Entries {
			got[entry.ID] = true
		}
		if len(got) != len(check.want) {
			t.Fatalf("%s %v: %d titles, want %d", check.op, check.value, len(got), len(check.want))
		}
		for _, index := range check.want {
			if !got[ids[index]] {
				t.Fatalf("%s %v: missing %q", check.op, check.value, tags[index])
			}
		}
	}
}

// A restricted viewer's deep page in a fact order (file size here) takes its
// positions from the published class and checks the live restriction on the
// selected page, not on every row before it.
func TestRestrictedDeepFactPageChecksOnlyItsPage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < 400; i++ {
		item := c.Movie(lib, fmt.Sprintf("/m/%03d.mkv", i), fmt.Sprintf("Movie %03d", i), 2020)
		c.Attributes(item.ID, "contentRating", "PG")
	}
	c.Drain()
	s := New(c.DB)
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	r := classesRestrictionOf(classesCeiling(13), true)
	if err := s.RebuildVisibilityClass(context.Background(), "m", r); err != nil {
		t.Fatal(err)
	}
	c.Exec(`ANALYZE`)
	page := func(viewer Viewer, start int) (BrowseResult, int64) {
		var out BrowseResult
		steps := readSteps(t, c, func(s *Service) error {
			var err error
			out, err = s.BrowseEntities(viewer, BrowseRequest{Library: "m", Pivot: "movies", Sort: []BrowseSortSelection{{Field: "fileSize", Direction: "desc"}}, Limit: 20, Range: &BrowseRange{Start: start}})
			return err
		})
		return out, steps
	}
	open := Viewer{Profile: "p", Fence: "open", Libraries: []string{"m"}}
	restricted := Viewer{Profile: "p", Fence: "restricted", Libraries: []string{"m"}, Restrictions: r}
	openPage, openSteps := page(open, 360)
	restrictedPage, restrictedSteps := page(restricted, 360)
	if restrictedPage.PageInfo.Total != 400 || len(restrictedPage.Entries) != 20 {
		t.Fatalf("restricted deep page: %+v with %d entries", restrictedPage.PageInfo, len(restrictedPage.Entries))
	}
	for i := range openPage.Entries {
		if openPage.Entries[i].ID != restrictedPage.Entries[i].ID {
			t.Fatalf("entry %d differs between the open and the restricted page", i)
		}
	}
	t.Logf("deep fact page: open %d steps, restricted %d steps", openSteps, restrictedSteps)
	if restrictedSteps > openSteps*3 {
		t.Fatalf("restricted deep page took %d steps against the open page's %d", restrictedSteps, openSteps)
	}
}

// A search sorted by title, year or date added takes one indexed word's total
// from its maintained count, and marks a several-word total as the page's,
// instead of counting every full-text match a second time.
func TestSortedSearchCountsFromMaintainedTotals(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("films", "Films", "movie", "/films")
	for i := 0; i < 30; i++ {
		c.Movie(library, fmt.Sprintf("/films/%d.mkv", i), fmt.Sprintf("Harbor %d", i), 1990+i)
	}
	c.Drain()
	for _, sortID := range []string{"title", "releaseYear", "dateAdded"} {
		for _, check := range []struct {
			q     string
			exact bool
		}{{"Harbor", true}, {"Harbor 1", false}} {
			ctx, trace := dbwork.TraceQueryPlans(context.Background())
			out, err := New(c.DB).Search(ctx, SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"films"}}, Q: check.q, Group: "movies", Sort: sortID, Limit: 5})
			if err != nil || len(out.Groups) != 1 || len(out.Groups[0].Items) != 5 {
				t.Fatalf("%s %q: %+v %v", sortID, check.q, out, err)
			}
			group := out.Groups[0]
			if group.TotalCountExact != check.exact || check.exact && group.TotalCount != 30 {
				t.Fatalf("%s %q: total %d exact %v", sortID, check.q, group.TotalCount, group.TotalCountExact)
			}
			for _, query := range trace() {
				if strings.Contains(query, "SELECT count(*) FROM (SELECT rowid AS entity_id FROM catalog_search_titles") {
					t.Fatalf("%s %q counted the full-text matches again", sortID, check.q)
				}
			}
		}
	}
}

// A person filter starts from the person's credits. A credit on a show counts
// for the show, a credit on an episode for the episode and its show.
func TestPersonFilterFindsItemAndContainerCredits(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Harbor", 2020)
	season := c.Season(show, 1)
	first := c.Episode(show, season, 1, "/tv/1.mkv")
	second := c.Episode(show, season, 2, "/tv/2.mkv")
	movies := c.Library("m", "Movies", "movie", "/m")
	film := c.Movie(movies, "/m/a.mkv", "Film", 2001)
	credit := func(key string) compactcatalog.Credit {
		return compactcatalog.Credit{PersonKey: key, PersonName: key, PersonSortName: key, CreditID: key, CreditedName: key, Role: "Actor", Department: "Acting"}
	}
	compactCredits(c, show.ID, credit("tmdb:lead"))
	compactCredits(c, second.ID, credit("tmdb:guest"))
	compactCredits(c, film.ID, credit("tmdb:lead"))
	c.Drain()
	lead, guest := personToken(t, c.DB, "tmdb:lead"), personToken(t, c.DB, "tmdb:guest")
	v := Viewer{Profile: "p", Fence: "person", Libraries: []string{"tv", "m"}}
	for _, check := range []struct {
		library, pivot, person, operator string
		want                             []string
	}{
		{"tv", "shows", lead, "equals", []string{show.Public}},
		{"tv", "shows", guest, "equals", []string{show.Public}},
		{"tv", "episodes", guest, "equals", []string{second.Public}},
		{"tv", "episodes", lead, "equals", nil},
		{"tv", "episodes", guest, "not-in", []string{first.Public}},
		{"m", "movies", lead, "in", []string{film.Public}},
		{"m", "movies", guest, "equals", nil},
	} {
		value := any(check.person)
		if check.operator != "equals" {
			value = []any{check.person}
		}
		page, err := New(c.DB).BrowseEntities(v, BrowseRequest{Library: check.library, Pivot: check.pivot, Query: &BrowseNode{Field: "person", Operator: check.operator, Value: value}, Limit: 40})
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, entry := range page.Entries {
			got = append(got, entry.ID)
		}
		if fmt.Sprint(got) != fmt.Sprint(append([]string{}, check.want...)) && !(len(got) == 0 && len(check.want) == 0) {
			t.Fatalf("%s/%s person %s: got %v want %v", check.library, check.pivot, check.operator, got, check.want)
		}
	}
}

// A restricted viewer's page of containers checks the live restriction on the
// page's shows, not on every show the page skipped inside its block.
func TestRestrictedContainerPageChecksOnlyItsRows(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	for i := 0; i < 60; i++ {
		show := c.Show(tv, fmt.Sprintf("Show %02d", i), 2000)
		season := c.Season(show, 1)
		for n := 1; n <= 12; n++ {
			episode := c.Episode(show, season, n, fmt.Sprintf("/tv/%d/%d.mkv", i, n))
			c.Attributes(episode.ID, "contentRating", "TV-PG")
		}
	}
	c.Drain()
	s := New(c.DB)
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	r := classesRestrictionOf(classesCeiling(13), true)
	if err := s.RebuildVisibilityClass(context.Background(), "tv", r); err != nil {
		t.Fatal(err)
	}
	read := func(viewer Viewer) (BrowseResult, int64) {
		var out BrowseResult
		steps := readSteps(t, c, func(s *Service) error {
			var err error
			out, err = s.BrowseEntities(viewer, BrowseRequest{Library: "tv", Pivot: "shows", Limit: 5, Range: &BrowseRange{Start: 50}})
			return err
		})
		return out, steps
	}
	open, openSteps := read(Viewer{Profile: "p", Fence: "open", Libraries: []string{"tv"}})
	restricted, restrictedSteps := read(Viewer{Profile: "p", Fence: "restricted", Libraries: []string{"tv"}, Restrictions: r})
	if len(restricted.Entries) != 5 || restricted.PageInfo.Total != 60 {
		t.Fatalf("restricted page %+v with %d entries", restricted.PageInfo, len(restricted.Entries))
	}
	for i := range open.Entries {
		if open.Entries[i].ID != restricted.Entries[i].ID {
			t.Fatalf("entry %d differs", i)
		}
	}
	t.Logf("container page at 50: open %d steps, restricted %d steps", openSteps, restrictedSteps)
	if restrictedSteps > openSteps*2 {
		t.Fatalf("restricted container page took %d steps against the open page's %d", restrictedSteps, openSteps)
	}
}

// A broad title-sorted search reads its page from the title index: the same
// pages as ranking every match, at a cost that does not grow with the matches.
func TestTitleSortedSearchWalksTheTitleIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	defer func(window int) { titleWalkWindow = window }(titleWalkWindow)
	search := func(c *catalogtest.Catalog, q, direction string) ([]string, int64) {
		got := []string{}
		var steps int64
		r := SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"films", "more"}}, Q: q, Group: "movies", Sort: "title", Direction: direction, Limit: 5}
		for page := 0; page < 3; page++ {
			var out SearchEnvelope
			steps += readSteps(t, c, func(s *Service) error {
				var err error
				out, err = s.Search(s.Context(), r)
				return err
			})
			for _, entry := range out.Groups[0].Items {
				got = append(got, entry.ID)
			}
			r.Cursor = out.Groups[0].NextCursor
		}
		return got, steps
	}
	steps := []int64{}
	for _, n := range []int{100, 400} {
		c := catalogtest.Open(t)
		films := c.Library("films", "Films", "movie", "/films")
		more := c.Library("more", "More", "movie", "/more")
		for i := 0; i < n; i++ {
			library, root := films, "/films"
			if i%3 == 0 {
				library, root = more, "/more"
			}
			c.Movie(library, fmt.Sprintf("%s/h%d.mkv", root, i), fmt.Sprintf("Harbor %03d", i), 2000)
			// Rare matches sort after every broad one: the walk's window holds
			// none of them and the search falls back to ranking the matches.
			if i%5 == 0 {
				c.Movie(library, fmt.Sprintf("%s/z%d.mkv", root, i), fmt.Sprintf("Zulu Pier %03d", i), 2000)
			}
		}
		c.Drain()
		c.Exec(`ANALYZE`)
		for _, q := range []string{"Harbor", "pier"} {
			for _, direction := range []string{"asc", "desc"} {
				titleWalkWindow = 1 << 30 // every word is rare: rank the matches
				ranked, _ := search(c, q, direction)
				titleWalkWindow = 64
				walked, n := search(c, q, direction)
				if fmt.Sprint(ranked) != fmt.Sprint(walked) || len(walked) != 15 && q == "Harbor" {
					t.Fatalf("%q %s: walked %v, ranked %v", q, direction, walked, ranked)
				}
				if q == "Harbor" && direction == "asc" {
					steps = append(steps, n)
				}
			}
		}
	}
	t.Logf("three title-sorted pages of a broad word: %d steps at 100 titles, %d at 400", steps[0], steps[1])
	if steps[1] > steps[0]*3/2 {
		t.Fatalf("title-sorted search grew from %d to %d steps", steps[0], steps[1])
	}
}

// A shuffled library selection finds the library's newest entity with one
// seek per kind; the work does not grow with the library's other entities.
func TestLibraryMixFindsItsUpperBoundBySeeking(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	steps := []int64{}
	for _, songs := range []int{60, 240} {
		c := catalogtest.Open(t)
		lib := c.Library("music", "Music", "music", "/music")
		artist := c.Artist(lib, "Someone")
		var album catalogtest.Item
		for i := 0; i < songs; i++ {
			if i%20 == 0 {
				album = c.Album(artist, fmt.Sprintf("Album %d", i/20), 2000)
			}
			c.Song(album, i%20+1, fmt.Sprintf("/music/%d.flac", i), fmt.Sprintf("Song %d", i))
		}
		c.Drain()
		c.Exec(`ANALYZE`)
		v := Viewer{Profile: "p", Fence: "mix", Libraries: []string{"music"}}
		ctx, trace := dbwork.TraceQueryPlans(context.Background())
		ctx, cost := dbwork.Measure(ctx)
		if err := dbwork.WithReadSnapshot(ctx, c.DB, func(ctx context.Context) error {
			_, err := New(c.DB).WithContext(ctx).ListeningSelection(ContentRequest{Viewer: v, Library: "music", Limit: 40}, ListeningTarget{Kind: "library", ID: "music", LibraryID: "music"}, "mix", "fixture", false, "")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		for _, query := range trace() {
			if strings.Contains(query, "max(id)") && !strings.Contains(query, "catalog_kinds") {
				t.Fatalf("library bound reads every entity: %s", query)
			}
		}
		steps = append(steps, cost().VMSteps)
	}
	t.Logf("library mix: %d steps at 60 songs, %d at 240", steps[0], steps[1])
}

// An ordered walk of a music library reads its first page from the album and
// track indexes; it does not first read every entity of the catalogue.
func TestOrderedLibraryListeningStaysFlat(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	steps := []int64{}
	for _, extra := range []int{0, 600} {
		c := catalogtest.Open(t)
		lib := c.Library("music", "Music", "music", "/music")
		artist := c.Artist(lib, "Someone")
		var album catalogtest.Item
		for i := 0; i < 60; i++ {
			if i%20 == 0 {
				album = c.Album(artist, fmt.Sprintf("Album %d", i/20), 2000)
			}
			c.Song(album, i%20+1, fmt.Sprintf("/music/%d.flac", i), fmt.Sprintf("Song %d", i))
		}
		// Other titles of the catalogue: the walk must not grow with them.
		movies := c.Library("m", "Movies", "movie", "/m")
		for i := 0; i < extra; i++ {
			c.Movie(movies, fmt.Sprintf("/m/%d.mkv", i), fmt.Sprintf("Film %d", i), 2000)
		}
		c.Drain()
		c.Exec(`ANALYZE`)
		v := Viewer{Profile: "p", Fence: "ordered", Libraries: []string{"music"}}
		steps = append(steps, readSteps(t, c, func(s *Service) error {
			_, err := s.ListeningSelection(ContentRequest{Viewer: v, Library: "music", Limit: 20}, ListeningTarget{Kind: "library", ID: "music", LibraryID: "music"}, "ordered", "fixture", false, "")
			return err
		}))
	}
	t.Logf("ordered library page: %d steps beside no other titles, %d beside 600", steps[0], steps[1])
	if steps[1] > steps[0]*3/2 {
		t.Fatalf("ordered library page grew from %d to %d steps with the rest of the catalogue", steps[0], steps[1])
	}
}

// A card for a small artist must not count albums through unrelated songs.
// Exercise the enrichment producer directly as well as the full browse path.
func TestSelectedArtistFactsStayFlatBesideUnrelatedSongs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	var steps [2]int64
	for size, songs := range []int{80, 320} {
		c := catalogtest.Open(t)
		lib := c.Library("music", "Music", "music", "/music")
		artist := c.Artist(lib, "Rare artist")
		album := c.Album(artist, "Rare album", 2000)
		for i := 0; i < 2; i++ {
			c.Song(album, i+1, fmt.Sprintf("/music/rare/%d.flac", i), "Rare song")
		}
		other := c.Album(c.Artist(lib, "Other artist"), "Other album", 2000)
		for i := 0; i < songs; i++ {
			c.Song(other, i+1, fmt.Sprintf("/music/other/%d.flac", i), "Other song")
		}
		c.Drain()
		c.Exec(`ANALYZE`)
		viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"music"}}
		var entry ContentEntry
		steps[size] = readSteps(t, c, func(s *Service) error {
			return s.listeningArtistFacts(viewer, artist.Public, &entry)
		})
		if entry.AlbumCount == nil || *entry.AlbumCount != 1 {
			t.Fatalf("small artist's album count: %+v", entry)
		}
	}
	if steps[1] > steps[0]*3/2+500 {
		t.Fatalf("selected artist enrichment grew from %d to %d steps beside unrelated songs", steps[0], steps[1])
	}
}

func TestPlayablePersonalFiltersKeepDirectAndContainerIntent(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(lib, "Artist")
	first := c.Album(artist, "First", 2000)
	second := c.Album(artist, "Second", 2000)
	favorite := c.Song(first, 1, "/music/favorite.flac", "Favorite")
	plain := c.Song(first, 2, "/music/plain.flac", "Plain")
	albumOnly := c.Song(second, 1, "/music/album-only.flac", "Album only")
	for _, item := range []catalogtest.Item{favorite, plain, albumOnly} {
		c.Genres(item.ID, "fixture", "Genre")
	}
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,favorite,revision) VALUES('p',?,1,1),('p',?,1,1)`, favorite.ID, second.ID)
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"music"}}
	for _, tc := range []struct {
		pivot string
		value bool
		ids   []string
	}{
		{"songs", true, []string{favorite.Public}},
		{"songs", false, []string{plain.Public, albumOnly.Public}},
		{"albums", true, []string{first.Public, second.Public}},
	} {
		page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "music", Pivot: tc.pivot, Query: &BrowseNode{Field: "favorite", Operator: "equals", Value: tc.value}, Limit: 40})
		if err != nil || page.PageInfo.Total != len(tc.ids) || len(page.Entries) != len(tc.ids) {
			t.Fatalf("%s favorite=%v: %+v %v", tc.pivot, tc.value, page, err)
		}
		want := map[string]bool{}
		for _, id := range tc.ids {
			want[id] = true
		}
		for _, entry := range page.Entries {
			if !want[entry.ID] {
				t.Fatalf("%s favorite=%v: unexpected %+v", tc.pivot, tc.value, entry)
			}
			delete(want, entry.ID)
		}
		if len(want) != 0 {
			t.Fatal("missing personal matches", want)
		}
	}
	page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "music", Pivot: "genres", Query: &BrowseNode{Field: "favorite", Operator: "equals", Value: true}, Limit: 40})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Count == nil || *page.Entries[0].Count != 1 {
		t.Fatalf("grouping counted a container's intent as a song: %+v %v", page, err)
	}
	viewer.Profile = "other"
	page, err = s.BrowseEntities(viewer, BrowseRequest{Library: "music", Pivot: "songs", Query: &BrowseNode{Field: "favorite", Operator: "equals", Value: true}, Limit: 40})
	if err != nil || page.PageInfo.Total != 0 || len(page.Entries) != 0 {
		t.Fatalf("personal matches crossed profiles: %+v %v", page, err)
	}
	viewer.Profile = "p"
	c.Attributes(favorite.ID, "label", "Hidden")
	c.Drain()
	viewer.Restrictions.BlockedLabels = []string{"hidden"}
	for _, pivot := range []string{"songs", "genres"} {
		page, err = s.BrowseEntities(viewer, BrowseRequest{Library: "music", Pivot: pivot, Query: &BrowseNode{Field: "favorite", Operator: "equals", Value: true}, Limit: 40})
		if err != nil || page.PageInfo.Total != 0 || len(page.Entries) != 0 {
			t.Fatalf("%s personal filter bypassed the current restriction: %+v %v", pivot, page, err)
		}
	}
}
