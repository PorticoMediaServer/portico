package catalog

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"reflect"
	"testing"
)

func TestQueueKeysMatchEveryBrowseOrder(t *testing.T) {
	db, s, _ := browseFixture(t)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	for _, definition := range browseSorts {
		if !containsString(definition.ApplicableKinds, "movie") || definition.Ordering == -3 {
			continue
		}
		for _, direction := range definition.Directions {
			t.Run(definition.ID+"-"+direction, func(t *testing.T) {
				selection := []BrowseSortSelection{{Field: definition.ID, Direction: direction}}
				if definition.ID == "random" {
					selection[0].Seed = 2147483648
				}
				page, err := s.BrowseEntities(v, BrowseRequest{Library: "a", Pivot: "movies", Sort: selection, Limit: 100})
				if err != nil {
					t.Fatal(err)
				}
				tx, close, err := dbwork.BeginRead(context.Background(), db)
				if err != nil {
					t.Fatal(err)
				}
				defer close()
				plan, err := s.QueueKeysTx(context.Background(), tx, v, ResourceActor{}, &QueueQuery{LibraryID: "a", Pivot: "movies", Sort: selection}, "", nil)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := tx.QueryContext(context.Background(), plan.SQL, plan.Args...)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				got := []string{}
				for rows.Next() {
					var id string
					if err = rows.Scan(&id); err != nil {
						t.Fatal(err)
					}
					got = append(got, id)
				}
				if err = rows.Err(); err != nil {
					t.Fatal(err)
				}
				want := []string{}
				for _, entry := range page.Entries {
					want = append(want, entry.ID)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("queue order %v; browse %v", got, want)
				}
			})
		}
	}
}

func TestQueueKeysResolveSavedViewsAndSharedCollections(t *testing.T) {
	db, s, names := browseFixture(t)
	actor := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "profile"}
	viewer := Viewer{Profile: actorKey(actor), Fence: "f", Libraries: []string{"a"}}
	name := "Recent films"
	definition := &SavedViewDefinition{LibraryID: "a", Pivot: "movies", Sort: []BrowseSortSelection{{Field: "year", Direction: "desc"}}, Presentation: "grid", Query: &BrowseNode{Field: "year", Operator: "at-least", Value: 2000}}
	created, err := s.MutateSavedResource(actor, "", "create", SavedResourceMutation{OperationID: "saved-queue-view-0001", Kind: "view", Name: &name, Definition: definition}, func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	read := func(a ResourceActor, id string) ([]string, error) {
		tx, close, err := dbwork.BeginRead(context.Background(), db)
		if err != nil {
			return nil, err
		}
		defer close()
		plan, err := s.QueueKeysTx(context.Background(), tx, viewer, a, nil, id, nil)
		if err != nil {
			return nil, err
		}
		rows, err := tx.QueryContext(context.Background(), plan.SQL, plan.Args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		ids := []string{}
		for rows.Next() {
			var key string
			if err = rows.Scan(&key); err != nil {
				return nil, err
			}
			ids = append(ids, key)
		}
		return ids, rows.Err()
	}
	got, err := read(actor, created.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{names["m4"].Public, names["m3"].Public, names["m2"].Public}) {
		t.Fatalf("saved-view order: %v", got)
	}
	other := ResourceActor{Authority: "local", AccountID: "other", ProfileID: "other"}
	if _, err = read(other, created.ResourceID); err != sql.ErrNoRows {
		t.Fatalf("private saved view leaked: %v", err)
	}
	visibility := "server"
	collection, err := s.MutateSavedResource(actor, "", "create", SavedResourceMutation{OperationID: "saved-queue-list-0001", Kind: "collection", Name: &name, Visibility: &visibility}, func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.MutateSavedResource(actor, collection.ResourceID, "entries", SavedResourceMutation{OperationID: "saved-queue-entries-01", ExpectedRevision: collection.Revision, AddItemIDs: []string{names["m2"].Public, names["m1"].Public}}, func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	got, err = read(other, collection.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	// A collection plays in its own order: the order entries were added in...
	if !reflect.DeepEqual(got, []string{names["m2"].Public, names["m1"].Public}) {
		t.Fatalf("shared collection membership %v", got)
	}
	// ...or wherever its owner has moved them since.
	var second string
	if err = db.QueryRow(`SELECT id FROM saved_resource_entries WHERE resource_id=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, collection.ResourceID, names["m1"].Public).Scan(&second); err != nil {
		t.Fatal(err)
	}
	front := ""
	if _, err = s.MutateSavedResource(actor, collection.ResourceID, "reorder", SavedResourceMutation{OperationID: "saved-queue-order-01", ExpectedRevision: added.Revision, EntryIDs: []string{second}, AfterEntryID: &front}, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got, err = read(other, collection.ResourceID); err != nil || !reflect.DeepEqual(got, []string{names["m1"].Public, names["m2"].Public}) {
		t.Fatalf("after a move %v %v", got, err)
	}
}
