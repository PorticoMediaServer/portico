package api

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestListCardsNameTheirVisibleRoot(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	key := func(name string) db.ItemKey { return db.ItemKey{Kind: db.KeyName, Key: name} }
	show, season, hidden := key("show"), key("season"), key("hidden")
	artist, album, standalone := key("artist"), key("album"), key("standalone")
	eight, eighteen, six, fourteen := 8, 18, 6, 14
	credit := []db.ImportCredit{{Name: "Root Tester", Role: db.RoleGuest}}
	var showLibrary string
	for _, fixture := range []struct {
		kind  string
		items []db.ImportItem
	}{
		{db.LibraryShows, []db.ImportItem{
			{Kind: db.KindShow, Keys: []db.ItemKey{show}, Title: "The Show", RatingAge: &eight, Credits: credit},
			{Kind: db.KindSeason, Keys: []db.ItemKey{season}, Parent: &show, Title: "Season 6", IndexNumber: &six},
			{Kind: db.KindEpisode, Parent: &season, ChildKey: "e14", Title: "Episode", IndexNumber: &fourteen, ParentIndex: &six, Credits: credit},
			{Kind: db.KindShow, Keys: []db.ItemKey{hidden}, Title: "Hidden Show", RatingAge: &eighteen},
			// An explicit lower rating makes this child visible even while its root is hidden.
			{Kind: db.KindEpisode, Parent: &hidden, ChildKey: "e1", Title: "Lower Rated Episode", RatingAge: &eight, Credits: credit},
		}},
		{db.LibraryMusic, []db.ImportItem{
			{Kind: db.KindArtist, Keys: []db.ItemKey{artist}, Title: "Artist", RatingAge: &eight},
			{Kind: db.KindAlbum, Keys: []db.ItemKey{album}, Parent: &artist, Title: "Album"},
			{Kind: db.KindTrack, Parent: &album, ChildKey: "t1", Title: "Track", Credits: credit},
			{Kind: db.KindAlbum, Keys: []db.ItemKey{standalone}, Title: "Standalone Album", RatingAge: &eight},
			{Kind: db.KindTrack, Parent: &standalone, ChildKey: "t1", Title: "Album Track", Credits: credit},
		}},
	} {
		lib, err := s.db.CreateLibrary(ctx, fixture.kind, fixture.kind, "en", []string{t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if fixture.kind == db.LibraryShows {
			showLibrary = lib.PublicID
		}
		if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1,
			Dirs: []db.ImportDir{{}}, Items: fixture.items}); err != nil {
			t.Fatal(err)
		}
	}
	var profile Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Child"}, &profile)
	call(t, s, "PUT", "/v1/profiles/"+profile.ID+"/restrictions", owner, Restrictions{AllLibraries: true, MaxRatingAge: &eight}, nil)
	var child Session
	call(t, s, "POST", "/v1/profiles/"+profile.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &child)
	episode := get[ItemPage](t, s, owner, "/v1/items?library="+showLibrary+"&kind=episode&limit=1").Items[0]
	if episode.RootTitle != "The Show" {
		t.Fatalf("a library card cannot name its show: %+v", episode)
	}
	person := get[Item](t, s, owner, "/v1/items/"+episode.ID).Credits[0].PersonID
	for _, token := range []string{owner, child.AccessToken} {
		spotlight, err := s.db.Spotlight(ctx, viewerOfToken(t, s, token))
		if err != nil {
			t.Fatal(err)
		}
		featuredHiddenShow := false
		for _, item := range spotlight {
			featuredHiddenShow = featuredHiddenShow || item.Title == "Hidden Show"
		}
		if featuredHiddenShow != (token == owner) {
			t.Fatalf("spotlight promoted a hidden root from its visible episode: %+v", spotlight)
		}
		page := get[ItemPage](t, s, token, "/v1/items?person="+person)
		if len(page.Items) != 5 {
			t.Fatalf("person list has %d cards, want 5", len(page.Items))
		}
		for _, item := range page.Items {
			want := map[string]string{"Episode": "The Show", "Lower Rated Episode": "Hidden Show", "Track": "Artist", "Album Track": "Standalone Album"}[item.Title]
			if token == child.AccessToken && item.Title == "Lower Rated Episode" {
				want = ""
				if w := call(t, s, "GET", "/v1/items/"+item.RootID, token, nil, nil); w.Code != 404 {
					t.Fatalf("root should be hidden, got %d", w.Code)
				}
			}
			if item.RootTitle != want {
				t.Errorf("%s rootTitle = %q, want %q", item.Title, item.RootTitle, want)
			}
			if item.Title == "Episode" && (item.Index == nil || *item.Index != 14 || item.ParentIndex == nil || *item.ParentIndex != 6) {
				t.Fatalf("episode numbering changed: %+v", item)
			}
			if item.RootTitle != "" && get[Item](t, s, token, "/v1/items/"+item.RootID).Title != item.RootTitle {
				t.Fatalf("%s rootTitle does not name rootId", item.Title)
			}
		}
	}
}
