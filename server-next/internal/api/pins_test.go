package api

import (
	"fmt"
	"strings"
	"testing"
)

// Pins are the profile's own setting, in its order and at most 200. A pin to a list another
// profile keeps to itself, or to a list since deleted, is kept as the id the profile saved: the
// settings still read, and nothing the profile reads names it.
func TestPinsNameNothingTheProfileMayNotSee(t *testing.T) {
	s, token, other := seeded(t)
	var private, mine ListCreated
	if w := call(t, s, "POST", "/v1/lists", token, ListCreate{Kind: "playlist", Name: "Not for others"}, &private); w.Code != 201 {
		t.Fatalf("making a playlist answered %d: %s", w.Code, w.Body)
	}
	if w := call(t, s, "POST", "/v1/lists", other, ListCreate{Kind: "collection", Name: "Soon gone"}, &mine); w.Code != 201 {
		t.Fatalf("making a collection answered %d: %s", w.Code, w.Body)
	}
	library := get[Libraries](t, s, other, "/v1/libraries").Items[0].ID
	pins := []string{"collection:" + mine.ID, "library:" + library, "playlist:" + private.ID}
	if code, _ := patchPreferences(t, s, other, "profile-server", map[string]any{"pins": pins}); code != 200 {
		t.Fatalf("saving pins answered %d", code)
	}
	if code := status(t, s, "DELETE", "/v1/lists/"+mine.ID, other, nil); code != 204 {
		t.Fatalf("deleting the collection answered %d", code)
	}
	w := call(t, s, "GET", "/v1/preferences?deviceClass=web", other, nil, nil)
	var read Preferences
	if err := jsonInto(w, &read); w.Code != 200 || err != nil {
		t.Fatalf("the settings after a pinned list went: %d %v", w.Code, err)
	}
	if got, _ := read.Effective["pins"].([]any); len(got) != 3 || got[0] != pins[0] || got[2] != pins[2] {
		t.Fatalf("pins read back as %v", read.Effective["pins"])
	}
	if strings.Contains(w.Body.String(), "Not for others") {
		t.Fatal("the settings name another profile's playlist")
	}
	for _, l := range get[Page[List]](t, s, other, "/v1/lists").Items {
		if l.ID == private.ID || l.ID == mine.ID {
			t.Fatalf("the lists name a pin the profile may not open: %+v", l)
		}
	}
	for _, f := range read.Registry.Fields {
		if f.Key == "pins" && f.MaxItems != 200 {
			t.Fatalf("the pins setting says it holds %d", f.MaxItems)
		}
	}

	tooMany := make([]string, 201)
	for n := range tooMany {
		tooMany[n] = fmt.Sprintf("view:lst_%d", n)
	}
	for _, bad := range [][]string{tooMany, {"album:" + library}, {"library:"}, {"library:" + library, "library:" + library}} {
		if code, _ := patchPreferences(t, s, other, "profile-server", map[string]any{"pins": bad}); code != 400 {
			t.Fatalf("pins %v answered %d", bad[:min(len(bad), 2)], code)
		}
	}
	if code, _ := patchPreferences(t, s, other, "profile-server", map[string]any{"pins": tooMany[:200]}); code != 200 {
		t.Fatalf("200 pins answered %d", code)
	}
}
