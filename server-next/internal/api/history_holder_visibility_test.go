package api

import (
	"context"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"testing"
)

func TestRemovedEpisodeDoesNotRestoreItsStillHiddenShowsName(t *testing.T) {
	th := newTheatre(t)
	s, owner := th.s, th.owner.AccessToken
	ctx := context.Background()
	episode := get[Item](t, s, owner, "/v1/items/"+th.eps[0])
	for _, edit := range []struct{ id, rating string }{{episode.ID, "TV-G"}, {episode.RootID, "NC-17"}} {
		locks, err := s.db.MetadataLocks(ctx, edit.id)
		if err == nil {
			_, err = s.db.EditMetadata(ctx, edit.id, locks.Revision, db.MetadataEdit{ContentRating: &edit.rating})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	main := get[Page[Profile]](t, s, owner, "/v1/profiles").Items[0].ID
	call(t, s, "PUT", "/v1/profiles/"+main+"/restrictions", owner, Restrictions{AllLibraries: true, MaxRatingAge: new(13), AllowUnrated: true}, nil)
	session := th.play(owner, episode.ID)
	if code := status(t, s, "DELETE", "/v1/playback/sessions/"+session.ID, owner, PlaybackStop{PositionMs: new(hour)}); code != 204 {
		t.Fatal(code)
	}
	before := get[HistoryPage](t, s, owner, "/v1/history")
	if len(before.Items) != 1 || before.Items[0].Subtitle != "" {
		t.Fatal(before)
	}
	remove := func(id string) {
		t.Helper()
		del, err := s.db.ItemDeletion(ctx, db.Viewer{UserID: 1, ProfileID: 1}, id)
		if err != nil {
			t.Fatal(err)
		}
		var files []int64
		for _, f := range del.Files {
			files = append(files, f.ID)
		}
		if err := s.db.Deleted(ctx, del, files); err != nil {
			t.Fatal(err)
		}
	}
	remove(episode.ID)
	if _, err := s.db.MetadataLocks(ctx, episode.RootID); err != nil {
		t.Fatal("fixture must retain show", err)
	}
	after := get[HistoryPage](t, s, owner, "/v1/history")
	if len(after.Items) != 1 || after.Items[0].Title != episode.Title || after.Items[0].Item != nil || after.Items[0].Subtitle != "" {
		t.Errorf("removed episode exposes hidden show: %+v", after.Items)
	}
	management := get[Page[PlayEntry]](t, s, owner, "/v1/admin/play-history")
	if len(management.Items) != 1 || management.Items[0].Subtitle != "Dark" {
		t.Errorf("management history changed: %+v", management.Items)
	}
	// A metadata merge must retain that same check against the surviving holder.
	lib := th.libs[db.LibraryShows]
	showKey := db.ItemKey{Kind: db.KeyFolder, Key: "history-other-show"}
	seasonKey := db.ItemKey{Kind: db.KeyFolder, Key: "history-other-show/2"}
	_, err := s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Items: []db.ImportItem{
		{Kind: db.KindShow, Keys: []db.ItemKey{showKey}, Title: "Other hidden show", ContentRating: "NC-17", RatingAge: new(18)},
		{Kind: db.KindSeason, Keys: []db.ItemKey{seasonKey}, Parent: &showKey, Title: "Season 2", IndexNumber: new(2)},
		{Kind: db.KindEpisode, Keys: []db.ItemKey{{Kind: db.KeyFolder, Key: "history-other-show/2/1"}}, Parent: &seasonKey, Title: "Other", IndexNumber: new(1)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	full, err := s.db.CreateSeedProfile(ctx, db.SeedProfile{Name: "Unrestricted fixture"})
	if err != nil {
		t.Fatal(err)
	}
	shows, err := s.db.Items(ctx, full, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindShow}, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	target := ""
	for _, show := range shows.Items {
		if show.Title == "Other hidden show" {
			target = show.PublicID
		}
	}
	if target == "" {
		t.Fatal("no merge target")
	}
	if err := s.db.MergeItems(ctx, episode.RootID, target); err != nil {
		t.Fatal(err)
	}
	merged := get[HistoryPage](t, s, owner, "/v1/history")
	if len(merged.Items) != 1 || merged.Items[0].Subtitle != "" {
		t.Errorf("merge restored hidden historical holder: %+v", merged.Items)
	}
	remove(target)
	gone := get[HistoryPage](t, s, owner, "/v1/history")
	if len(gone.Items) != 1 || gone.Items[0].Title != episode.Title || gone.Items[0].Item != nil {
		t.Errorf("removed title lost its history name: %+v", gone.Items)
	}
}
