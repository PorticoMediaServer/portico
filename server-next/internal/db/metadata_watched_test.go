package db_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A show's watched mark follows the editor and Undo: a profile limited to 13 that has watched
// every episode of a show but one it can see gets the mark when the owner makes that episode
// adults only, loses it when Undo puts the rating back, and gets it again when the owner gives
// the episode a label the profile blocks. Every stored mark stays right throughout.
func TestAShowsWatchedMarkFollowsTheEditorAndUndo(t *testing.T) {
	l := newLibrary(t, 1500)
	shows := showsLibrary(l)
	marks := func(when string) {
		t.Helper()
		if _, wrong, err := db.WatchedMarks(l.d); err != nil || len(wrong) > 0 {
			t.Fatalf("%s: %v %v", when, wrong, err)
		}
	}
	edit := func(id string, e db.MetadataEdit) {
		t.Helper()
		locks, err := l.d.MetadataLocks(ctx, id)
		if err == nil {
			_, err = l.d.EditMetadata(ctx, id, locks.Revision, e)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	// The label exists before the profile that blocks it is made.
	films := everything(t, l.d, l.all, db.ItemQuery{Library: l.libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}, Limit: 200})
	edit(films[0].PublicID, db.MetadataEdit{Labels: &[]string{"Too much"}})
	tags, err := l.d.Tags(ctx, db.TagLabel)
	if err != nil {
		t.Fatal(err)
	}
	var blocked string
	for _, tag := range tags {
		if tag.Name == "Too much" {
			blocked = tag.PublicID
		}
	}
	age := 13
	child, err := l.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "child", MaxRatingAge: &age, BlockedTags: []string{blocked}})
	if err != nil {
		t.Fatal(err)
	}
	// Every episode the child sees, by show.
	byShow := map[string][]db.Item{}
	for _, e := range everything(t, l.d, child, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{db.KindEpisode}, Limit: 200}) {
		byShow[e.RootPublicID] = append(byShow[e.RootPublicID], e)
	}
	var episodes []db.Item
	for _, list := range byShow {
		if len(list) >= 2 && (episodes == nil || len(list) < len(episodes)) {
			episodes = list
		}
	}
	if episodes == nil {
		t.Fatal("no show the child sees has two episodes")
	}
	yes := true
	for _, e := range episodes {
		if e.PublicID != episodes[0].PublicID {
			if _, _, err := l.d.SetState(ctx, child, e.PublicID, db.StateEdit{Watched: &yes}); err != nil {
				t.Fatal(err)
			}
		}
	}
	marks("with one episode left to watch")
	rating := "NC-17"
	edit(episodes[0].PublicID, db.MetadataEdit{ContentRating: &rating})
	marks("after the owner made the episode left adults only")
	history, err := l.d.MetadataHistory(ctx, episodes[0].PublicID)
	if err != nil || len(history) == 0 {
		t.Fatalf("History: %v, %v", history, err)
	}
	if _, err := l.d.UndoMetadata(ctx, episodes[0].PublicID, history[0].ID); err != nil {
		t.Fatal(err)
	}
	marks("after Undo put the episode's rating back")
	edit(episodes[0].PublicID, db.MetadataEdit{Labels: &[]string{"Too much"}})
	marks("after the owner gave the episode a label the child blocks")
}
