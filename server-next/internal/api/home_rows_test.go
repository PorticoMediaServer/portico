package api

import (
	"context"
	"fmt"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/titledata/titledatatest"
	"net/url"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A row continues beyond Home's preview, and See all uses the ordinary sorts, filters and
// counts. Restricted profiles count and receive only what they may open.
func TestHomeRowsPageTheirWholeSource(t *testing.T) {
	s, token, limited := seeded(t)
	library := get[Libraries](t, s, token, "/v1/libraries").Items[0].ID
	for _, who := range []string{token, limited} {
		expected := get[ItemPage](t, s, who, "/v1/items?library="+library+"&kind=movie&sort=title&limit=200&total=true")
		path := "/v1/home/rows/recently_added?library=" + library + "&kind=movie&sort=title&limit=7&total=true"
		var got []string
		for cursor := ""; ; {
			page := get[HomeRow](t, s, who, path+"&cursor="+url.QueryEscape(cursor))
			if page.Total == nil || *page.Total != *expected.Total || page.Title != "Recently Added" {
				t.Fatalf("row header/count: %+v", page)
			}
			for _, item := range page.Items {
				got = append(got, item.ID)
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		want := []string{}
		for _, item := range expected.Items {
			want = append(want, item.ID)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("row paged %d of %d titles, or changed order", len(got), len(want))
		}
	}
	// A Continue row is not limited to Home's forty. Changing the sort does not lose its source.
	films := get[ItemPage](t, s, token, "/v1/items?library="+library+"&kind=movie&limit=50").Items
	at := int64(60000)
	for _, item := range films {
		if w := call(t, s, "PUT", "/v1/items/"+item.ID+"/state", token, map[string]any{"positionMs": at}, nil); w.Code != 200 {
			t.Fatalf("start: %d %s", w.Code, w.Body)
		}
	}
	page := get[HomeRow](t, s, token, "/v1/home/rows/continue?library="+library+"&sort=title&limit=200&total=true")
	if len(page.Items) != 50 || page.Total == nil || *page.Total != 50 {
		t.Fatalf("Continue holds %d, count %v", len(page.Items), page.Total)
	}
	initial := get[Home](t, s, token, "/v1/home")
	for _, row := range initial.Rows {
		if row.ID == "continue" && (len(row.Items) != 40 || row.NextCursor == "") {
			t.Fatalf("initial Continue: %d, cursor %q", len(row.Items), row.NextCursor)
		}
	}
	if w := call(t, s, "GET", "/v1/home/rows/view:not-yours", limited, nil, nil); w.Code != 404 {
		t.Fatalf("hidden view: %d", w.Code)
	}
	if w := call(t, s, "GET", "/v1/home/rows/recently_added?library=missing", limited, nil, nil); w.Code != 404 {
		t.Fatalf("missing library: %d", w.Code)
	}
}

// A preview's cursor starts exactly after its last card, including ties between libraries.
func TestRecentlyAddedContinuesFromHome(t *testing.T) {
	s, token, limited := seeded(t)
	// One timestamp across two libraries exercises the id tie-break at Home's page edge.
	for libn := range 2 {
		lib, err := s.db.CreateLibrary(context.Background(), fmt.Sprint("Tied ", libn), db.LibraryMovies, "en", []string{t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		batch := db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}}
		for n := range 25 {
			name := fmt.Sprintf("Tied %d %d", libn, n)
			batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindMovie, Title: name, AddedAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), ContentRating: "G", Keys: []db.ItemKey{{Kind: db.KeyName, Key: name}}})
		}
		if _, err := s.db.Import(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
	}

	for _, who := range []string{token, limited} {
		all := get[HomeRow](t, s, who, "/v1/home/rows/recently_added?limit=200")
		got := []string{}
		var cursor string
		for _, row := range get[Home](t, s, who, "/v1/home").Rows {
			if row.ID == "recently_added" {
				for _, it := range row.Items {
					got = append(got, it.ID)
				}
				cursor = row.NextCursor
			}
		}
		for cursor != "" {
			page := get[HomeRow](t, s, who, "/v1/home/rows/recently_added?limit=13&cursor="+url.QueryEscape(cursor))
			for _, it := range page.Items {
				got = append(got, it.ID)
			}
			cursor = page.NextCursor
		}
		want := []string{}
		for _, it := range all.Items {
			want = append(want, it.ID)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("Home plus remaining row: %d titles, want %d; order equal %v", len(got), len(want), slices.Equal(got, want))
		}
	}
}

// A mood preview and its continuation are the same bounded kept choice. Already liked
// titles stay out, and sorting or filtering See all never widens that choice to the library.
func TestMoodRowContinuesItsKeptChoiceFromHome(t *testing.T) {
	s, token, other := seeded(t)
	library := get[Libraries](t, s, token, "/v1/libraries").Items[0].ID
	films := get[ItemPage](t, s, token, "/v1/items?library="+library+"&kind=movie&limit=100").Items
	f := titledatatest.File{Vocabulary: []titledatatest.Tag{{ID: 1, Key: "mood:cozy", RowLabel: "cozy stories"}}}
	liked := map[string]bool{}
	for n, it := range films {
		external := get[Item](t, s, token, "/v1/items/"+it.ID).ExternalIDs["tmdb"]
		f.Works = append(f.Works, titledatatest.Work{QID: int64(100 + n), IDs: map[int]string{titledatatest.TMDBMovie: external}, Tags: map[int]int{1: 3}})
		if n < 3 {
			liked[it.ID] = true
			call(t, s, "PUT", "/v1/items/"+it.ID+"/state", token, map[string]any{"favorite": true}, nil)
		}
	}
	path := filepath.Join(t.TempDir(), "titles.sqlite")
	titledatatest.Write(t, path, f)
	ctx := context.Background()
	if err := db.PrepareTitleData(ctx, path, "screen", "2026.45"); err != nil {
		t.Fatal(err)
	}
	if err := s.db.UseTitleData(path); err != nil {
		t.Fatal(err)
	}
	for after := int64(0); ; {
		next, err := s.db.ApplyTitleData(ctx, after, 100)
		if err != nil {
			t.Fatal(err)
		}
		if next == 0 {
			break
		}
		after = next
	}
	get[Home](t, s, token, "/v1/home") // asks for the profile's kept choice
	if err := s.db.RefreshMoodRows(ctx); err != nil {
		t.Fatal(err)
	}
	var preview HomeRow
	for _, row := range get[Home](t, s, token, "/v1/home").Rows {
		if row.ID == "mood:cozy" {
			preview = row
		}
	}
	if len(preview.Items) != 40 || preview.NextCursor == "" {
		t.Fatalf("mood preview: %d, cursor %q", len(preview.Items), preview.NextCursor)
	}
	got := []string{}
	for _, it := range preview.Items {
		got = append(got, it.ID)
	}
	for cursor := preview.NextCursor; cursor != ""; {
		page := get[HomeRow](t, s, token, "/v1/home/rows/mood:cozy?limit=9&cursor="+url.QueryEscape(cursor))
		for _, it := range page.Items {
			got = append(got, it.ID)
		}
		cursor = page.NextCursor
	}
	all := get[HomeRow](t, s, token, "/v1/home/rows/mood:cozy?limit=200&total=true")
	want := []string{}
	for _, it := range all.Items {
		want = append(want, it.ID)
		if liked[it.ID] {
			t.Fatal("liked title returned in mood row")
		}
	}
	if len(want) <= 40 || !slices.Equal(got, want) || all.Total == nil || *all.Total != len(want) {
		t.Fatalf("mood continuation: %d, want %d", len(got), len(want))
	}
	if w := call(t, s, "GET", "/v1/home/rows/mood:cozy", other, nil, nil); w.Code != 404 {
		t.Fatalf("another profile's row: %d", w.Code)
	}
}

// A bulk import can put hundreds of new episodes of a few shows ahead of older shows.
// The indexed preview must read another batch before choosing its continuation cursor.
func TestRecentlyAddedContinuesAfterABulkEpisodeImport(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	lib, err := s.db.CreateLibrary(ctx, "Shows", db.LibraryShows, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	batch := db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}}
	base := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	for n := range 53 {
		show := db.ItemKey{Kind: db.KeyName, Key: fmt.Sprint("show", n)}
		batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindShow, Title: fmt.Sprint("Show ", n), Keys: []db.ItemKey{show}, AddedAt: base - 100000})
		season := db.ItemKey{Kind: db.KeyName, Key: fmt.Sprint("season", n)}
		one := 1
		batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindSeason, Parent: &show, Keys: []db.ItemKey{season}, IndexNumber: &one, Title: "Season 1", AddedAt: base - 100000})
		count, added := 1, base-int64(n)*1000
		if n < 3 {
			count, added = 150, base+1000
		}
		for ep := range count {
			number := ep + 1
			batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindEpisode, Title: fmt.Sprintf("Episode %d %d", n, ep), Parent: &season, IndexNumber: &number, ParentIndex: &one, AddedAt: added, Keys: []db.ItemKey{{Kind: db.KeyName, Key: fmt.Sprintf("episode%d-%d", n, ep)}}})
		}
	}
	if _, err := s.db.Import(ctx, batch); err != nil {
		t.Fatal(err)
	}
	all := get[HomeRow](t, s, owner, "/v1/home/rows/recently_added?library="+lib.PublicID+"&limit=200")
	if len(all.Items) != 53 {
		t.Fatalf("source has %d shows", len(all.Items))
	}
	for _, start := range []string{"/v1/home", "/v1/libraries/" + lib.PublicID + "/landing"} {
		var row HomeRow
		for _, candidate := range get[Home](t, s, owner, start).Rows {
			if candidate.ID == "recently_added" {
				row = candidate
			}
		}
		if len(row.Items) != 40 || row.NextCursor == "" {
			t.Fatalf("%s stops at %d shows", start, len(row.Items))
		}
		got := []string{}
		for _, it := range row.Items {
			got = append(got, it.ID)
		}
		for cursor := row.NextCursor; cursor != ""; {
			page := get[HomeRow](t, s, owner, "/v1/home/rows/recently_added?library="+lib.PublicID+"&cursor="+url.QueryEscape(cursor))
			for _, it := range page.Items {
				got = append(got, it.ID)
			}
			cursor = page.NextCursor
		}
		want := []string{}
		for _, it := range all.Items {
			want = append(want, it.ID)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s plus continuation: %d shows, want 53 exactly once", start, len(got))
		}
	}
}
