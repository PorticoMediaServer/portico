package db_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A library's collection has no rating of its own. A profile under a rating limit sees one
// only while it holds a film the profile may see, and then only those films: in the library,
// on the collection's page, in a list filtered by it, and in a queue played from it.
func TestCollectionShowsAChildOnlyWhatItMaySee(t *testing.T) {
	l := newLibrary(t, 300)
	owner, child := l.all, l.viewers[9] // the seeder's profile limited to a rating of 13
	var films db.Library
	all, err := l.d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, lib := range all {
		if lib.Kind == db.LibraryMovies {
			films = lib
		}
	}
	family, adult := 7, 18
	mixed, grown := db.ItemKey{Kind: db.KeyName, Key: "mixed"}, db.ItemKey{Kind: db.KeyName, Key: "grown"}
	film := func(key, title string, age *int, of db.ItemKey) db.ImportItem {
		return db.ImportItem{Kind: db.KindMovie, Keys: []db.ItemKey{{Kind: db.KeyName, Key: key}}, Title: title, RatingAge: age,
			ExtraOf: []db.ItemKey{of}, ExtraKind: db.RelationCollectionMember}
	}
	if _, err := l.d.Import(ctx, db.ImportBatch{LibraryID: films.ID, PathID: films.Paths[0].ID, Scan: 2, Dirs: []db.ImportDir{{}}, Items: []db.ImportItem{
		{Kind: db.KindCollection, Keys: []db.ItemKey{mixed}, Title: "Mixed Collection"},
		{Kind: db.KindCollection, Keys: []db.ItemKey{grown}, Title: "Grown Collection"},
		film("m1", "Mixed One", &family, mixed),
		film("m2", "Mixed Two", &adult, mixed),
		film("g1", "Grown One", &adult, grown),
	}}); err != nil {
		t.Fatal(err)
	}
	collections := func(v db.Viewer) (ids, titles []string) {
		for _, it := range everything(t, l.d, v, db.ItemQuery{Library: films.PublicID, Kinds: []db.ItemKind{db.KindCollection}}) {
			ids, titles = append(ids, it.PublicID), append(titles, it.Title)
		}
		return ids, titles
	}
	ids, titles := collections(owner)
	if !slices.Equal(titles, []string{"Grown Collection", "Mixed Collection"}) {
		t.Fatalf("the owner sees %v", titles)
	}
	grownID, mixedID := ids[0], ids[1]
	if _, titles := collections(child); !slices.Equal(titles, []string{"Mixed Collection"}) {
		t.Fatalf("the child sees %v", titles)
	}
	if _, err := l.d.Item(ctx, child, grownID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("the child opened a collection of films it may not see: %v", err)
	}
	inIt := func(v db.Viewer) (titles []string) {
		for _, it := range everything(t, l.d, v, db.ItemQuery{Collection: mixedID}) {
			titles = append(titles, it.Title)
		}
		return titles
	}
	if got := inIt(owner); !slices.Equal(got, []string{"Mixed One", "Mixed Two"}) {
		t.Fatalf("the owner's collection filter: %v", got)
	}
	if got := inIt(child); !slices.Equal(got, []string{"Mixed One"}) {
		t.Fatalf("the child's collection filter: %v", got)
	}
	related, err := l.d.Related(ctx, child, mixedID)
	if err != nil || len(related[db.RelationCollectionMember]) != 1 {
		t.Fatalf("the child's collection page: %v, %v", related, err)
	}
	for v, want := range map[db.Viewer]int{owner: 2, child: 1} {
		if queue, err := l.d.PlayableUnder(ctx, v, mixedID); err != nil || len(queue) != want {
			t.Fatalf("a queue from the collection holds %d, want %d: %v", len(queue), want, err)
		}
	}
}
