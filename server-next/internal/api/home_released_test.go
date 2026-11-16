package api

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestRecentlyReleasedPagesVisibleTitlesAndFollowsLayout(t *testing.T) {
	s, _, members := seededWith(t, 600, 4) // unrestricted, Tag, library and rating limits
	today := time.Now().UTC().Format(time.DateOnly)
	library := get[Libraries](t, s, members[0], "/v1/libraries").Items[0].ID
	libs, err := s.db.AllLibraries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lib := libs[0]
	// Ties straddle the preview's boundary. Future and undated titles must never enter the row.
	batch := db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 2, Dirs: []db.ImportDir{{}}}
	for n := range 55 {
		name := fmt.Sprint("Released ", n)
		date := today
		if n == 0 {
			date = "2999-01-01"
		}
		if n == 1 {
			date = ""
		}
		batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindMovie, Title: name, ReleaseDate: date, ContentRating: "G",
			Keys: []db.ItemKey{{Kind: db.KeyName, Key: name}}})
	}
	if _, err := s.db.Import(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	ids := func(items []Item) []string {
		out := []string{}
		for _, it := range items {
			out = append(out, it.ID)
		}
		return out
	}
	for _, who := range members {
		want := get[ItemPage](t, s, who, "/v1/items?library="+library+"&kind=movie&sort=release&released=.."+today+"&limit=200&total=true")
		var got []Item
		path := "/v1/home/rows/recently_released?library=" + library + "&kind=movie&limit=7&total=true"
		for cursor := ""; ; {
			page := get[HomeRow](t, s, who, path+"&cursor="+url.QueryEscape(cursor))
			if page.Title != "Recently Released" || page.Total == nil || *page.Total != *want.Total {
				t.Fatalf("row header/count: %+v", page)
			}
			got = append(got, page.Items...)
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
			if len(got) > 200 {
				t.Fatal("row did not reach its end")
			}
		}
		if !slices.Equal(ids(got), ids(want.Items)) {
			t.Fatalf("visible release order differs: %d, want %d", len(got), len(want.Items))
		}
		// A first page from Home carries exactly the cursor for the complete row's ranking.
		var preview HomeRow
		for _, row := range get[Home](t, s, who, "/v1/home").Rows {
			if row.ID == "recently_released" {
				preview = row
			}
		}
		if len(preview.Items) != 40 || preview.NextCursor == "" {
			t.Fatalf("preview has %d titles, cursor %q", len(preview.Items), preview.NextCursor)
		}
		all := get[HomeRow](t, s, who, "/v1/home/rows/recently_released?limit=200")
		rest := get[HomeRow](t, s, who, "/v1/home/rows/recently_released?limit=200&cursor="+url.QueryEscape(preview.NextCursor))
		if !slices.Equal(ids(append(preview.Items, rest.Items...)), ids(all.Items)) {
			t.Fatal("Home continuation changed order or lost titles")
		}
		for _, it := range all.Items {
			if it.ReleaseDate == "" || it.ReleaseDate > today {
				t.Fatalf("unreleased title: %+v", it)
			}
		}
	}
	who := members[0]
	// The same row exists on its library landing page, in that library alone.
	landing := get[Home](t, s, who, "/v1/libraries/"+library+"/landing")
	found := false
	for _, row := range landing.Rows {
		if row.ID == "recently_released" {
			found = true
			for _, it := range row.Items {
				if it.LibraryID != library {
					t.Fatal("landing crossed libraries")
				}
			}
		}
	}
	if !found {
		t.Fatal("landing has no Recently Released row")
	}
	edit := PreferencesEdit{Scope: "profile-server", DeviceClass: "web", Values: map[string]any{
		"home.rowOrder": []string{"recently_released"}, "home.hiddenRowIds": []string{"recently_released"}}}
	if status(t, s, "PATCH", "/v1/preferences", who, edit) != 200 {
		t.Fatal("save layout")
	}
	for _, row := range get[Home](t, s, who, "/v1/home").Rows {
		if row.ID == "recently_released" {
			t.Fatal("hidden row shown")
		}
	}
	editor := get[Home](t, s, who, "/v1/home?hidden=include").Rows
	if len(editor) == 0 || editor[0].ID != "recently_released" || !editor[0].Hidden || len(editor[0].Items) != 0 {
		t.Fatal("hidden row missing from editor or order lost")
	}
	if status(t, s, "GET", "/v1/home/rows/recently_released?library=missing", who, nil) != 404 {
		t.Fatal("missing library accepted")
	}
}
