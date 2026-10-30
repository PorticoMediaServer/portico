package catalog

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func playlistOrder(t *testing.T, s *Service, playlist string) []string {
	t.Helper()
	rows, e := s.db.Query(`SELECT e.token FROM catalog_playlist_entries e JOIN catalog_playlists p ON p.id=e.playlist_id WHERE p.token=? ORDER BY e.order_key,e.id`, playlist)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var token string
		if e = rows.Scan(&token); e != nil {
			t.Fatal(e)
		}
		out = append(out, token)
	}
	return out
}

// Removing one copy of a title that a playlist holds twice, then Undo, puts
// back that copy, with its own entry id, where it was. The token is spent by
// the Undo, belongs to the person who removed, and lapses.
func TestPlaylistEntryRemovalUndoesInPlace(t *testing.T) {
	_, s, names := phase34ListFixture(t)
	owner := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "owner"}
	name := "Twice"
	a, b := names["a"].Public, names["b"].Public
	created, e := s.MutatePlaylist(owner, "", "create", "", PlaylistMutation{OperationID: "create-twice", Name: &name, ItemIDs: []string{a, b, a, b}}, allow)
	if e != nil {
		t.Fatal(e)
	}
	id := created.PlaylistID
	before := playlistOrder(t, s, id)
	removed, e := s.MutatePlaylist(owner, id, "remove", before[2], PlaylistMutation{OperationID: "remove-third", ExpectedRevision: created.Revision}, allow)
	if e != nil {
		t.Fatal(e)
	}
	if removed.UndoToken == "" || removed.UndoUntil == "" {
		t.Fatalf("a removal answers an undo token: %+v", removed)
	}
	if got := playlistOrder(t, s, id); len(got) != 3 {
		t.Fatalf("after the removal %v", got)
	}
	stranger := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "stranger"}
	if _, e = takeEntryUndoFor(s, stranger, "playlist", id, removed.UndoToken); !errors.Is(e, sql.ErrNoRows) {
		t.Fatalf("someone else's token: %v", e)
	}
	restored, e := s.MutatePlaylist(owner, id, "add", "", PlaylistMutation{OperationID: "undo-third", ExpectedRevision: removed.Revision, UndoToken: removed.UndoToken}, allow)
	if e != nil {
		t.Fatal(e)
	}
	if restored.EntryID != before[2] {
		t.Fatalf("the same occurrence comes back: %q, wanted %q", restored.EntryID, before[2])
	}
	if got := playlistOrder(t, s, id); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatalf("order after Undo %v, wanted %v", got, before)
	}
	var count int64
	if e = s.db.QueryRow(`SELECT entry_count FROM catalog_playlists WHERE token=?`, id).Scan(&count); e != nil || count != 4 {
		t.Fatalf("entry count %d %v", count, e)
	}
	if _, e = s.MutatePlaylist(owner, id, "add", "", PlaylistMutation{OperationID: "undo-again", ExpectedRevision: restored.Revision, UndoToken: removed.UndoToken}, allow); !errors.Is(e, sql.ErrNoRows) {
		t.Fatalf("a spent token: %v", e)
	}
	again, e := s.MutatePlaylist(owner, id, "remove", before[0], PlaylistMutation{OperationID: "remove-first", ExpectedRevision: restored.Revision}, allow)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`UPDATE saved_entry_undo SET expires_ms=1`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.MutatePlaylist(owner, id, "add", "", PlaylistMutation{OperationID: "undo-late", ExpectedRevision: again.Revision, UndoToken: again.UndoToken}, allow); !errors.Is(e, sql.ErrNoRows) {
		t.Fatalf("a lapsed token: %v", e)
	}
	if _, e = s.MutatePlaylist(owner, id, "add", "", PlaylistMutation{OperationID: "undo-and-add", ExpectedRevision: again.Revision, UndoToken: again.UndoToken, ItemID: a}, allow); e == nil {
		t.Fatal("an undo token with an item")
	}
}

// A personal collection's removal undoes the same way: the entries return
// under their own ids, so they keep their places.
func TestCollectionEntryRemovalUndoes(t *testing.T) {
	_, s, names := phase34ListFixture(t)
	owner := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "owner"}
	name := "Picks"
	created, e := s.MutateSavedResource(owner, "", "create", SavedResourceMutation{OperationID: "create-picks", Kind: "collection", Name: &name}, allow)
	if e != nil {
		t.Fatal(e)
	}
	id := created.ResourceID
	added, e := s.MutateSavedResource(owner, id, "entries", SavedResourceMutation{OperationID: "add-picks", ExpectedRevision: created.Revision, AddItemIDs: []string{names["a"].Public, names["b"].Public}}, allow)
	if e != nil {
		t.Fatal(e)
	}
	var entry string
	if e = s.db.QueryRow(`SELECT id FROM saved_resource_entries WHERE resource_id=? ORDER BY id LIMIT 1`, id).Scan(&entry); e != nil {
		t.Fatal(e)
	}
	removed, e := s.MutateSavedResource(owner, id, "entries", SavedResourceMutation{OperationID: "remove-pick", ExpectedRevision: added.Revision, RemoveEntryIDs: []string{entry}}, allow)
	if e != nil || removed.UndoToken == "" {
		t.Fatalf("removal %+v %v", removed, e)
	}
	restored, e := s.MutateSavedResource(owner, id, "entries", SavedResourceMutation{OperationID: "undo-pick", ExpectedRevision: removed.Revision, UndoToken: removed.UndoToken}, allow)
	if e != nil {
		t.Fatal(e)
	}
	if restored.Entries == nil || len(restored.Entries.Added) != 1 || restored.Entries.Added[0] != entry {
		t.Fatalf("restored %+v", restored.Entries)
	}
	var n int
	if e = s.db.QueryRow(`SELECT count(*) FROM saved_resource_entries WHERE resource_id=? AND id=?`, id, entry).Scan(&n); e != nil || n != 1 {
		t.Fatalf("the entry is back under its id: %d %v", n, e)
	}
	if _, e = s.MutateSavedResource(owner, id, "entries", SavedResourceMutation{OperationID: "undo-mixed", ExpectedRevision: restored.Revision, UndoToken: removed.UndoToken, RemoveEntryIDs: []string{entry}}, allow); e == nil {
		t.Fatal("an undo token with other changes")
	}
}

func takeEntryUndoFor(s *Service, a ResourceActor, kind, resource, token string) ([]removedEntry, error) {
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	return takeEntryUndo(tx, actorKey(a), kind, resource, token)
}
