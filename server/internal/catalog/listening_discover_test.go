package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestListeningMusicDiscoverWindowsCountsOrderAndPlaylistPrivacy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, n := phase34ListeningFixture(t)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	c.Fields(n["song-001"].ID, map[string]any{"added_text": now.Add(-time.Hour).Format(time.RFC3339Nano), "poster_url": "local:cover"})
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,favorite,last_played_at,revision) VALUES('local:account:profile',?,1,?,1)`, n["song-001"].ID, now.Format("2006-01-02T15:04:05.000Z"))
	for _, v := range []struct {
		id, profile string
		item        int64
		at          time.Time
		completed   bool
	}{{"new1", "profile", n["song-001"].ID, now.Add(-time.Hour), true}, {"new2", "profile", n["song-001"].ID, now.Add(-time.Hour), true}, {"one", "profile", n["song-002"].ID, now.Add(-time.Hour), true}, {"old", "profile", n["song-002"].ID, now.Add(-31 * 24 * time.Hour), true}, {"unfinished", "profile", n["song-002"].ID, now.Add(-time.Hour), false}, {"foreign", "other", n["song-002"].ID, now.Add(-time.Hour), true}} {
		c.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,completed) VALUES(?,'local','account',?,'Fixture',?,'music',7,'Song',?,?,?)`, v.id, v.profile, v.item, v.at.UnixMilli(), v.at.UnixMilli(), v.completed)
	}
	c.Exec(`INSERT INTO catalog_playlists(token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at) VALUES('allowed','local','account','profile','Music list','op','hash','now'),('private','local','else','other','Private list','op2','hash2','now')`)
	c.Exec(`INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position,order_key) VALUES('e1',(SELECT id FROM catalog_playlists WHERE token='allowed'),?,1,'a'),('e2',(SELECT id FROM catalog_playlists WHERE token='private'),?,1,'a')`, n["song-001"].ID, n["song-002"].ID)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetTermsTx(ctx, tx, n["song-001"].ID, compactcatalog.VocabGenre, "local", []compactcatalog.Term{{SourceID: "rock", Name: "Rock"}})
	})
	c.Drain()
	key := identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"})
	c.Exec(`UPDATE personal_items SET profile_id=? WHERE profile_id='local:account:profile'`, key)
	c.Drain()
	r := listeningRequest("music", "discover", "")
	r.Profile = key
	r.Viewer.Profile = key
	r.Now = now
	r.Limit = 1
	page, err := s.Content(r)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ContentSection{}
	order := map[string]int{}
	for i, sec := range page.Sections {
		byID[sec.ID] = sec
		order[sec.ID] = i
	}
	for _, id := range []string{"recently_played", "new_albums", "artists", "genres", "playlists", "most_played_songs"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("missing %s: %+v", id, page.Sections)
		}
	}
	if _, ok := byID["recently_added"]; ok {
		t.Fatal("old unlimited recent row duplicated new albums")
	}
	if byID["new_albums"].TotalCount != 1 || byID["new_albums"].Entries[0].ID != n["album"].Public {
		t.Fatal(byID["new_albums"])
	}
	most := byID["most_played_songs"]
	if most.TotalCount != 2 || most.Entries[0].ID != n["song-001"].Public || most.NextCursor == "" || most.Entries[0].Favorite == nil || !*most.Entries[0].Favorite {
		t.Fatalf("30-day plays/favorite: %+v", most)
	}
	playlists := byID["playlists"]
	if playlists.TotalCount != 1 || playlists.Entries[0].ID != "allowed" || *playlists.Entries[0].Count != 1 || len(playlists.Entries[0].ArtworkPaths) != 1 {
		t.Fatal(playlists)
	}
	if order["artists"] < order["new_albums"] || order["playlists"] > order["most_played_songs"] {
		t.Fatal(order)
	}
	r.Cursor = most.NextCursor
	next, err := s.Content(r)
	if err != nil || len(next.Sections) != 1 || next.Sections[0].ID != "most_played_songs" || len(next.Sections[0].Entries) != 1 || next.Sections[0].Entries[0].ID != n["song-002"].Public {
		t.Fatalf("continuation: %+v %v", next, err)
	}
	r.Viewer.Profile = "local:account:other"
	r.Profile = r.Viewer.Profile
	if _, err = s.Content(r); err == nil {
		t.Fatal("discover cursor crossed profile")
	}
}

func TestListeningSimilarUsesLocalGenresAndCurrentVisibility(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, n := phase34ListeningFixture(t)
	a := c.Album(n["artist"], "Genre match", 2021)
	song := c.Song(a, 1, "/music/match.mp3", "Match")
	b := c.Album(n["artist"], "Different genre", 2021)
	other := c.Song(b, 1, "/music/other.mp3", "Other")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for _, id := range []int64{n["song-001"].ID, song.ID} {
			if err := compactcatalog.SetTermsTx(ctx, tx, id, compactcatalog.VocabGenre, "local", []compactcatalog.Term{{SourceID: "rock", Name: "Rock"}}); err != nil {
				return err
			}
		}
		return compactcatalog.SetTermsTx(ctx, tx, other.ID, compactcatalog.VocabGenre, "local", []compactcatalog.Term{{SourceID: "jazz", Name: "Jazz"}})
	})
	c.Drain()
	v := listeningRequest("music", "album", n["album"].Public).Viewer
	page, err := s.ListeningSimilar(v, n["album"].Public, 12)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != a.Public || page.Entries[0].Playback != nil {
		t.Fatalf("similar local genres: %+v %v", page, err)
	}
	// The candidate still has a playable song after its matching genre is
	// unavailable. Hidden source metadata must not rank that visible album.
	c.Song(a, 2, "/music/match-unrelated.mp3", "Unrelated available song")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, song.Asset, false)
	})
	c.Drain()
	page, err = s.ListeningSimilar(v, n["album"].Public, 12)
	if err != nil || len(page.Entries) != 0 {
		t.Fatal("unavailable candidate", page, err)
	}
	v.Libraries = []string{"books"}
	if _, err = s.ListeningSimilar(v, n["album"].Public, 12); err == nil {
		t.Fatal("cross-library seed")
	}
}

func TestListeningSimilarRequiresThreeActualSharingListeners(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, n := phase34ListeningFixture(t)
	candidate := c.Album(n["artist"], "Played alongside", 2021)
	song := c.Song(candidate, 1, "/music/alongside.mp3", "Alongside")
	c.Drain()
	c.Exec(`INSERT INTO server_settings(singleton,"home.communityActivityEnabled") VALUES(1,1) ON CONFLICT(singleton) DO UPDATE SET "home.communityActivityEnabled"=1`)
	c.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('a','a',x'00','p',1),('b','b',x'00','q',1),('c','c',x'00','r',1)`)
	shareFixtureProfiles(t, c.DB)
	c.Exec(`UPDATE direct_memberships SET role='member',allowed_libraries='["music"]' WHERE account_id IN('a','b','c')`)
	viewer := listeningRequest("music", "album", n["album"].Public).Viewer
	check := func(want int) {
		t.Helper()
		page, err := s.ListeningSimilar(viewer, n["album"].Public, 12)
		if err != nil || len(page.Entries) != want {
			t.Fatalf("got %d want %d: %v", len(page.Entries), want, err)
		}
	}
	for i, actor := range []identity.Viewer{{Authority: "local", AccountID: "a", ProfileID: "p"}, {Authority: "local", AccountID: "b", ProfileID: "q"}, {Authority: "local", AccountID: "c", ProfileID: "r"}} {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			for _, item := range []int64{n["song-001"].ID, song.ID} {
				if _, err := recordPlay(tx, actor, actor.AccountID+"-"+fmt.Sprint(item), item, 7, time.Now().Add(-time.Hour), 300000, 300, true, true, true); err != nil {
					return err
				}
				if _, err := countPersonalPlay(tx, identity.PersonalKey(actor), actor.AccountID+"-"+fmt.Sprint(item), item); err != nil {
					return err
				}
			}
			return nil
		})
		c.Drain()
		if i < 2 {
			check(0)
		} else {
			check(1)
		}
	}
	for _, body := range []string{`{"privacy.pauseWatchHistory":true}`, `{"privacy.showActivityToMembers":false}`} {
		var patch map[string]any
		json.Unmarshal([]byte(body), &patch)
		storePreference(t, c.DB, identity.Viewer{Authority: "local", AccountID: "b", ProfileID: "q"}, patch)
		check(0)
		storePreference(t, c.DB, identity.Viewer{Authority: "local", AccountID: "b", ProfileID: "q"}, map[string]any{"privacy.showActivityToMembers": true, "privacy.pauseWatchHistory": false})
		check(1)
	}
	c.Exec(`UPDATE direct_memberships SET allowed_libraries='[]' WHERE account_id='b'`)
	check(0)
	c.Exec(`UPDATE direct_memberships SET allowed_libraries='["music"]',disabled=1 WHERE account_id='b'`)
	check(0)
}

func TestListeningSharedBrowsePreservesFavoriteDateAndLocalGenres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, n := phase34ListeningFixture(t)
	viewer := listeningRequest("music", "songs", "").Viewer
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,favorite,revision) VALUES(?,?,1,1)`, viewer.Profile, n["song-001"].ID)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.SetFactsTx(ctx, tx, n["album"].ID, map[string]any{"release_date": "2020-03-20"}); err != nil {
			return err
		}
		return compactcatalog.SetTermsTx(ctx, tx, n["song-001"].ID, compactcatalog.VocabGenre, "local", []compactcatalog.Term{{SourceID: "genre", Name: "Local genre"}})
	})
	c.Drain()
	songs, err := s.BrowseEntities(viewer, BrowseRequest{Library: "music", Profile: viewer.Profile, ViewerFence: viewer.Fence, Pivot: "songs", Limit: 10, Query: &BrowseNode{Field: "favorite", Operator: "equals", Value: true}})
	if err != nil || len(songs.Entries) != 1 || songs.Entries[0].Favorite == nil || !*songs.Entries[0].Favorite {
		t.Fatalf("favorite missing in ordinary browse: %+v %v", songs, err)
	}
	albums, err := s.BrowseEntities(viewer, BrowseRequest{Library: "music", Profile: viewer.Profile, ViewerFence: viewer.Fence, Pivot: "albums", Limit: 10})
	if err != nil || len(albums.Entries) != 1 || albums.Entries[0].ReleaseDate != "2020-03-20" || len(albums.Entries[0].Genres) != 1 || albums.Entries[0].Genres[0] != "Local genre" || albums.Entries[0].AlbumType != "" {
		t.Fatalf("album facts absent or invented: %+v %v", albums, err)
	}
	// The shared across-library consumer keeps the same returned identity and
	// real page facts after 03b's final decorations and duration projection.
	favorites := &BrowseNode{Field: "favorite", Operator: "equals", Value: true}
	acrossSongs, err := s.BrowseAcross(viewer, AcrossRequest{Pivot: "items", Query: favorites, Limit: 10})
	if err != nil || len(acrossSongs.Entries) != 1 || acrossSongs.Entries[0].ID != n["song-001"].Public || acrossSongs.Entries[0].LibraryID != "music" || acrossSongs.Entries[0].Favorite == nil || !*acrossSongs.Entries[0].Favorite {
		t.Fatalf("favorite or identity lost across libraries: %+v %v", acrossSongs, err)
	}
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,favorite,revision) VALUES(?,?,1,1)`, viewer.Profile, n["album"].ID)
	c.Drain()
	acrossAlbums, err := s.BrowseAcross(viewer, AcrossRequest{Pivot: "titles", Query: favorites, Limit: 10})
	if err != nil || len(acrossAlbums.Entries) != 1 || acrossAlbums.Entries[0].ID != n["album"].Public || acrossAlbums.Entries[0].LibraryID != "music" || acrossAlbums.Entries[0].ReleaseDate != "2020-03-20" || len(acrossAlbums.Entries[0].Genres) != 1 || acrossAlbums.Entries[0].Genres[0] != "Local genre" || acrossAlbums.Entries[0].AlbumType != "" {
		t.Fatalf("album facts or identity lost across libraries: %+v %v", acrossAlbums, err)
	}
}

// The similar row pages past its first 24 to the end of the ranking (WEB-050,
// TV-005): every page is a slice of one ranking, and a publication under a
// continuation is explicit.
func TestListeningSimilarPagesToExhaustion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, n := phase34ListeningFixture(t)
	songs := []int64{n["song-001"].ID}
	expected := map[string]bool{}
	for i := 0; i < 61; i++ {
		a := c.Album(n["artist"], fmt.Sprintf("Rock %02d", i), 2021)
		songs = append(songs, c.Song(a, 1, fmt.Sprintf("/music/rock-%02d.mp3", i), "Rock").ID)
		expected[a.Public] = true
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for _, id := range songs {
			if err := compactcatalog.SetTermsTx(ctx, tx, id, compactcatalog.VocabGenre, "local", []compactcatalog.Term{{SourceID: "rock", Name: "Rock"}}); err != nil {
				return err
			}
		}
		return nil
	})
	c.Drain()
	v := listeningRequest("music", "album", n["album"].Public).Viewer
	seen := map[string]bool{}
	cursor, first := "", ""
	for pages := 0; pages < 10; pages++ {
		page, err := s.ListeningSimilarPage(v, n["album"].Public, false, cursor, 24)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items.Entries) > 24 || page.Items.HasMore != (page.Items.NextCursor != "") {
			t.Fatalf("page bound: %d %v", len(page.Items.Entries), page.Items.HasMore)
		}
		for _, entry := range page.Items.Entries {
			if seen[entry.ID] || !expected[entry.ID] {
				t.Fatal("repeated or foreign similar title", entry.ID)
			}
			seen[entry.ID] = true
		}
		if cursor == "" {
			first = page.Items.NextCursor
		}
		if cursor = page.Items.NextCursor; cursor == "" {
			break
		}
	}
	if len(seen) != len(expected) {
		t.Fatalf("similar exhaustion %d/%d", len(seen), len(expected))
	}
	// A cursor names its basis: asked by sound, a seed with no vector falls
	// back to the same genre ranking, which its cursor continues.
	if page, err := s.ListeningSimilarPage(v, n["album"].Public, true, first, 24); err != nil || page.BySound || len(page.Items.Entries) != 24 {
		t.Fatal("genre fallback did not continue its ranking", err)
	}
	c.Album(n["artist"], "Late arrival", 2021)
	c.Drain()
	if _, err := s.ListeningSimilarPage(v, n["album"].Public, false, first, 24); !errors.Is(err, ErrStaleContinuation) {
		t.Fatal("a publication continued the old ranking", err)
	}
}
