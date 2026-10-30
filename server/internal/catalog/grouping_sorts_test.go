package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func walkBrowse(t *testing.T, s *Service, v Viewer, r BrowseRequest) []ContentEntry {
	t.Helper()
	out := []ContentEntry{}
	seen := map[string]bool{}
	for pages := 0; pages < 1000; pages++ {
		p, err := s.BrowseEntities(v, r)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range p.Entries {
			if seen[entry.ID] {
				t.Fatalf("duplicate %s", entry.ID)
			}
			seen[entry.ID] = true
			out = append(out, entry)
		}
		if p.PageInfo.NextCursor == "" {
			return out
		}
		if len(p.Entries) == 0 {
			t.Fatal("empty continuation")
		}
		r.Cursor = p.PageInfo.NextCursor
	}
	t.Fatal("unbounded cursor walk")
	return nil
}

func TestGroupingTitleSortsUseExactKeysAndRestrictedKeysets(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < 23; i++ {
		item := c.Movie(library, fmt.Sprintf("/m/%d.mkv", i), fmt.Sprintf("Harbor %d", i), 2000+i%4)
		c.Fields(item.ID, map[string]any{"release_date": fmt.Sprintf("2020-01-%02d", 1+i%6), "added_text": fmt.Sprintf("2026-01-%02dT00:00:00.000Z", 1+i%5)})
		c.Genres(item.ID, "fixture", "Drama")
		if i%4 == 0 {
			c.Attributes(item.ID, "contentRating", "R")
		}
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.SetAssetTx(ctx, tx, item.Asset, map[string]any{"size": int64(100 + i%3), "height": 480 + 240*(i%4)})
		})
		c.Exec(`INSERT INTO metadata_ratings(item_id,provider,value,scale,votes,source_url,observed_at) VALUES(?,'metacritic',?,10,0,'','now')`, item.ID, float64(i%5)+0.123456789)
		c.Exec(`INSERT INTO personal_play_counts(profile_id,item_id,plays) VALUES('p',?,?)`, item.ID, i%7)
	}
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('r',17)`)
	c.Drain()
	s := New(c.DB)
	restricted := classesRestrictionOf(classesCeiling(13), false)
	for _, v := range []Viewer{{Profile: "p", Fence: "open", Libraries: []string{"m"}}, {Profile: "p", Fence: "restricted", Libraries: []string{"m"}, Restrictions: restricted}} {
		for _, field := range []string{"releaseDate", "criticRating", "fileSize", "resolution", "playCount", "random"} {
			for _, direction := range []string{"asc", "desc"} {
				r := BrowseRequest{Library: "m", Pivot: "movies", Query: &BrowseNode{Field: "genre", Operator: "equals", Value: "Drama"}, Sort: []BrowseSortSelection{{Field: field, Direction: direction}}, Limit: 3}
				got := entryIDs(walkBrowse(t, s, v, r))
				r.Limit = 100
				want := entryIDs(walkBrowse(t, s, v, r))
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s %s %s: %v != %v", v.Fence, field, direction, got, want)
				}
				if len(got) != 23 && !v.Restrictions.Active() || len(got) != 17 && v.Restrictions.Active() {
					t.Fatalf("visible count %s %d", v.Fence, len(got))
				}
				// Independently verify the indexed scalar order, including duplicate keys.
				if field != "playCount" && field != "random" {
					def, _ := browseSortByID(field)
					restriction, args := EntityRestrictionSQL("e.entity_id", v.Restrictions)
					rows, err := c.DB.Query(`SELECT pid(ce.public_id) FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id WHERE e.kind=1 AND `+restriction+` ORDER BY `+def.Expression+` `+direction+`,e.entity_id `+direction, args...)
					if err != nil {
						t.Fatal(err)
					}
					expected := []string{}
					for rows.Next() {
						var id string
						if err = rows.Scan(&id); err != nil {
							t.Fatal(err)
						}
						expected = append(expected, id)
					}
					rows.Close()
					if !reflect.DeepEqual(got, expected) {
						t.Fatalf("%s %s wrong order", field, direction)
					}
				}
			}
		}
	}
	v := Viewer{Profile: "p", Fence: "open", Libraries: []string{"m"}}
	r := BrowseRequest{Library: "m", Pivot: "movies", Sort: []BrowseSortSelection{{Field: "random", Direction: "asc", Seed: 2000000000}}, Limit: 4}
	a := entryIDs(walkBrowse(t, s, v, r))
	r.Limit = 100
	b := entryIDs(walkBrowse(t, s, v, r))
	if !reflect.DeepEqual(a, b) {
		t.Fatal("seed changes with page size")
	}
	r.Sort[0].Seed = 0
	if reflect.DeepEqual(a, entryIDs(walkBrowse(t, s, v, r))) {
		t.Fatal("seed did not rotate order")
	}
}

func TestGroupingCountActivityAndFilters(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < 8; i++ {
		item := c.Movie(library, fmt.Sprintf("/m/%d.mkv", i), fmt.Sprintf("Film %d", i), 1990+i)
		c.Fields(item.ID, map[string]any{"added_text": fmt.Sprintf("2026-01-%02dT00:00:00.000Z", 1+i)})
		if i < 3 {
			c.Attributes(item.ID, "contentRating", "R")
		}
	}
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('r',17)`)
	c.Drain()
	s := New(c.DB)
	for _, v := range []Viewer{{Profile: "p", Fence: "o", Libraries: []string{"m"}}, {Profile: "p", Fence: "r", Libraries: []string{"m"}, Restrictions: classesRestrictionOf(classesCeiling(13), false)}} {
		caps, err := s.BrowseCapabilitiesFor("m", "categories")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(caps.ResolvedPivot.Sorts, []string{"title", "count", "recentActivity"}) || len(caps.Fields) == 0 {
			t.Fatalf("capabilities %+v", caps)
		}
		for _, field := range []string{"title", "count", "recentActivity"} {
			for _, direction := range []string{"asc", "desc"} {
				entries := walkBrowse(t, s, v, BrowseRequest{Library: "m", Pivot: "categories", Sort: []BrowseSortSelection{{Field: field, Direction: direction}}, Limit: 1})
				if len(entries) != 1 || entries[0].Count == nil {
					t.Fatalf("groups %+v", entries)
				}
				count := 8
				if v.Restrictions.Active() {
					count = 5
				}
				if *entries[0].Count != count {
					t.Fatalf("restricted count %d", *entries[0].Count)
				}
			}
		}
		entries := walkBrowse(t, s, v, BrowseRequest{Library: "m", Pivot: "categories", Query: &BrowseNode{Field: "year", Operator: "at-least", Value: float64(1995)}, Sort: []BrowseSortSelection{{Field: "count", Direction: "desc"}}, Limit: 1})
		if len(entries) != 1 || *entries[0].Count != 3 {
			t.Fatalf("filtered groups %+v", entries)
		}
	}
}

func TestGroupingScopesRespectListsAndHiddenMembers(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	visible := c.Movie(movies, "/m/a.mkv", "Harbor", 2000)
	hidden := c.Movie(movies, "/m/b.mkv", "Harbor Adult", 2000)
	c.Attributes(hidden.ID, "contentRating", "R")
	c.Attributes(visible.ID, "subtitleLanguage", "fr")
	collection := c.Collection(movies, "Harbor collection", visible, hidden)
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('r',17)`)
	c.Exec(`INSERT INTO catalog_playlists(token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at) VALUES('list','local','account','profile','Private','op','hash','now')`)
	c.Exec(`INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position) SELECT 'entry',id,?,1 FROM catalog_playlists WHERE token='list'`, visible.ID)
	c.Drain()
	s := New(c.DB)
	owner := identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"})
	for _, tc := range []struct {
		profile, field, op string
		value              any
		want               int
	}{{owner, "list", "equals", "list", 1}, {"unrelated", "list", "equals", "list", 0}, {owner, "collection", "equals", collection.Public, 1}, {owner, "memberOf", "equals", collection.Public, 1}, {owner, "subtitleLanguage", "equals", "fr", 1}, {owner, "versions", "equals", float64(1), 1}, {owner, "search", "contains", "Harbor", 1}} {
		v := Viewer{Profile: tc.profile, Fence: tc.profile, Libraries: []string{"m"}, Restrictions: classesRestrictionOf(classesCeiling(13), false)}
		got := walkBrowse(t, s, v, BrowseRequest{Library: "m", Pivot: "movies", Query: &BrowseNode{Field: tc.field, Operator: tc.op, Value: tc.value}, Sort: []BrowseSortSelection{{Field: "fileSize", Direction: "desc"}}, Limit: 1})
		if len(got) != tc.want {
			t.Fatalf("%s %s: %+v", tc.profile, tc.field, got)
		}
	}
}

func TestGroupingKindSortsFollowSourceEdits(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(music, "Zulu")
	album := c.Album(artist, "Zulu album", 2000)
	for i := 3; i > 0; i-- {
		c.Song(album, i, fmt.Sprintf("/music/%d.flac", i), fmt.Sprintf("Song %d", i))
	}
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Show", 2000)
	season := c.Season(show, 1)
	for i := 1; i < 4; i++ {
		ep := c.Episode(show, season, i, fmt.Sprintf("/tv/%d.mkv", i))
		c.Fields(ep.ID, map[string]any{"release_date": fmt.Sprintf("2020-01-%02d", i)})
	}
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"music", "tv"}}
	for _, field := range []string{"album", "artist", "trackNumber"} {
		for _, direction := range []string{"asc", "desc"} {
			got := walkBrowse(t, s, v, BrowseRequest{Library: "music", Pivot: "songs", Sort: []BrowseSortSelection{{Field: field, Direction: direction}}, Limit: 1})
			if len(got) != 3 {
				t.Fatalf("%s %s %+v", field, direction, got)
			}
			if field == "trackNumber" && direction == "asc" && got[0].Title != "Song 1" {
				t.Fatal("track order")
			}
		}
	}
	for _, field := range []string{"episodeCount", "latestAired"} {
		got := walkBrowse(t, s, v, BrowseRequest{Library: "tv", Pivot: "shows", Sort: []BrowseSortSelection{{Field: field, Direction: "desc"}}, Limit: 1})
		if len(got) != 1 || got[0].ID != show.Public {
			t.Fatalf("%s %+v", field, got)
		}
	}
	var count int
	var latest string
	if err := c.DB.QueryRow(`SELECT episode_count,latest_aired_key FROM catalog_browse_rows WHERE entity_id=?`, show.ID).Scan(&count, &latest); err != nil || count != 3 || latest != "2020-01-03T00:00:00.000Z" {
		t.Fatalf("episode keys %d %s %v", count, latest, err)
	}
	c.Fields(artist.ID, map[string]any{"title": "Alpha"})
	c.Fields(album.ID, map[string]any{"title": "Alpha album"})
	c.Drain()
	var stale int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_browse_rows WHERE kind=7 AND (artist_key NOT LIKE 'alpha%' OR album_key NOT LIKE 'alpha%')`).Scan(&stale); err != nil || stale != 0 {
		t.Fatalf("rename left %d stale keys: %v", stale, err)
	}
	c.Fields(show.ID, map[string]any{"title": "New show"})
	c.Drain()
}

func TestGroupingCursorSurvivesRemovedAnchorAndFencesSeed(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("m", "Movies", "movie", "/m")
	ids := map[string]int64{}
	for i := 0; i < 7; i++ {
		item := c.Movie(library, fmt.Sprintf("/m/%d.mkv", i), fmt.Sprintf("Movie %d", i), 2000)
		ids[item.Public] = item.ID
		c.Fields(item.ID, map[string]any{"release_date": fmt.Sprintf("2020-01-%02d", i+1)})
	}
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	r := BrowseRequest{Library: "m", Pivot: "movies", Sort: []BrowseSortSelection{{Field: "releaseDate", Direction: "asc"}}, Limit: 2}
	first, err := s.BrowseEntities(v, r)
	if err != nil {
		t.Fatal(err)
	}
	c.Delete(ids[first.Entries[1].ID])
	c.Drain()
	r.Cursor = first.PageInfo.NextCursor
	if _, err := s.BrowseEntities(v, r); !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("removed anchor did not invalidate its old ordering: %v", err)
	}
	// A published ordering requires a range recovery, whose missing anchor
	// retains the viewport's position in the current list.
	r.Cursor = ""
	r.Range = &BrowseRange{Start: 1, Revision: first.PageInfo.Revision, AnchorID: first.Entries[1].ID}
	next, err := s.BrowseEntities(v, r)
	if err != nil || next.PageInfo.Total != 6 || next.PageInfo.Start != 1 || len(next.Entries) != 2 || next.Entries[0].Title != "Movie 2" || next.Entries[1].Title != "Movie 3" {
		t.Fatalf("removed anchor recovery %+v %v", next, err)
	}
	r.Range = nil
	r.Sort = []BrowseSortSelection{{Field: "random", Direction: "asc", Seed: 123}}
	first, err = s.BrowseEntities(v, r)
	if err != nil {
		t.Fatal(err)
	}
	r.Cursor = first.PageInfo.NextCursor
	r.Sort[0].Seed = 124
	if _, err = s.BrowseEntities(v, r); err == nil {
		t.Fatal("cursor accepted another seed")
	}
}

func TestGroupingTextFiltersFoldCaseAndAccents(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("m", "Movies", "movie", "/m")
	film := c.Movie(library, "/m/a.mkv", "Amélie", 2000)
	c.Genres(film.ID, "fixture", "Comédie")
	c.Drain()
	s := New(c.DB)
	for _, node := range []BrowseNode{{Field: "title", Operator: "contains", Value: "amelie"}, {Field: "title", Operator: "equals", Value: "AMÉLIE"}, {Field: "genre", Operator: "equals", Value: "COMEDIE"}, {Field: "genre", Operator: "contains-all", Value: []any{"comédie"}}} {
		got := walkBrowse(t, s, Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}, BrowseRequest{Library: "m", Pivot: "movies", Query: &node, Limit: 1})
		if len(got) != 1 || got[0].ID != film.Public {
			t.Fatalf("%+v: %+v", node, got)
		}
	}
}

func TestGroupingSubtitleFilterReadsCurrentEvidenceAndPrivateUploads(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("m", "Movies", "movie", "/m")
	item := c.Movie(library, "/m/a.mkv", "Film", 2000)
	c.Exec(`INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) SELECT token,1,size,modified_ns,0,1,'known','f' FROM catalog_assets WHERE id=?`, item.Asset)
	c.Exec(`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,format,language,title,is_default,is_forced,reason) VALUES('file-caption',?,'embedded','stream1',1,'srt','fr','French',0,0,'')`, item.Token)
	c.Exec(`INSERT INTO subtitle_resources(id,item_id,source_id,scope,owner,current_revision) VALUES('private-caption',?,?,'personal','p',1)`, item.ID, item.Token)
	c.Exec(`INSERT INTO subtitle_revisions(resource_id,revision,digest,size,format,language,title,origin,rights,original_digest,source_size,source_modified_ns,source_facts_revision,created_at) SELECT 'private-caption',1,'d',1,'srt','es','Spanish','upload','owner','d',size,modified_ns,1,'now' FROM catalog_assets WHERE id=?`, item.Asset)
	c.Drain()
	s := New(c.DB)
	for _, tc := range []struct {
		profile, language string
		want              int
	}{{"p", "fr", 1}, {"other", "fr", 1}, {"p", "es", 1}, {"other", "es", 0}} {
		got := walkBrowse(t, s, Viewer{Profile: tc.profile, Fence: tc.profile, Libraries: []string{"m"}}, BrowseRequest{Library: "m", Pivot: "movies", Query: &BrowseNode{Field: "subtitleLanguage", Operator: "equals", Value: tc.language}, Limit: 1})
		if len(got) != tc.want {
			t.Fatalf("%s %s %+v", tc.profile, tc.language, got)
		}
	}
	c.Exec(`UPDATE asset_subtitle_facts SET size=size+1 WHERE asset_id=?`, item.Token)
	got := walkBrowse(t, s, Viewer{Profile: "p", Fence: "p", Libraries: []string{"m"}}, BrowseRequest{Library: "m", Pivot: "movies", Query: &BrowseNode{Field: "subtitleLanguage", Operator: "equals", Value: "fr"}, Limit: 1})
	if len(got) != 0 {
		t.Fatal("obsolete subtitle evidence still matched")
	}
	page, err := s.BrowseEntities(Viewer{Profile: "p", Fence: "p", Libraries: []string{"m"}}, BrowseRequest{Library: "m", Pivot: "movies", Query: &BrowseNode{Field: "subtitleLanguage", Operator: "equals", Value: "fr"}, Limit: 1})
	if err != nil || page.PageInfo.Total != 0 {
		t.Fatalf("stale subtitle total %+v %v", page.PageInfo, err)
	}
}

func TestGroupingOrderCountsAndRecentAddition(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < 9; i++ {
		item := c.Movie(library, fmt.Sprintf("/m/%d.mkv", i), fmt.Sprintf("Film %d", i), 1990)
		year := 1980
		if i < 2 {
			year = 1970
		} else if i < 5 {
			year = 2000
		}
		c.Fields(item.ID, map[string]any{"year": year, "added_text": fmt.Sprintf("2026-01-%02dT00:00:00.000Z", i+1)})
	}
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	for _, tc := range []struct {
		field, direction string
		want             []string
	}{{"title", "asc", []string{"decade:1970", "decade:1980", "decade:2000"}}, {"count", "desc", []string{"decade:1980", "decade:2000", "decade:1970"}}, {"recentActivity", "desc", []string{"decade:1980", "decade:2000", "decade:1970"}}} {
		got := entryIDs(walkBrowse(t, s, v, BrowseRequest{Library: "m", Pivot: "categories", Sort: []BrowseSortSelection{{Field: tc.field, Direction: tc.direction}}, Limit: 1}))
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s %s: %v", tc.field, tc.direction, got)
		}
	}
}

func TestGroupingPersonalKeyPagesUseTheirIndexes(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("m", "Movies", "movie", "/m")
	c.Movie(library, "/m/a.mkv", "Film", 2000)
	c.Drain()
	for _, field := range []string{"personalRating", "lastPlayed", "playCount"} {
		definition, _ := browseSortByID(field)
		p := pageShape{request: BrowseRequest{Library: "m", Profile: "p"}, where: "e.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND e.kind=?", args: []any{"m", 1}, kind: 1, limit: 4}
		query, args, err := personalKeyPageSQL(p, definition, true)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := c.DB.Query("EXPLAIN QUERY PLAN "+compactcatalog.ServingSQL(query), args...)
		if err != nil {
			t.Fatal(err)
		}
		_, _, index := personalSortColumn(field)
		found := false
		for rows.Next() {
			var a, b, c int
			var plan string
			if err = rows.Scan(&a, &b, &c, &plan); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(plan, index) {
				found = true
			}
			if strings.Contains(plan, "USE TEMP B-TREE") || strings.Contains(plan, "MATERIALIZE") {
				t.Fatalf("%s materializes its order: %s", field, plan)
			}
		}
		rows.Close()
		if !found {
			t.Fatalf("%s did not use profile index", field)
		}
	}
}

func TestGroupingPersonalOrderPublishesOnlyChangedKeys(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("m", "Movies", "movie", "/m")
	item := c.Movie(library, "/m/a.mkv", "Film", 2000)
	c.Exec(`INSERT INTO personal_play_counts(profile_id,item_id,plays) VALUES('p',?,1)`, item.ID)
	c.Drain()
	s := New(c.DB)
	before, err := s.browseContentRevision("m", "p")
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE personal_play_counts SET plays=2 WHERE profile_id='p' AND item_id=?`, item.ID)
	c.Drain()
	after, err := s.browseContentRevision("m", "p")
	if err != nil || after.Viewer <= before.Viewer {
		t.Fatalf("changed count not published: %+v %+v %v", before, after, err)
	}
	c.Exec(`UPDATE personal_play_counts SET plays=plays WHERE profile_id='p' AND item_id=?`, item.ID)
	c.Drain()
	unchanged, err := s.browseContentRevision("m", "p")
	if err != nil || unchanged.Viewer != after.Viewer {
		t.Fatalf("unchanged count published: %+v %+v %v", after, unchanged, err)
	}
}

func TestGroupingPersonalFiltersAndSortsIncludeContainerIntent(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(library, "Artist")
	album := c.Album(artist, "Album", 2000)
	c.Song(album, 1, "/music/a.flac", "Song")
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,favorite,rating,revision,last_played_at) VALUES('p',?,1,9,1,'2026-01-01T00:00:00.000Z')`, album.ID)
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"music"}}
	for _, node := range []BrowseNode{{Field: "favorite", Operator: "equals", Value: true}, {Field: "personalRating", Operator: "at-least", Value: float64(8)}, {Field: "lastPlayedAt", Operator: "is-present"}} {
		got := walkBrowse(t, s, viewer, BrowseRequest{Library: "music", Pivot: "albums", Query: &node, Sort: []BrowseSortSelection{{Field: "personalRating", Direction: "desc"}}, Limit: 1})
		if len(got) != 1 || got[0].ID != album.Public {
			t.Fatalf("container intent %+v: %+v", node, got)
		}
	}
	var rating int
	if err := c.DB.QueryRow(`SELECT rating FROM catalog_personal_sort_rows WHERE profile_id='p' AND entity_id=?`, album.ID).Scan(&rating); err != nil || rating != 9 {
		t.Fatalf("container rating %d %v", rating, err)
	}
}
