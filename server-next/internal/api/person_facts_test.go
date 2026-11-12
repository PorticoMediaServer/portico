package api

import (
	"context"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestAPersonsKnownTitlesAndKindCounts(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	films, err := s.db.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	credit := func(role db.CreditRole) db.ImportCredit { return db.ImportCredit{Name: "Test Person", Role: role} }
	other := db.ImportCredit{Name: "Someone Else", Role: db.RoleActor}
	film := func(title string, rating float64, year int, credits ...db.ImportCredit) db.ImportItem {
		return db.ImportItem{Kind: db.KindMovie, Keys: []db.ItemKey{{Kind: db.KeyName, Key: title}}, Title: title,
			CommunityRating: rating, Year: year, Credits: credits}
	}
	items := []db.ImportItem{
		film("Lead", 6, 2000, db.ImportCredit{Name: "Other Director", Role: db.RoleDirector},
			db.ImportCredit{Name: "Other Writer", Role: db.RoleWriter}, db.ImportCredit{Name: "Producer", Role: db.RoleProducer}, credit(db.RoleActor)),
		film("Director", 9, 2020, other, credit(db.RoleDirector)),
		film("Writer", 9, 2024, other, credit(db.RoleWriter)),
		film("Both", 9, 2024, credit(db.RoleActor), credit(db.RoleDirector)),
		film("Supporting", 10, 2026, other, credit(db.RoleActor)),
		film("Hidden", 10, 2030, credit(db.RoleActor)),
	}
	items[5].Tags = []db.TagName{{Kind: db.TagLabel, Name: "Hidden"}}
	for n := range 13 {
		items = append(items, film("Later "+itoa(n), 0, 2025-n, other, other, credit(db.RoleActor)))
	}
	if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: films.ID, PathID: films.Paths[0].ID, Scan: 1,
		Dirs: []db.ImportDir{{}}, Items: items}); err != nil {
		t.Fatal(err)
	}
	books, err := s.db.CreateLibrary(ctx, "Books", db.LibraryBooks, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: books.ID, PathID: books.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}},
		Items: []db.ImportItem{{Kind: db.KindBook, Keys: []db.ItemKey{{Kind: db.KeyName, Key: "Book"}}, Title: "Book", Credits: []db.ImportCredit{credit(db.RoleAuthor)}}}}); err != nil {
		t.Fatal(err)
	}
	labels, err := s.db.Tags(ctx, db.TagLabel)
	if err != nil || len(labels) != 1 {
		t.Fatalf("labels: %v %v", labels, err)
	}
	var profile Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Quiet"}, &profile)
	call(t, s, "PUT", "/v1/profiles/"+profile.ID+"/restrictions", owner,
		Restrictions{AllLibraries: true, BlockedLabels: []string{labels[0].PublicID}}, nil)
	var quiet Session
	call(t, s, "POST", "/v1/profiles/"+profile.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &quiet)
	var person string
	for _, who := range get[PersonPage](t, s, owner, "/v1/people?library="+books.PublicID+"&role=author").Items {
		if who.Name == "Test Person" {
			person = who.ID
		}
	}
	p := get[Person](t, s, quiet.AccessToken, "/v1/people/"+person)
	if p.KindCounts["movie"] != 18 || p.KindCounts["book"] != 1 || len(p.KindCounts) != 2 || *p.TitleCount != 19 {
		t.Fatalf("visible distinct counts: %+v, total %v", p.KindCounts, p.TitleCount)
	}
	var titles []string
	seen := map[string]bool{}
	for _, item := range p.KnownFor {
		if seen[item.ID] || item.Title == "Hidden" {
			t.Fatalf("repeated or hidden known title: %+v", item)
		}
		seen[item.ID] = true
		titles = append(titles, item.Title)
	}
	if len(titles) != 12 || !slices.Equal(titles[:6], []string{"Writer", "Both", "Director", "Lead", "Book", "Supporting"}) {
		t.Fatalf("known titles, in billing/rating/year/id order: %v", titles)
	}
}
