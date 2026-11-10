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

// A show matched in DVD order takes each episode's details from its DVD place, in absolute
// order from its place through the whole show, and with an offset from the provider's number
// that far from the files'. The choice is kept through a refresh.
func TestEpisodeOrderAndOffset(t *testing.T) {
	f := newFixture(t, db.LibraryShows, "Breaking Bad/Season 01/Breaking Bad - S01E01.mkv", "Breaking Bad/Season 01/Breaking Bad - S01E02.mkv",
		"Breaking Bad/Season 01/Breaking Bad - S01E03.mkv")
	f.match()
	show := f.title(db.KindShow, "Breaking Bad").PublicID
	titles := func() [3]string {
		t.Helper()
		e := f.byNumber()
		return [3]string{e[[2]int{1, 1}].Title, e[[2]int{1, 2}].Title, e[[2]int{1, 3}].Title}
	}
	if got := titles(); got != [3]string{"Pilot", "Cat's in the Bag...", "Episode 3"} {
		t.Fatalf("aired order: %q", got)
	}
	identify := func(order string, offset int) {
		t.Helper()
		if _, err := f.s.IdentifyShow(ctx, show, metadata.Ref{ID: "1396", Order: order}, offset); err != nil {
			t.Fatal(err)
		}
	}
	identify(metadata.OrderDVD, 0)
	if got := titles(); got != [3]string{"Cat's in the Bag...", "Pilot", "Episode 3"} {
		t.Fatalf("DVD order: %q", got)
	}
	identify(metadata.OrderAbsolute, 0)
	if got := titles(); got != [3]string{"Pilot", "Cat's in the Bag...", "Seven Thirty-Seven"} {
		t.Fatalf("absolute order: %q", got)
	}
	// Files numbered from 0 for a provider that numbers from 1: the offset is 1.
	identify(metadata.OrderAired, 1)
	if got := titles(); got != [3]string{"Cat's in the Bag...", "Episode 2", "Episode 3"} {
		t.Fatalf("aired order with offset 1: %q", got)
	}
	if _, err := f.s.Refresh(ctx, show); err != nil {
		t.Fatal(err)
	}
	if got := titles(); got != [3]string{"Cat's in the Bag...", "Episode 2", "Episode 3"} {
		t.Fatalf("a refresh lost the offset: %q", got)
	}
	if _, err := f.s.IdentifyShow(ctx, show, metadata.Ref{ID: "1396", Order: "broadcast"}, 0); err != metadata.ErrUnknownOrder {
		t.Fatalf("an unknown order: %v", err)
	}
}

// A show matched to another show keeps nothing of the first on an episode the second does not
// have: it is named from its number again and loses the first show's id and description.
func TestAnotherShowLeavesNothingOfTheFirstOnItsEpisodes(t *testing.T) {
	f := newFixture(t, db.LibraryShows, "Breaking Bad/Season 01/Breaking Bad - S01E01.mkv", "Breaking Bad/Season 01/Breaking Bad - S01E02.mkv")
	f.match()
	show := f.title(db.KindShow, "Breaking Bad").PublicID
	if _, err := f.s.Identify(ctx, show, metadata.Ref{ID: "60059"}); err != nil {
		t.Fatal(err)
	}
	e := f.byNumber()
	first, second := e[[2]int{1, 1}], e[[2]int{1, 2}]
	if first.Title != "Uno" || second.Title != "Episode 2" {
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
