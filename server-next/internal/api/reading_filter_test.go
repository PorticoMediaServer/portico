package api

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestReadingInProgressIsBetweenStartAndFinish(t *testing.T) {
	th := newTheatre(t)
	token, ctx := th.owner.AccessToken, context.Background()
	other, err := th.s.db.CreateSeedProfile(ctx, db.SeedProfile{Name: "another reader"})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []db.ItemKind{db.KindBook, db.KindComic, db.KindIssue} {
		libraryKind := db.LibraryBooks
		if kind != db.KindBook {
			libraryKind = db.LibraryComics
		}
		lib, err := th.s.db.CreateLibrary(ctx, kind.String(), libraryKind, "en", []string{t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		var items []db.ImportItem
		for _, title := range []string{"Absent", "Start", "Middle A", "Middle B", "End", "Finished", "Marked"} {
			items = append(items, db.ImportItem{Kind: kind, Title: title, Keys: []db.ItemKey{{Kind: db.KeyName, Key: title}}})
		}
		if _, err := th.s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}, Items: items}); err != nil {
			t.Fatal(err)
		}
		library := get[Library](t, th.s, token, "/v1/libraries/"+lib.PublicID)
		advertised := false
		for _, filter := range library.Filters {
			advertised = advertised || filter.ID == "inProgress" && filter.Type == "boolean"
		}
		if !advertised {
			t.Fatal("reading library does not advertise inProgress")
		}
		query := url.Values{"library": {lib.PublicID}, "kind": {kind.String()}, "total": {"true"}}
		for _, item := range get[ItemPage](t, th.s, token, "/v1/items?"+query.Encode()).Items {
			if item.Title == "Absent" {
				continue
			}
			progress := map[string]float64{"Start": 0, "Middle A": .3, "Middle B": .8, "End": 1, "Finished": .4, "Marked": .4}[item.Title]
			if item.Title == "Marked" {
				status(t, th.s, "PUT", "/v1/items/"+item.ID+"/state", token, map[string]any{"watched": true})
			}
			place := fmt.Sprintf(`{"progression":%g,"atEnd":%t}`, progress, item.Title == "Finished")
			if code := status(t, th.s, "PUT", "/v1/items/"+item.ID+"/state", token, map[string]any{"position": place}); code != 200 {
				t.Fatalf("save %s place: %d", item.Title, code)
			}
		}
		query.Set("inProgress", "true")
		query.Set("limit", "1")
		first := get[ItemPage](t, th.s, token, "/v1/items?"+query.Encode())
		if *first.Total != 2 || len(first.Items) != 1 || first.Items[0].Title != "Middle A" || first.NextCursor == "" {
			t.Fatalf("%s first progress page: %+v", kind, first)
		}
		query.Set("cursor", first.NextCursor)
		last := get[ItemPage](t, th.s, token, "/v1/items?"+query.Encode())
		if len(last.Items) != 1 || last.Items[0].Title != "Middle B" || last.NextCursor != "" {
			t.Fatalf("%s last progress page: %+v", kind, last)
		}
		query.Del("cursor")
		query.Set("inProgress", "false")
		if rest := get[ItemPage](t, th.s, token, "/v1/items?"+query.Encode()); *rest.Total != 5 {
			t.Fatalf("%s not in progress: %+v", kind, rest)
		}
		page, err := th.s.db.Items(ctx, other, db.ItemQuery{Library: lib.PublicID, Conditions: []db.Condition{{Field: "inProgress", Values: []string{"true"}}}})
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("another profile's reading place: %+v, %v", page, err)
		}
	}
}
