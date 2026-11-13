package api

import (
	"context"
	"testing"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestHomeSpotlightIsVisiblePlayableAndPersonal(t *testing.T) {
	s, token, limited := seeded(t)
	read := func(who string) []HomeSpotlight { return get[Home](t, s, who, "/v1/home").Spotlight }
	first := read(token)
	if len(first) != 5 {
		t.Fatalf("spotlight has %d picks", len(first))
	}
	for _, who := range []string{token, limited} {
		seen := map[string]bool{}
		for _, slide := range read(who) {
			it := slide.Entry
			if seen[it.ID] || (it.Kind != "movie" && it.Kind != "show") || it.Available == nil || !*it.Available || !slide.Play || !slide.MyList {
				t.Fatalf("not a distinct playable film/show: %+v", slide)
			}
			seen[it.ID] = true
			detail := get[Item](t, s, who, "/v1/items/"+it.ID)
			if it.Summary == "" || utf8.RuneCountInString(it.Summary) > 300 || len(it.Tags) == 0 || it.Year != detail.Year || len(it.Versions) != 0 {
				t.Fatalf("missing or unbounded hero facts: %+v", it)
			}
			if slide.Reason.Kind != "recent" || slide.Reason.Text.Fallback != "Recently added" {
				t.Fatalf("untruthful reason: %+v", slide.Reason)
			}
		}
	}
	skipped := first[0].Entry.ID
	beforeOther := read(limited)
	if code := status(t, s, "PUT", "/v1/items/"+skipped+"/state", token, map[string]any{"notInterested": true}); code != 200 {
		t.Fatal(code)
	}
	after := read(token)
	if len(after) != 5 {
		t.Fatalf("not filled after Not interested: %d", len(after))
	}
	for _, slide := range after {
		if slide.Entry.ID == skipped {
			t.Fatal("Not interested was featured")
		}
	}
	other := read(limited)
	for n := range other {
		if other[n].Entry.ID != beforeOther[n].Entry.ID {
			t.Fatal("another profile's spotlight changed")
		}
	}
	// A missing film stays in the library but is not featured. The remaining shows have
	// real, visible regular-episode targets, including the person's current resume place.
	libraries, err := s.db.AllLibraries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gone := func(kind string) {
		t.Helper()
		for _, lib := range libraries {
			if lib.Kind != kind {
				continue
			}
			root, err := s.db.Folder(context.Background(), lib.Paths[0].ID, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, folder := range root.Folders {
				if err := s.db.MarkFolderGone(context.Background(), lib.ID, lib.Paths[0].ID, folder); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	gone(db.LibraryMovies)
	shows := read(token)
	if len(shows) == 0 {
		t.Fatal("no shows featured when films are missing")
	}
	for _, slide := range shows {
		target := slide.ResumeTarget
		if slide.Entry.Kind != "show" || target == nil || target.Kind != "episode" || target.RootID != slide.Entry.ID || target.ParentIndex == nil || *target.ParentIndex == 0 {
			t.Fatalf("show has no regular, visible episode target: %+v", slide)
		}
		get[Item](t, s, token, "/v1/items/"+target.ID)
	}
	target := shows[0].ResumeTarget.ID
	status(t, s, "PUT", "/v1/items/"+target+"/state", token, map[string]any{"positionMs": 60_000})
	found := false
	for _, slide := range read(token) {
		if slide.Entry.ID != shows[0].Entry.ID {
			continue
		}
		found = true
		if slide.ResumeTarget == nil || slide.ResumeTarget.ID != target || slide.ResumeTarget.State.PositionMs != 60_000 {
			t.Fatal("show lost its resume target")
		}
	}
	if !found {
		t.Fatal("the resumed show disappeared")
	}
	gone(db.LibraryShows)
	if got := read(token); len(got) != 0 {
		t.Fatalf("missing video replaced with non-video: %+v", got)
	}
}
