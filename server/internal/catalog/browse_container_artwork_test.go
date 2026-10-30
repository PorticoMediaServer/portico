package catalog

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// NEW-23: browse results carry the selected poster for shows and the selected
// portrait for artists, through the same batched resolver search and Home use
// (one read per page, no per-row lookup).
func TestBrowseShowAndArtistRowsCarrySelectedArtwork(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	music := c.Library("music", "Music", "music", "/mu")
	names := catalogtest.Names{}
	names["show"] = c.Show(tv, "Harbor Story", 2026)
	names["bare"] = c.Show(tv, "Bare Show", 2026)
	names["artist"] = c.Artist(music, "Harbor Artist")
	season := c.Season(names["show"], 1)
	c.Episode(names["show"], season, 1, "/tv/episode.mkv")
	bareSeason := c.Season(names["bare"], 1)
	c.Episode(names["bare"], bareSeason, 1, "/tv/episode2.mkv")
	album := c.Album(names["artist"], "Album", 2026)
	c.Song(album, 1, "/mu/song.flac", "Song")
	poster, portrait, thumb := strings.Repeat("a", 64), strings.Repeat("c", 64), strings.Repeat("b", 64)
	c.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at,status) VALUES(?, 'image/png',1000,1500,100,'now','ready'),(?, 'image/png',1000,1000,100,'now','ready'),(?, 'image/png',400,400,50,'now','ready'); INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`, poster, portrait, thumb)
	c.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) VALUES('show-poster','show',?,'poster','','fixture','show','local','fixture','fence','now'),('artist-portrait','artist',?,'portrait','','fixture','artist','local','fixture','fence','now')`, names["show"].ID, names["artist"].ID)
	c.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) SELECT column1,column2,column3,column4,column5,column6,column8,column9,column10,column11 FROM (VALUES('show',?,'poster','','show-poster',?, ?,0,1,'fixture','now'),('artist',?,'portrait','','artist-portrait',?, ?,0,1,'fixture','now'))`, names["show"].ID, poster, thumb, names["artist"].ID, portrait, thumb)
	c.Drain()
	s := New(c.DB)
	for _, item := range []struct {
		library, pivot, name, digest string
		height                       int
	}{{"tv", "shows", "show", poster, 1500}, {"music", "artists", "artist", portrait, 1000}} {
		page, e := s.BrowseEntities(Viewer{Profile: "p", Fence: "f", Libraries: []string{item.library}}, BrowseRequest{Library: item.library, Profile: "p", ViewerFence: "f", Pivot: item.pivot, Limit: 20})
		if e != nil {
			t.Fatal(item.pivot, e)
		}
		found := false
		for _, entry := range page.Entries {
			if entry.ID == names[item.name].Public {
				found = true
				if !strings.Contains(entry.PosterURL, "?"+fixtureArtQuery(item.digest, 1000, item.height, 100)) {
					t.Fatalf("%s %s has no selected artwork: %+v", item.pivot, item.name, entry)
				}
			} else if entry.PosterURL != "" {
				t.Fatalf("%s %s without a selection carries artwork: %+v", item.pivot, entry.ID, entry)
			}
		}
		if !found {
			t.Fatalf("%s page lacks %s: %+v", item.pivot, item.name, page.Entries)
		}
	}
}
