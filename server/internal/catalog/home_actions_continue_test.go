package catalog

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// HM-1 (motion.html 3.10, tv-focus.html f4): a live Continue Watching card offers
// "Remove from Continue Watching"; the same title in another row does not, and a
// viewer without personal authority is never offered it.
func TestHomeActionContinueCardOffersItsDismissal(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	movie := homeItem(t, db, "m1")
	project := func(personal bool) []HomeRow {
		t.Helper()
		rows := []HomeRow{
			{ID: "continue", Entries: []ContentEntry{{ID: movie.Public, LibraryID: "movies", Kind: "movie"}}},
			{ID: "new_on_server", Entries: []ContentEntry{{ID: movie.Public, LibraryID: "movies", Kind: "movie"}}},
		}
		if err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
			return s.WithContext(ctx).ProjectHomeActions(homeRequestFixture().Viewer, HomeActionAuthority{Personal: personal, Playback: true, Queue: true}, rows, nil)
		}); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	rows := project(true)
	if a := rows[0].Entries[0].HomeActions; a == nil || !a.RemoveFromContinue {
		t.Fatalf("Continue Watching card: %+v", a)
	}
	if a := rows[1].Entries[0].HomeActions; a == nil || a.RemoveFromContinue {
		t.Fatalf("the same title in another row offered the dismissal: %+v", a)
	}
	if a := project(false)[0].Entries[0].HomeActions; a == nil || a.RemoveFromContinue {
		t.Fatalf("offered without personal authority: %+v", a)
	}
}
