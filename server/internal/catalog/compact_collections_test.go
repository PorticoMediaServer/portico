package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// tl10ListFixture replaces the legacy listFixture without editing its
// unowned source file.
func tl10ListFixture(t *testing.T) (*sql.DB, *Service, catalogtest.Names) {
	t.Helper()
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	other := c.Library("other", "Other", "movie", "/other")
	names := catalogtest.Names{
		"a":       c.Movie(movies, "/m/a.mkv", "Alpha", 2000),
		"b":       c.Movie(movies, "/m/b.mkv", "Bravo", 2001),
		"c":       c.Movie(movies, "/m/c.mkv", "Charlie", 2002),
		"outside": c.Movie(other, "/other/outside.mkv", "Outside", 2003),
	}
	c.Drain()
	return c.DB, New(c.DB), names
}

func TestCompactCollectionHeadCountAndPendingPairFence(t *testing.T) {
	db, s, names := tl10ListFixture(t)
	c, err := s.CreateCollection("m", "Films")
	if err != nil || c.ItemCount != 0 {
		t.Fatalf("create collection: %+v %v", c, err)
	}
	if err = s.SetCollectionItem(c.ID, names["a"].Public, true); err != nil {
		t.Fatal(err)
	}
	if c, err = s.Collection(c.ID); err != nil || c.ItemCount != 1 {
		t.Fatalf("interactive member count: %+v %v", c, err)
	}
	if err = s.SetCollectionItem(c.ID, names["b"].Public, true); err != nil {
		t.Fatal(err)
	}
	if current, readErr := s.Collection(c.ID); readErr != nil || current.ItemCount != 2 {
		t.Fatalf("synchronous pair count: %+v %v", current, readErr)
	}
	viewer := Viewer{Libraries: []string{"m"}}
	if current, readErr := s.VisibleCollection(context.Background(), viewer, c.ID); readErr != nil || current.ItemCount != 2 {
		t.Fatalf("synchronous collection visibility: %+v %v", current, readErr)
	}
	if listed, _, err := s.Collections("m", viewer, "", 20); err != nil || len(listed) != 1 || listed[0].ItemCount != 2 {
		t.Fatalf("synchronous pair remained in list: %+v %v", listed, err)
	}
	if entries, _, _, err := s.contentEntities(ContentRequest{View: "collections", Library: "m", Viewer: viewer}, "", 20); err != nil || len(entries) != 1 {
		t.Fatalf("synchronous pair remained in content page: %+v %v", entries, err)
	}
	catalogtest.New(t, db).Drain()
	if c, err = s.VisibleCollection(context.Background(), viewer, c.ID); err != nil || c.ItemCount != 2 {
		t.Fatalf("published pair count: %+v %v", c, err)
	}
	if entries, _, _, err := s.contentEntities(ContentRequest{View: "collections", Library: "m", Viewer: viewer}, "", 20); err != nil || len(entries) != 1 || entries[0].ID != c.ID {
		t.Fatalf("published content page: %+v %v", entries, err)
	}
	if _, err = s.RenameCollection(c.ID, "Changed"); err != nil {
		t.Fatal(err)
	}
	catalogtest.New(t, db).Drain()
	if c, err = s.Collection(c.ID); err != nil || c.Name != "Changed" {
		t.Fatalf("published collection rename: %+v %v", c, err)
	}
}

func TestCompactPlaylistHeadCountIsSynchronous(t *testing.T) {
	db, s, names := tl10ListFixture(t)
	a := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "profile"}
	name := "Queue"
	r, err := s.MutatePlaylist(a, "", "create", "", PlaylistMutation{OperationID: "create", Name: &name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutatePlaylist(a, r.PlaylistID, "add", "", PlaylistMutation{OperationID: "add", ExpectedRevision: r.Revision, ItemID: names["a"].Public}, nil); err != nil {
		t.Fatal(err)
	}
	// The count is kept by triggers in the add's own transaction.
	if p, err := s.Playlist("server", "fence", r.PlaylistID, a); err != nil || p.EntryCount != 1 {
		t.Fatalf("entry count after the add: %+v %v", p, err)
	}
	if order, err := s.PlaylistOrder("server", "fence", r.PlaylistID, a, nil); err != nil || len(order.EntryIDs) != 1 {
		t.Fatalf("synchronous playlist order: %+v %v", order, err)
	}
	catalogtest.New(t, db).Drain()
	p, err := s.Playlist("server", "fence", r.PlaylistID, a)
	if err != nil || p.EntryCount != 1 {
		t.Fatalf("published entry count: %+v %v", p, err)
	}
}

func TestRestrictedCollectionCounterPublishesVisibleMembersOnly(t *testing.T) {
	db, s, names := tl10RestrictionFixture(t)
	collection, err := s.CreateCollection("a", "Mixed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetCollectionItems(collection.ID, []string{names["kids"].Public, names["adult"].Public}, nil); err != nil {
		t.Fatal(err)
	}
	restrictions := restrictionOf(ceiling(13), true)
	viewer := Viewer{Profile: "p", Fence: "restricted", Libraries: []string{"a"}, Restrictions: restrictions}
	catalogtest.New(t, db).Drain()
	if err = s.RebuildVisibilityClass(context.Background(), "a", restrictions); err != nil {
		t.Fatal(err)
	}
	catalogtest.New(t, db).Drain()
	visible, err := s.VisibleCollection(context.Background(), viewer, collection.ID)
	if err != nil || visible.ItemCount != 1 {
		t.Fatalf("restricted detail count: %+v %v", visible, err)
	}
	listed, _, err := s.Collections("a", viewer, "", 20)
	if err != nil || len(listed) != 2 {
		t.Fatalf("restricted collection list: %+v %v", listed, err)
	}
	for _, row := range listed {
		if row.ID == collection.ID && row.ItemCount != 1 {
			t.Fatalf("restricted list count: %+v", row)
		}
	}
	page, err := s.Content(ContentRequest{Viewer: viewer, ServerID: "server", Library: "a", Profile: viewer.Profile, ViewerFence: viewer.Fence, View: "collections", Limit: 20})
	if err != nil || len(page.Sections) == 0 {
		t.Fatalf("restricted content page: %+v %v", page, err)
	}
	searched, err := s.Content(ContentRequest{Viewer: viewer, ServerID: "server", Library: "a", Profile: viewer.Profile, ViewerFence: viewer.Fence, View: "collections", Q: "Mix", Limit: 20})
	if err != nil || len(searched.Sections) == 0 || searched.Sections[0].TotalCount != 1 || len(searched.Sections[0].Entries) != 1 || searched.Sections[0].Entries[0].ID != collection.ID {
		t.Fatalf("restricted collection search: %+v %v", searched, err)
	}
	if _, err = s.SetCollectionItems(collection.ID, nil, []string{names["kids"].Public}); err != nil {
		t.Fatal(err)
	}
	if stale, readErr := s.VisibleCollection(context.Background(), viewer, collection.ID); readErr != nil || stale.ItemCount != 1 {
		t.Fatalf("queued member change did not retain the last published count: %+v %v", stale, readErr)
	}
	if _, err = s.Content(ContentRequest{Viewer: viewer, ServerID: "server", Library: "a", Profile: viewer.Profile, ViewerFence: viewer.Fence, View: "collections", Limit: 20}); err != nil {
		t.Fatalf("a queued member change failed the content page: %v", err)
	}
	catalogtest.New(t, db).Drain()
	if err = s.RebuildVisibilityClass(context.Background(), "a", restrictions); err != nil {
		t.Fatal(err)
	}
	catalogtest.New(t, db).Drain()
	if _, err = s.VisibleCollection(context.Background(), viewer, collection.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("adult-only collection remained visible: %v", err)
	}
}
