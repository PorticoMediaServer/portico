package api

import (
	"context"
	"net/url"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Read frames define Short reads as under three hours at the ordinary 250 words/minute.
func TestWordCountFilterSelectsShortReadsWithPaging(t *testing.T) {
	th := newTheatre(t)
	ctx := context.Background()
	lib, err := th.s.db.CreateLibrary(ctx, "Books", db.LibraryBooks, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	zero, brief, edge, threeHours := 0, 500, 44999, 45000
	var items []db.ImportItem
	for _, c := range []struct {
		name  string
		words *int
	}{{"Unknown", nil}, {"Empty", &zero}, {"Brief book", &brief}, {"Near three hours", &edge}, {"Three hours", &threeHours}} {
		items = append(items, db.ImportItem{Kind: db.KindBook, Title: c.name, WordCount: c.words, Keys: []db.ItemKey{{Kind: db.KeyName, Key: c.name}}})
	}
	if _, err := th.s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}, Items: items}); err != nil {
		t.Fatal(err)
	}
	token := th.owner.AccessToken
	library := get[Library](t, th.s, token, "/v1/libraries/"+lib.PublicID)
	supported := false
	for _, f := range library.Filters {
		if f.ID == "wordCount" && f.Type == "number" {
			supported = true
		}
	}
	if !supported {
		t.Fatal("Books does not advertise its word-count filter")
	}
	query := url.Values{"library": {lib.PublicID}, "kind": {"book"}, "publication": {"book"}, "wordCount": {"1..44999"}, "limit": {"1"}, "total": {"true"}}
	first := get[ItemPage](t, th.s, token, "/v1/items?"+query.Encode())
	if first.Total == nil || *first.Total != 2 || len(first.Items) != 1 || first.Items[0].Title != "Brief book" || first.NextCursor == "" {
		t.Fatalf("first Short reads page: %+v", first)
	}
	query.Set("cursor", first.NextCursor)
	last := get[ItemPage](t, th.s, token, "/v1/items?"+query.Encode())
	if len(last.Items) != 1 || last.Items[0].Title != "Near three hours" || last.NextCursor != "" {
		t.Fatalf("last Short reads page: %+v", last)
	}
	var structured ItemPage
	w := call(t, th.s, "POST", "/v1/items/query", token, ItemQueryRequest{Library: lib.PublicID, Kind: []string{"book"},
		Filter: []Condition{{Field: "wordCount", Values: []string{"1..44999"}}}, Total: true}, &structured)
	if w.Code != 200 || structured.Total == nil || *structured.Total != 2 || len(structured.Items) != 2 {
		t.Fatalf("structured Short reads: %d %+v", w.Code, structured)
	}
}
