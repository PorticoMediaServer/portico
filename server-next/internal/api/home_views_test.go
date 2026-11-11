package api

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Saved views put on Home are rows in the profile's order. Home reads the titles of the first
// few and sends the rest deferred, with what the app needs to read them; a view the profile
// may not read, or one deleted, is no row and no name; an empty one is left out; a 21st is
// refused.
func TestSavedViewsOnHome(t *testing.T) {
	s, token, other := seeded(t)
	films := get[Libraries](t, s, token, "/v1/libraries").Items[0].ID
	view := func(who, name, query string) string {
		t.Helper()
		var made ListCreated
		if w := call(t, s, "POST", "/v1/lists", who, ListCreate{Kind: "saved_view", Name: name, Library: films, Query: query}, &made); w.Code != 201 {
			t.Fatalf("saving a view answered %d: %s", w.Code, w.Body)
		}
		return made.ID
	}
	save := func(values map[string]any) int {
		t.Helper()
		code, _ := patchPreferences(t, s, token, "profile-server", values)
		return code
	}
	empty := view(token, "My favourites", "kind=movie&favorite=true")
	gone := view(token, "Soon deleted", "kind=movie&sort=year")
	theirs := view(other, "Not yours", "kind=movie&sort=added")
	// A genre since gone is left out of the row, as when the view opens.
	dropped := view(token, "Gone genre", "kind=movie&genre=NoSuchGenre&sort=title")
	var named []string
	for n := range 5 {
		named = append(named, view(token, fmt.Sprint("View ", n), fmt.Sprintf("kind=movie&sort=added&limit=%d", n+1)))
	}
	ids := append([]string{empty, gone, theirs, dropped}, named...)
	if code := status(t, s, "DELETE", "/v1/lists/"+gone, token, nil); code != 204 {
		t.Fatalf("deleting a view answered %d", code)
	}
	if code := save(map[string]any{"home.savedViews": ids, "home.rowOrder": []string{"view:" + named[0]}}); code != 200 {
		t.Fatalf("putting views on Home answered %d", code)
	}
	w := call(t, s, "GET", "/v1/home", token, nil, nil)
	if strings.Contains(w.Body.String(), "Not yours") || strings.Contains(w.Body.String(), "Soon deleted") {
		t.Fatal("Home names a view the profile may not read, or one deleted")
	}
	var home Home
	if err := jsonInto(w, &home); err != nil || w.Code != 200 {
		t.Fatalf("Home answered %d: %v", w.Code, err)
	}
	var got []string
	for _, row := range home.Rows {
		got = append(got, row.ID)
	}
	// View 0 first, as ordered; then the usual rows; then the others in the order put on Home.
	// The first four view rows are read: View 0, the empty one (left out), Gone genre and
	// View 1. The rest are deferred.
	want := []string{"view:" + named[0], "recently_added", "view:" + dropped, "view:" + named[1], "view:" + named[2], "view:" + named[3], "view:" + named[4]}
	if !slices.Equal(got, want) {
		t.Fatalf("Home rows %v, want %v", got, want)
	}
	for n, row := range home.Rows {
		filled := n < 4
		if !strings.HasPrefix(row.ID, "view:") {
			continue
		}
		if row.Library != films || row.Query == "" || row.Deferred == filled || (len(row.Items) > 0) != filled || filled && len(row.Items) > viewRowLength {
			t.Fatalf("row %d %s: library %q query %q deferred %v, %d items", n, row.ID, row.Library, row.Query, row.Deferred, len(row.Items))
		}
	}
	if home.Rows[0].Title != "View 0" || strings.Contains(home.Rows[0].Query, "limit") {
		t.Fatalf("the first view row: %+v", home.Rows[0])
	}

	// Hidden like any row: the editor names it without items.
	if code := save(map[string]any{"home.hiddenRowIds": []string{"view:" + named[0]}}); code != 200 {
		t.Fatalf("hiding a view row answered %d", code)
	}
	editor := get[Home](t, s, token, "/v1/home?hidden=include").Rows
	if editor[0].ID != "view:"+named[0] || !editor[0].Hidden || editor[0].Deferred || len(editor[0].Items) != 0 {
		t.Fatalf("the hidden view row for the editor: %+v", editor[0])
	}
	if rows := get[Home](t, s, other, "/v1/home").Rows; len(rows) != 1 {
		t.Fatalf("another profile's Home has %d rows", len(rows))
	}

	// Out of time, the view rows are sent deferred and Home still answers.
	if code := save(map[string]any{"home.hiddenRowIds": nil}); code != 200 {
		t.Fatalf("showing the row again answered %d", code)
	}
	budget := viewRowsBudget
	viewRowsBudget = 0
	late := get[Home](t, s, token, "/v1/home").Rows
	viewRowsBudget = budget
	for _, row := range late {
		if strings.HasPrefix(row.ID, "view:") && (!row.Deferred || len(row.Items) > 0) {
			t.Fatalf("a view row read with no time left: %+v", row)
		}
	}
	if len(late) != 8 { // the empty view is not read, so it is sent deferred too
		t.Fatalf("Home out of time has %d rows", len(late))
	}

	tooMany := make([]string, 21)
	for n := range tooMany {
		tooMany[n] = fmt.Sprint("lst_", n)
	}
	if code := save(map[string]any{"home.savedViews": tooMany}); code != 400 {
		t.Fatalf("21 saved views on Home answered %d", code)
	}
}
