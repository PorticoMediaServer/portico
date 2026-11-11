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
	for _, id := range []string{grownID, "itm_nothing"} {
		if _, err := l.d.Items(ctx, child, db.ItemQuery{Collection: id, WithTotal: true}); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("the child listed collection %s: %v", id, err)
		}
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

	// The collection's one film the child could see becomes adults only: the collection goes too.
	var familyFilm string
	for _, it := range everything(t, l.d, child, db.ItemQuery{Collection: mixedID}) {
		familyFilm = it.PublicID
	}
	if err := db.SetRatingAge(l.d, familyFilm, 18); err != nil {
		t.Fatal(err)
	}
	if _, titles := collections(child); len(titles) != 0 {
		t.Fatalf("after its last family film became adults only, the child sees %v", titles)
	}
	if _, titles := collections(owner); len(titles) != 2 {
		t.Fatalf("the owner sees %v", titles)
	}
}

// A collection the owner makes by hand shows a rating-limited child only while it holds a film
// the child may see: taking that film out hides it again.
func TestAHandMadeCollectionFollowsWhatItHolds(t *testing.T) {
	l := newLibrary(t, 300)
	owner, child := l.all, l.viewers[9] // the seeder's profile limited to a rating of 13
	all, err := l.d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var films db.Library
	for _, lib := range all {
		if lib.Kind == db.LibraryMovies {
			films = lib
		}
	}
	var family, adult string
	for _, it := range everything(t, l.d, owner, db.ItemQuery{Library: films.PublicID, Kinds: []db.ItemKind{db.KindMovie}}) {
		switch {
		case it.ContentRating == "":
		case family == "" && slices.Contains([]string{"G", "PG"}, it.ContentRating):
			family = it.PublicID
		case adult == "" && slices.Contains([]string{"R", "NC-17"}, it.ContentRating):
			adult = it.PublicID
		}
	}
	if family == "" || adult == "" {
		t.Fatal("the seeded films have no family film and adult film to collect")
	}
	collection, err := l.d.CreateCollection(ctx, db.NewCollection{Viewer: owner, Library: films.PublicID, Title: "Picks", Items: []string{family, adult}})
	if err != nil {
		t.Fatal(err)
	}
	sees := func() bool {
		t.Helper()
		_, err := l.d.Item(ctx, child, collection)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			t.Fatal(err)
		}
		return err == nil
	}
	if !sees() {
		t.Fatal("the child does not see a collection that holds a family film")
	}
	if err := l.d.EditCollection(ctx, owner, collection, db.CollectionEdit{Remove: []string{family}}); err != nil {
		t.Fatal(err)
	}
	if sees() {
		t.Fatal("the child still sees the collection after its family film was taken out")
	}
	if err := l.d.EditCollection(ctx, owner, collection, db.CollectionEdit{Add: []string{family}}); err != nil {
		t.Fatal(err)
	}
	if !sees() {
		t.Fatal("the child does not see the collection after the family film was put back")
	}
}
