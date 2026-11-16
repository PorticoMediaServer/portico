package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestFilmCollectionsUseVisibleCountsAndPositions(t *testing.T) {
	s, owner, members := seededWith(t, 600, 2)
	limited := members[1]
	libraries := get[Libraries](t, s, owner, "/v1/libraries")
	var library string
	for _, lib := range libraries.Items {
		if lib.Kind == db.LibraryMovies {
			library = lib.ID
		}
	}
	all := get[ItemPage](t, s, owner, "/v1/items?library="+library+"&kind=movie&limit=200")
	shown := get[ItemPage](t, s, limited, "/v1/items?library="+library+"&kind=movie&limit=200")
	visible := map[string]bool{}
	for _, item := range shown.Items {
		visible[item.ID] = true
	}
	var hidden string
	for _, item := range all.Items {
		if !visible[item.ID] {
			hidden = item.ID
			break
		}
	}
	if hidden == "" || len(shown.Items) < 2 {
		t.Fatal("fixture needs two visible films and an adult film")
	}
	film := shown.Items[1].ID
	create := func(name string, ids []string) string {
		t.Helper()
		var out CollectionCreated
		if w := call(t, s, "POST", "/v1/admin/libraries/"+library+"/collections", owner,
			CollectionCreate{Title: name, ItemIDs: ids}, &out); w.Code != 200 {
			t.Fatalf("create collection: %d %s", w.Code, w.Body)
		}
		return out.ID
	}
	collection := create("Ordered", []string{hidden, shown.Items[0].ID, film})
	for token, count := range map[string]int{owner: 3, limited: 2} {
		item := get[Item](t, s, token, "/v1/items/"+film)
		if len(item.Collections) != 1 || item.Collections[0] != (FilmCollection{ID: collection, Name: "Ordered", MemberCount: count, Position: count - 1}) {
			t.Fatalf("membership: %+v, want %d visible members", item.Collections, count)
		}
		page := get[ItemPage](t, s, token, "/v1/items?collection="+collection+"&sort=order&order=asc&limit=1")
		for n := 1; n < count; n++ {
			page = get[ItemPage](t, s, token, "/v1/items?collection="+collection+"&sort=order&limit=1&cursor="+page.NextCursor)
		}
		if len(page.Items) != 1 || page.Items[0].ID != film || page.NextCursor != "" {
			t.Fatalf("the collection's own order: %+v", page)
		}
		at := get[ItemPage](t, s, token, fmt.Sprintf("/v1/items?collection=%s&sort=order&at=%d&limit=1&total=true", collection, count-1))
		if at.Total == nil || *at.Total != count || len(at.Items) != 1 || at.Items[0].ID != film {
			t.Fatalf("visible position paging/count: %+v", at)
		}
	}
	// Blocking the collection itself must hide its name, even while its film remains visible.
	private := create("Private", []string{film})
	ctx := context.Background()
	doc, err := s.db.MetadataDocument(ctx, private)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.EditMetadata(ctx, private, doc.Revision, db.MetadataEdit{Labels: new([]string{"Blocked"})}); err != nil {
		t.Fatal(err)
	}
	stored := get[Item](t, s, owner, "/v1/items/"+private)
	var tag string
	for _, label := range stored.Tags {
		if label.Name == "Blocked" {
			tag = label.ID
		}
	}
	claims, err := s.tokens.CheckAccess(limited, s.now())
	if err != nil || tag == "" {
		t.Fatalf("restriction fixture: %s %v", tag, err)
	}
	profile, err := s.db.ProfileByID(ctx, claims.UserID, claims.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	limits, err := s.db.ProfileRestrictions(ctx, claims.UserID, profile.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	limits.BlockedLabels = append(limits.BlockedLabels, tag)
	if _, err := s.db.SetProfileRestrictions(ctx, claims.UserID, profile.PublicID, limits, s.now()); err != nil {
		t.Fatal(err)
	}
	item := get[Item](t, s, limited, "/v1/items/"+film)
	if len(item.Collections) != 1 || item.Collections[0].ID != collection {
		t.Fatalf("a hidden collection leaked through its film: %+v", item.Collections)
	}
}
