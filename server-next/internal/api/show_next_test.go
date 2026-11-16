package api

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A show's own state selects its continuation even after sixty other episodes were played.
// The answer carries the season and episode numbers, and follows in-progress/visibility rules.
func TestShowDetailNamesItsNextEpisodeBeyondRecentHistory(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	lib, err := s.db.CreateLibrary(ctx, "Shows", db.LibraryShows, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	batch := db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}}
	target := db.ItemKey{Kind: db.KeyName, Key: "target"}
	season := db.ItemKey{Kind: db.KeyName, Key: "target-season7"}
	seven := 7
	zero := 0
	batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindShow, Title: "Target", Keys: []db.ItemKey{target}, ContentRating: "G", RatingAge: &zero},
		db.ImportItem{Kind: db.KindSeason, Title: "Season 7", Parent: &target, Keys: []db.ItemKey{season}, IndexNumber: &seven})
	other := db.ItemKey{Kind: db.KeyName, Key: "other"}
	otherSeason := db.ItemKey{Kind: db.KeyName, Key: "other-season"}
	one := 1
	batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindShow, Title: "Other", Keys: []db.ItemKey{other}},
		db.ImportItem{Kind: db.KindSeason, Title: "Season 1", Parent: &other, Keys: []db.ItemKey{otherSeason}, IndexNumber: &one})
	add := func(parent db.ItemKey, season, episode int, title string, rating int) {
		file := db.FileRef{Name: title + ".mkv"}
		contentRating := "G"
		if rating > 0 {
			contentRating = "R"
		}
		batch.Files = append(batch.Files, db.ImportFile{FileRef: file, Role: db.FileMedia, Size: 1, DurationMS: 120000, Streams: []db.Stream{{Kind: db.StreamVideo, Codec: "h264", Width: 320, Height: 180}}})
		batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindEpisode, Title: title, Parent: &parent, ParentIndex: &season, IndexNumber: &episode,
			ContentRating: contentRating, RatingAge: &rating, Keys: []db.ItemKey{{Kind: db.KeyName, Key: title}},
			Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: file}}}}})
	}
	add(season, 7, 24, "Target 24", 0)
	add(season, 7, 25, "Target 25", 17)
	for n := range 60 {
		add(otherSeason, 1, n+1, fmt.Sprint("Other ", n), 0)
	}
	if _, err := s.db.Import(ctx, batch); err != nil {
		t.Fatal(err)
	}
	list := get[ItemPage](t, s, owner, "/v1/items?library="+lib.PublicID+"&kind=episode&limit=100")
	var showID, finished, next string
	v := db.Viewer{UserID: 1, ProfileID: 1}
	yes := true
	at := int64(60000)
	for _, it := range list.Items {
		ago := time.Duration(0)
		if it.Title == "Target 24" {
			finished, showID = it.ID, it.RootID
			ago = 48 * time.Hour
		}
		if it.Title == "Target 25" {
			next = it.ID
			continue
		}
		if _, _, err := s.db.SetState(ctx, v, it.ID, db.StateEdit{PositionMS: &at, Ago: ago}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.db.SetState(ctx, v, it.ID, db.StateEdit{Watched: &yes, Ago: ago}); err != nil {
			t.Fatal(err)
		}
	}
	read := func() *Item { t.Helper(); return get[Item](t, s, owner, "/v1/items/"+showID).NextEpisode }
	got := read()
	if got == nil || got.ID != next || got.ParentID == "" || got.ParentIndex == nil || *got.ParentIndex != 7 || got.Index == nil || *got.Index != 25 {
		t.Fatalf("next episode: %+v", got)
	}
	// Dismissing Home does not change what the show's Play action means.
	if w := call(t, s, "PUT", "/v1/items/"+finished+"/state", owner, map[string]any{"continueDismissed": true}, nil); w.Code != 200 {
		t.Fatalf("dismiss: %d", w.Code)
	}
	if got := read(); got == nil || got.ID != next {
		t.Fatalf("dismiss changed show target: %+v", got)
	}
	if w := call(t, s, "PUT", "/v1/items/"+next+"/state", owner, map[string]any{"positionMs": 40000}, nil); w.Code != 200 {
		t.Fatalf("progress: %d", w.Code)
	}
	if got := read(); got == nil || got.ID != next || got.State.PositionMs != 40000 {
		t.Fatalf("resume target: %+v", got)
	}
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kids"}, &kid)
	maxAge := 13
	call(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, Restrictions{AllLibraries: true, MaxRatingAge: &maxAge}, nil)
	var picked Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &picked)
	if got := get[Item](t, s, picked.AccessToken, "/v1/items/"+showID).NextEpisode; got != nil && got.ID == next {
		t.Fatal("next episode revealed a rating-hidden title")
	}
}
