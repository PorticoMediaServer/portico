package api

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Two TV libraries behave as one scoped list without widening to other libraries. Each
// profile's rating limits still decide both the cards/counts and Continue's next episodes.
func TestListsAndHomeRowsScopeSeveralLibraries(t *testing.T) {
	s, _, members := seededWith(t, 600, 3)
	ctx := context.Background()
	lib, err := s.db.CreateLibrary(ctx, "More shows", db.LibraryShows, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	batch := db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}}
	for n := range 2 {
		show := db.ItemKey{Kind: db.KeyName, Key: fmt.Sprint("scope-show", n)}
		season := db.ItemKey{Kind: db.KeyName, Key: fmt.Sprint("scope-season", n)}
		one, zero := 1, 0
		batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindShow, Title: fmt.Sprint("Scope show ", n), Keys: []db.ItemKey{show},
			ContentRating: "G", RatingAge: &zero}, db.ImportItem{Kind: db.KindSeason, Title: "Season 1", Parent: &show, Keys: []db.ItemKey{season}, IndexNumber: &one})
		for episode := 1; episode <= 2; episode++ {
			name := fmt.Sprintf("scope-%d-%d", n, episode)
			rating, age := "G", 0
			if n == 0 && episode == 2 {
				rating, age = "R", 17
			}
			file := db.FileRef{Name: name + ".mkv"}
			batch.Files = append(batch.Files, db.ImportFile{FileRef: file, Role: db.FileMedia, Size: 1, DurationMS: 120000,
				Streams: []db.Stream{{Kind: db.StreamVideo, Codec: "h264", Width: 320, Height: 180}}})
			batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindEpisode, Title: name, Parent: &season, ParentIndex: &one, IndexNumber: new(episode),
				ContentRating: rating, RatingAge: &age, Keys: []db.ItemKey{{Kind: db.KeyName, Key: name}},
				Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: file}}}}})
		}
	}
	if _, err := s.db.Import(ctx, batch); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{members[0], members[2]} {
		var original, movieLibrary string
		for _, l := range get[Libraries](t, s, token, "/v1/libraries").Items {
			if l.Kind == db.LibraryShows && l.ID != lib.PublicID {
				original = l.ID
			}
			if l.Kind == db.LibraryMovies {
				movieLibrary = l.ID
			}
		}
		scope := "any:" + original + "," + lib.PublicID
		var expected []string
		for _, id := range []string{original, lib.PublicID} {
			page := get[ItemPage](t, s, token, "/v1/items?library="+id+"&kind=show&limit=200")
			for _, it := range page.Items {
				expected = append(expected, it.ID)
			}
		}
		var posted ItemPage
		request := ItemQueryRequest{Library: scope, Kind: []string{"show"}, Limit: 200, Total: true}
		if w := call(t, s, "POST", "/v1/items/query", token, request, &posted); w.Code != 200 || posted.Total == nil || *posted.Total != len(expected) {
			t.Fatalf("posted library scope: %d %+v", w.Code, posted)
		}
		for _, route := range []string{"/v1/items", "/v1/home/rows/recently_added"} {
			var got []string
			for cursor := ""; ; {
				query := url.Values{"library": {scope}, "kind": {"show"}, "sort": {"title"}, "limit": {"3"}, "total": {"true"}, "cursor": {cursor}}
				page := get[ItemPage](t, s, token, route+"?"+query.Encode())
				if page.Total == nil || *page.Total != len(expected) {
					t.Fatalf("%s scope count: %+v, want %d", route, page.Total, len(expected))
				}
				for _, it := range page.Items {
					if !slices.Contains(expected, it.ID) || slices.Contains(got, it.ID) {
						t.Fatalf("%s repeated or widened the scope: %s", route, it.ID)
					}
					got = append(got, it.ID)
				}
				if cursor = page.NextCursor; cursor == "" {
					break
				}
			}
			if len(got) != len(expected) {
				t.Fatalf("%s returned %d of %d", route, len(got), len(expected))
			}
		}
		// Start a movie outside the scope and finish each new show's first episode.
		movie := get[ItemPage](t, s, token, "/v1/items?library="+movieLibrary+"&kind=movie&limit=1").Items[0]
		call(t, s, "PUT", "/v1/items/"+movie.ID+"/state", token, map[string]any{"positionMs": 20000}, nil)
		episodes := get[ItemPage](t, s, token, "/v1/items?library="+lib.PublicID+"&kind=episode&limit=20").Items
		var wantNext []string
		for _, it := range episodes {
			if *it.Index == 2 {
				wantNext = append(wantNext, it.ID)
			} else {
				call(t, s, "PUT", "/v1/items/"+it.ID+"/state", token, map[string]any{"positionMs": 20000}, nil)
				call(t, s, "PUT", "/v1/items/"+it.ID+"/state", token, map[string]any{"watched": true}, nil)
			}
		}
		row := get[HomeRow](t, s, token, "/v1/home/rows/continue?library="+url.QueryEscape(scope)+"&total=true")
		if len(row.Items) != len(wantNext) || row.Total == nil || *row.Total != len(wantNext) {
			t.Fatalf("scoped next episodes: %+v, want %v", row, wantNext)
		}
		for _, it := range row.Items {
			if !slices.Contains(wantNext, it.ID) {
				t.Fatalf("unexpected continuation: %s", it.ID)
			}
		}
		if code := status(t, s, "GET", "/v1/items?kind=show&library="+url.QueryEscape(scope+",lib_absent"), token, nil); code != 404 {
			t.Fatalf("partly absent scope: %d", code)
		}
		if code := status(t, s, "GET", "/v1/items?kind=show&library=any:", token, nil); code != 400 {
			t.Fatalf("empty scope: %d", code)
		}
	}
	// The other seeded profile is limited to films/music. A mixed visible/hidden scope is
	// refused just like the partially absent one, rather than returning partial counts.
	visible := get[Libraries](t, s, members[1], "/v1/libraries").Items[0].ID
	query := url.Values{"library": {"any:" + visible + "," + lib.PublicID}, "kind": {"movie"}, "total": {"true"}}
	for _, route := range []string{"/v1/items", "/v1/home/rows/recently_added", "/v1/home/rows/continue"} {
		if code := status(t, s, "GET", route+"?"+query.Encode(), members[1], nil); code != 404 {
			t.Fatalf("%s partly hidden scope: %d", route, code)
		}
	}

}
