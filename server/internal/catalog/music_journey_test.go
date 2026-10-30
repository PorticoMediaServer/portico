package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

func TestMusicJourneyStreamsOnlyCurrentProfilePermittedModelTracks(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("music", "Music", "music", "/music")
	album := c.Album(c.Artist(library, "Artist"), "Album", 2026)
	items := []catalogtest.Item{}
	for n := 0; n <= 8; n++ {
		item := c.Song(album, n+1, fmt.Sprintf("/music/%d.flac", n), fmt.Sprintf("Song %d", n))
		sonicStoredFixture(t, c, item, float32(8-n), float32(n))
		items = append(items, item)
	}
	c.Attributes(items[4].ID, "label", "private")
	c.Drain()
	viewer := Viewer{Profile: identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}), Libraries: []string{"music"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"private"}}}
	s := New(c.DB)
	got, err := s.MusicJourney(viewer, items[0].Public, items[8].Public, 5)
	if err != nil || len(got.Tracks) != 5 || got.Tracks[0] != items[0].Public || got.Tracks[4] != items[8].Public || got.Model == nil || got.Model.SHA256 != sonic.ModelSHA256 {
		t.Fatal(got, err)
	}
	for _, id := range got.Tracks {
		if id == items[4].Public {
			t.Fatal("hidden intermediate shaped journey", got)
		}
	}
	if _, err := s.MusicJourney(viewer, items[0].Public, items[8].Public, 9); !errors.Is(err, sonic.ErrUnavailable) {
		t.Fatal("sparse permitted coverage silently shortened the requested journey", err)
	}
	if _, err := s.MusicJourney(viewer, items[4].Public, items[8].Public, 5); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("hidden anchor disclosed", err)
	}
	c.Exec(`UPDATE music_sonic_embeddings SET model_sha256='old' WHERE entity_id=?`, items[8].ID)
	if _, err := s.MusicJourney(viewer, items[0].Public, items[8].Public, 5); !errors.Is(err, sonic.ErrUnavailable) {
		t.Fatal("stale endpoint claimed current journey", err)
	}
	c.Exec(`UPDATE music_sonic_embeddings SET model_sha256=? WHERE entity_id=?`, sonic.ModelSHA256, items[8].ID)
	c.Exec(`INSERT INTO library_sonic_settings(library_id,enabled,revision) VALUES('music',0,1)`)
	if _, err := s.MusicJourney(viewer, items[0].Public, items[8].Public, 5); !errors.Is(err, sonic.ErrUnavailable) {
		t.Fatal("Off produced a sonic journey", err)
	}
}
