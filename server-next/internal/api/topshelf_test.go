package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func init() {
	safeToRepeat(map[string]string{"POST /v1/topshelf/token": "the same sign-in on the same profile is given the same token"})
}

// The Apple TV Top Shelf (definition 05-browse rule 45a): Continue Watching with progress and
// the next Home row, posters only, at most 10 of each, only titles the profile may see; the
// feed token reads only the shelf and stops working when its sign-in ends.
func TestTopShelfShowsTheProfilesContinueWatchingAndNextRow(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	poster := func(n int) []db.ImportImage {
		return []db.ImportImage{{Role: db.ImagePoster, Source: db.ImageFromFile, FileHash: fmt.Sprintf("%064d", n)}}
	}
	eight, eighteen, one := 8, 18, 1
	// Films 1 to 3 are rated 18, which the profile may not see; every film lasts 100 seconds.
	var films []db.ImportItem
	for n := 1; n <= 25; n++ {
		age := &eight
		if n <= 3 {
			age = &eighteen
		}
		films = append(films, db.ImportItem{Kind: db.KindMovie, Keys: []db.ItemKey{{Kind: db.KeyName, Key: fmt.Sprint("film", n)}},
			Title: fmt.Sprintf("Film %02d", n), RatingAge: age, DurationMS: 100_000, Images: poster(n)})
	}
	show, season := db.ItemKey{Kind: db.KeyName, Key: "show"}, db.ItemKey{Kind: db.KeyName, Key: "season"}
	episodes := []db.ImportItem{
		{Kind: db.KindShow, Keys: []db.ItemKey{show}, Title: "The Show", RatingAge: &eight, Images: poster(99)},
		{Kind: db.KindSeason, Keys: []db.ItemKey{season}, Parent: &show, Title: "Season 1", IndexNumber: &one},
		{Kind: db.KindEpisode, Parent: &season, ChildKey: "e1", Title: "Pilot", IndexNumber: &one, ParentIndex: &one, DurationMS: 100_000,
			Images: []db.ImportImage{{Role: db.ImageThumb, Source: db.ImageFromFile, FileHash: strings.Repeat("e", 64)}}},
	}
	libraries := map[string]string{}
	for kind, items := range map[string][]db.ImportItem{db.LibraryMovies: films, db.LibraryShows: episodes} {
		lib, err := s.db.CreateLibrary(ctx, kind, kind, "en", []string{t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}, Items: items}); err != nil {
			t.Fatal(err)
		}
		libraries[kind] = lib.PublicID
	}
	var profile Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Child"}, &profile)
	call(t, s, "PUT", "/v1/profiles/"+profile.ID+"/restrictions", owner, Restrictions{AllLibraries: true, MaxRatingAge: &eight}, nil)
	signedIn := signIn(t, s, 1)
	var child Session
	call(t, s, "POST", "/v1/profiles/"+profile.ID+"/select", signedIn.Session.AccessToken, ProfileSelect{}, &child)
	v := viewerOfToken(t, s, child.AccessToken)
	ids := map[string]string{}
	for _, item := range get[ItemPage](t, s, owner, "/v1/items?library="+libraries[db.LibraryMovies]+"&kind=movie&sort=title&limit=50").Items {
		ids[item.Title] = item.ID
	}
	// The child is part-way through films 4 to 15, each watched a minute before the one after
	// it, and has just watched some of the episode.
	at := int64(30_000)
	pilot := get[ItemPage](t, s, owner, "/v1/items?library="+libraries[db.LibraryShows]+"&kind=episode&limit=1").Items[0].ID
	for n := 4; n <= 15; n++ {
		edit := db.StateEdit{PositionMS: &at, Ago: time.Duration(20-n) * time.Minute}
		if _, _, err := s.db.SetState(ctx, v, ids[fmt.Sprintf("Film %02d", n)], edit); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.db.SetState(ctx, v, pilot, db.StateEdit{PositionMS: &at}); err != nil {
		t.Fatal(err)
	}

	var grant TopShelfToken
	if w := call(t, s, "POST", "/v1/topshelf/token", child.AccessToken, TopShelfTokenRequest{InstallationID: device(1).InstallationID}, &grant); w.Code != 200 {
		t.Fatalf("the token answered %d: %s", w.Code, w.Body)
	}
	if grant.FeedURL != "/v1/topshelf" || len(grant.Token) < 16 || !grant.ExpiresAt.Equal(child.SessionExpiresAt) {
		t.Fatalf("the token: %+v; want the feed's path and the sign-in's end %v", grant, child.SessionExpiresAt)
	}
	shelf := get[TopShelf](t, s, grant.Token, "/v1/topshelf")
	if len(shelf.Sections) != 2 || shelf.Sections[0].ID != "continue" || shelf.Sections[1].ID != "recently_added" {
		t.Fatalf("the shelf's sections: %+v; want Continue Watching and Recently Added", shelf.Sections)
	}
	onShelf := map[string]bool{}
	for _, section := range shelf.Sections {
		if len(section.Entries) != 10 {
			t.Errorf("%s holds %d titles; want 10", section.ID, len(section.Entries))
		}
		for _, entry := range section.Entries {
			if onShelf[entry.ID] || entry.ID == ids["Film 01"] || entry.ID == ids["Film 02"] || entry.ID == ids["Film 03"] {
				t.Errorf("%s shows %s, which is hidden from the profile or already on the shelf", section.ID, entry.Title)
			}
			onShelf[entry.ID] = true
			plays := entry.PlayURL == "portico://play/"+entry.ID || entry.PlayURL == "" && entry.Title == "The Show"
			if entry.DisplayURL != "portico://item/"+entry.ID || !plays || !strings.HasPrefix(entry.ImageURL, "/v1/images/") {
				t.Errorf("%s: %+v", section.ID, entry)
			}
			if (section.ID == "continue") != (entry.ProgressPercent == 30) {
				t.Errorf("%s shows %s at %d%%; want 30%% in Continue Watching only", section.ID, entry.Title, entry.ProgressPercent)
			}
			if entry.ID == pilot && (entry.ShowTitle != "The Show" || entry.ImageURL != "/v1/images/"+fmt.Sprintf("%064d", 99)) {
				t.Errorf("the episode is not shown with its show's poster: %+v", entry)
			}
		}
	}
	if !onShelf[pilot] || shelf.Sections[0].TitleText.Code != "home.row.continueWatching" {
		t.Errorf("Continue Watching: %+v", shelf.Sections[0])
	}

	// The feed token signs nobody in, and a sign-in's own token does not read the shelf.
	if code := status(t, s, "GET", "/v1/home", grant.Token, nil); code != 401 {
		t.Errorf("the feed token read Home: %d", code)
	}
	if code := status(t, s, "GET", "/v1/topshelf", child.AccessToken, nil); code != 401 {
		t.Errorf("an access token read the shelf: %d", code)
	}
	// Signed out, the shelf is refused, so the extension clears what it kept.
	if code := status(t, s, "DELETE", "/v1/auth/session", child.AccessToken, nil); code != 204 {
		t.Fatalf("signing out answered %d", code)
	}
	if code := status(t, s, "GET", "/v1/topshelf", grant.Token, nil); code != 401 {
		t.Errorf("the shelf answered %d after signing out; want 401", code)
	}
}
