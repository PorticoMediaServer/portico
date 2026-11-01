package catalog

import (
	"errors"
	"testing"
)

// A Home row's next page survives the decoration that background work writes
// all day (metadata, extras, analysis, artwork), and is still refused once the
// row's membership changes.
func TestHomeRowNextPageSurvivesBackgroundDecoration(t *testing.T) {
	f := recommendationFixtureDB(t)
	s := New(f.db)
	r := engineRequest()
	r.RowOrder = []string{"new_on_server"}
	doc, err := s.HomeRows(r)
	if err != nil {
		t.Fatal(err)
	}
	var first HomeRow
	for _, row := range doc.Rows {
		if row.ID == "new_on_server" {
			first = row
		}
	}
	if first.ID == "" {
		t.Fatal("no new_on_server row")
	}
	if first, err = s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 1}); err != nil || first.NextCursor == "" {
		t.Fatalf("first page: %+v %v", first, err)
	}

	// Decoration only: a metadata match and a catalogue revision bump of the
	// kind analysis, extras and screen fields make.
	engineMeta(t, f, "m1", "tmdb", "603")
	engineExec(t, f, `UPDATE library_revisions SET revision=revision+5`)
	f.c.Drain()
	second, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Entries) != 1 || second.Entries[0].ID == first.Entries[0].ID {
		t.Fatalf("decoration stranded the next page: %+v %v", second, err)
	}
	if second.Revision != first.Revision {
		t.Fatalf("the paging revision moved with decoration: %+v then %+v", first.Revision, second.Revision)
	}
	if again, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 1, Start: 1, Revision: revisionParam(first.Revision)}); err != nil || len(again.Entries) != 1 {
		t.Fatalf("decoration stranded a ranged page: %+v %v", again, err)
	}

	// A new title changes the row's membership: the old cursor is stale.
	f.c.Movie(f.libs["movies"], "/movies/late.mkv", "Late Arrival", 2026)
	f.c.Drain()
	if _, err = s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 1, Cursor: first.NextCursor}); !errors.Is(err, ErrStaleContinuation) {
		t.Fatal("a membership change did not fence the cursor:", err)
	}
}

func revisionParam(r ContentRevision) string {
	return itoa(r.Catalog) + ":" + itoa(r.Viewer)
}
