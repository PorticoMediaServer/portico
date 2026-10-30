package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func phase34PlaylistOrderFixture(t *testing.T) (*catalogtest.Catalog, *Service, catalogtest.Item) {
	t.Helper()
	c := catalogtest.Open(t)
	library := c.Library("movies", "Movies", "movie", "/movies")
	item := c.Movie(library, "/movies/title.mkv", "Title", 2020)
	c.Drain()
	return c, New(c.DB), item
}

func TestPlaylistOrderRightsSnapshotAndCAS(t *testing.T) {
	_, s, item := phase34PlaylistOrderFixture(t)
	owner := ResourceActor{"local", "owner", "p"}
	editor := ResourceActor{"local", "editor", "p"}
	viewer := ResourceActor{"local", "viewer", "p"}
	name := "Order"
	r, e := s.MutatePlaylist(owner, "", "create", "", PlaylistMutation{OperationID: "create", Name: &name}, nil)
	if e != nil {
		t.Fatal(e)
	}
	firstEntry, e := s.MutatePlaylist(owner, r.PlaylistID, "add", "", PlaylistMutation{OperationID: "add-a", ExpectedRevision: r.Revision, ItemID: item.Public}, nil)
	if e != nil {
		t.Fatal(e)
	}
	secondEntry, e := s.MutatePlaylist(owner, r.PlaylistID, "add", "", PlaylistMutation{OperationID: "add-b", ExpectedRevision: firstEntry.Revision, ItemID: item.Public}, nil)
	if e != nil {
		t.Fatal(e)
	}
	shared, e := s.MutatePlaylist(owner, r.PlaylistID, "share", "", PlaylistMutation{OperationID: "share-editor", ExpectedRevision: secondEntry.Revision, Authority: editor.Authority, AccountID: editor.AccountID, ProfileID: editor.ProfileID, Role: "editor"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.MutatePlaylist(owner, r.PlaylistID, "share", "", PlaylistMutation{OperationID: "share-viewer", ExpectedRevision: shared.Revision, Authority: viewer.Authority, AccountID: viewer.AccountID, ProfileID: viewer.ProfileID, Role: "viewer"}, nil); e != nil {
		t.Fatal(e)
	}
	phase34SettlePlaylistProjection(t, s.db)
	id := r.PlaylistID
	for _, a := range []ResourceActor{owner, editor} {
		out, e := s.PlaylistOrder("server", "fence", id, a, nil)
		if e != nil || out.Revision != shared.Revision+1 || len(out.EntryIDs) != 2 || out.EntryIDs[0] != firstEntry.EntryID {
			t.Fatal(out, e)
		}
		raw, _ := json.Marshal(out)
		var object map[string]any
		json.Unmarshal(raw, &object)
		if len(object) != 5 {
			t.Fatal("unexpected metadata", string(raw))
		}
	}
	if _, e = s.PlaylistOrder("server", "fence", id, viewer, nil); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("viewer gained order edit read", e)
	}
	if _, e = s.PlaylistOrder("server", "fence", id, ResourceActor{"hosted", "editor", "p"}, nil); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("cross-authority access", e)
	}
	if _, e = s.PlaylistOrder("server", "fence", id, owner, func(*sql.Tx) error { return identity.ErrUnauthorized }); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("transaction auth ignored", e)
	}
	snapshot, e := s.PlaylistOrder("server", "fence", id, editor, nil)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.MutatePlaylist(owner, id, "reorder", "", PlaylistMutation{OperationID: "reorder", ExpectedRevision: snapshot.Revision, EntryIDs: []string{secondEntry.EntryID, firstEntry.EntryID}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CheckPlaylistOrder(id, editor, snapshot.Revision); !errors.Is(e, ErrStaleContinuation) {
		t.Fatal("snapshot fence ignored", e)
	}
	if _, e = s.MutatePlaylist(editor, id, "reorder", "", PlaylistMutation{OperationID: "stale", ExpectedRevision: snapshot.Revision, EntryIDs: snapshot.EntryIDs}, nil); !errors.Is(e, ErrPlaylistConflict) {
		t.Fatal("stale snapshot overwrote edit", e)
	}
	if _, e = s.MutatePlaylist(owner, id, "unshare", "", PlaylistMutation{OperationID: "unshare-editor", ExpectedRevision: snapshot.Revision + 1, Authority: editor.Authority, AccountID: editor.AccountID, ProfileID: editor.ProfileID}, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.CheckPlaylistOrder(id, editor, snapshot.Revision+2); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("share revocation ignored", e)
	}
}

func TestPlaylistOrderConcurrentWALReadIsCoherent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	db, s, item := phase34OpenPlaylistDB(t, path)
	defer db.Close()
	a := ResourceActor{"local", "a", "p"}
	name := "Concurrent"
	r, e := s.MutatePlaylist(a, "", "create", "", PlaylistMutation{OperationID: "create", Name: &name}, nil)
	if e != nil {
		t.Fatal(e)
	}
	first, e := s.MutatePlaylist(a, r.PlaylistID, "add", "", PlaylistMutation{OperationID: "add-a", ExpectedRevision: 1, ItemID: item.Public}, nil)
	if e != nil {
		t.Fatal(e)
	}
	secondEntry, e := s.MutatePlaylist(a, r.PlaylistID, "add", "", PlaylistMutation{OperationID: "add-b", ExpectedRevision: first.Revision, ItemID: item.Public}, nil)
	if e != nil {
		t.Fatal(e)
	}
	phase34SettlePlaylistProjection(t, db)
	writerDB, e := persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer writerDB.Close()
	writer := New(writerDB)
	var wg sync.WaitGroup
	fail := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := int64(1); i <= 60; i++ {
			order := []string{first.EntryID, secondEntry.EntryID}
			if i%2 == 1 {
				order = []string{secondEntry.EntryID, first.EntryID}
			}
			if _, e := writer.MutatePlaylist(a, r.PlaylistID, "reorder", "", PlaylistMutation{OperationID: fmt.Sprintf("order-%d", i), ExpectedRevision: i + 2, EntryIDs: order}, nil); e != nil {
				fail <- e
				return
			}
		}
	}()
	for i := 0; i < 100; i++ {
		out, e := s.PlaylistOrder("s", "f", r.PlaylistID, a, nil)
		if e != nil {
			t.Fatal(e)
		}
		want := first.EntryID
		if (out.Revision-3)%2 == 1 {
			want = secondEntry.EntryID
		}
		if len(out.EntryIDs) != 2 || out.EntryIDs[0] != want {
			t.Fatal("torn order/revision snapshot", out)
		}
	}
	wg.Wait()
	select {
	case e := <-fail:
		t.Fatal(e)
	default:
	}
}

func TestPlaylistOrderWindowsBeyondThousand(t *testing.T) {
	_, s, item := phase34PlaylistOrderFixture(t)
	db := s.db
	a := ResourceActor{"local", "a", "p"}
	name := "Windows"
	r, e := s.MutatePlaylist(a, "", "create", "", PlaylistMutation{OperationID: "create", Name: &name}, nil)
	if e != nil {
		t.Fatal(e)
	}
	entryIDs := make([]string, 1205)
	revision := r.Revision
	for i := range entryIDs {
		added, err := s.MutatePlaylist(a, r.PlaylistID, "add", "", PlaylistMutation{OperationID: fmt.Sprintf("add-%04d", i+1), ExpectedRevision: revision, ItemID: item.Public}, nil)
		if err != nil {
			t.Fatal(i, err)
		}
		entryIDs[i] = added.EntryID
		revision = added.Revision
	}
	phase34SettlePlaylistProjection(t, db)
	cursor := ""
	seen := map[string]bool{}
	for {
		page, e := s.PlaylistOrder("s", "f", r.PlaylistID, a, nil, PlaylistOrderWindow{Cursor: cursor, Limit: 77})
		if e != nil {
			t.Fatal(e)
		}
		if len(page.EntryIDs) > 77 {
			t.Fatal("unbounded page")
		}
		for _, id := range page.EntryIDs {
			if seen[id] {
				t.Fatal("duplicate", id)
			}
			seen[id] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 1205 {
		t.Fatal(len(seen))
	}
	var original string
	if e = db.QueryRow(`SELECT order_key FROM catalog_playlist_entries WHERE token=?`, entryIDs[1]).Scan(&original); e != nil {
		t.Fatal(e)
	}
	first, e := s.PlaylistOrder("s", "f", r.PlaylistID, a, nil, PlaylistOrderWindow{Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	front := ""
	mutation := PlaylistMutation{OperationID: "move", ExpectedRevision: revision, EntryIDs: []string{entryIDs[1204], entryIDs[1203]}, AfterEntryID: &front}
	if _, e = s.MutatePlaylist(a, r.PlaylistID, "reorder", "", mutation, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = s.MutatePlaylist(a, r.PlaylistID, "reorder", "", mutation, nil); e != nil {
		t.Fatal("replay", e)
	}
	after, e := s.PlaylistOrder("s", "f", r.PlaylistID, a, nil, PlaylistOrderWindow{Limit: 3})
	if e != nil || after.EntryIDs[0] != entryIDs[1204] || after.EntryIDs[1] != entryIDs[1203] || after.EntryIDs[2] != entryIDs[0] {
		t.Fatal(after, e)
	}
	var unchanged string
	if e = db.QueryRow(`SELECT order_key FROM catalog_playlist_entries WHERE token=?`, entryIDs[1]).Scan(&unchanged); e != nil || unchanged != original {
		t.Fatal("rewrote unselected entry", unchanged, e)
	}
	if _, e = s.PlaylistOrder("s", "f", r.PlaylistID, a, nil, PlaylistOrderWindow{Limit: 1, Cursor: first.NextCursor}); e != nil {
		t.Fatal("cursor did not continue after publication", e)
	}
}

func phase34OpenPlaylistDB(t *testing.T, path string) (*sql.DB, *Service, catalogtest.Item) {
	t.Helper()
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, db)
	library := c.Library("movies", "Movies", "movie", "/movies")
	item := c.Movie(library, "/movies/title.mkv", "Title", 2020)
	c.Drain()
	return db, New(db), item
}
