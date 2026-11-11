package api

import (
	"slices"
	"testing"
)

// Customize Home, saved as the profile's settings, changes the order and the rows of that
// profile's Home only, and Continue Watching cannot be hidden.
func TestHomeFollowsTheSavedLayout(t *testing.T) {
	s, token, limited := seeded(t)
	films := get[Libraries](t, s, token, "/v1/libraries").Items[0].ID
	for _, who := range []string{token, limited} {
		film := get[ItemPage](t, s, who, "/v1/items?library="+films+"&kind=movie&limit=1").Items[0]
		status(t, s, "PUT", "/v1/items/"+film.ID+"/state", who, map[string]any{"positionMs": 60_000})
	}
	rows := func(who string) []string {
		t.Helper()
		var ids []string
		for _, row := range get[Home](t, s, who, "/v1/home").Rows {
			ids = append(ids, row.ID)
		}
		return ids
	}
	save := func(values map[string]any) {
		t.Helper()
		body := PreferencesEdit{Scope: "profile-server", DeviceClass: "web", Values: values}
		if w := call(t, s, "PATCH", "/v1/preferences", token, body, nil); w.Code != 200 {
			t.Fatalf("saving the layout answered %d: %s", w.Code, w.Body)
		}
	}
	usual := []string{"continue", "recently_added"}
	if got := rows(token); !slices.Equal(got, usual) {
		t.Fatalf("Home before any layout: %v", got)
	}
	save(map[string]any{"home.rowOrder": []string{"recently_added", "row_gone"}})
	if got := rows(token); !slices.Equal(got, []string{"recently_added", "continue"}) {
		t.Fatalf("Home after moving a row first: %v", got)
	}
	save(map[string]any{"home.hiddenRowIds": []string{"recently_added", "continue"}})
	if got := rows(token); !slices.Equal(got, []string{"continue"}) {
		t.Fatalf("Home after hiding both rows: %v", got)
	}
	// The Customize Home editor asks for the hidden rows too, by name, in their place, without
	// items; the always-on row it could not hide is sent as usual.
	editor := get[Home](t, s, token, "/v1/home?hidden=include").Rows
	if len(editor) != 2 || editor[0].ID != "recently_added" || !editor[0].Hidden || editor[0].Title != "Recently Added" ||
		len(editor[0].Items) != 0 || editor[1].ID != "continue" || editor[1].Hidden || len(editor[1].Items) == 0 {
		t.Fatalf("Home for the editor: %+v", editor)
	}
	if code := status(t, s, "GET", "/v1/home?hidden=yes", token, nil); code != 400 {
		t.Fatalf("an unknown hidden value answered %d", code)
	}
	if got := rows(limited); !slices.Equal(got, usual) {
		t.Fatalf("another profile's Home changed: %v", got)
	}
	// Reset: both settings taken away.
	save(map[string]any{"home.rowOrder": nil, "home.hiddenRowIds": nil})
	if got := rows(token); !slices.Equal(got, usual) {
		t.Fatalf("Home after reset: %v", got)
	}
}
