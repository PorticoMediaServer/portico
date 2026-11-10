package api

import (
	"slices"
	"testing"
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
