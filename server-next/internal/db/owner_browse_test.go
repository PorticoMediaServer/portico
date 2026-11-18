package db_test

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The owner and administrators sort by file size, largest first, and filter by whether a title
// is matched, an episode by its show's match; a member may do neither.
func TestOwnerSortsBySizeAndFiltersByMatched(t *testing.T) {
	l := newLibrary(t, 600)
	admin, member := l.viewers[0], l.viewers[1]
	if err := l.d.ExecForTest(ctx, `UPDATE users SET role = 'admin' WHERE id = ?`, admin.UserID); err != nil {
		t.Fatal(err)
	}
	var films, shows db.Library
	for _, lib := range l.libs {
		switch lib.Kind {
		case db.LibraryMovies:
			films = lib
		case db.LibraryShows:
			shows = lib
		}
	}

	bySize := db.ItemQuery{Library: films.PublicID, Kinds: []db.ItemKind{db.KindMovie}, Sort: db.SortFileSize, Desc: true}
	listed := everything(t, l.d, admin, bySize)
	last := int64(-1)
	for _, it := range listed {
		detail, err := l.d.Item(ctx, admin, it.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		var size int64
		for _, v := range detail.Versions {
			size += v.Size
		}
		if last >= 0 && size > last {
			t.Fatalf("%q (%d bytes) comes after a smaller film (%d bytes)", it.Title, size, last)
		}
		last = size
	}
	if len(listed) < 2 {
		t.Fatalf("only %d films to sort", len(listed))
	}
	if _, err := l.d.Items(ctx, member, bySize); !errors.Is(err, db.ErrInvalid) {
		t.Errorf("a member sorting by file size got %v", err)
	}

	showList := everything(t, l.d, admin, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{db.KindShow}})
	matched := showList[0]
	if err := l.d.ExecForTest(ctx, `UPDATE items SET match_state = ? WHERE public_id = ?`, db.MatchFound, matched.PublicID); err != nil {
		t.Fatal(err)
	}
	filtered := func(kind db.ItemKind, value string) []db.Item {
		return everything(t, l.d, admin, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{kind},
			Conditions: []db.Condition{{Field: "matched", Values: []string{value}}}})
	}
	if got := filtered(db.KindShow, "true"); len(got) != 1 || got[0].PublicID != matched.PublicID {
		t.Errorf("matched shows: %d", len(got))
	}
	if got := filtered(db.KindShow, "false"); len(got) != len(showList)-1 {
		t.Errorf("%d unmatched shows of %d", len(got), len(showList))
	}
	if got := filtered(db.KindEpisode, "true"); len(got) == 0 || len(got) != matched.Leaves {
		t.Errorf("%d matched episodes, want the matched show's %d", len(got), matched.Leaves)
	}
	if _, err := l.d.Items(ctx, member, db.ItemQuery{Library: shows.PublicID,
		Conditions: []db.Condition{{Field: "matched", Values: []string{"true"}}}}); !errors.Is(err, db.ErrInvalid) {
		t.Errorf("a member filtering by matched got %v", err)
	}
}
