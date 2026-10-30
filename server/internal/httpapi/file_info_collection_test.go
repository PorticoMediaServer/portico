package httpapi

import (
	"context"
	"net/http"
	"testing"
)

func TestFileInfoCollectionDetailOpeningAndHeadLifetime(t *testing.T) {
	f := newFileInfoWindowsFixture(t)
	svc := f.d.Catalog
	owner, err := svc.CreateCollection("movies", "Owner collection")
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.SetCollectionItem(owner.ID, f.base.item.Public, true); err != nil {
		t.Fatal(err)
	}
	second := f.base.c.Movie(f.base.c.Handle("movies"), "/movies/collection-second.mkv", "Second", 2001)
	// These are ordinary normalized provider relation facts; the real automatic
	// producer creates the collection entity and its head through normal INSERT.
	for _, item := range []int64{f.base.item.ID, second.ID} {
		f.base.c.Exec(`INSERT INTO catalog_automatic_members(item_id,library_id,provider,provider_id,name) VALUES(?,?,'tmdb','collection-lifetime','Automatic collection')`, item, f.base.c.Handle("movies"))
	}
	for i := 0; i < 20; i++ {
		more, e := svc.SyncAutomaticCollections(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if !more {
			break
		}
		if i == 19 {
			t.Fatal("automatic collection producer did not settle")
		}
	}
	f.base.c.Drain()
	var automatic string
	if err = f.base.db.QueryRow(`SELECT pid(e.public_id) FROM catalog_automatic_collections c JOIN catalog_entities e ON e.id=c.entity_id WHERE c.provider='tmdb' AND c.provider_id='collection-lifetime'`).Scan(&automatic); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{owner.ID, automatic} {
		internal := f.base.c.ID(id)
		var heads, dirty int
		if err = f.base.db.QueryRow(`SELECT count(*) FROM artwork_entity_heads WHERE kind='item' AND entity_id=? AND length(incarnation)=32`, internal).Scan(&heads); err != nil || heads != 1 {
			t.Fatal("collection insertion has no lifetime head", id, heads, err)
		}
		if err = f.base.db.QueryRow(`SELECT count(*) FROM artwork_dirty WHERE kind='item' AND entity_id=?`, internal).Scan(&dirty); err != nil || dirty != 0 {
			t.Fatal("collection nonce queued artwork work", dirty, err)
		}
		detail := f.lane(t, "/v1/items/"+id+"/detail?related=none", nil)
		if detail.Code != http.StatusOK {
			t.Fatal("collection Detail", detail.Code, detail.Body.String())
		}
		opening := fileInfoBody[struct {
			Opening string `json:"fileInfoOpening"`
		}](t, detail).Opening
		if !fileInfoTextToken(opening, fileInfoOpeningText) {
			t.Fatal("collection Detail did not mint opening", detail.Body.String())
		}
		files := f.lane(t, fileInfoLanePath(id, "/file-info", opening), nil)
		if files.Code != http.StatusOK {
			t.Fatal("collection FileInfo", files.Code, files.Body.String())
		}
		if err = svc.DeleteCollection(id); err != nil {
			t.Fatal(err)
		}
		if err = f.base.db.QueryRow(`SELECT count(*) FROM artwork_entity_heads WHERE kind='item' AND entity_id=?`, internal).Scan(&heads); err != nil || heads != 0 {
			t.Fatal("collection deletion retained nonce", heads, err)
		}
	}
}
