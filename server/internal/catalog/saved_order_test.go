package catalog

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func collectionOrder(t *testing.T, s *Service, id string) []string {
	t.Helper()
	rows, e := s.db.Query(`SELECT pid(i.public_id) FROM saved_resource_entries e JOIN catalog_entities i ON i.id=e.item_id WHERE e.resource_id=? ORDER BY e.order_key,e.id`, id)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var item string
		if e = rows.Scan(&item); e != nil {
			t.Fatal(e)
		}
		out = append(out, item)
	}
	return out
}

// A personal collection has its own order: new entries go at the end, the
// owner (or an editor of a shared one) moves entries, a viewer can't, and a
// move made against an older revision is refused. The listing, Undo and the
// browse group order follow the same order.
func TestCollectionManualOrder(t *testing.T) {
	_, s, names := phase34ListFixture(t)
	owner := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "owner"}
	name := "Picks"
	created, e := s.MutateSavedResource(owner, "", "create", SavedResourceMutation{OperationID: "create-order", Kind: "collection", Name: &name}, allow)
	if e != nil {
		t.Fatal(e)
	}
	id := created.ResourceID
	a, b, c := names["a"].Public, names["b"].Public, names["c"].Public
	rev := created.Revision
	for i, item := range []string{c, a, b} {
		r, e := s.MutateSavedResource(owner, id, "entries", SavedResourceMutation{OperationID: "add-" + string(rune('0'+i)), ExpectedRevision: rev, AddItemIDs: []string{item}}, allow)
		if e != nil {
			t.Fatal(e)
		}
		rev = r.Revision
	}
	if got := collectionOrder(t, s, id); !reflect.DeepEqual(got, []string{c, a, b}) {
		t.Fatalf("appended in order: %v", got)
	}
	entry := func(item string) string {
		var out string
		if e := s.db.QueryRow(`SELECT id FROM saved_resource_entries WHERE resource_id=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, id, item).Scan(&out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	move := func(actor ResourceActor, op string, expected int64, ids []string, after *string) (SavedResourceReceipt, error) {
		return s.MutateSavedResource(actor, id, "reorder", SavedResourceMutation{OperationID: op, ExpectedRevision: expected, EntryIDs: ids, AfterEntryID: after}, allow)
	}
	front := ""
	// To the front, to the end, and a swap in place.
	r, e := move(owner, "move-front", rev, []string{entry(b)}, &front)
	if e != nil {
		t.Fatal(e)
	}
	if got := collectionOrder(t, s, id); !reflect.DeepEqual(got, []string{b, c, a}) {
		t.Fatalf("to the front: %v", got)
	}
	afterB := entry(b)
	if r, e = move(owner, "move-after", r.Revision, []string{entry(a)}, &afterB); e != nil {
		t.Fatal(e)
	}
	if got := collectionOrder(t, s, id); !reflect.DeepEqual(got, []string{b, a, c}) {
		t.Fatalf("after b: %v", got)
	}
	if r, e = move(owner, "swap", r.Revision, []string{entry(c), entry(b)}, nil); e != nil {
		t.Fatal(e)
	}
	if got := collectionOrder(t, s, id); !reflect.DeepEqual(got, []string{c, a, b}) {
		t.Fatalf("a swap: %v", got)
	}
	// A move against an older revision is a conflict; the same operation replays.
	if _, e = move(owner, "stale", r.Revision-1, []string{entry(a)}, &front); !errors.Is(e, ErrPlaylistConflict) {
		t.Fatalf("stale revision: %v", e)
	}
	if again, e := move(owner, "swap", r.Revision-1, []string{entry(c), entry(b)}, nil); e != nil || again.Revision != r.Revision {
		t.Fatalf("replay %+v %v", again, e)
	}
	// Refused: an entry moved after itself, repeated entries, none, too many, unknown ones.
	self := entry(a)
	for name, bad := range map[string]SavedResourceMutation{
		"after itself": {EntryIDs: []string{self}, AfterEntryID: &self},
		"repeated":     {EntryIDs: []string{self, self}},
		"none":         {},
	} {
		bad.OperationID, bad.ExpectedRevision = "bad-"+identity.Token()[:8], r.Revision
		if _, e = s.MutateSavedResource(owner, id, "reorder", bad, allow); e == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, e = move(owner, "unknown", r.Revision, []string{"no-such-entry"}, &front); !errors.Is(e, sql.ErrNoRows) {
		t.Fatalf("unknown entry: %v", e)
	}
	// Shared: an editor moves entries, a viewer can't.
	editor := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "editor"}
	viewer := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "viewer"}
	for _, share := range []struct {
		who  ResourceActor
		role string
	}{{editor, "editor"}, {viewer, "viewer"}} {
		if r, e = s.MutateSavedResource(owner, id, "share", SavedResourceMutation{OperationID: "share-" + share.role, ExpectedRevision: r.Revision, Authority: share.who.Authority, AccountID: share.who.AccountID, ProfileID: share.who.ProfileID, Role: share.role}, allow); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = move(viewer, "viewer-move", r.Revision, []string{entry(b)}, &front); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatalf("a viewer moved an entry: %v", e)
	}
	if r, e = move(editor, "editor-move", r.Revision, []string{entry(b)}, &front); e != nil {
		t.Fatal(e)
	}
	if got := collectionOrder(t, s, id); !reflect.DeepEqual(got, []string{b, c, a}) {
		t.Fatalf("an editor's move: %v", got)
	}
	// Undo of a removal puts the entry back in its place.
	removed, e := s.MutateSavedResource(owner, id, "entries", SavedResourceMutation{OperationID: "remove-c", ExpectedRevision: r.Revision, RemoveEntryIDs: []string{entry(c)}}, allow)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.MutateSavedResource(owner, id, "entries", SavedResourceMutation{OperationID: "undo-c", ExpectedRevision: removed.Revision, UndoToken: removed.UndoToken}, allow); e != nil {
		t.Fatal(e)
	}
	if got := collectionOrder(t, s, id); !reflect.DeepEqual(got, []string{b, c, a}) {
		t.Fatalf("after Undo: %v", got)
	}
	// The group's own order across libraries follows it.
	members, args, ordered, e := acrossMembers(&BrowseNode{Field: "savedCollection", Operator: "equals", Value: id}, actorKey(owner), nil)
	if e != nil || !ordered {
		t.Fatalf("a collection offers its own order: %v %v", ordered, e)
	}
	rows, e := s.db.Query(`SELECT pid(i.public_id) FROM (`+members+`) m JOIN catalog_entities i ON i.id=m.id ORDER BY m.pos`, args...)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	got := []string{}
	for rows.Next() {
		var item string
		_ = rows.Scan(&item)
		got = append(got, item)
	}
	if !reflect.DeepEqual(got, []string{b, c, a}) {
		t.Fatalf("group order %v", got)
	}
}

// Moving entries rewrites only their own keys, however long the collection.
func TestCollectionMoveWritesOnlyTheMovedEntries(t *testing.T) {
	_, s, _ := phase34ListFixture(t)
	owner := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "owner"}
	name := "Long"
	created, e := s.MutateSavedResource(owner, "", "create", SavedResourceMutation{OperationID: "create-long", Kind: "collection", Name: &name}, allow)
	if e != nil {
		t.Fatal(e)
	}
	id := created.ResourceID
	tx, e := s.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	ids := []string{}
	for i := 0; i < 2000; i++ {
		entry := identity.Token()
		ids = append(ids, entry)
		if _, e = tx.Exec(`INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key) VALUES(?,?,NULL,?)`, entry, id, playlistEndKey(func() string {
			var last string
			_ = tx.QueryRow(`SELECT order_key FROM saved_resource_entries WHERE resource_id=? ORDER BY order_key DESC LIMIT 1`, id).Scan(&last)
			return last
		}())); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	before := map[string]string{}
	rows, _ := s.db.Query(`SELECT id,order_key FROM saved_resource_entries WHERE resource_id=?`, id)
	for rows.Next() {
		var entry, key string
		_ = rows.Scan(&entry, &key)
		before[entry] = key
	}
	rows.Close()
	after := ids[10]
	if _, e = s.MutateSavedResource(owner, id, "reorder", SavedResourceMutation{OperationID: "move-two", ExpectedRevision: created.Revision, EntryIDs: []string{ids[1500], ids[1999]}, AfterEntryID: &after}, allow); e != nil {
		t.Fatal(e)
	}
	changed := 0
	rows, _ = s.db.Query(`SELECT id,order_key FROM saved_resource_entries WHERE resource_id=? ORDER BY order_key,id`, id)
	order := []string{}
	for rows.Next() {
		var entry, key string
		_ = rows.Scan(&entry, &key)
		if before[entry] != key {
			changed++
		}
		order = append(order, entry)
	}
	rows.Close()
	if changed != 2 || order[11] != ids[1500] || order[12] != ids[1999] || order[13] != ids[11] {
		t.Fatalf("%d keys rewritten; order around the move %v", changed, order[9:14])
	}
}

// A library collection's order is the owner's: titles added go to the end, the
// owner moves them (a move against an older revision is refused), titles added
// before collections had an order get keys on the first move, and the group's
// own order (the collection page's "Collection order") follows.
func TestLibraryCollectionManualOrder(t *testing.T) {
	db, s, names := phase34ListFixture(t)
	created, e := s.CreateCollection("m", "Trilogy")
	if e != nil {
		t.Fatal(e)
	}
	a, b, c := names["a"].Public, names["b"].Public, names["c"].Public
	batch, e := s.SetCollectionItems(created.ID, []string{c, a}, nil)
	if e != nil || len(batch.Entries.Added) != 2 {
		t.Fatalf("batch %+v %v", batch, e)
	}
	if e = s.SetCollectionItem(created.ID, b, true); e != nil {
		t.Fatal(e)
	}
	order := func() []string {
		source, args, e := s.groupOrderSource(&BrowseNode{Field: "collection", Operator: "equals", Value: created.ID}, nil)
		if e != nil {
			t.Fatal(e)
		}
		rows, e := db.Query(`SELECT pid(i.public_id) FROM (`+source+`) o JOIN catalog_entities i ON i.id=o.id ORDER BY o.pos`, args...)
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			out = append(out, id)
		}
		return out
	}
	if got := order(); !reflect.DeepEqual(got, []string{c, a, b}) {
		t.Fatalf("appended in order: %v", got)
	}
	// Adding a member again keeps its place.
	if e = s.SetCollectionItem(created.ID, c, true); e != nil {
		t.Fatal(e)
	}
	current, e := s.Collection(created.ID)
	if e != nil {
		t.Fatal(e)
	}
	front := ""
	moved, e := s.ReorderCollection(created.ID, current.Revision, []string{b}, &front)
	if e != nil {
		t.Fatal(e)
	}
	if got := order(); !reflect.DeepEqual(got, []string{b, c, a}) {
		t.Fatalf("to the front: %v", got)
	}
	if moved.Revision == current.Revision {
		t.Fatal("a move leaves the revision where it was")
	}
	if _, e = s.ReorderCollection(created.ID, current.Revision, []string{a}, &front); !errors.Is(e, ErrPlaylistConflict) {
		t.Fatalf("stale move: %v", e)
	}
	if _, e = s.ReorderCollection(created.ID, moved.Revision, []string{names["outside"].Public}, &front); !errors.Is(e, sql.ErrNoRows) {
		t.Fatalf("a title outside the collection: %v", e)
	}
	// Titles from before collections had an order (one empty key) get keys on the first move.
	if _, e = db.Exec(`UPDATE catalog_collection_members SET order_key='' WHERE collection_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, created.ID); e != nil {
		t.Fatal(e)
	}
	before := order()
	current, _ = s.Collection(created.ID)
	afterFirst := before[0]
	if _, e = s.ReorderCollection(created.ID, current.Revision, []string{before[2]}, &afterFirst); e != nil {
		t.Fatal(e)
	}
	if got := order(); !reflect.DeepEqual(got, []string{before[0], before[2], before[1]}) {
		t.Fatalf("legacy members after a move: %v (from %v)", got, before)
	}
}
