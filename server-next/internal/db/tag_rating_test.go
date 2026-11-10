package db_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A title's rating can change after it was created. A rating-limited profile's genre choices
// are counted from copies of the rating on the tag rows, so they must follow: after a show
// goes from allowed to not allowed, each genre's count is still the size of that genre's list.
func TestGenreChoicesFollowAChangedRating(t *testing.T) {
	l := newLibrary(t, 1500)
	limited := l.viewers[9] // rating limit 13, unrated hidden
	var shows db.Library
	for _, lib := range l.libs {
		if lib.Kind == db.LibraryShows {
			shows = lib
		}
	}
	agree := func(kind db.ItemKind) (sum int) {
		t.Helper()
		q := db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{kind}}
		choices, err := l.d.Facets(ctx, limited, q, db.FacetGenre)
		if err != nil {
			t.Fatal(err)
		}
		for _, choice := range choices {
			q.Tag, q.WithTotal = choice.Value, true
			page, err := l.d.Items(ctx, limited, q)
			if err != nil {
				t.Fatal(err)
			}
			// The list is counted from item rows here: a rare tag's total reads them.
			if listed := len(everything(t, l.d, limited, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{kind}, Tag: choice.Value, Limit: 200})); listed != choice.Count || page.Total != choice.Count {
				t.Fatalf("kind %d, genre %q: the choice says %d, the total %d, the list holds %d", kind, choice.Label, choice.Count, page.Total, listed)
			}
			sum += choice.Count
		}
		return sum
	}
	before := agree(db.KindShow) + agree(db.KindEpisode)
	// Every show the profile can see becomes adults only, with its episodes.
	visible := everything(t, l.d, limited, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{db.KindShow}, Limit: 200})
	if len(visible) < 2 {
		t.Fatalf("the test library shows the limited profile %d shows", len(visible))
	}
	for _, show := range visible[:len(visible)/2] {
		if err := db.SetRatingAge(l.d, show.PublicID, 18); err != nil {
			t.Fatal(err)
		}
	}
	if after := agree(db.KindShow) + agree(db.KindEpisode); after >= before {
		t.Fatalf("genre counts were %d before half the shows became adults only and %d after", before, after)
	}
}
