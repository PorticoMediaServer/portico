package api

import (
	"testing"
)

// Home has a row of new titles for each library, a row reads on to its end, a profile can
// reorder and hide rows but not the Continue rows, and a library has a landing page.
func TestHomeRowsLayoutAndLanding(t *testing.T) {
	s, token, limited := seeded(t)
	libraries := get[Libraries](t, s, token, "/v1/libraries").Items
	home := get[Home](t, s, token, "/v1/home")
	if len(home.Rows) != len(libraries) {
		t.Fatalf("Home has %d rows for %d libraries with nothing played", len(home.Rows), len(libraries))
	}
	films := home.Rows[0]
	if films.ID != "recently_added."+libraries[0].ID || len(films.Items) != homeRowSize || films.NextCursor == "" {
		t.Fatalf("the first row: %s, %d items, cursor %q", films.ID, len(films.Items), films.NextCursor)
	}
	// The row reads on until the library's films run out, each once.
	seen, cursor := map[string]bool{}, ""
	for {
		page := get[ItemPage](t, s, token, "/v1/home/rows/"+films.ID+"?limit=40&cursor="+cursor)
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("the row sent %q twice", item.Title)
			}
			seen[item.ID] = true
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	if len(seen) != 90 {
		t.Fatalf("the row ended after %d of 90 films", len(seen))
	}
	// A profile limited by rating reads fewer, and none it may not open.
	fewer := 0
	for cursor = ""; ; {
		page := get[ItemPage](t, s, limited, "/v1/home/rows/"+films.ID+"?limit=200&cursor="+cursor)
		for _, item := range page.Items {
			fewer++
			if code := status(t, s, "GET", "/v1/items/"+item.ID, limited, nil); code != 200 {
				t.Fatalf("the limited profile's row holds %q, which it may not open (%d)", item.Title, code)
			}
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	if fewer == 0 || fewer >= 90 {
		t.Fatalf("the limited profile's row holds %d of 90 films", fewer)
	}

	// Starting a film puts Continue Watching first, on Home and on the film library's landing.
	if code := status(t, s, "PUT", "/v1/items/"+films.Items[0].ID+"/state", token, map[string]any{"positionMs": 60_000}); code != 200 {
		t.Fatalf("saving a position answered %d", code)
	}
	if home = get[Home](t, s, token, "/v1/home"); home.Rows[0].ID != "continue" || len(home.Rows[0].Items) != 1 {
		t.Fatalf("Home after starting a film starts with %s", home.Rows[0].ID)
	}
	landing := get[Home](t, s, token, "/v1/libraries/"+libraries[0].ID+"/landing")
	if len(landing.Rows) != 2 || landing.Rows[0].ID != "continue" || landing.Rows[1].ID != films.ID || landing.Rows[1].Items[0].Kind != "movie" {
		t.Fatalf("the film library's landing: %+v", landing.Rows)
	}
	if other := get[Home](t, s, token, "/v1/libraries/"+libraries[1].ID+"/landing"); len(other.Rows) != 1 || other.Rows[0].Items[0].Kind != "show" {
		t.Fatalf("the show library's landing holds the film in progress, or no shows: %+v", other.Rows)
	}

	// The profile moves the shows row to the top and hides the films row. Hiding Continue
	// Watching is not taken. The other profile's Home is as it was.
	layout := get[HomeLayout](t, s, token, "/v1/home/layout")
	if len(layout.Rows) != 3+len(libraries) || !layout.Rows[0].AlwaysOn || layout.Rows[3].AlwaysOn {
		t.Fatalf("the default layout: %+v", layout.Rows)
	}
	shows := "recently_added." + libraries[1].ID
	edit := HomeLayout{Rows: []HomeLayoutRow{{ID: shows}, {ID: "continue", Hidden: true}, {ID: films.ID, Hidden: true}, {ID: "recently_added.lib_gone"}}}
	var saved HomeLayout
	if w := call(t, s, "PUT", "/v1/home/layout", token, edit, &saved); w.Code != 200 || saved.Rows[0].ID != shows || saved.Rows[1].Hidden ||
		!saved.Rows[2].Hidden || len(saved.Rows) != len(layout.Rows) {
		t.Fatalf("saving a layout answered %d %+v", w.Code, saved.Rows)
	}
	home = get[Home](t, s, token, "/v1/home")
	if home.Rows[0].ID != shows || home.Rows[1].ID != "continue" {
		t.Fatalf("Home after the move starts %s, %s", home.Rows[0].ID, home.Rows[1].ID)
	}
	for _, row := range home.Rows {
		if row.ID == films.ID {
			t.Fatal("a hidden row is on Home")
		}
	}
	if page := get[ItemPage](t, s, token, "/v1/home/rows/"+films.ID); len(page.Items) == 0 {
		t.Fatal("a hidden row cannot be opened by itself")
	}
	if theirs := get[Home](t, s, limited, "/v1/home"); theirs.Rows[0].ID != films.ID {
		t.Fatalf("another profile's Home changed: it starts with %s", theirs.Rows[0].ID)
	}
	// An empty layout is the default again.
	if w := call(t, s, "PUT", "/v1/home/layout", token, HomeLayout{}, &saved); w.Code != 200 || saved.Rows[0].ID != "continue" || saved.Rows[3].Hidden {
		t.Fatalf("resetting the layout answered %d %+v", w.Code, saved.Rows)
	}
	for path, want := range map[string]int{"/v1/home/rows/recently_added.lib_nothing": 404, "/v1/home/rows/" + films.ID + "?cursor=x": 400,
		"/v1/libraries/lib_nothing/landing": 404} {
		if code := status(t, s, "GET", path, token, nil); code != want {
			t.Errorf("GET %s answered %d, want %d", path, code, want)
		}
	}
}
