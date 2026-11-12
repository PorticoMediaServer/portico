package api

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// An artist's page: every song crediting the artist, a page at a time with the total, from the
// artist's own id; and Popular, the artist's songs most played on the server in the last
// seven days first, then by plays ever, with songs nobody has played left out. A song a
// profile may not see is in neither, nor in a count.
func TestAnArtistsSongsAndPopularSongs(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	lib, err := s.db.CreateLibrary(ctx, "Music", db.LibraryMusic, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	one := 1
	artist := func(name string) []db.ImportCredit { return []db.ImportCredit{{Name: name, Role: db.RoleArtist}} }
	ada, mix := db.ItemKey{Kind: db.KeyName, Key: "artist|ada"}, db.ItemKey{Kind: db.KeyName, Key: "artist|various"}
	first, hits := db.ItemKey{Kind: db.KeyName, Key: "album|first"}, db.ItemKey{Kind: db.KeyName, Key: "album|hits"}
	track := func(title string, on db.ItemKey, by string, labels ...string) db.ImportItem {
		it := db.ImportItem{Kind: db.KindTrack, Keys: []db.ItemKey{{Kind: db.KeyName, Key: "track|" + title}}, Parent: &on, Title: title,
			IndexNumber: &one, Credits: artist(by), DurationMS: 60_000}
		for _, l := range labels {
			it.Tags = append(it.Tags, db.TagName{Kind: db.TagLabel, Name: l})
		}
		return it
	}
	if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}, Items: []db.ImportItem{
		{Kind: db.KindArtist, Keys: []db.ItemKey{ada}, Title: "Ada"},
		{Kind: db.KindAlbum, Keys: []db.ItemKey{first}, Parent: &ada, Title: "First", Credits: artist("Ada")},
		track("Alpha", first, "Ada"),
		track("Bravo", first, "Ada"),
		track("Charlie", first, "Ada", "Loud"),
		track("Delta", first, "Ada"),
		// A compilation under another artist, with one of Ada's songs and one of Bo's.
		{Kind: db.KindArtist, Keys: []db.ItemKey{mix}, Title: "Various Artists"},
		{Kind: db.KindAlbum, Keys: []db.ItemKey{hits}, Parent: &mix, Title: "Hits", Credits: artist("Various Artists")},
		track("Echo", hits, "Ada"),
		track("Foxtrot", hits, "Bo"),
	}}); err != nil {
		t.Fatal(err)
	}
	labels, err := s.db.Tags(ctx, db.TagLabel)
	if err != nil || len(labels) != 1 {
		t.Fatalf("labels: %v %v", labels, err)
	}
	var quiet Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Quiet"}, &quiet)
	if w := call(t, s, "PUT", "/v1/profiles/"+quiet.ID+"/restrictions", owner,
		Restrictions{AllLibraries: true, BlockedLabels: []string{labels[0].PublicID}}, nil); w.Code != 200 {
		t.Fatalf("restricting answered %d: %s", w.Code, w.Body)
	}
	var onQuiet Session
	if w := call(t, s, "POST", "/v1/profiles/"+quiet.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &onQuiet); w.Code != 200 {
		t.Fatalf("choosing the profile answered %d", w.Code)
	}
	ids := map[string]string{}
	for _, it := range get[ItemPage](t, s, owner, "/v1/items?library="+lib.PublicID+"&kind=artist,track").Items {
		ids[it.Title] = it.ID
		if it.Kind == "track" {
			ids[it.Title+" id"] = it.ID
		}
	}
	titles := func(token, path string) (out []string, page ItemPage) {
		t.Helper()
		page = get[ItemPage](t, s, token, path)
		for _, it := range page.Items {
			out = append(out, it.Title)
		}
		return out, page
	}

	// Every song crediting Ada, from the artist's id, two at a time with the total.
	songs := "/v1/items?artist=" + ids["Ada"] + "&kind=track"
	var all []string
	cursor := ""
	for range 5 {
		got, page := titles(owner, songs+"&limit=2&total=true&cursor="+cursor)
		all = append(all, got...)
		if page.Total == nil || *page.Total != 5 {
			t.Errorf("the total of Ada's songs is not 5: %v", page.Total != nil)
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	if !slices.Equal(all, []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo"}) {
		t.Errorf("Ada's songs: %v", all)
	}
	got, page := titles(onQuiet.AccessToken, songs+"&total=true")
	if !slices.Equal(got, []string{"Alpha", "Bravo", "Delta", "Echo"}) || *page.Total != 4 {
		t.Errorf("Ada's songs for the profile kept from the label: %v, total %d", got, *page.Total)
	}
	// The artist of a compilation is its album artist, not everyone on it.
	if got, _ := titles(owner, "/v1/items?artist="+ids["Various Artists"]+"&kind=track"); len(got) != 0 {
		t.Errorf("songs crediting Various Artists: %v", got)
	}

	// Popular: nobody has played anything, so there is no row.
	popular := songs + "&sort=popular&limit=10"
	if got, _ := titles(owner, popular); len(got) != 0 {
		t.Errorf("Popular before any play: %v", got)
	}
	ownerView, n := db.Viewer{UserID: 1, ProfileID: 1}, 0
	play := func(title string, ago time.Duration) {
		t.Helper()
		n++
		at := time.Now().Add(-ago)
		if _, _, err := s.db.SavePlays(ctx, db.User, []db.Progress{{Viewer: ownerView, ItemID: ids[title+" id"], PlayID: strconv.Itoa(n),
			PositionMS: 60_000, WatchedMS: 60_000, At: at}}); err != nil {
			t.Fatal(err)
		}
	}
	day := 24 * time.Hour
	for title, days := range map[string][]int{ // when each song was played, as days ago
		"Alpha":   {30, 30, 30, 30},
		"Bravo":   {1},
		"Charlie": {1, 2, 3},
		"Delta":   {2, 40},
		"Foxtrot": {1, 1, 1, 1},
	} {
		for _, ago := range days {
			play(title, time.Duration(ago)*day)
		}
	}
	// A play made with history paused is not counted.
	if err := s.db.SetProfilePreference(ctx, 1, db.PrefPauseHistory, "true", time.Now()); err != nil {
		t.Fatal(err)
	}
	play("Echo", time.Hour)
	if err := s.db.SetProfilePreference(ctx, 1, db.PrefPauseHistory, "false", time.Now()); err != nil {
		t.Fatal(err)
	}
	// Charlie has three plays this week, Delta and Bravo one each (Delta more ever); Alpha none
	// this week but four ever; Echo was only played with history paused; Foxtrot is Bo's.
	if got, _ := titles(owner, popular); !slices.Equal(got, []string{"Charlie", "Delta", "Bravo", "Alpha"}) {
		t.Errorf("Popular: %v", got)
	}
	if got, _ := titles(onQuiet.AccessToken, popular); !slices.Equal(got, []string{"Delta", "Bravo", "Alpha"}) {
		t.Errorf("Popular for the profile kept from the label: %v", got)
	}
	// One album's songs can be sorted so too; a whole library cannot.
	if got, _ := titles(owner, "/v1/items?parent="+get[Item](t, s, owner, "/v1/items/"+ids["Alpha id"]).ParentID+"&sort=popular"); len(got) != 4 {
		t.Errorf("an album's songs by popularity: %v", got)
	}
	if code := status(t, s, "GET", "/v1/items?library="+lib.PublicID+"&kind=track&sort=popular", owner, nil); code != 400 {
		t.Errorf("a library's songs by popularity answered %d, want 400", code)
	}
}
