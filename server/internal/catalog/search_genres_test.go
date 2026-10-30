package catalog

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func selectSearchGenreArt(t *testing.T, c *catalogtest.Catalog, item catalogtest.Item, kind, role string) string {
	t.Helper()
	key := fmt.Sprintf("genre-%d-%s", item.ID, role)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
	c.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at,status) VALUES(?,'image/jpeg',1920,1080,100,'now','ready'); INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`, digest)
	c.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at)
	 VALUES(?,?,?,?,'','fixture',?,'local','fixture','fence','now')`, key, kind, item.ID, role, key)
	c.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) SELECT column1,column2,column3,column4,column5,column6,column8,column9,column10,column11 FROM (VALUES(?,?,?,'',?,?,?,0,1,'fixture','now'))`, kind, item.ID, role, key, digest, digest)
	return digest
}

func TestSearchGenresRuling36CountsOrderAndRepresentativeArt(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	tv := c.Library("tv", "TV", "tv", "/tv")
	music := c.Library("music", "Music", "music", "/music")
	private := c.Library("private", "Private", "movie", "/private")
	items := map[string]catalogtest.Item{}
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "no-art"} {
		items[name] = c.Movie(movies, "/movies/"+name+".mkv", name, 2026)
	}
	c.Genres(items["a"].ID, "fixture", "Crime", "Drama", "Sci-Fi & Fantasy", "Repeat")
	c.Genres(items["b"].ID, "fixture", "Crime", "Drama")
	c.Genres(items["c"].ID, "fixture", "Crime")
	c.Genres(items["d"].ID, "fixture", "Drama")
	c.Genres(items["e"].ID, "fixture", "Sci-Fi & Fantasy")
	c.Genres(items["f"].ID, "fixture", "No backdrop")
	c.Genres(items["no-art"].ID, "fixture", "No art")
	hidden := c.Movie(private, "/private/hidden.mkv", "Hidden", 2026)
	c.Genres(hidden.ID, "fixture", "Hidden only", "Crime")
	blocked := c.Movie(movies, "/movies/blocked.mkv", "Blocked", 2026)
	c.Genres(blocked.ID, "fixture", "Blocked only", "Crime")
	c.Attributes(blocked.ID, "label", "Adult")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Album", 2026)
	c.Genres(album.ID, "fixture", "Music only", "Crime")
	c.Song(album, 1, "/music/song.flac", "Song")
	show := c.Show(tv, "Show", 2026)
	season := c.Season(show, 1)
	c.Episode(show, season, 1, "/tv/one.mkv")
	c.Episode(show, season, 2, "/tv/two.mkv")
	c.Genres(show.ID, "fixture", "Television")
	emptyShow := c.Show(tv, "Empty show", 2026)
	c.Genres(emptyShow.ID, "fixture", "Quiet")
	c.Collection(movies, "Not a title", items["a"], items["b"])
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		selectSearchGenreArt(t, c, items[name], "item", "poster")
		selectSearchGenreArt(t, c, items[name], "item", "backdrop")
	}
	selectSearchGenreArt(t, c, items["f"], "item", "poster")
	selectSearchGenreArt(t, c, show, "show", "backdrop")
	c.Drain()
	for index, name := range []string{"a", "b", "c", "d", "e", "f"} {
		c.Exec(`UPDATE catalog_browse_rows SET rating_max=? WHERE entity_id=?`, 10-index, items[name].ID)
	}
	s := New(c.DB)
	viewer := Viewer{Profile: "viewer", Fence: "authority", Libraries: []string{"movies", "tv", "music"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"Adult"}}}
	page, err := s.searchGenresReference(viewer, SearchGenreRequest{})
	if err != nil {
		t.Fatal(err)
	}
	runtime, _, _ := genreTestRuntime(t, c)
	maintained := genreReady(t, runtime, viewer, SearchGenreRequest{})
	if !reflect.DeepEqual(maintained.Genres, page.Genres) {
		t.Fatalf("maintained count/art differs from SQL oracle: got %+v want %+v", maintained.Genres, page.Genres)
	}
	values := []string{}
	byValue := map[string]SearchGenre{}
	for _, genre := range page.Genres {
		values = append(values, genre.Value)
		byValue[genre.Value] = genre
		if genre.Label != genre.Value || genre.Covers == nil || len(genre.Covers) > 2 {
			t.Fatal("genre lost its label or bounded cover list", genre)
		}
	}
	if want := []string{"Crime", "Drama", "Sci-Fi & Fantasy", "No art", "No backdrop", "Quiet", "Repeat", "Television"}; !reflect.DeepEqual(values, want) {
		t.Fatalf("video title counts/sort/restrictions: %v want %v", values, want)
	}
	// Opening a tile uses the same restricted movie/show membership, including
	// a genuinely empty show. It never becomes a mixed music/title browse.
	for _, genre := range page.Genres {
		query := &BrowseNode{Field: "genre", Operator: "equals", Value: genre.Value}
		opened, err := s.BrowseAcross(viewer, AcrossRequest{Pivot: "video-titles", Query: query, Limit: 1})
		if err != nil || opened.PageInfo.Total != genre.Count {
			t.Fatalf("genre %q counted %d but opened %+v: %v", genre.Value, genre.Count, opened.PageInfo, err)
		}
		entries := walkAcross(t, s, viewer, AcrossRequest{Pivot: "video-titles", Query: query, Limit: 1})
		if len(entries) != genre.Count {
			t.Fatalf("%s destination: %+v", genre.Value, entries)
		}
		for _, entry := range entries {
			if entry.Kind != "movie" && entry.Kind != "show" {
				t.Fatalf("non-video title: %+v", entry)
			}
		}
	}
	crime := &BrowseNode{Field: "genre", Operator: "equals", Value: "Crime"}
	if _, err := s.BrowseAcross(viewer, AcrossRequest{Pivot: "titles", Query: crime}); !errors.Is(err, ErrBrowseAcrossGroup) {
		t.Fatal("mixed titles silently acquired a genre-only group", err)
	}
	filtered := &BrowseNode{All: []BrowseNode{*crime, {Field: "title", Operator: "equals", Value: "b"}}}
	opened, err := s.BrowseAcross(viewer, AcrossRequest{Pivot: "video-titles", Query: filtered})
	if err != nil || len(opened.Entries) != 1 || opened.Entries[0].ID != items["b"].Public {
		t.Fatal("additional filter lost", opened, err)
	}
	capabilities, err := s.BrowseAcrossCapabilities(viewer, "video-titles")
	if err != nil || capabilities.ResolvedPivot == nil || !reflect.DeepEqual(capabilities.ResolvedPivot.EntityKinds, []string{"movie", "show"}) {
		t.Fatal(capabilities, err)
	}
	foundGenre := false
	for _, surface := range capabilities.Surfaces {
		if surface.ID == "genre-titles" && surface.MembershipField == "genre" {
			foundGenre = true
		}
	}
	if !foundGenre {
		t.Fatal("genre destination was not published", capabilities.Surfaces)
	}
	// Playing the genre resolves its actual complete member set, not the first
	// displayed page. Empty shows remain visible but have no queue members.
	tx, done, err := dbwork.BeginRead(context.Background(), c.DB)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.QueueKeysTx(context.Background(), tx, viewer, ResourceActor{}, &QueueQuery{LibraryID: AcrossLibraries, Pivot: "video-titles", Query: crime}, "", nil)
	if err != nil {
		done()
		t.Fatal(err)
	}
	rows, err := tx.QueryContext(context.Background(), plan.SQL, plan.Args...)
	got := []string{}
	if err == nil {
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			got = append(got, id)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
	}
	done()
	if err != nil || !reflect.DeepEqual(got, []string{items["a"].Public, items["b"].Public, items["c"].Public}) {
		t.Fatal("genre queue diverged", got, err)
	}
	for value, count := range map[string]int{"Crime": 3, "Drama": 3, "Sci-Fi & Fantasy": 2, "Television": 1} {
		if byValue[value].Count != count {
			t.Fatalf("%s counted episodes, collections, hidden titles or music: %+v", value, byValue[value])
		}
	}
	for value, name := range map[string]string{"Crime": "a", "Drama": "b", "Sci-Fi & Fantasy": "e", "Repeat": "a"} {
		if art := byValue[value].Backdrop; art == nil || art.ID != items[name].Public || art.Kind != "item" || !strings.HasPrefix(art.Path, "/v1/metadata/item/"+art.ID+"/art/backdrop?v=") {
			t.Fatalf("%s representative does not follow rating/no-repeat/fallback: %+v", value, art)
		}
	}
	if art := byValue["Television"].Backdrop; art == nil || art.ID != show.Public || art.Kind != "show" {
		t.Fatal("show must use its own selected backdrop", art)
	}
	if byValue["No backdrop"].Backdrop != nil || len(byValue["No backdrop"].Covers) != 1 || byValue["No art"].Backdrop != nil || len(byValue["No art"].Covers) != 0 {
		t.Fatal("missing art was invented or the counted genre was removed", byValue)
	}
	if got := byValue["Crime"].Covers; len(got) != 2 || got[0].ID != items["a"].Public || got[1].ID != items["b"].Public {
		t.Fatal("first two best posters", got)
	}
	if got := byValue["Drama"].Covers; len(got) != 2 || got[0].ID != items["d"].Public || got[1].ID != items["a"].Public {
		t.Fatal("unused poster then ranked fallback", got)
	}
	short, err := s.searchGenresReference(viewer, SearchGenreRequest{Limit: 2})
	if err != nil || !reflect.DeepEqual(short.Genres, page.Genres[:2]) || short.NextCursor == "" {
		t.Fatal("the initial prefix changed with surface limit", short, err)
	}
	continued, err := s.searchGenresReference(viewer, SearchGenreRequest{Limit: 2, Cursor: short.NextCursor})
	if err != nil || len(continued.Genres) != 2 || continued.Genres[0].Value != "Sci-Fi & Fantasy" || continued.Genres[1].Value != "No art" {
		t.Fatal("All genres cannot continue through missing art", continued, err)
	}
	otherViewer := viewer
	otherViewer.Fence = "revoked-and-regranted"
	if _, err = s.searchGenresReference(otherViewer, SearchGenreRequest{Limit: 2, Cursor: short.NextCursor}); !errors.Is(err, ErrCursor) {
		t.Fatal("cursor crossed an authority fence", err)
	}
	// A cached counted list must still follow the current selections.
	c.Exec(`DELETE FROM artwork_selections WHERE entity_id=? AND role='backdrop'`, items["a"].ID)
	withoutReady, err := s.searchGenresReference(viewer, SearchGenreRequest{})
	if err != nil || withoutReady.Genres[0].Backdrop == nil || withoutReady.Genres[0].Backdrop.ID != items["b"].Public {
		t.Fatal("cached art outlived readiness", withoutReady, err)
	}
	c.Exec(`DELETE FROM artwork_selections WHERE entity_id=? AND role='poster'`, items["a"].ID)
	if _, err = s.searchGenresReference(viewer, SearchGenreRequest{Limit: 2, Cursor: short.NextCursor}); !errors.Is(err, ErrStaleContinuation) {
		t.Fatal("selected-art revision did not invalidate the old cursor", err)
	}
	if empty, err := s.searchGenresReference(Viewer{Profile: "none"}, SearchGenreRequest{}); err != nil || len(empty.Genres) != 0 {
		t.Fatal("empty library authority disclosed genres", empty, err)
	}
}

func TestSearchGenresRejectInvalidWindows(t *testing.T) {
	s := New(nil)
	for _, limit := range []int{-1, 51} {
		if _, err := s.searchGenresReference(Viewer{}, SearchGenreRequest{Limit: limit}); err == nil {
			t.Fatal("invalid window accepted", limit)
		}
	}
}
