package metadata_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// byNumber is a show's episodes' titles by season and episode number.
func (f *fixture) byNumber() map[[2]int]db.Item {
	f.t.Helper()
	out := map[[2]int]db.Item{}
	for _, e := range f.episodes() {
		out[[2]int{*e.ParentIndex, *e.IndexNumber}] = e
	}
	return out
}

// titles are the titles of a show's episodes S01E01, S01E02, S01E03 and S06E01.
func (f *fixture) titles() [4]string {
	f.t.Helper()
	e := f.byNumber()
	return [4]string{e[[2]int{1, 1}].Title, e[[2]int{1, 2}].Title, e[[2]int{1, 3}].Title, e[[2]int{6, 1}].Title}
}

var breakingBad = []string{"Breaking Bad/Season 01/Breaking Bad - S01E01.mkv", "Breaking Bad/Season 01/Breaking Bad - S01E02.mkv",
	"Breaking Bad/Season 01/Breaking Bad - S01E03.mkv", "Breaking Bad/Season 06/Breaking Bad - S06E01.mkv"}

// A show matched in DVD order takes each episode's details from its DVD place, and with an
// offset from the provider's number that far from the files'. The choice is kept through a
// refresh. TMDB's DVD order of Breaking Bad is its aired order with the last season split in
// two, so DVD season 6 is the second half of aired season 5. TMDB has no absolute order for it.
func TestEpisodeOrderAndOffset(t *testing.T) {
	f := newFixture(t, db.LibraryShows, breakingBad...)
	f.match()
	show := f.title(db.KindShow, "Breaking Bad").PublicID
	titles := f.titles
	if got := titles(); got != [4]string{"Pilot", "Cat's in the Bag...", "...And the Bag's in the River", "Episode 1"} {
		t.Fatalf("aired order: %q", got)
	}
	identify := func(order string, offset int) {
		t.Helper()
		if _, err := f.s.IdentifyShow(ctx, show, metadata.Ref{ID: "1396", Order: order}, offset); err != nil {
			t.Fatal(err)
		}
	}
	identify(metadata.OrderDVD, 0)
	if got := titles(); got != [4]string{"Pilot", "Cat's in the Bag...", "...And the Bag's in the River", "Blood Money"} {
		t.Fatalf("DVD order: %q", got)
	}
	// A show TMDB has in no such order keeps what its files say.
	identify(metadata.OrderAbsolute, 0)
	if got := titles(); got != [4]string{"Episode 1", "Episode 2", "Episode 3", "Episode 1"} {
		t.Fatalf("absolute order: %q", got)
	}
	// Files numbered from 0 for a provider that numbers from 1: the offset is 1.
	identify(metadata.OrderAired, 1)
	want := [4]string{"Cat's in the Bag...", "...And the Bag's in the River", "Cancer Man", "Episode 1"}
	if got := titles(); got != want {
		t.Fatalf("aired order with offset 1: %q", got)
	}
	if _, err := f.s.Refresh(ctx, show); err != nil {
		t.Fatal(err)
	}
	if got := titles(); got != want {
		t.Fatalf("a refresh lost the offset: %q", got)
	}
	if _, err := f.s.IdentifyShow(ctx, show, metadata.Ref{ID: "1396", Order: "broadcast"}, 0); err != metadata.ErrUnknownOrder {
		t.Fatalf("an unknown order: %v", err)
	}
}

// A show matched to another show keeps nothing of the first on an episode the second does not
// have: it is named from its number again and loses the first show's id and description.
// Better Call Saul's first season has ten episodes and Breaking Bad's seven.
func TestAnotherShowLeavesNothingOfTheFirstOnItsEpisodes(t *testing.T) {
	f := newFixture(t, db.LibraryShows, "Breaking Bad/Season 01/Breaking Bad - S01E01.mkv", "Breaking Bad/Season 01/Breaking Bad - S01E09.mkv")
	f.match()
	show := f.title(db.KindShow, "Breaking Bad").PublicID
	if _, err := f.s.Identify(ctx, show, metadata.Ref{ID: "60059"}); err != nil {
		t.Fatal(err)
	}
	if e := f.byNumber(); e[[2]int{1, 9}].Title != "Pimento" {
		t.Fatalf("matched to Better Call Saul: %q", e[[2]int{1, 9}].Title)
	}
	if _, err := f.s.Identify(ctx, show, metadata.Ref{ID: "1396"}); err != nil {
		t.Fatal(err)
	}
	e := f.byNumber()
	first, second := e[[2]int{1, 1}], e[[2]int{1, 9}]
	if first.Title != "Pilot" || second.Title != "Episode 9" {
		t.Fatalf("after the match changed: %q and %q", first.Title, second.Title)
	}
	detail, err := f.d.Item(ctx, f.viewer, second.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Summary != "" || len(detail.ExternalIDs) != 0 || detail.ReleaseDate != "" || len(detail.Credits) != 0 {
		t.Fatalf("the second episode kept the first show's details: %+v", detail)
	}
}

// Undo of a show's match change puts the show back as a whole: its episodes' details and ids,
// and the episode order and offset the owner had chosen, which a refresh then keeps using.
func TestUndoOfAShowsMatchChangePutsItsEpisodesAndOrderBack(t *testing.T) {
	f := newFixture(t, db.LibraryShows, breakingBad...)
	f.match()
	show := f.title(db.KindShow, "Breaking Bad").PublicID
	titles := f.titles
	if _, err := f.s.IdentifyShow(ctx, show, metadata.Ref{ID: "1396", Order: metadata.OrderDVD}, 0); err != nil {
		t.Fatal(err)
	}
	dvd := titles()
	if _, err := f.s.Identify(ctx, show, metadata.Ref{ID: "60059"}); err != nil {
		t.Fatal(err)
	}
	if got := titles(); got != [4]string{"Uno", "Mijo", "Nacho", "Episode 1"} {
		t.Fatalf("after matching another show: %q", got)
	}
	history, err := f.d.MetadataHistory(ctx, show)
	if err != nil || len(history) != 2 {
		t.Fatalf("history: %+v %v", history, err)
	}
	if _, err := f.d.UndoMetadata(ctx, show, history[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := titles(); got != dvd || dvd != [4]string{"Pilot", "Cat's in the Bag...", "...And the Bag's in the River", "Blood Money"} {
		t.Fatalf("after Undo: %q, before the change: %q", got, dvd)
	}
	doc, err := f.d.MetadataDocument(ctx, show)
	if err != nil || doc.Fields[db.FieldTitle] != "Breaking Bad" || doc.Numbering.Order != metadata.OrderDVD {
		t.Fatalf("the show after Undo: %+v %v", doc, err)
	}
	first, err := f.d.Item(ctx, f.viewer, f.byNumber()[[2]int{1, 1}].PublicID)
	if err != nil || len(first.ExternalIDs) != 1 || first.ExternalIDs[0].ID != "62085" {
		t.Fatalf("the first episode's ids after Undo: %+v %v", first.ExternalIDs, err)
	}
	// The order put back is the one a refresh uses.
	if _, err := f.s.Refresh(ctx, show); err != nil {
		t.Fatal(err)
	}
	if got := titles(); got != dvd {
		t.Fatalf("a refresh after Undo: %q", got)
	}
}

// A match change never touches labels, so its undo keeps a label the owner gave an episode
// since: that label may be what keeps the episode from a limited profile.
func TestUndoOfAShowsMatchChangeKeepsALabelAddedSince(t *testing.T) {
	f := newFixture(t, db.LibraryShows, "Breaking Bad/Season 01/Breaking Bad - S01E01.mkv", "Breaking Bad/Season 01/Breaking Bad - S01E02.mkv")
	f.match()
	show := f.title(db.KindShow, "Breaking Bad").PublicID
	if _, err := f.s.Identify(ctx, show, metadata.Ref{ID: "60059"}); err != nil {
		t.Fatal(err)
	}
	episode := f.byNumber()[[2]int{1, 2}].PublicID
	doc, err := f.d.MetadataDocument(ctx, episode)
	if err != nil {
		t.Fatal(err)
	}
	labels := []string{"Not for kids"}
	if _, err := f.d.EditMetadata(ctx, episode, doc.Revision, db.MetadataEdit{Labels: &labels}); err != nil {
		t.Fatal(err)
	}
	history, err := f.d.MetadataHistory(ctx, show)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.UndoMetadata(ctx, show, history[0].ID); err != nil {
		t.Fatal(err)
	}
	if doc, err = f.d.MetadataDocument(ctx, episode); err != nil || len(doc.Lists["labels"]) != 1 {
		t.Fatalf("the episode's labels after Undo: %v %v", doc.Lists["labels"], err)
	}
}
