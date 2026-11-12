package api

import (
	"context"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A film's and a show's page rows after More like this: one per lead actor, then its creator,
// then its genre, always in that order; each of other titles the profile may see, never the
// title itself, and a row with nothing in it left out.
func TestATitlesPageRowsOfItsCastCreatorAndGenre(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	films, err := s.db.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	shows, err := s.db.CreateLibrary(ctx, "Shows", db.LibraryShows, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	cast := func(names ...string) (out []db.ImportCredit) {
		for _, n := range names {
			out = append(out, db.ImportCredit{Name: n, Role: db.RoleActor})
		}
		return out
	}
	title := func(kind db.ItemKind, name string, rating float64, credits []db.ImportCredit, tags ...db.TagName) db.ImportItem {
		return db.ImportItem{Kind: kind, Keys: []db.ItemKey{{Kind: db.KeyName, Key: name}}, Title: name, CommunityRating: rating,
			Credits: credits, Tags: tags}
	}
	drama, crime, secret := db.TagName{Kind: db.TagGenre, Name: "Drama"}, db.TagName{Kind: db.TagGenre, Name: "Crime"}, db.TagName{Kind: db.TagLabel, Name: "Secret"}
	director := db.ImportCredit{Name: "Dee Rector", Role: db.RoleDirector}
	if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: films.ID, PathID: films.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}, Items: []db.ImportItem{
		// The film whose page is read: four actors, its director, two genres (Crime is first by name).
		title(db.KindMovie, "Heat", 8, append(cast("Al", "Bo", "Cy", "Di"), director), drama, crime),
		title(db.KindMovie, "Ronin", 7, cast("Al"), crime),
		title(db.KindMovie, "Hidden", 9, cast("Al"), crime, secret), // kept from the restricted profile
		title(db.KindMovie, "Collateral", 6, append(cast("Cy"), director), crime),
		title(db.KindMovie, "Thief", 5, nil, drama),
		// Bo has no other title: no row. Di is the fourth actor: no row.
		title(db.KindMovie, "Insomnia", 6, cast("Di"), drama),
	}}); err != nil {
		t.Fatal(err)
	}
	writer := db.ImportCredit{Name: "Wren Writer", Role: db.RoleWriter}
	if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: shows.ID, PathID: shows.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}, Items: []db.ImportItem{
		// A show whose writer is also its lead: one row for the person, as cast.
		title(db.KindShow, "Wire", 9, []db.ImportCredit{{Name: "Wren Writer", Role: db.RoleActor}, writer}),
		title(db.KindShow, "Treme", 8, []db.ImportCredit{writer}),
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
	ids := map[string]string{}
	for _, lib := range []string{films.PublicID, shows.PublicID} {
		for _, it := range get[ItemPage](t, s, owner, "/v1/items?library="+lib).Items {
			ids[it.Title] = it.ID
		}
	}
	rows := func(token, title string) (out []string) {
		t.Helper()
		for _, g := range get[Related](t, s, token, "/v1/items/"+ids[title]+"/related").Groups {
			row := g.Relation
			switch {
			case g.Person != nil:
				row += " " + g.Person.Name
			case g.Tag != nil:
				row += " " + g.Tag.Label
			}
			for _, it := range g.Items {
				row += " | " + it.Title
			}
			out = append(out, row)
		}
		return out
	}
	want := []string{
		"cast Al | Hidden | Ronin",
		"cast Cy | Collateral",
		"creator Dee Rector | Collateral",
		"genre Crime | Hidden | Ronin | Collateral", // best rated first
	}
	if got := rows(owner, "Heat"); !slices.Equal(got, want) {
		t.Errorf("Heat's rows:\n%q\nwant\n%q", got, want)
	}
	want = []string{"cast Al | Ronin", "cast Cy | Collateral", "creator Dee Rector | Collateral", "genre Crime | Ronin | Collateral"}
	if got := rows(quiet.AccessToken, "Heat"); !slices.Equal(got, want) {
		t.Errorf("Heat's rows for the profile kept from the label:\n%q\nwant\n%q", got, want)
	}
	if got := rows(owner, "Wire"); !slices.Equal(got, []string{"cast Wren Writer | Treme"}) {
		t.Errorf("Wire's rows: %q", got)
	}
	if got := rows(owner, "Thief"); len(got) != 1 || got[0] != "genre Drama | Heat | Insomnia" {
		t.Errorf("Thief's rows: %q", got)
	}
}
