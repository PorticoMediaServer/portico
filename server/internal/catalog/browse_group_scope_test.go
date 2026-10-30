package catalog

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func entryTitles(entries []ContentEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Title)
	}
	return out
}

func reversed(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}

// A group's own order is a server sort, so it survives a filter and pages by
// its own place in the group, in both directions.
func TestGroupOrderSurvivesFiltersAndPages(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	titles := []string{"Delta", "Alpha", "Charlie", "Bravo", "Echo"}
	items := []catalogtest.Item{}
	for i, title := range titles {
		item := c.Movie(movies, fmt.Sprintf("/m/%d.mkv", i), title, 2000+i)
		if title != "Charlie" {
			c.Genres(item.ID, "fixture", "Drama")
		}
		items = append(items, item)
	}
	collection := c.Collection(movies, "Shelf", items[2], items[0], items[4], items[1], items[3])
	c.Exec(`INSERT INTO catalog_playlists(token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at) VALUES('list','local','account','profile','Mine','op','hash','now')`)
	for position, index := range []int{4, 1, 3, 0, 2} {
		c.Exec(`INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position) SELECT ?,id,?,? FROM catalog_playlists WHERE token='list'`, fmt.Sprintf("entry%d", position), items[index].ID, position+1)
	}
	c.Drain()
	s := New(c.DB)
	owner := identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"})
	v := Viewer{Profile: owner, Fence: "f", Libraries: []string{"m"}}
	drama := BrowseNode{Field: "genre", Operator: "equals", Value: "Drama"}
	var stored []string
	rows, err := c.DB.Query(`SELECT ce.title FROM catalog_collection_members m JOIN catalog_entities ce ON ce.id=m.item_id WHERE m.collection_id=? ORDER BY m.order_key,m.item_id`, collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var title string
		if err = rows.Scan(&title); err != nil {
			t.Fatal(err)
		}
		if title != "Charlie" {
			stored = append(stored, title)
		}
	}
	rows.Close()
	for _, tc := range []struct {
		name  string
		group BrowseNode
		want  []string
	}{
		{"playlist", BrowseNode{Field: "list", Operator: "equals", Value: "list"}, []string{"Echo", "Alpha", "Bravo", "Delta"}},
		{"collection", BrowseNode{Field: "collection", Operator: "equals", Value: collection.Public}, stored},
	} {
		for _, direction := range []string{"asc", "desc"} {
			want := tc.want
			if direction == "desc" {
				want = reversed(want)
			}
			r := BrowseRequest{Library: "m", Pivot: "movies", Query: &BrowseNode{All: []BrowseNode{tc.group, drama}}, Sort: []BrowseSortSelection{{Field: "groupOrder", Direction: direction}}, Limit: 1}
			if got := entryTitles(walkBrowse(t, s, v, r)); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s %s: %v, want %v", tc.name, direction, got, want)
			}
			r.Limit, r.Range = 2, &BrowseRange{Start: 2}
			page, err := s.BrowseEntities(v, r)
			if err != nil {
				t.Fatal(err)
			}
			if got := entryTitles(page.Entries); !reflect.DeepEqual(got, want[2:]) || page.PageInfo.Total != 4 {
				t.Fatalf("%s %s range: %v (%d)", tc.name, direction, got, page.PageInfo.Total)
			}
		}
	}
	_, err = s.BrowseEntities(v, BrowseRequest{Library: "m", Pivot: "movies", Query: &drama, Sort: []BrowseSortSelection{{Field: "groupOrder"}}})
	if !errors.Is(err, ErrBrowseGroupOrder) {
		t.Fatalf("groupOrder without a group: %v", err)
	}
}

func TestGroupOrderOfShowsAlbumsAndBookParts(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("t", "TV", "tv", "/t")
	show := c.Show(tv, "Long", 2000)
	specials, one, two := c.Season(show, 0), c.Season(show, 1), c.Season(show, 2)
	c.Episode(show, two, 1, "/t/s2e1.mkv")
	c.Episode(show, specials, 1, "/t/s0e1.mkv")
	c.Episode(show, one, 2, "/t/s1e2.mkv")
	c.Episode(show, one, 1, "/t/s1e1.mkv")
	music := c.Library("u", "Music", "music", "/u")
	album := c.Album(c.Artist(music, "Band"), "Record", 2001)
	c.Song(album, 2, "/u/2.flac", "Second")
	c.Song(album, 1, "/u/1.flac", "First")
	c.Song(album, 3, "/u/3.flac", "Third")
	books := c.Library("b", "Books", "audiobook", "/b")
	book := c.Book(books, "Saga", "Writer")
	c.BookFile(book, 2, "/b/2.m4b")
	c.BookFile(book, 1, "/b/1.m4b")
	c.Drain()
	s := New(c.DB)
	for _, tc := range []struct {
		library, pivot, container string
		want                      []int
	}{{"t", "episodes", show.Public, nil}, {"u", "songs", album.Public, nil}, {"b", "parts", book.Public, nil}} {
		v := Viewer{Profile: "p", Fence: "f", Libraries: []string{tc.library}}
		got := walkBrowse(t, s, v, BrowseRequest{Library: tc.library, Pivot: tc.pivot, Query: &BrowseNode{Field: "memberOf", Operator: "equals", Value: tc.container}, Sort: []BrowseSortSelection{{Field: "groupOrder", Direction: "asc"}}, Limit: 1})
		ids := []int64{}
		for _, entry := range got {
			ids = append(ids, c.ID(entry.ID))
		}
		var want []int64
		switch tc.pivot {
		case "episodes":
			rows, err := c.DB.Query(`SELECT ep.entity_id FROM catalog_episodes ep JOIN catalog_seasons sd ON sd.entity_id=ep.season_id WHERE ep.show_id=? ORDER BY sd.number=0,sd.number,ep.number`, show.ID)
			want = scanIDs(t, rows, err)
		case "songs":
			rows, err := c.DB.Query(`SELECT entity_id FROM catalog_songs WHERE album_id=? ORDER BY track_number`, album.ID)
			want = scanIDs(t, rows, err)
		case "parts":
			rows, err := c.DB.Query(`SELECT entity_id FROM catalog_book_files WHERE book_id=? ORDER BY part_number`, book.ID)
			want = scanIDs(t, rows, err)
		}
		if !reflect.DeepEqual(ids, want) || len(want) < 2 {
			t.Fatalf("%s: %v, want %v", tc.pivot, ids, want)
		}
	}
	// One of the show's groups: its specials, and its first range of regular episodes.
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"t"}}
	for group, want := range map[string]int{"specials": 1, "range:0": 3, "all": 4, "unassigned_absolute": 0} {
		got := walkBrowse(t, s, v, BrowseRequest{Library: "t", Pivot: "episodes", Query: &BrowseNode{Field: "episodeGroup", Operator: "equals", Value: show.Public + "/" + group}, Sort: []BrowseSortSelection{{Field: "groupOrder"}}, Limit: 2})
		if len(got) != want {
			t.Fatalf("episode group %s: %d, want %d", group, len(got), want)
		}
	}
}

func scanIDs(t *testing.T, rows interface {
	Next() bool
	Scan(...any) error
	Close() error
	Err() error
}, err error) []int64 {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

func TestPersonalCollectionAndPersonRoleScopes(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	acted := c.Movie(movies, "/m/a.mkv", "Acted", 2000)
	directed := c.Movie(movies, "/m/b.mkv", "Directed", 2001)
	adult := c.Movie(movies, "/m/c.mkv", "Adult", 2002)
	c.Attributes(adult.ID, "contentRating", "R")
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('r',17)`)
	c.Exec(`INSERT INTO catalog_people(token,identity_key,name,sort_name) VALUES('person','tmdb:1','Pat','Pat')`)
	c.Exec(`INSERT INTO catalog_credit_labels(label) VALUES('Acting'),('Directing'),('Actor'),('Director')`)
	credit := func(item int64, ord int, role, department string) {
		c.Exec(`INSERT INTO catalog_credits(entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(?,?,(SELECT id FROM catalog_people WHERE token='person'),(SELECT id FROM catalog_credit_labels WHERE label=?),(SELECT id FROM catalog_credit_labels WHERE label=?),'Pat','tmdb',?,?)`, item, ord, role, department, fmt.Sprint(item, ord), ord)
	}
	credit(acted.ID, 0, "Actor", "Acting")
	credit(directed.ID, 0, "Director", "Directing")
	credit(adult.ID, 0, "Actor", "Acting")
	owner := identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"})
	c.Exec(`INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at) VALUES('mine','collection',?,'local','account','profile','Mine','now'),('everyone','collection',?,'local','account','profile','Everyone','now')`, owner, owner)
	c.Exec(`UPDATE saved_resources SET visibility='server' WHERE id='everyone'`)
	c.Exec(`INSERT INTO saved_resource_entries(id,resource_id,item_id) VALUES('e1','mine',?),('e2','mine',?),('e3','everyone',?)`, acted.ID, adult.ID, directed.ID)
	c.Drain()
	s := New(c.DB)
	restricted := classesRestrictionOf(classesCeiling(13), false)
	for _, tc := range []struct {
		profile string
		node    BrowseNode
		limited bool
		want    int
	}{
		{owner, BrowseNode{Field: "savedCollection", Operator: "equals", Value: "mine"}, false, 2},
		{owner, BrowseNode{Field: "savedCollection", Operator: "equals", Value: "mine"}, true, 1},
		{"other", BrowseNode{Field: "savedCollection", Operator: "equals", Value: "mine"}, false, 0},
		{"other", BrowseNode{Field: "savedCollection", Operator: "equals", Value: "everyone"}, false, 0},
		{identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "else", ProfileID: "p"}), BrowseNode{Field: "savedCollection", Operator: "equals", Value: "everyone"}, false, 1},
		{owner, BrowseNode{Field: "cast", Operator: "equals", Value: "person"}, false, 2},
		{owner, BrowseNode{Field: "cast", Operator: "equals", Value: "person"}, true, 1},
		{owner, BrowseNode{Field: "crew", Operator: "equals", Value: "person"}, false, 1},
		{owner, BrowseNode{Field: "person", Operator: "equals", Value: "person"}, false, 3},
	} {
		v := Viewer{Profile: tc.profile, Fence: tc.profile, Libraries: []string{"m"}}
		if tc.limited {
			v.Restrictions, v.Fence = restricted, tc.profile+":limited"
		}
		got := walkBrowse(t, s, v, BrowseRequest{Library: "m", Pivot: "movies", Query: &tc.node, Sort: []BrowseSortSelection{{Field: "title", Direction: "asc"}}, Limit: 1})
		if len(got) != tc.want {
			t.Fatalf("%s %s limited=%v: %v", tc.node.Field, tc.node.Value, tc.limited, entryTitles(got))
		}
	}
}

func walkAcross(t *testing.T, s *Service, v Viewer, r AcrossRequest) []ContentEntry {
	t.Helper()
	out := []ContentEntry{}
	for pages := 0; pages < 1000; pages++ {
		p, err := s.BrowseAcross(v, r)
		if err != nil {
			t.Fatalf("%+v: %v", r, err)
		}
		out = append(out, p.Entries...)
		if p.PageInfo.NextCursor == "" {
			return out
		}
		r.Cursor = p.PageInfo.NextCursor
	}
	t.Fatal("unbounded cursor walk")
	return nil
}

// A list that spans libraries is sorted and filtered as one list, with each
// entry naming its library, and a group is required.
func TestBrowseAcrossLibraries(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	tv := c.Library("t", "TV", "tv", "/t")
	film := c.Movie(movies, "/m/a.mkv", "Zulu", 2001)
	other := c.Movie(movies, "/m/b.mkv", "Alpha", 1999)
	adult := c.Movie(movies, "/m/c.mkv", "Mike", 2005)
	c.Attributes(adult.ID, "contentRating", "R")
	show := c.Show(tv, "Kilo", 2003)
	episode := c.Episode(show, c.Season(show, 1), 1, "/t/e1.mkv")
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('r',17)`)
	owner := identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"})
	for _, id := range []int64{film.ID, other.ID, adult.ID, show.ID} {
		c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,revision) VALUES(?,?,1,1)`, owner, id)
	}
	c.Exec(`INSERT INTO catalog_playlists(token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at) VALUES('list','local','account','profile','Mine','op','hash','now')`)
	for position, id := range []int64{episode.ID, film.ID, other.ID} {
		c.Exec(`INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position) SELECT ?,id,?,? FROM catalog_playlists WHERE token='list'`, fmt.Sprintf("e%d", position), id, position+1)
	}
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: owner, Fence: "f", Libraries: []string{"m", "t"}}
	myList := BrowseNode{Field: "watchlisted", Operator: "equals", Value: true}
	got := walkAcross(t, s, v, AcrossRequest{Pivot: "titles", Query: &myList, Sort: []BrowseSortSelection{{Field: "title", Direction: "asc"}}, Limit: 1})
	if titles := entryTitles(got); !reflect.DeepEqual(titles, []string{"Alpha", "Kilo", "Mike", "Zulu"}) {
		t.Fatalf("My List across libraries: %v", titles)
	}
	for _, entry := range got {
		if want := map[string]string{"Kilo": "t"}[entry.Title]; entry.LibraryID != want && !(want == "" && entry.LibraryID == "m") {
			t.Fatalf("%s names library %q", entry.Title, entry.LibraryID)
		}
	}
	lettered, err := s.BrowseAcross(v, AcrossRequest{Pivot: "titles", Query: &myList, Sort: []BrowseSortSelection{{Field: "title", Direction: "asc"}}, Limit: 1})
	if err != nil || fmt.Sprint(lettered.PositionIndex) != "[{A 0} {K 1} {M 2} {Z 3}]" {
		t.Fatalf("letter index across libraries: %v %v", lettered.PositionIndex, err)
	}
	limited := v
	limited.Restrictions, limited.Fence = classesRestrictionOf(classesCeiling(13), false), "limited"
	filtered := walkAcross(t, s, limited, AcrossRequest{Pivot: "titles", Query: &BrowseNode{All: []BrowseNode{myList, {Field: "year", Operator: "at-least", Value: float64(2000)}}}, Sort: []BrowseSortSelection{{Field: "year", Direction: "desc"}}, Limit: 1})
	if titles := entryTitles(filtered); !reflect.DeepEqual(titles, []string{"Kilo", "Zulu"}) {
		t.Fatalf("restricted, filtered and sorted: %v", titles)
	}
	ordered := walkAcross(t, s, v, AcrossRequest{Pivot: "items", Query: &BrowseNode{Field: "list", Operator: "equals", Value: "list"}, Sort: []BrowseSortSelection{{Field: "groupOrder", Direction: "asc"}}, Limit: 2})
	if titles := entryTitles(ordered); !reflect.DeepEqual(titles, []string{"Episode 1", "Zulu", "Alpha"}) {
		t.Fatalf("playlist across libraries in its own order: %v", titles)
	}
	page, err := s.BrowseAcross(v, AcrossRequest{Pivot: "items", Query: &BrowseNode{Field: "list", Operator: "equals", Value: "list"}, Sort: []BrowseSortSelection{{Field: "groupOrder", Direction: "desc"}}, Limit: 1, Range: &BrowseRange{Start: 1}})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Title != "Zulu" || page.PageInfo.Total != 3 {
		t.Fatalf("direct range: %+v %v", page.PageInfo, err)
	}
	if _, err = s.BrowseAcross(v, AcrossRequest{Pivot: "titles", Query: &BrowseNode{Field: "year", Operator: "at-least", Value: float64(2000)}}); !errors.Is(err, ErrBrowseAcrossGroup) {
		t.Fatalf("no group: %v", err)
	}
	if _, err = s.BrowseAcross(v, AcrossRequest{Pivot: "titles", Query: &myList, Sort: []BrowseSortSelection{{Field: "groupOrder"}}}); !errors.Is(err, ErrBrowseGroupOrder) {
		t.Fatalf("My List has no order of its own: %v", err)
	}
	capabilities, err := s.BrowseAcrossCapabilities(v, "titles")
	if err != nil || capabilities.ResolvedPivot == nil || len(capabilities.Surfaces) == 0 || len(capabilities.Sorts) == 0 {
		t.Fatalf("capabilities: %+v %v", capabilities, err)
	}
	for _, field := range capabilities.Fields {
		if field.FacetSource != nil && field.FacetSource.Endpoint != "/v1/browse/facets" {
			t.Fatalf("facet source %+v", field.FacetSource)
		}
	}
	facets, err := s.FacetsAcross(v, FacetRequest{Field: "decade"})
	if err != nil || len(facets.Values) == 0 {
		t.Fatalf("facets across libraries: %+v %v", facets, err)
	}
}

// Playing a group in its own order follows the same places a page shows.
func TestQueueFollowsGroupOrder(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("u", "Music", "music", "/u")
	album := c.Album(c.Artist(music, "Band"), "Record", 2001)
	c.Song(album, 3, "/u/3.flac", "Third")
	c.Song(album, 1, "/u/1.flac", "First")
	c.Song(album, 2, "/u/2.flac", "Second")
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"u"}}
	query := &BrowseNode{Field: "memberOf", Operator: "equals", Value: album.Public}
	for _, direction := range []string{"asc", "desc"} {
		selection := []BrowseSortSelection{{Field: "groupOrder", Direction: direction}}
		page := walkBrowse(t, s, v, BrowseRequest{Library: "u", Pivot: "songs", Query: query, Sort: selection, Limit: 100})
		tx, done, err := dbwork.BeginRead(context.Background(), c.DB)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := s.QueueKeysTx(context.Background(), tx, v, ResourceActor{}, &QueueQuery{LibraryID: "u", Pivot: "songs", Query: query, Sort: selection}, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := tx.QueryContext(context.Background(), plan.SQL, plan.Args...)
		got := []string{}
		for err == nil && rows.Next() {
			var id string
			err = rows.Scan(&id)
			got = append(got, id)
		}
		if rows != nil {
			rows.Close()
		}
		done()
		want := []string{}
		for _, entry := range page {
			want = append(want, entry.ID)
		}
		if err != nil || !reflect.DeepEqual(got, want) || len(want) != 3 {
			t.Fatalf("%s: queue %v, page %v (%v)", direction, got, want, err)
		}
	}
}

// Playing a list across libraries plays what its pages show, in their order.
func TestQueueAcrossLibrariesFollowsTheList(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	tv := c.Library("t", "TV", "tv", "/t")
	zulu := c.Movie(movies, "/m/z.mkv", "Zulu", 2001)
	alpha := c.Movie(movies, "/m/a.mkv", "Alpha", 1999)
	show := c.Show(tv, "Kilo", 2003)
	episode := c.Episode(show, c.Season(show, 1), 1, "/t/e1.mkv")
	owner := identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"})
	c.Exec(`INSERT INTO catalog_playlists(token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at) VALUES('list','local','account','profile','Mine','op','hash','now')`)
	for position, id := range []int64{zulu.ID, episode.ID, alpha.ID} {
		c.Exec(`INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position) SELECT ?,id,?,? FROM catalog_playlists WHERE token='list'`, fmt.Sprintf("e%d", position), id, position+1)
	}
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: owner, Fence: "f", Libraries: []string{"m", "t"}}
	for _, selection := range []BrowseSortSelection{{Field: "groupOrder", Direction: "asc"}, {Field: "groupOrder", Direction: "desc"}, {Field: "title", Direction: "asc"}} {
		query := &BrowseNode{Field: "list", Operator: "equals", Value: "list"}
		want := []string{}
		for _, entry := range walkAcross(t, s, v, AcrossRequest{Pivot: "items", Query: query, Sort: []BrowseSortSelection{selection}, Limit: 2}) {
			want = append(want, entry.ID)
		}
		tx, done, err := dbwork.BeginRead(context.Background(), c.DB)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := s.QueueKeysTx(context.Background(), tx, v, ResourceActor{}, &QueueQuery{LibraryID: AcrossLibraries, Pivot: "items", Query: query, Sort: []BrowseSortSelection{selection}}, "", nil)
		if err != nil {
			done()
			t.Fatal(err)
		}
		rows, err := tx.QueryContext(context.Background(), plan.SQL, plan.Args...)
		got := []string{}
		for err == nil && rows.Next() {
			var id string
			err = rows.Scan(&id)
			got = append(got, id)
		}
		if rows != nil {
			rows.Close()
		}
		done()
		if err != nil || !reflect.DeepEqual(got, want) || len(want) != 3 {
			t.Fatalf("%v: queue %v, list %v (%v)", selection, got, want, err)
		}
	}
}

// Playing a Home row plays the members the row held when Play was pressed, in
// the row's order or sorted as its See all page shows them.
func TestQueuePlaysAHomeRowSnapshot(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	zulu := c.Movie(movies, "/m/z.mkv", "Zulu", 2001)
	alpha := c.Movie(movies, "/m/a.mkv", "Alpha", 1999)
	mike := c.Movie(movies, "/m/k.mkv", "Mike", 2005)
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "profile", Fence: "f", Libraries: []string{"m"}}
	rows := map[string][]int64{"trending_now": {zulu.ID, mike.ID, alpha.ID}}
	query := &BrowseNode{Field: "homeRow", Operator: "equals", Value: "trending_now"}
	keys := func(selection BrowseSortSelection, rows map[string][]int64) ([]string, error) {
		tx, done, err := dbwork.BeginRead(context.Background(), c.DB)
		if err != nil {
			t.Fatal(err)
		}
		defer done()
		plan, err := s.QueueKeysTx(context.Background(), tx, v, ResourceActor{}, &QueueQuery{LibraryID: AcrossLibraries, Pivot: "titles", Query: query, Sort: []BrowseSortSelection{selection}}, "", rows)
		if err != nil {
			return nil, err
		}
		found, err := tx.QueryContext(context.Background(), plan.SQL, plan.Args...)
		if err != nil {
			return nil, err
		}
		defer found.Close()
		got := []int64{}
		for found.Next() {
			var id string
			if err = found.Scan(&id); err != nil {
				return nil, err
			}
			var entity int64
			if err = c.DB.QueryRow(`SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, id).Scan(&entity); err != nil {
				return nil, err
			}
			got = append(got, entity)
		}
		out := []string{}
		for _, entity := range got {
			out = append(out, map[int64]string{zulu.ID: "Zulu", alpha.ID: "Alpha", mike.ID: "Mike"}[entity])
		}
		return out, found.Err()
	}
	if got, err := keys(BrowseSortSelection{Field: "groupOrder", Direction: "asc"}, rows); err != nil || strings.Join(got, ",") != "Zulu,Mike,Alpha" {
		t.Fatalf("row order: %v %v", got, err)
	}
	if got, err := keys(BrowseSortSelection{Field: "title", Direction: "asc"}, rows); err != nil || strings.Join(got, ",") != "Alpha,Mike,Zulu" {
		t.Fatalf("sorted row: %v %v", got, err)
	}
	if _, err := keys(BrowseSortSelection{Field: "groupOrder", Direction: "asc"}, nil); err == nil {
		t.Fatal("a Home row nobody resolved was played")
	}
}

// The history across libraries pages in its own order past the totals limit,
// its total an open lower bound that grows as pages are read.
func TestHistoryAcrossLibrariesRunsPastTheTotalsLimit(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	tv := c.Library("t", "TV", "tv", "/t")
	film := c.Movie(movies, "/m/a.mkv", "Zulu", 2001)
	other := c.Movie(movies, "/m/b.mkv", "Alpha", 1999)
	third := c.Movie(movies, "/m/c.mkv", "Mike", 2005)
	show := c.Show(tv, "Kilo", 2003)
	episode := c.Episode(show, c.Season(show, 1), 1, "/t/e1.mkv")
	owner := identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"})
	for at, id := range []int64{other.ID, episode.ID, third.ID, film.ID} {
		c.Exec(`INSERT INTO personal_items(profile_id,item_id,last_played_at,revision) VALUES(?,?,?,1)`, owner, id, fmt.Sprintf("2026-10-0%dT00:00:00Z", at+1))
	}
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: owner, Fence: "f", Libraries: []string{"m", "t"}}
	history := BrowseNode{Field: "lastPlayedAt", Operator: "is-present"}
	got := walkAcross(t, s, v, AcrossRequest{Pivot: "titles", Query: &history, Sort: []BrowseSortSelection{{Field: "groupOrder", Direction: "asc"}}, Limit: 1})
	if titles := entryTitles(got); !reflect.DeepEqual(titles, []string{"Zulu", "Mike", "Kilo", "Alpha"}) {
		t.Fatalf("history across libraries: %v", titles)
	}
	before := browseTotalThreshold
	browseTotalThreshold = 2
	defer func() { browseTotalThreshold = before }()
	first, err := s.BrowseAcross(v, AcrossRequest{Pivot: "titles", Query: &history, Sort: []BrowseSortSelection{{Field: "groupOrder", Direction: "asc"}}, Limit: 1})
	if err != nil || first.PageInfo.Total != 2 || !first.PageInfo.TotalIsLowerBound || first.PageInfo.NextCursor == "" {
		t.Fatalf("first page past the limit: %+v %v", first.PageInfo, err)
	}
	deep, err := s.BrowseAcross(v, AcrossRequest{Pivot: "titles", Query: &history, Sort: []BrowseSortSelection{{Field: "groupOrder", Direction: "asc"}}, Limit: 1, Range: &BrowseRange{Start: 3}})
	if err != nil || len(deep.Entries) != 1 || deep.Entries[0].Title != "Alpha" || deep.PageInfo.HasMore {
		t.Fatalf("a page past the counted limit: %+v %+v %v", deep.Entries, deep.PageInfo, err)
	}
	all := walkAcross(t, s, v, AcrossRequest{Pivot: "items", Query: &BrowseNode{All: []BrowseNode{history, {Field: "year", Operator: "at-least", Value: float64(2000)}}}, Sort: []BrowseSortSelection{{Field: "groupOrder", Direction: "desc"}}, Limit: 1})
	if titles := entryTitles(all); !reflect.DeepEqual(titles, []string{"Mike", "Zulu"}) {
		t.Fatalf("filtered history, oldest first: %v", titles)
	}
}

// A history longer than the members a list reads is still sorted by title and
// queued whole, from the profile's own rows.
func TestHistoryAcrossLibrariesSortsAndQueuesPastTheMembersLimit(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	owner := identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"})
	names := []string{"Zulu", "Alpha", "Mike", "Kilo", "Echo"}
	for at, name := range names {
		film := c.Movie(movies, "/m/"+name+".mkv", name, 2001)
		c.Exec(`INSERT INTO personal_items(profile_id,item_id,last_played_at,revision) VALUES(?,?,?,1)`, owner, film.ID, fmt.Sprintf("2026-10-0%dT00:00:00Z", at+1))
	}
	c.Drain()
	// A renamed title moves in the history's title order.
	c.Exec(`UPDATE catalog_entities SET title='Bravo',sort_title='',sort_key='bravo' WHERE title='Mike'`)
	c.Exec(`UPDATE catalog_browse_rows SET title='Bravo',sort_key='bravo' WHERE title='Mike'`)
	before := acrossMaximum
	acrossMaximum = 2
	defer func() { acrossMaximum = before }()
	s := New(c.DB)
	v := Viewer{Profile: owner, Fence: "f", Libraries: []string{"m"}}
	history := BrowseNode{Field: "lastPlayedAt", Operator: "is-present"}
	for _, tc := range []struct {
		sort BrowseSortSelection
		want []string
	}{
		{BrowseSortSelection{Field: "title", Direction: "asc"}, []string{"Alpha", "Bravo", "Echo", "Kilo", "Zulu"}},
		{BrowseSortSelection{Field: "title", Direction: "desc"}, []string{"Zulu", "Kilo", "Echo", "Bravo", "Alpha"}},
		{BrowseSortSelection{Field: "lastPlayed", Direction: "desc"}, []string{"Echo", "Kilo", "Bravo", "Alpha", "Zulu"}},
	} {
		if titles := entryTitles(walkAcross(t, s, v, AcrossRequest{Pivot: "titles", Query: &history, Sort: []BrowseSortSelection{tc.sort}, Limit: 2})); !reflect.DeepEqual(titles, tc.want) {
			t.Fatalf("%v: %v", tc.sort, titles)
		}
		tx, done, err := dbwork.BeginRead(context.Background(), c.DB)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := s.QueueKeysTx(context.Background(), tx, v, ResourceActor{}, &QueueQuery{LibraryID: AcrossLibraries, Pivot: "items", Query: &history, Sort: []BrowseSortSelection{tc.sort}}, "", nil)
		if err != nil {
			done()
			t.Fatal(err)
		}
		rows, err := tx.QueryContext(context.Background(), plan.SQL, plan.Args...)
		queued := 0
		for err == nil && rows.Next() {
			queued++
		}
		if rows != nil {
			rows.Close()
		}
		done()
		if err != nil || queued != len(names) {
			t.Fatalf("%v: queued %d of %d (%v)", tc.sort, queued, len(names), err)
		}
	}
}
