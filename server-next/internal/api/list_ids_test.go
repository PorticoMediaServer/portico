package api

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// A pin's name is looked up with the other pinned ids, not by walking a directory or
// reading each list separately. Every kind keeps the ordinary list visibility rules.
func TestListIDsReadOnlyTheRequestedVisibleLists(t *testing.T) {
	th := newTheatre(t)
	owner := th.owner.AccessToken
	addMember(t, th.s, owner, "reader", "member")
	reader := signInAs(t, th.s, "reader", testPassword, 7).AccessToken
	makeList := func(token string, req ListCreate) ListCreated {
		t.Helper()
		var made ListCreated
		if w := call(t, th.s, "POST", "/v1/lists", token, req, &made); w.Code != 201 {
			t.Fatal(w.Code, w.Body)
		}
		return made
	}
	playlist := makeList(owner, ListCreate{Kind: "playlist", Name: "Road"})
	collection := makeList(owner, ListCreate{Kind: "collection", Name: "Films"})
	view := makeList(owner, ListCreate{Kind: "saved_view", Name: "Long films", Library: th.libs["movies"].PublicID, Query: "kind=movie"})
	hidden := makeList(reader, ListCreate{Kind: "playlist", Name: "Private"})
	makeList(owner, ListCreate{Kind: "collection", Name: "Not requested"})
	ids := []string{view.ID, hidden.ID, playlist.ID, "unknown", collection.ID, playlist.ID}
	path := "/v1/lists?ids=" + url.QueryEscape(strings.Join(ids, ","))
	page := get[Page[List]](t, th.s, owner, path)
	got := []string{}
	for _, l := range page.Items {
		got = append(got, l.ID)
	}
	want := []string{playlist.ID, collection.ID, view.ID}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) || page.NextCursor != "" {
		t.Fatalf("lookup: %v, next %q", got, page.NextCursor)
	}
	if got := get[Page[List]](t, th.s, owner, path+"&kind=collection"); len(got.Items) != 1 || got.Items[0].ID != collection.ID {
		t.Fatalf("kind filter: %+v", got)
	}
	if got := get[Page[List]](t, th.s, reader, path); len(got.Items) != 1 || got.Items[0].ID != hidden.ID {
		t.Fatalf("private lists leaked: %+v", got)
	}
	if got := get[Page[List]](t, th.s, owner, "/v1/lists?ids=unknown"); len(got.Items) != 0 || got.NextCursor != "" {
		t.Fatalf("unknown id: %+v", got)
	}
	// A bounded id lookup defaults to the whole batch, not the ordinary first 40 lists.
	ids = []string{playlist.ID, collection.ID, view.ID}
	for n := 0; n < 42; n++ {
		ids = append(ids, makeList(owner, ListCreate{Kind: "playlist", Name: fmt.Sprintf("Pinned %02d", n)}).ID)
	}
	for len(ids) < 200 {
		ids = append(ids, "unknown")
	}
	page = get[Page[List]](t, th.s, owner, "/v1/lists?ids="+url.QueryEscape(strings.Join(ids, ",")))
	if len(page.Items) != 45 || page.NextCursor != "" {
		t.Fatalf("bounded batch was truncated: %d, %q", len(page.Items), page.NextCursor)
	}
	for _, bad := range []string{"", ",", strings.Repeat("x,", 200) + "x", strings.Repeat("x", 129)} {
		if code := status(t, th.s, "GET", "/v1/lists?ids="+url.QueryEscape(bad), owner, nil); code != 400 {
			t.Fatalf("invalid ids answered %d", code)
		}
	}
}
