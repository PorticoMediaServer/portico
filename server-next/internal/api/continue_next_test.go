package api

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestContinueWatchingOffersTheNextVisibleEpisode(t *testing.T) {
	th := newTheatre(t)
	ctx, token := context.Background(), th.owner.AccessToken
	lib := th.libs[db.LibraryShows]
	three, one := 3, 1
	if _, err := th.s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 2, Dirs: []db.ImportDir{{}},
		Items: []db.ImportItem{{Kind: db.KindEpisode, Title: "Next chapter", IndexNumber: &three, ParentIndex: &one,
			Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "e3"}}, Parent: &db.ItemKey{Kind: db.KeyName, Key: "dark/1"}}}}); err != nil {
		t.Fatal(err)
	}
	edit := func(item string, body map[string]any) {
		t.Helper()
		th.clock.Set(th.clock.Now().Add(time.Millisecond))
		if code := status(t, th.s, "PUT", "/v1/items/"+item+"/state", token, body); code != 200 {
			t.Fatal(code)
		}
	}
	continuing := func(path string) []Item {
		t.Helper()
		for _, row := range get[Home](t, th.s, token, path).Rows {
			if row.ID == "continue" {
				return row.Items
			}
		}
		return nil
	}
	if rows := continuing("/v1/home"); len(rows) != 0 {
		t.Fatal("a show never played is on Continue Watching")
	}
	session := th.play(token, th.eps[0])
	end := hour
	if code := status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, token, PlaybackStop{PositionMs: &end}); code != 204 {
		t.Fatalf("finish first episode: %d", code)
	}
	for _, path := range []string{"/v1/home", "/v1/libraries/" + lib.PublicID + "/landing"} {
		rows := continuing(path)
		if len(rows) != 1 || rows[0].ID != th.eps[1] || rows[0].Kind != "episode" || rows[0].RootTitle != "Dark" || rows[0].RootID == "" {
			t.Fatalf("between episodes %s: %+v", path, rows)
		}
	}
	edit(th.eps[1], map[string]any{"continueDismissed": true})
	if rows := continuing("/v1/home"); len(rows) != 0 {
		t.Fatalf("the dismissed next episode was replaced or kept: %+v", rows)
	}
	edit(th.eps[1], map[string]any{"continueDismissed": false})
	edit(th.eps[1], map[string]any{"positionMs": 60_000})
	if rows := continuing("/v1/home"); len(rows) != 1 || rows[0].ID != th.eps[1] || rows[0].State.PositionMs != 60_000 {
		t.Fatalf("the resumed episode was duplicated or replaced: %+v", rows)
	}
	other, err := th.s.db.CreateSeedProfile(ctx, db.SeedProfile{Name: "next episode viewer"})
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := th.s.db.ContinueWatching(ctx, other); err != nil || len(rows) != 0 {
		t.Fatalf("another profile's next episode: %+v, %v", rows, err)
	}
	position, yes := int64(60_000), true
	if _, _, err := th.s.db.SetState(ctx, other, th.eps[0], db.StateEdit{PositionMS: &position}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := th.s.db.SetState(ctx, other, th.eps[0], db.StateEdit{Watched: &yes}); err != nil {
		t.Fatal(err)
	}
	locks, err := th.s.db.MetadataLocks(ctx, th.eps[1])
	if err != nil {
		t.Fatal(err)
	}
	rating := "NC-17"
	if _, err := th.s.db.EditMetadata(ctx, th.eps[1], locks.Revision, db.MetadataEdit{ContentRating: &rating}); err != nil {
		t.Fatal(err)
	}
	profiles, err := th.s.db.Profiles(ctx, other.UserID)
	if err != nil || len(profiles) != 1 {
		t.Fatalf("profiles: %v, %v", profiles, err)
	}
	age := 13
	if _, err := th.s.db.SetProfileRestrictions(ctx, other.UserID, profiles[0].PublicID,
		db.Restrictions{AllLibraries: true, MaxRatingAge: &age, AllowUnrated: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rows, err := th.s.db.ContinueWatching(ctx, other)
	if err != nil || len(rows) != 1 || rows[0].Title != "Next chapter" || rows[0].RootTitle != "Dark" {
		t.Fatalf("next visible episode: %+v, %v", rows, err)
	}
	edit(th.eps[1], map[string]any{"watched": true})
	edit(rows[0].PublicID, map[string]any{"watched": true})
	if rows := continuing("/v1/home"); len(rows) != 0 {
		t.Fatalf("a finished show is still on Continue Watching: %+v", rows)
	}
}
