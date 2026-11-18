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
	whole := get[HomeRow](t, s, token, "/v1/home/rows/continue?limit=200&total=true")
	for _, row := range initial.Rows {
		if row.ID != "continue" {
			continue
		}
		if len(row.Items) != 40 || row.NextCursor == "" {
			t.Fatalf("initial Continue: %d, cursor %q", len(row.Items), row.NextCursor)
		}
		// See all carries on from Home's last card in the row's own order, and its count holds.
		rest := get[HomeRow](t, s, token, "/v1/home/rows/continue?cursor="+url.QueryEscape(row.NextCursor))
		var got, want []string
		for _, item := range append(row.Items, rest.Items...) {
			got = append(got, item.ID)
		}
		for _, item := range whole.Items {
			want = append(want, item.ID)
		}
		if !slices.Equal(got, want) || whole.Total == nil || *whole.Total != len(want) {
			t.Fatalf("Continue from Home %v, whole %v (count %v)", got, want, whole.Total)
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
			batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindMovie, Title: name,
				AddedAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), ContentRating: "G",
				Keys: []db.ItemKey{{Kind: db.KeyName, Key: name}}})
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

// A mood preview and its continuation are the same complete kept choice. Already liked
// titles stay out, and sorting or filtering See all never widens that choice to the library.
func TestMoodRowContinuesItsKeptChoiceFromHome(t *testing.T) {
	s, token, other := seeded(t)
	library := get[Libraries](t, s, token, "/v1/libraries").Items[0].ID
	films := get[ItemPage](t, s, token, "/v1/items?library="+library+"&kind=movie&limit=100").Items
	f := titledatatest.File{Vocabulary: []titledatatest.Tag{{ID: 1, Key: "mood:cozy", RowLabel: "cozy stories"},
		{ID: 2, Key: "mood:warm", RowLabel: "warm stories"}}}
	liked := map[string]bool{}
	for n, it := range films {
		external := get[Item](t, s, token, "/v1/items/"+it.ID).ExternalIDs["tmdb"]
		f.Works = append(f.Works, titledatatest.Work{QID: int64(100 + n), IDs: map[int]string{titledatatest.TMDBMovie: external}, Tags: map[int]int{1: 3, 2: 3}})
		if n == 0 {
			for similar := range 13 {
				f.Works[0].Similar = append(f.Works[0].Similar, int64(101+similar))
			}
		}
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
	// The title page's related row continues by its own signal rank, not a library sort.
	initial := get[Related](t, s, token, "/v1/items/"+films[0].ID+"/related?limit=2")
	var like RelatedGroup
	for _, group := range initial.Groups {
		if group.Relation == "more_like_this" {
			like = group
		}
	}
	if len(like.Items) != 2 || like.NextCursor == "" {
		t.Fatalf("related preview: %+v", like)
	}
	var relatedIDs []string
	for {
		for _, it := range like.Items {
			relatedIDs = append(relatedIDs, it.ID)
		}
		if like.NextCursor == "" {
			break
		}
		page := get[Related](t, s, token, "/v1/items/"+films[0].ID+"/related?relation=more_like_this&limit=2&cursor="+url.QueryEscape(like.NextCursor))
		if len(page.Groups) != 1 {
			t.Fatalf("continued unrelated groups: %+v", page)
		}
		like = page.Groups[0]
	}
	var ranked []string
	for _, it := range films[1:14] {
		ranked = append(ranked, it.ID)
	}
	if !slices.Equal(relatedIDs, ranked) {
		t.Fatalf("related row: %v, want %v", relatedIDs, ranked)
	}

	get[Home](t, s, token, "/v1/home") // asks for the profile's kept choice
	if err := s.db.RefreshMoodRows(ctx); err != nil {
		t.Fatal(err)
	}
	var preview, overlapping HomeRow
	for _, row := range get[Home](t, s, token, "/v1/home").Rows {
		if row.ID == "mood:warm" {
			overlapping = row
		}
		if row.ID == "mood:cozy" {
			preview = row
		}
	}
	if len(preview.Items) != 40 || preview.NextCursor == "" {
		t.Fatalf("mood preview: %d, cursor %q", len(preview.Items), preview.NextCursor)
	}
	// Overlapping tags keep later Home rows: only the first row's 80 preview candidates
	// are excluded, not its entire deeper ranking. Deep See all rows may overlap.
	if len(overlapping.Items) != len(films)-len(liked)-80 {
		t.Fatalf("the overlapping mood row lost its remaining titles: %d", len(overlapping.Items))
	}
	for _, a := range preview.Items {
		for _, b := range overlapping.Items {
			if a.ID == b.ID {
				t.Fatal("Home previews repeat a title across mood rows")
			}
		}
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
	if len(want) != len(films)-len(liked) || !slices.Equal(got, want) || all.Total == nil || *all.Total != len(want) {
		t.Fatalf("mood continuation: %d, want %d", len(got), len(want))
	}
	// Removing earlier cards from the visible set must not renumber the kept cursor or skip
	// later cards. Nor may a newly liked title return on a later page.
	call(t, s, "PUT", "/v1/items/"+preview.Items[0].ID+"/state", token, map[string]any{"favorite": true}, nil)
	call(t, s, "PUT", "/v1/items/"+all.Items[50].ID+"/state", token, map[string]any{"favorite": true}, nil)
	continued := get[HomeRow](t, s, token, "/v1/home/rows/mood:cozy?limit=200&cursor="+url.QueryEscape(preview.NextCursor))
	tail := append(slices.Clone(want[40:50]), want[51:]...)
	var remaining []string
	for _, it := range continued.Items {
		remaining = append(remaining, it.ID)
	}
	if !slices.Equal(remaining, tail) || continued.NextCursor != "" {
		t.Fatalf("filtered continuation: %v, want %v", remaining, tail)
	}
	// The film library's Discover page has the same mood row, read for that library, and it
	// continues there as on Home.
	var discover HomeRow
	for _, row := range get[Home](t, s, token, "/v1/libraries/"+library+"/landing").Rows {
		if row.ID == "mood:cozy" {
			discover = row
		}
	}
	if len(discover.Items) != 40 || discover.Title == "" || discover.NextCursor == "" {
		t.Fatalf("the library's Discover page has no cozy row: %+v", discover)
	}
	if page := get[HomeRow](t, s, token, "/v1/home/rows/mood:cozy?library="+library+"&cursor="+url.QueryEscape(discover.NextCursor)); len(page.Items) == 0 {
		t.Fatal("the Discover mood row does not continue")
	}
	// A changed ranking expires the old cursor instead of silently moving cards between pages.
	if err := s.db.RefreshMoodRows(ctx); err != nil {
		t.Fatal(err)
	}
	get[Home](t, s, token, "/v1/home") // asks for the now-stale choice to be remade
	if err := s.db.RefreshMoodRows(ctx); err != nil {
		t.Fatal(err)
	}
	response := call(t, s, "GET", "/v1/home/rows/mood:cozy?cursor="+url.QueryEscape(preview.NextCursor), token, nil, nil)
	var expired errorBody
	if err := jsonInto(response, &expired); err != nil || response.Code != 400 || expired.Error.Code != "cursor_expired" {
		t.Fatalf("changed choice accepted its old cursor: %d %s", response.Code, response.Body)
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
			batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindEpisode, Title: fmt.Sprintf("Episode %d %d", n, ep),
				Parent: &season, IndexNumber: &number, ParentIndex: &one, AddedAt: added,
				Keys: []db.ItemKey{{Kind: db.KeyName, Key: fmt.Sprintf("episode%d-%d", n, ep)}}})
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
