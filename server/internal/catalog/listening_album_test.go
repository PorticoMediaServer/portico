package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// linkAlbumEvidence stores one sealed release revision for an album, with its
// primary type, secondary types and payload genres.
func linkAlbumEvidence(t *testing.T, db *sql.DB, album int64, revision, primary string, secondaries []string, payload string) {
	t.Helper()
	if _, e := db.Exec(`INSERT INTO mb_release_evidence(revision_id,provider_id,digest,title,artist,release_group_id,group_title,primary_type,date,country,barcode,disambiguation,observed_at,payload,sealed) VALUES(?,?,'`+fmt.Sprintf("%064d", 7)+`','T','A','grp','G',?,'2020','US','','','now',?,0)`, revision, revision, primary, payload); e != nil {
		t.Fatal(e)
	}
	for n, value := range secondaries {
		if _, e := db.Exec(`INSERT INTO mb_release_group_types VALUES(?,?,?)`, revision, n, value); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := db.Exec(`UPDATE mb_release_evidence SET sealed=1 WHERE revision_id=?`, revision); e != nil {
		t.Fatal(e)
	}
	catalogtest.New(t, db).PinMBRelease(album, true, revision)
}

// Spec — Page Content §2 item 5: an artist's releases carry their
// release-group type (compilations, EPs and singles split out of albums),
// and read newest-first.
func TestArtistReleaseRolesAndYearOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, names := phase34ListeningFixture(t)
	for _, v := range []struct {
		id, title string
		year      int
	}{{"ep-album", "EP Release", 2021}, {"single-album", "Single Release", 2022}, {"comp-album", "Collected", 2019}, {"plain-album", "Plain", 2018}} {
		album := c.Album(names["artist"], v.title, v.year)
		names[v.id] = album
		names["song-"+v.id] = c.Song(album, 1, "/music/song-"+v.id+".mp3", v.title)
	}
	// An appearance on another artist's album stays an appearance.
	guestArtist := c.Artist(c.Handle("music"), "Guest")
	names["other-release"] = c.Album(guestArtist, "Guest Spot", 2023)
	names["guest-song"] = c.Song(names["other-release"], 1, "/music/guest-song.mp3", "Guest Song")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetSongArtistsTx(ctx, tx, names["guest-song"].ID, []int64{names["artist"].ID})
	})
	linkAlbumEvidence(t, c.DB, names["ep-album"].ID, "rev-ep", "EP", nil, `{}`)
	linkAlbumEvidence(t, c.DB, names["single-album"].ID, "rev-single", "Single", nil, `{}`)
	linkAlbumEvidence(t, c.DB, names["comp-album"].ID, "rev-comp", "Album", []string{"Compilation"}, `{}`)
	c.Drain()
	page, e := s.Content(phase34ListeningRequest("music", "artist", names["artist"].Public))
	if e != nil {
		t.Fatal(e)
	}
	var releases *ContentSection
	for i := range page.Sections {
		if page.Sections[i].ID == "releases" {
			releases = &page.Sections[i]
		}
	}
	if releases == nil {
		t.Fatal("no releases section")
	}
	roles := map[string]string{}
	years := []string{}
	for _, entry := range releases.Entries {
		roles[entry.ID] = entry.Role
		years = append(years, entry.ID)
	}
	if roles[names["ep-album"].Public] != "ep" || roles[names["single-album"].Public] != "single" || roles[names["comp-album"].Public] != "compilation" || roles[names["plain-album"].Public] != "album" || roles[names["album"].Public] != "album" || roles[names["other-release"].Public] != "appearance" {
		t.Fatalf("release roles: %v", roles)
	}
	// Newest first; the unknown-year release sorts with its year, not first.
	for i, want := range names.Publics("other-release", "single-album", "ep-album", "album", "comp-album", "plain-album") {
		if i >= len(years) || years[i] != want {
			t.Fatalf("year order: %v", years)
		}
	}
}

// The album entity carries its label, release-group type and genres; the
// artist entity carries its top local genres.
func TestAlbumEntityLabelTypeGenres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, names := phase34ListeningFixture(t)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		localKey := fmt.Sprintf("release|%d", names["artist"].ID)
		if err := compactcatalog.SetFactsTx(ctx, tx, names["album"].ID, map[string]any{"artist_id": names["artist"].ID, "local_key": localKey, "label": "Owner Label"}); err != nil {
			return err
		}
		for _, entry := range []struct {
			name  string
			terms []compactcatalog.Term
		}{{"song-001", []compactcatalog.Term{{SourceID: "1", Name: "Rock"}}}, {"song-002", []compactcatalog.Term{{SourceID: "1", Name: "Rock"}}}, {"song-003", []compactcatalog.Term{{SourceID: "2", Name: "Jazz"}}}} {
			if err := compactcatalog.SetTermsTx(ctx, tx, names[entry.name].ID, compactcatalog.VocabGenre, "local", entry.terms); err != nil {
				return err
			}
		}
		return nil
	})
	linkAlbumEvidence(t, c.DB, names["album"].ID, "rev-album", "Single", nil, `{"genres":[{"name":"Rock"},{"name":"Pop"}]}`)
	c.Drain()
	page, e := s.Content(phase34ListeningRequest("music", "album", names["album"].Public))
	if e != nil || page.Entity == nil {
		t.Fatal(page, e)
	}
	entity := page.Entity
	if entity.Label != "Owner Label" || entity.AlbumType != "Single" || len(entity.Genres) != 2 || entity.Genres[0] != "Rock" || entity.Genres[1] != "Pop" {
		t.Fatalf("album facts: %+v", entity)
	}
	artist, e := s.Content(phase34ListeningRequest("music", "artist", names["artist"].Public))
	if e != nil || artist.Entity == nil {
		t.Fatal(artist, e)
	}
	if len(artist.Entity.Genres) != 2 || artist.Entity.Genres[0] != "Rock" || artist.Entity.Genres[1] != "Jazz" {
		t.Fatalf("artist genres: %+v", artist.Entity)
	}
}
