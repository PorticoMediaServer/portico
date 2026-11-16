package api

import (
	"context"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/titledata/titledatatest"
	"net/url"
	"path/filepath"
	"slices"
	"testing"
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
	if w := call(t, s, "GET", "/v1/items/hidden/related/more-like-this", limited, nil, nil); w.Code != 404 {
		t.Fatalf("hidden related origin: %d", w.Code)
	}
}

// A player reads a similar-title row in rank order to its actual end, with hidden results
// absent from both pages and counts. The fixture is a local title-data file, never a provider.
func TestMoreLikeThisPagesVisibleResults(t *testing.T) {
	s, token, limited := seeded(t)
	library := get[Libraries](t, s, token, "/v1/libraries").Items[0].ID
	visible := get[ItemPage](t, s, limited, "/v1/items?library="+library+"&kind=movie&limit=1").Items[0]
	films := get[ItemPage](t, s, token, "/v1/items?library="+library+"&kind=movie&limit=50").Items
	works := []titledatatest.Work{{QID: 100, IDs: map[int]string{titledatatest.TMDBMovie: get[Item](t, s, token, "/v1/items/"+visible.ID).ExternalIDs["tmdb"]}}}
	for _, film := range films {
		if film.ID == visible.ID {
			continue
		}
		qid := int64(100 + len(works))
		works[0].Similar = append(works[0].Similar, qid)
		works = append(works, titledatatest.Work{QID: qid, IDs: map[int]string{titledatatest.TMDBMovie: get[Item](t, s, token, "/v1/items/"+film.ID).ExternalIDs["tmdb"]}})
		if len(works) == 16 {
			break
		}
	}
	path := filepath.Join(t.TempDir(), "titles.sqlite")
	titledatatest.Write(t, path, titledatatest.File{Works: works})
	if err := db.PrepareTitleData(context.Background(), path, "screen", "2026.45"); err != nil {
		t.Fatal(err)
	}
	if err := s.db.UseTitleData(path); err != nil {
		t.Fatal(err)
	}
	for _, who := range []string{token, limited} {
		full := get[ItemPage](t, s, who, "/v1/items/"+visible.ID+"/related/more-like-this?limit=200")
		got := []string{}
		for cursor := ""; ; {
			page := get[ItemPage](t, s, who, "/v1/items/"+visible.ID+"/related/more-like-this?limit=3&cursor="+cursor)
			if page.Total == nil || *page.Total != len(full.Items) {
				t.Fatalf("total: %v, want %d", page.Total, len(full.Items))
			}
			for _, it := range page.Items {
				got = append(got, it.ID)
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		want := []string{}
		for _, it := range full.Items {
			want = append(want, it.ID)
		}
		if len(want) == 0 || !slices.Equal(got, want) {
			t.Fatalf("similar titles: %v, want %v", got, want)
		}
	}
}
