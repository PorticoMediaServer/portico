package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"testing"
)

func allow(*sql.Tx) error { return nil }

func TestSavedCollectionVisibilityAndEntryOutcomes(t *testing.T) {
	_, s, names := phase34ListFixture(t)
	pub := func(name string) string { return names[name].Public }
	owner := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "owner"}
	stranger := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "stranger"}
	name := "Shared Picks"
	private := "private"
	server := "server"
	created, e := s.MutateSavedResource(owner, "", "create", SavedResourceMutation{OperationID: "create-1", Kind: "collection", Name: &name, Visibility: &private}, allow)
	if e != nil {
		t.Fatal(e)
	}
	id := created.ResourceID
	if _, e = s.SavedResource("s", "f", id, stranger); e != sql.ErrNoRows {
		t.Fatal("private collection exposed", e)
	}
	updated, e := s.MutateSavedResource(owner, id, "update", SavedResourceMutation{OperationID: "publish-1", ExpectedRevision: created.Revision, Visibility: &server}, allow)
	if e != nil {
		t.Fatal(e)
	}
	shared, e := s.SavedResource("s", "f", id, stranger)
	if e != nil {
		t.Fatal("server visibility not readable", e)
	}
	// Visibility publishes; it never delegates editing.
	if shared.Role != "viewer" || shared.Visibility != "server" {
		t.Fatal("role or visibility", shared)
	}
	for _, action := range shared.Actions {
		if action == "entries" || action == "update" || action == "delete" {
			t.Fatal("server visibility granted a write", shared.Actions)
		}
	}
	list, e := s.SavedResources("s", "f", stranger, "collection", "", false, 40)
	if e != nil || len(list.Resources) != 1 || list.Resources[0].ID != id {
		t.Fatal("server collection missing from member list", list, e)
	}
	entries, e := s.MutateSavedResource(owner, id, "entries", SavedResourceMutation{OperationID: "entries-1", ExpectedRevision: updated.Revision, AddItemIDs: []string{pub("a"), pub("b"), "missing"}}, allow)
	if e != nil {
		t.Fatal(e)
	}
	if entries.Entries == nil || len(entries.Entries.Added) != 2 || len(entries.Entries.Failed) != 1 || entries.Entries.Failed[0].Code != "not_found" {
		t.Fatal("entry outcomes", entries.Entries)
	}
	repeat, e := s.MutateSavedResource(owner, id, "entries", SavedResourceMutation{OperationID: "entries-2", ExpectedRevision: entries.Revision, AddItemIDs: []string{pub("a")}, RemoveEntryIDs: []string{"not-an-entry"}}, allow)
	if e != nil {
		t.Fatal(e)
	}
	if len(repeat.Entries.Unchanged) != 1 || repeat.Entries.Unchanged[0] != pub("a") || len(repeat.Entries.Failed) != 1 {
		t.Fatal("repeat outcomes", repeat.Entries)
	}
	content, e := s.SavedResourceContent(Viewer{Libraries: []string{"m"}}, "s", "f", id, owner, ResourceContentQuery{}, "", 40)
	if e != nil || len(content.Entries) != 2 {
		t.Fatal("membership", content, e)
	}
	removed, e := s.MutateSavedResource(owner, id, "entries", SavedResourceMutation{OperationID: "entries-3", ExpectedRevision: repeat.Revision, RemoveEntryIDs: []string{content.Entries[0].ID}}, allow)
	if e != nil {
		t.Fatal(e)
	}
	if len(removed.Entries.Removed) != 1 || len(removed.Entries.Failed) != 0 {
		t.Fatal("removal outcomes", removed.Entries)
	}
	bad := "public"
	if _, e = s.MutateSavedResource(owner, id, "update", SavedResourceMutation{OperationID: "publish-2", ExpectedRevision: removed.Revision, Visibility: &bad}, allow); e == nil {
		t.Fatal("unknown visibility accepted")
	}
}

func TestLibraryCollectionBatchReportsEachItem(t *testing.T) {
	_, s, names := phase34ListFixture(t)
	pub := func(name string) string { return names[name].Public }
	collection, e := s.CreateCollection("m", "Night Drives")
	if e != nil {
		t.Fatal(e)
	}
	out, e := s.SetCollectionItems(collection.ID, []string{pub("a"), pub("b"), pub("outside"), "missing"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	// An item in another library and an item that is gone are both outcomes,
	// never a rejected batch.
	if len(out.Entries.Added) != 2 || len(out.Entries.Failed) != 2 || out.Collection.ItemCount != 2 {
		t.Fatal("additions", out)
	}
	again, e := s.SetCollectionItems(collection.ID, []string{pub("a")}, []string{pub("b"), pub("c")})
	if e != nil {
		t.Fatal(e)
	}
	if len(again.Entries.Unchanged) != 1 || len(again.Entries.Removed) != 1 || len(again.Entries.Failed) != 1 || again.Collection.ItemCount != 1 {
		t.Fatal("mixed batch", again.Entries, again.Collection)
	}
	if _, e = s.SetCollectionItems(collection.ID, nil, nil); e == nil {
		t.Fatal("empty batch accepted")
	}
	if _, e = s.SetCollectionItems(collection.ID, []string{pub("a"), pub("a")}, nil); e == nil {
		t.Fatal("duplicate item accepted")
	}
}

func TestHomeAutomaticCollectionsKeepOwnerDecisionsThroughRescans(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("auto", "Films", "movie", "/auto")
	a := c.Movie(lib, "/auto/a.mkv", "One", 2001)
	b := c.Movie(lib, "/auto/b.mkv", "Two", 2002)
	extra := c.Movie(lib, "/auto/extra.mkv", "Owner addition", 2003)
	late := c.Movie(lib, "/auto/late.mkv", "Late addition", 2004)
	s := New(c.DB)
	publish := func(item catalogtest.Item, name string) {
		c.Exec(`INSERT INTO screen_metadata_work(target_kind,target_id,library_id,title_seed)VALUES('item',?,'auto','Film') ON CONFLICT DO NOTHING`, item.ID)
		raw := fmt.Sprintf(`[{"kind":"collection","target":{"provider":"tmdb","type":"collection","id":"42"},"name":%q}]`, name)
		c.Exec(`INSERT INTO screen_metadata_fields(target_kind,target_id,field,value,provider,source_kind,source_url,confidence,observed_at,language,region,selection_revision)VALUES('item',?,'relations',?,'tmdb','provider','',1,'now','en','US',1) ON CONFLICT(target_kind,target_id,field) DO UPDATE SET value=excluded.value`, item.ID, raw)
	}
	sync := func() {
		for i := 0; i < 20; i++ {
			more, err := s.SyncAutomaticCollections(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !more {
				c.Drain()
				return
			}
		}
		t.Fatal("automatic job did not settle")
	}
	publish(a, "Space Collection")
	sync()
	var count int
	c.DB.QueryRow(`SELECT count(*) FROM catalog_collections`).Scan(&count)
	if count != 0 {
		t.Fatal("one film created an automatic collection")
	}
	publish(b, "Space Collection")
	sync()
	var id string
	if err := c.DB.QueryRow(`SELECT pid(e.public_id) FROM catalog_entities e JOIN catalog_collections col ON col.entity_id=e.id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	collection, err := s.Collection(id)
	if err != nil || collection.Name != "Space" || collection.ItemCount != 2 || collection.Origin != "automatic" {
		t.Fatal(collection, err)
	}
	if _, err = s.RenameCollection(id, "Owner name"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetCollectionItem(id, a.Public, false); err != nil {
		t.Fatal(err)
	}
	if err = s.SetCollectionItem(id, extra.Public, true); err != nil {
		t.Fatal(err)
	}
	collection, _ = s.Collection(id)
	if _, err = s.ReorderCollection(id, collection.Revision, []string{extra.Public, b.Public}, nil); err != nil {
		t.Fatal(err)
	}
	publish(a, "Provider renamed Collection")
	publish(b, "Provider renamed Collection")
	publish(late, "Provider renamed Collection")
	sync()
	collection, err = s.Collection(id)
	if err != nil || collection.Name != "Owner name" || collection.ItemCount != 3 {
		t.Fatal("owner decisions lost", collection, err)
	}
	var contains bool
	c.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_collection_members WHERE collection_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND item_id=?)`, id, a.ID).Scan(&contains)
	if contains {
		t.Fatal("removed film re-added by rescan")
	}
	c.Exec(`DELETE FROM screen_metadata_fields WHERE target_id=?`, b.ID)
	sync()
	collection, _ = s.Collection(id)
	if collection.ItemCount != 2 {
		t.Fatal("stale automatic member retained or owner addition removed", collection)
	}
	if err = s.DeleteCollection(id); err != nil {
		t.Fatal(err)
	}
	publish(b, "Space Collection")
	sync()
	c.DB.QueryRow(`SELECT count(*) FROM catalog_collections`).Scan(&count)
	if count != 0 {
		t.Fatal("owner-hidden collection recreated")
	}
}
