package catalog

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestMusicRadioRankingUsesCurrentProfileHistoryVisibilityAndArtists(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(lib, "Seed artist")
	seedAlbum := c.Album(artist, "Seed", 2026)
	seed := c.Song(seedAlbum, 1, "/music/seed.flac", "Seed")
	same := c.Song(seedAlbum, 2, "/music/same.flac", "Same artist")
	nearAlbum := c.Album(c.Artist(lib, "Near artist"), "Near", 2026)
	near := c.Song(nearAlbum, 1, "/music/near.flac", "Near")
	farAlbum := c.Album(c.Artist(lib, "Far artist"), "Far", 2026)
	far := c.Song(farAlbum, 1, "/music/far.flac", "Far")
	sonicStoredFixture(t, c, seed, 1, 0)
	sonicStoredFixture(t, c, same, 1, 0)
	sonicStoredFixture(t, c, near, .9, .1)
	sonicStoredFixture(t, c, far, .1, .9)
	c.Genres(seed.ID, "local", "Rock")
	c.Genres(near.ID, "local", "Rock")
	c.Genres(far.ID, "local", "Jazz")
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}), Libraries: []string{"music"}}
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	request := RadioBatchRequest{Seed: seed.Public, LastItem: seed.Public, Limit: 1, Now: now}
	read := func(want string, sound bool) {
		t.Helper()
		got, err := s.MusicRadioBatch(viewer, request)
		if err != nil || len(got.IDs) != 1 || got.IDs[0] != want || got.BySound != sound {
			t.Fatal("radio selection", got, err, "want", want, sound)
		}
	}
	read(near.Public, true) // identical same-artist track must not win.
	c.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,watched_ms) VALUES('other','local','account','other','Fixture',?,'music',7,'Near',?,?,1000)`, near.ID, now.Add(-time.Hour).UnixMilli(), now.Add(-time.Hour).UnixMilli())
	read(near.Public, true) // another profile's history is never borrowed.
	c.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,watched_ms) VALUES('mine','local','account','profile','Fixture',?,'music',7,'Near',?,?,1000)`, near.ID, now.Add(-time.Hour).UnixMilli(), now.Add(-time.Hour).UnixMilli())
	read(far.Public, true)
	c.Attributes(far.ID, "label", "temporarily-blocked")
	c.Drain()
	viewer.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"temporarily-blocked"}}
	read(same.Public, true) // prefer a fresh song over reviving a recent different artist.
	viewer.Restrictions = identity.ContentRestrictions{}
	c.Exec(`DELETE FROM play_history WHERE playback_id='mine'`)
	c.Attributes(near.ID, "label", "private")
	c.Drain()
	viewer.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"private"}}
	read(far.Public, true)
	viewer.Restrictions = identity.ContentRestrictions{}
	c.Exec(`INSERT INTO library_sonic_settings(library_id,enabled,revision) VALUES('music',0,1)`)
	read(near.Public, false) // actual existing genre rule, no sound claim.
	viewer.Libraries = []string{"other"}
	if _, err := s.MusicRadioBatch(viewer, request); err != sql.ErrNoRows {
		t.Fatal("seed scope widened", err)
	}
}

func TestMusicRadioGenreFallbackSpansLibrariesWithoutHiddenSeedFacets(t *testing.T) {
	c := catalogtest.Open(t)
	seedLib := c.Library("seed-music", "Seed Music", "music", "/seed")
	otherLib := c.Library("other-music", "Other Music", "music", "/other")
	album := c.Album(c.Artist(seedLib, "Seed artist"), "Seed", 2026)
	seed := c.Song(album, 1, "/seed/rock.flac", "Rock")
	private := c.Song(album, 2, "/seed/jazz.flac", "Private jazz")
	rock := c.Song(c.Album(c.Artist(otherLib, "Rock artist"), "Rock", 2026), 1, "/other/rock.flac", "Rock elsewhere")
	jazz := c.Song(c.Album(c.Artist(otherLib, "Jazz artist"), "Jazz", 2026), 1, "/other/jazz.flac", "Jazz elsewhere")
	// Fallback still requires genuine available inventory/source facts, but no
	// embedding or inference result exists for any of these songs.
	for _, item := range []catalogtest.Item{seed, private, rock, jazz} {
		c.Exec(`UPDATE catalog_assets SET audio_codec='flac',duration=2 WHERE id=?`, item.Asset)
		c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT ?,src.id,a.token,src.incarnation,a.path,'r','{}',a.size,a.modified_ns FROM catalog_assets a JOIN catalog_asset_links link ON link.asset_id=a.id JOIN catalog_entities item ON item.id=link.entity_id JOIN catalog_libraries library ON library.id=item.library_id JOIN library_sources src ON src.library_id=library.library_id WHERE a.id=? LIMIT 1`, fmt.Sprint(item.ID), item.Asset)
	}
	c.Genres(seed.ID, "local", "Rock")
	c.Genres(private.ID, "local", "Jazz")
	c.Genres(rock.ID, "local", "Rock")
	c.Genres(jazz.ID, "local", "Jazz")
	c.Attributes(private.ID, "label", "private")
	c.Drain()
	viewer := Viewer{Profile: identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}), Libraries: []string{"seed-music", "other-music"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"private"}}}
	s := New(c.DB)
	request := RadioBatchRequest{Seed: album.Public, Exclude: []string{seed.Public}, Limit: 1, Now: time.Now()}
	for cycle := int64(0); cycle < 16; cycle++ {
		request.Cycle = cycle
		batch, err := s.MusicRadioBatch(viewer, request)
		if err != nil || len(batch.IDs) != 1 || batch.IDs[0] != rock.Public || batch.BySound {
			t.Fatal("cross-library genre fallback", cycle, batch, err)
		}
	}
	viewer.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"private", "blocked"}}
	c.Attributes(rock.ID, "label", "blocked")
	c.Drain()
	batch, err := s.MusicRadioBatch(viewer, request)
	if err != nil || len(batch.IDs) != 1 || batch.IDs[0] != jazz.Public {
		t.Fatal("hidden cross-library candidate", batch, err)
	}
}
