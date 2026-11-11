package db_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A label the owner puts on a show in the editor hides the show and its episodes from a
// profile that blocks it, Undo of that edit shows them again, and a show merged into another
// brings its labels with it, so its episodes stay hidden under their new show. The stored
// covering labels stay exactly right throughout.
func TestEditorLabelsUndoAndMergeKeepWhatABlockedLabelHides(t *testing.T) {
	l := newLibrary(t, 1500)
	shows := showsLibrary(l)
	consistent := func(when string) {
		t.Helper()
		if wrong, _, err := db.CoveringLabels(l.d); err != nil || len(wrong) > 0 {
			t.Fatalf("%s: stored labels wrong for %v (%v)", when, wrong, err)
		}
	}
	edit := func(id string, labels ...string) {
		t.Helper()
		locks, err := l.d.MetadataLocks(ctx, id)
		if err == nil {
			_, err = l.d.EditMetadata(ctx, id, locks.Revision, db.MetadataEdit{Labels: &labels})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	var from, into db.Item
	for _, s := range everything(t, l.d, l.all, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{db.KindShow}, Limit: 200}) {
		if s.ChildCount == 0 {
			continue
		}
		if from.ID == 0 {
			from = s
		} else if into.ID == 0 {
			into = s
		}
	}
	if into.ID == 0 {
		t.Fatal("the test library has fewer than two shows with episodes")
	}
	edit(into.PublicID, "Not for the children")
	edit(from.PublicID, "Too scary")
	consistent("after the editor labelled two shows")
	tags, err := l.d.Tags(ctx, db.TagLabel)
	if err != nil {
		t.Fatal(err)
	}
	var blocked []string
	for _, tag := range tags {
		if tag.Name == "Not for the children" || tag.Name == "Too scary" {
			blocked = append(blocked, tag.PublicID)
		}
	}
	child, err := l.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "blocks-two-labels", BlockedTags: blocked})
	if err != nil {
		t.Fatal(err)
	}
	episodesOf := func(show db.Item) int {
		t.Helper()
		n := 0
		for _, e := range everything(t, l.d, child, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{db.KindEpisode}, Limit: 200}) {
			if e.RootPublicID == show.PublicID {
				n++
			}
		}
		return n
	}
	if n := episodesOf(into) + episodesOf(from); n != 0 {
		t.Fatalf("the profile that blocks both labels is offered %d episodes of the labelled shows", n)
	}

	history, err := l.d.MetadataHistory(ctx, into.PublicID)
	if err != nil || len(history) == 0 {
		t.Fatalf("History: %v, %v", history, err)
	}
	if _, err := l.d.UndoMetadata(ctx, into.PublicID, history[0].ID); err != nil {
		t.Fatal(err)
	}
	consistent("after Undo took the label off")
	if episodesOf(into) == 0 {
		t.Fatal("after Undo took its label off, the show's episodes are still hidden")
	}

	if err := l.d.MergeItems(ctx, from.PublicID, into.PublicID); err != nil {
		t.Fatal(err)
	}
	consistent("after the labelled show was merged into the other")
	if n := episodesOf(into); n != 0 {
		t.Fatalf("after a show labelled Too scary was merged into it, %d of the show's episodes are offered to a profile that blocks that label", n)
	}
}
