package catalog

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

// A playlist reads in its own order with absolute starts and witnesses, or in
// a temporary display order that keeps hidden occurrences last and publishes
// no witness; a move to the end sent with the witnessed tail lands there.
func TestPlaylistContentQueryStartAndOrderWitnesses(t *testing.T) {
	db, c, s := phase34PlaylistDB(t, filepath.Join(t.TempDir(), "db"))
	defer db.Close()
	a := c.Library("a", "A", "movie", "/a")
	b := c.Library("b", "B", "movie", "/b")
	items := []struct {
		title string
		year  int
	}{{"Echo", 2001}, {"Delta", 2005}, {"Charlie", 1999}, {"Bravo", 2010}, {"Alpha", 2003}}
	owner := ResourceActor{"local", "owner", "p"}
	name := "Mixed"
	made, e := s.MutatePlaylist(owner, "", "create", "", PlaylistMutation{OperationID: "create", Name: &name}, nil)
	if e != nil {
		t.Fatal(e)
	}
	public := []string{}
	for _, item := range items {
		public = append(public, c.Movie(a, "/a/"+item.title+".mkv", item.title, item.year).Public)
	}
	public = append(public[:2], append([]string{c.Movie(b, "/b/secret.mkv", "Secret", 2000).Public}, public[2:]...)...)
	c.Drain()
	tokens := []string{}
	revision := made.Revision
	for n, item := range public {
		r, e := s.MutatePlaylist(owner, made.PlaylistID, "add", "", PlaylistMutation{OperationID: "add" + string(rune('a'+n)), ExpectedRevision: revision, ItemID: item}, nil)
		if e != nil {
			t.Fatal(e)
		}
		tokens, revision = append(tokens, r.EntryID), r.Revision
	}
	phase34SettlePlaylistProjection(t, db)
	read := func(q ResourceContentQuery, cursor string) ContentEnvelope {
		t.Helper()
		page, e := s.PlaylistContent(ContentRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, ServerID: "s", Profile: "p", ViewerFence: "f", EntityID: made.PlaylistID, Limit: 2, Cursor: cursor, Sort: q.Sort, Direction: q.Direction, Filter: q.Filter}, owner, []string{"a"})
		if e != nil {
			t.Fatal(q, e)
		}
		return page
	}
	ids := func(section ContentSection) []string {
		out := []string{}
		for _, entry := range section.Entries {
			out = append(out, entry.ID)
		}
		return out
	}
	// Own order: 0..5 in three pages, each fenced to its neighbours and the tail.
	cursor := ""
	for page := 0; page < 3; page++ {
		got := read(ResourceContentQuery{}, cursor)
		section := got.Sections[0]
		want := &ContentOrderWitness{Last: tokens[5]}
		if page > 0 {
			want.Previous = tokens[page*2-1]
		}
		if page < 2 {
			want.Next = tokens[page*2+2]
		}
		if !reflect.DeepEqual(ids(section), tokens[page*2:page*2+2]) || section.Start != page*2 || section.TotalCount != 6 || !reflect.DeepEqual(section.Order, want) {
			t.Fatalf("page %d: %v start %d total %d order %+v", page, ids(section), section.Start, section.TotalCount, section.Order)
		}
		if got.Query.Sort != "position" || got.Query.Direction != "asc" || got.Query.Category != "all" || len(got.Filters) != 1 || got.Filters[0].Options[0].Count != 6 {
			t.Fatalf("query %+v filters %+v", got.Query, got.Filters)
		}
		cursor = section.NextCursor
	}
	if cursor != "" {
		t.Fatal("own order did not end")
	}
	// Title order: visible titles ascending, the hidden occurrence last, no witness.
	first := read(ResourceContentQuery{Sort: "title"}, "")
	second := read(ResourceContentQuery{Sort: "title"}, first.Sections[0].NextCursor)
	third := read(ResourceContentQuery{Sort: "title"}, second.Sections[0].NextCursor)
	order := append(append(ids(first.Sections[0]), ids(second.Sections[0])...), ids(third.Sections[0])...)
	if want := []string{tokens[5], tokens[4], tokens[3], tokens[1], tokens[0], tokens[2]}; !reflect.DeepEqual(order, want) || first.Sections[0].Order != nil || third.Sections[0].Start != 4 {
		t.Fatal("title order", order, want, third.Sections[0].Start)
	}
	// Descending keeps the hidden occurrence last too.
	down := read(ResourceContentQuery{Sort: "title", Direction: "desc"}, "")
	more := read(ResourceContentQuery{Sort: "title", Direction: "desc"}, down.Sections[0].NextCursor)
	last := read(ResourceContentQuery{Sort: "title", Direction: "desc"}, more.Sections[0].NextCursor)
	if got := ids(last.Sections[0]); got[1] != tokens[2] || ids(down.Sections[0])[0] != tokens[0] {
		t.Fatal("descending title order", ids(down.Sections[0]), got)
	}
	// Every occurrence ties on length here: the pages still cover each exactly once.
	seen := map[string]bool{}
	for page, cursor := 0, ""; page == 0 || cursor != ""; page++ {
		got := read(ResourceContentQuery{Sort: "duration"}, cursor)
		for _, id := range ids(got.Sections[0]) {
			if seen[id] {
				t.Fatal("tied occurrence repeated", id)
			}
			seen[id] = true
		}
		cursor = got.Sections[0].NextCursor
	}
	if len(seen) != len(tokens) {
		t.Fatal("tied occurrences skipped", len(seen))
	}
	// A filter never matches a hidden occurrence and has no witness.
	unwatched := read(ResourceContentQuery{Filter: "unwatched"}, "")
	if unwatched.Sections[0].TotalCount != 5 || unwatched.Sections[0].Order != nil {
		t.Fatal("unwatched", unwatched.Sections[0].TotalCount, unwatched.Sections[0].Order)
	}
	for _, q := range []ResourceContentQuery{{Sort: "position", Direction: "desc"}, {Sort: "random"}, {Filter: "favorites"}} {
		if _, e = s.PlaylistContent(ContentRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, ServerID: "s", Profile: "p", ViewerFence: "f", EntityID: made.PlaylistID, Sort: q.Sort, Direction: q.Direction, Filter: q.Filter}, owner, []string{"a"}); e == nil {
			t.Fatal("admitted", q)
		}
	}
	// Move to bottom from the first page, with only its witnessed tail.
	top := read(ResourceContentQuery{}, "")
	tail := top.Sections[0].Order.Last
	if _, e = s.MutatePlaylist(owner, made.PlaylistID, "reorder", "", PlaylistMutation{OperationID: "bottom", ExpectedRevision: *top.PlaylistRevision, EntryIDs: []string{tokens[0]}, AfterEntryID: &tail}, nil); e != nil {
		t.Fatal(e)
	}
	moved := read(ResourceContentQuery{}, "")
	if moved.Sections[0].Order.Last != tokens[0] || ids(moved.Sections[0])[0] != tokens[1] {
		t.Fatal("move to bottom", ids(moved.Sections[0]), moved.Sections[0].Order)
	}
	// The witness is fenced: the old revision cannot place an entry.
	if _, e = s.MutatePlaylist(owner, made.PlaylistID, "reorder", "", PlaylistMutation{OperationID: "stale", ExpectedRevision: *top.PlaylistRevision, EntryIDs: []string{tokens[1]}, AfterEntryID: &tail}, nil); e == nil {
		t.Fatal("stale witness admitted")
	}
}

func TestCollectionContentQueryStartAndOrderWitnesses(t *testing.T) {
	db, c, s := phase34PlaylistDB(t, filepath.Join(t.TempDir(), "db"))
	defer db.Close()
	lib := c.Library("a", "A", "movie", "/a")
	add := []string{}
	for _, title := range []string{"Charlie", "Alpha", "Bravo"} {
		add = append(add, c.Movie(lib, "/a/"+title+".mkv", title, 2000).Public)
	}
	c.Drain()
	a := ResourceActor{Authority: "local", AccountID: "owner", ProfileID: "p"}
	noop := func(*sql.Tx) error { return nil }
	name := "Collection"
	made, e := s.MutateSavedResource(a, "", "create", SavedResourceMutation{OperationID: "create", Kind: "collection", Name: &name}, noop)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.MutateSavedResource(a, made.ResourceID, "entries", SavedResourceMutation{OperationID: "add", ExpectedRevision: made.Revision, AddItemIDs: add}, noop); e != nil {
		t.Fatal(e)
	}
	viewer := Viewer{Libraries: []string{"a"}}
	read := func(q ResourceContentQuery, cursor string) SavedResourceContent {
		t.Helper()
		out, e := s.SavedResourceContent(viewer, "s", "f", made.ResourceID, a, q, cursor, 2)
		if e != nil {
			t.Fatal(q, e)
		}
		return out
	}
	first := read(ResourceContentQuery{}, "")
	second := read(ResourceContentQuery{}, first.NextCursor)
	if first.Start != 0 || second.Start != 2 || first.Order == nil || first.Order.Previous != "" || first.Order.Next != second.Entries[0].ID || first.Order.Last != second.Entries[0].ID {
		t.Fatalf("first %+v second %+v", first.Order, second.Order)
	}
	if second.Order == nil || second.Order.Previous != first.Entries[1].ID || second.Order.Next != "" || second.Order.Last != second.Entries[0].ID {
		t.Fatalf("second %+v", second.Order)
	}
	if first.Query == nil || first.Query.Sort != "position" || first.TotalCount == nil || *first.TotalCount != 3 || len(first.Sorts) != len(resourceContentSorts) {
		t.Fatal(first.Query, first.TotalCount)
	}
	// Every entry has the same year: the second page continues past the tie.
	byYear := read(ResourceContentQuery{Sort: "year"}, "")
	if more := read(ResourceContentQuery{Sort: "year"}, byYear.NextCursor); len(more.Entries) != 1 || more.Entries[0].ID == byYear.Entries[0].ID || more.Entries[0].ID == byYear.Entries[1].ID {
		t.Fatal("tied year skipped or repeated", byYear.Entries, more.Entries)
	}
	titled := read(ResourceContentQuery{Sort: "title"}, "")
	rest := read(ResourceContentQuery{Sort: "title"}, titled.NextCursor)
	if titled.Order != nil || titled.Entries[0].Media.Title != "Alpha" || titled.Entries[1].Media.Title != "Bravo" || rest.Entries[0].Media.Title != "Charlie" || rest.Start != 2 {
		t.Fatal("title order", titled.Entries, rest.Entries)
	}
}
