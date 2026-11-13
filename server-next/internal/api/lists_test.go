package api

import (
	"context"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A saved view keeps a library's sort and filters and opens with them. A filter naming
// something that no longer exists is left out when it opens, and the view says so.
func TestSavedViewOpensWithWhatStillExists(t *testing.T) {
	th := newTheatre(t)
	films := th.libs["movies"].PublicID
	var made ListCreated
	w := th.queue("POST", "/v1/lists", "", ListCreate{Kind: "saved_view", Name: "Long films", Library: films,
		Query: "kind=movie&sort=year&order=desc&genre=Gone&cursor=abc&limit=3"}, &made)
	if w.Code != 201 || made.Library != films || made.Query != "genre=Gone&kind=movie&order=desc&sort=year" {
		t.Fatalf("saving a view answered %d: %s", w.Code, w.Body)
	}
	var opened List
	th.queue("GET", "/v1/lists/"+made.ID, "", nil, &opened)
	if opened.Query != "kind=movie&order=desc&sort=year" || !slices.Equal(opened.Dropped, []string{"genre"}) {
		t.Fatalf("the view opens as %q, dropping %v", opened.Query, opened.Dropped)
	}
	// Every repeated and comma-separated value is checked; surviving values keep their mode.
	var mixed ListCreated
	query := "kind=movie&collection=any:" + th.film + ",itm_gone&collection=itm_gone"
	th.queue("POST", "/v1/lists", "", ListCreate{Kind: "saved_view", Name: "Some titles", Library: films, Query: query}, &mixed)
	th.queue("GET", "/v1/lists/"+mixed.ID, "", nil, &opened)
	if opened.Query != "collection=any%3A"+th.film+"&kind=movie" || !slices.Equal(opened.Dropped, []string{"collection"}) {
		t.Fatalf("surviving filter values: %q, dropping %v", opened.Query, opened.Dropped)
	}
	th.queue("GET", "/v1/lists/"+made.ID, "", nil, &opened)

	var page ItemPage
	if w := th.queue("GET", "/v1/items?library="+films+"&"+opened.Query, "", nil, &page); w.Code != 200 || len(page.Items) == 0 {
		t.Fatalf("the view's list answered %d: %s", w.Code, w.Body)
	}
	if w := th.queue("POST", "/v1/lists", "", ListCreate{Kind: "saved_view", Name: "Bad", Library: films, Query: "watched=perhaps"}, nil); w.Code != 400 {
		t.Fatalf("a view with a filter the list refuses answered %d", w.Code)
	}
	if w := th.queue("POST", "/v1/lists", "", ListCreate{Kind: "saved_view", Name: "Nowhere", Library: "lib_none"}, nil); w.Code != 404 {
		t.Fatalf("a view of no library answered %d", w.Code)
	}
	if w := th.queue("POST", "/v1/lists/"+made.ID+"/entries", "", ListAdd{Items: []string{th.film}}, nil); w.Code != 400 {
		t.Fatalf("adding to a saved view answered %d", w.Code)
	}
}

// Deleting a title removes every occurrence from a playlist; its summary and items must agree.
func TestListCountAfterTitleDeletion(t *testing.T) {
	th := newTheatre(t)
	var made ListCreated
	if w := th.queue("POST", "/v1/lists", "", ListCreate{Kind: "playlist", Name: "Films", Items: []string{th.film, th.eps[0], th.film}}, &made); w.Code != 201 {
		t.Fatalf("creating the list: %d %s", w.Code, w.Body)
	}
	var summary List
	if w := th.queue("GET", "/v1/lists/"+made.ID, "", nil, &summary); w.Code != 200 || summary.Count != 3 {
		t.Fatalf("before deletion: %d %s", w.Code, w.Body)
	}
	// Complete the same database deletion used after the files have been removed from disk.
	ctx := context.Background()
	del, err := th.s.db.ItemDeletion(ctx, db.Viewer{UserID: 1, ProfileID: 1}, th.film)
	if err != nil {
		t.Fatal(err)
	}
	var files []int64
	for _, file := range del.Files {
		files = append(files, file.ID)
	}
	if err := th.s.db.Deleted(ctx, del, files); err != nil {
		t.Fatal(err)
	}
	var page ItemPage
	if w := th.queue("GET", "/v1/items?list="+made.ID+"&total=true", "", nil, &page); w.Code != 200 || len(page.Items) != 1 || page.Items[0].ID != th.eps[0] {
		t.Fatalf("remaining items: %d %s", w.Code, w.Body)
	}
	if w := th.queue("GET", "/v1/lists/"+made.ID, "", nil, &summary); w.Code != 200 || summary.Count != len(page.Items) {
		t.Fatalf("after deletion: summary count %d, items %d: %d %s", summary.Count, len(page.Items), w.Code, w.Body)
	}
}
