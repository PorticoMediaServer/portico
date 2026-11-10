package db_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// entries reads a whole list in its own order, two at a time so paging is crossed.
func entries(t *testing.T, d *db.DB, v db.Viewer, list string) (titles []string, ids []int64, total int) {
	t.Helper()
	q := db.ItemQuery{List: list, Limit: 2, WithTotal: true}
	for {
		page, err := d.ListEntries(ctx, v, q)
		if err != nil {
			t.Fatal(err)
		}
		total = page.Total
		for _, it := range page.Items {
			titles, ids = append(titles, it.PublicID), append(ids, it.EntryID)
		}
		if page.NextCursor == "" {
			return titles, ids, total
		}
		q.Cursor = page.NextCursor
	}
}

// profileID is a viewer's profile's public id.
func profileID(t *testing.T, d *db.DB, v db.Viewer) string {
	t.Helper()
	p, err := d.ProfileByID(ctx, v.UserID, v.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	return p.PublicID
}

// A playlist keeps its maker's order through adds, moves and removals, holds a title twice,
// and a profile it is shared with sees only the titles it may see, in counts as well.
func TestPlaylistOrderAndWhatAReaderSees(t *testing.T) {
	l := newLibrary(t, 600)
	owner, child := l.all, l.viewers[9] // the seeder's profile limited by rating
	films := everything(t, l.d, owner, db.ItemQuery{Library: l.libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}, Limit: 200})
	var seen, hidden []string
	for _, f := range films {
		_, err := l.d.Item(ctx, child, f.PublicID)
		switch {
		case err == nil && len(seen) < 3:
			seen = append(seen, f.PublicID)
		case errors.Is(err, db.ErrNotFound) && len(hidden) < 1:
			hidden = append(hidden, f.PublicID)
		}
	}
	if len(seen) < 3 || len(hidden) < 1 {
		t.Fatalf("the test library needs films both sides of the limit: %d, %d", len(seen), len(hidden))
	}
	now := time.Now()
	list, err := l.d.CreateList(ctx, owner, db.NewList{Kind: db.ListPlaylist, Name: "Mix", SortName: "mix", Items: []string{seen[0], hidden[0], seen[1]}},
		"key-1", "hash-1", now)
	if err != nil {
		t.Fatal(err)
	}
	again, err := l.d.CreateList(ctx, owner, db.NewList{Kind: db.ListPlaylist, Name: "Mix", SortName: "mix"}, "key-1", "hash-1", now)
	if err != nil || again.PublicID != list.PublicID {
		t.Fatalf("a repeated create made %s, not %s: %v", again.PublicID, list.PublicID, err)
	}
	// Twice the same title, then the third film moved to the front and the hidden one removed
	// and put back: the order is the maker's.
	added, err := l.d.AddToList(ctx, owner, list.PublicID, 0, []string{seen[0], seen[2]}, 0, "key-2", "hash-2", now)
	if err != nil || added.Added != 2 {
		t.Fatalf("added %d: %v", added.Added, err)
	}
	if again, err := l.d.AddToList(ctx, owner, list.PublicID, 0, []string{seen[0], seen[2]}, 0, "key-2", "hash-2", now); err != nil ||
		again.Added != 2 || again.List.Count != 5 {
		t.Fatalf("a repeated add: added %d, count %d: %v", again.Added, again.List.Count, err)
	}
	_, ids, _ := entries(t, l.d, owner, list.PublicID)
	if _, err := l.d.MoveInList(ctx, owner, list.PublicID, 0, ids[4], ids[0], 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := l.d.MoveInList(ctx, owner, list.PublicID, added.List.Revision, ids[1], 0, ids[2], now); !errors.Is(err, db.ErrStale) {
		t.Fatalf("a move on an out-of-date copy: %v", err)
	}
	titles, _, total := entries(t, l.d, owner, list.PublicID)
	want := []string{seen[2], seen[0], hidden[0], seen[1], seen[0]}
	if !slices.Equal(titles, want) || total != 5 {
		t.Fatalf("owner sees %v (%d), want %v", titles, total, want)
	}

	// Shared with the limited profile: the hidden film is in no entry, total or count.
	_, people, err := l.d.ListShares(ctx, owner, list.PublicID)
	if err != nil || len(people) < 2 {
		t.Fatalf("people to share with: %+v, %v", people, err)
	}
	childID := profileID(t, l.d, child)
	if !slices.ContainsFunc(people, func(p db.ListShare) bool { return p.ProfilePublicID == childID }) {
		t.Fatalf("the child is not among the people to share with: %+v", people)
	}
	if _, err := l.d.SetListShares(ctx, owner, list.PublicID, 0, []db.ListShare{{ProfilePublicID: childID}}, now); err != nil {
		t.Fatal(err)
	}
	if shares, _, err := l.d.ListShares(ctx, owner, list.PublicID); err != nil || len(shares) != 1 || shares[0].CanEdit {
		t.Fatalf("shares: %+v, %v", shares, err)
	}
	if _, _, err := l.d.ListShares(ctx, child, list.PublicID); !errors.Is(err, db.ErrNotAllowed) {
		t.Fatalf("a profile it is shared with read the shares: %v", err)
	}
	titles, _, total = entries(t, l.d, child, list.PublicID)
	if want := []string{seen[2], seen[0], seen[1], seen[0]}; !slices.Equal(titles, want) || total != 4 {
		t.Fatalf("the limited profile sees %v (%d), want %v", titles, total, want)
	}
	read, err := l.d.List(ctx, child, list.PublicID)
	if err != nil || read.Count != 4 || read.Owned || read.CanEdit {
		t.Fatalf("the limited profile's list: %+v, %v", read, err)
	}
	if lists, _, err := l.d.Lists(ctx, child, "", "", 0); err != nil || len(lists) != 1 || lists[0].Count != 4 {
		t.Fatalf("the limited profile's lists: %+v, %v", lists, err)
	}
	if _, err := l.d.AddToList(ctx, child, list.PublicID, 0, []string{seen[1]}, 0, "", "", now); !errors.Is(err, db.ErrNotAllowed) {
		t.Fatalf("a profile shared to view added: %v", err)
	}
	if err := l.d.DeleteList(ctx, child, list.PublicID); !errors.Is(err, db.ErrNotAllowed) {
		t.Fatalf("a profile shared to view deleted: %v", err)
	}
	// Shared to edit, the same profile adds a title it may see; unshared, it no longer finds it.
	if _, err := l.d.SetListShares(ctx, owner, list.PublicID, 0, []db.ListShare{{ProfilePublicID: childID, CanEdit: true}}, now); err != nil {
		t.Fatal(err)
	}
	if added, err := l.d.AddToList(ctx, child, list.PublicID, 0, []string{seen[1]}, 0, "", "", now); err != nil || added.List.Count != 5 {
		t.Fatalf("a profile shared to edit added: %+v, %v", added, err)
	}
	if _, err := l.d.SetListShares(ctx, owner, list.PublicID, 0, nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := l.d.List(ctx, child, list.PublicID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("an unshared profile still reads the list: %v", err)
	}
	// Another profile it is not shared with does not know it exists.
	if _, err := l.d.List(ctx, l.viewers[1], list.PublicID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("a stranger read the list: %v", err)
	}
	if err := l.d.DeleteList(ctx, owner, list.PublicID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.d.List(ctx, owner, list.PublicID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("a deleted list is still read: %v", err)
	}
}

// Moving entries over and over into the same place runs out of room between two positions;
// the list is numbered afresh and keeps its order.
func TestListRenumbersWhenAGapRunsOut(t *testing.T) {
	l := newLibrary(t, 300)
	owner := l.all
	films := everything(t, l.d, owner, db.ItemQuery{Library: l.libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}, Limit: 20})[:20]
	var names []string
	for _, f := range films {
		names = append(names, f.PublicID)
	}
	now := time.Now()
	list, err := l.d.CreateList(ctx, owner, db.NewList{Kind: db.ListPlaylist, Name: "Order", SortName: "order", Items: names[:2]}, "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	_, ids, _ := entries(t, l.d, owner, list.PublicID)
	// Each new title goes just after the first entry: the gap there halves every time.
	want := []string{names[0]}
	for _, name := range names[2:] {
		if _, err := l.d.AddToList(ctx, owner, list.PublicID, 0, []string{name}, ids[0], "", "", now); err != nil {
			t.Fatal(err)
		}
		want = append(want[:1], append([]string{name}, want[1:]...)...)
	}
	want = append(want, names[1])
	if titles, _, _ := entries(t, l.d, owner, list.PublicID); !slices.Equal(titles, want) {
		t.Fatalf("order %v, want %v", titles, want)
	}
}
