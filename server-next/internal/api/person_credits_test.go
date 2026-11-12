package api

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A person's page: their roles with how many titles each, most first, and their titles, one
// card per title with every role they have on it (browse rule 26), all of them or one role's,
// a page at a time with the total. A title the profile may not see is in no group and no count.
func TestAPersonsRolesAndCredits(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	films, err := s.db.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	actor := func(character string) db.ImportCredit {
		return db.ImportCredit{Name: "Clint Maker", Role: db.RoleActor, Character: character}
	}
	directs := db.ImportCredit{Name: "Clint Maker", Role: db.RoleDirector}
	writes := db.ImportCredit{Name: "Clint Maker", Role: db.RoleWriter}
	crew := db.ImportCredit{Name: "Clint Maker", Role: db.RoleCrew}
	film := func(title string, credits ...db.ImportCredit) db.ImportItem {
		return db.ImportItem{Kind: db.KindMovie, Keys: []db.ItemKey{{Kind: db.KeyName, Key: title}}, Title: title, Credits: credits}
	}
	secret := film("Secret", actor("Spy"), directs)
	secret.Tags = []db.TagName{{Kind: db.TagLabel, Name: "Secret"}}
	if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: films.ID, PathID: films.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}, Items: []db.ImportItem{
		film("Alpha", actor("Joe"), directs, writes),
		film("Bravo", actor("Bill")),
		film("Charlie", actor("Will")),
		film("Delta", directs, crew),
		film("Echo", db.ImportCredit{Name: "Someone Else", Role: db.RoleActor}),
		secret,
	}}); err != nil {
		t.Fatal(err)
	}
	labels, err := s.db.Tags(ctx, db.TagLabel)
	if err != nil || len(labels) != 1 {
		t.Fatalf("labels: %v %v", labels, err)
	}
	var p Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Quiet"}, &p)
	if w := call(t, s, "PUT", "/v1/profiles/"+p.ID+"/restrictions", owner, Restrictions{AllLibraries: true, BlockedLabels: []string{labels[0].PublicID}}, nil); w.Code != 200 {
		t.Fatalf("restricting answered %d: %s", w.Code, w.Body)
	}
	var quiet Session
	if w := call(t, s, "POST", "/v1/profiles/"+p.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &quiet); w.Code != 200 {
		t.Fatalf("choosing the profile answered %d", w.Code)
	}
	var person string
	for _, c := range get[Item](t, s, owner, "/v1/items/"+get[ItemPage](t, s, owner, "/v1/items?library="+films.PublicID).Items[0].ID).Credits {
		if c.Name == "Clint Maker" {
			person = c.PersonID
		}
	}
	roles := func(token string) (out []string) {
		for _, r := range get[Person](t, s, token, "/v1/people/"+person).Roles {
			out = append(out, r.Role+" "+itoa(r.TitleCount))
		}
		return out
	}
	if got := roles(owner); !slices.Equal(got, []string{"actor 4", "director 3", "writer 1", "crew 1"}) {
		t.Errorf("the owner's groups: %v", got)
	}
	if got := roles(quiet.AccessToken); !slices.Equal(got, []string{"actor 3", "director 2", "writer 1", "crew 1"}) {
		t.Errorf("the restricted profile's groups: %v", got)
	}
	if p := get[Person](t, s, owner, "/v1/people/"+person); p.CrewTitleCount == nil || *p.CrewTitleCount != 3 {
		t.Errorf("the owner's Crew total: %+v", p)
	}
	if p := get[Person](t, s, quiet.AccessToken, "/v1/people/"+person); p.CrewTitleCount == nil || *p.CrewTitleCount != 2 {
		t.Errorf("the restricted profile's Crew total: %+v", p)
	}
	// cards walks a person's list two at a time; each card is the title and its roles.
	cards := func(token, query string) (out []string, total int) {
		t.Helper()
		cursor := ""
		for range 10 {
			page := get[ItemPage](t, s, token, "/v1/items?person="+person+query+"&limit=2&total=true&cursor="+cursor)
			for _, it := range page.Items {
				var parts []string
				for _, c := range it.Credits {
					parts = append(parts, strings.TrimSpace(c.Role+" "+c.Character))
				}
				out = append(out, it.Title+": "+strings.Join(parts, ", "))
			}
			total = *page.Total
			if cursor = page.NextCursor; cursor == "" {
				break
			}
		}
		return out, total
	}
	want := []string{"Alpha: actor Joe, director, writer", "Bravo: actor Bill", "Charlie: actor Will", "Delta: director, crew", "Secret: actor Spy, director"}
	if got, total := cards(owner, ""); !slices.Equal(got, want) || total != 5 {
		t.Errorf("the owner's credits (total %d):\n%q", total, got)
	}
	if got, total := cards(quiet.AccessToken, "&role=director"); !slices.Equal(got, []string{want[0], want[3]}) || total != 2 {
		t.Errorf("the restricted profile's Directing (total %d): %q", total, got)
	}
	if got, total := cards(owner, "&creditGroup=crew"); !slices.Equal(got, []string{want[0], want[3], want[4]}) || total != 3 {
		t.Errorf("the owner's Crew (total %d): %q", total, got)
	}
	if got, total := cards(quiet.AccessToken, "&creditGroup=crew"); !slices.Equal(got, []string{want[0], want[3]}) || total != 2 {
		t.Errorf("the restricted profile's Crew (total %d): %q", total, got)
	}
	if got, total := cards(owner, "&role=crew"); !slices.Equal(got, []string{want[3]}) || total != 1 {
		t.Errorf("the literal crew role (total %d): %q", total, got)
	}
	// Another list carries no credits.
	for _, it := range get[ItemPage](t, s, owner, "/v1/items?library="+films.PublicID).Items {
		if len(it.Credits) > 0 {
			t.Errorf("a library list carried credits on %s", it.Title)
		}
	}
	for _, query := range []string{"/v1/items?library=" + films.PublicID + "&role=actor", "/v1/items?person=" + person + "&role=hero",
		"/v1/items?creditGroup=crew", "/v1/items?person=" + person + "&creditGroup=cast", "/v1/items?person=" + person + "&creditGroup=crew&role=crew"} {
		if code := status(t, s, "GET", query, owner, nil); code != 400 {
			t.Errorf("%s answered %d, want 400", query, code)
		}
	}
}
