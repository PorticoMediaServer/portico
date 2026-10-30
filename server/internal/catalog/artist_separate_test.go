package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
)

// "Not this artist": albums filed under the wrong artist move to an artist of
// their own with their songs' credits, the artist they left keeps the rest,
// and provider identity never joins the two again.
func TestOwnerSeparatesAlbumsFromAnArtist(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(music, "Nirvana")
	grunge := c.Album(artist, "Nevermind", 1991)
	sixties := c.Album(artist, "The Story of Simon Simopath", 1968)
	c.Song(grunge, 1, "/music/a/01.flac", "Smells Like Teen Spirit")
	old := c.Song(sixties, 1, "/music/b/01.flac", "Wings of Love")
	c.Drain()
	s := New(c.DB)
	allow := func(*sql.Tx) error { return nil }
	ctx := context.Background()

	// Every album leaving would leave nothing to tell apart.
	if _, err := s.SeparateArtist(ctx, []string{c.Public(grunge.ID), c.Public(sixties.ID)}, allow); !errors.Is(err, ErrAdminQuery) {
		t.Fatalf("moving every album: %v", err)
	}
	public, err := s.SeparateArtist(ctx, []string{c.Public(sixties.ID)}, allow)
	if err != nil {
		t.Fatal(err)
	}
	separate, err := entityid.Resolve(ctx, c.DB, public)
	if err != nil || separate == artist.ID {
		t.Fatalf("new artist %s: %v", public, err)
	}
	// The page that opens next reads the new artist before any derivation has run.
	if err := s.VisibleEntity(ctx, Viewer{Libraries: []string{"music"}}, "artist", public); err != nil {
		t.Fatalf("the new artist is not visible at once: %v", err)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_albums WHERE artist_id=? AND entity_id=?`, separate, sixties.ID); n != 1 {
		t.Fatal("the album did not move")
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=? AND parent_id=?`, sixties.ID, separate); n != 1 {
		t.Fatal("the album's parent did not move")
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_albums WHERE artist_id=? AND entity_id=?`, artist.ID, grunge.ID); n != 1 {
		t.Fatal("the other album left too")
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_song_artists WHERE song_id=? AND artist_id=?`, old.ID, separate); n != 1 {
		t.Fatal("the song's credit did not follow its album")
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=? AND title='Nirvana'`, separate); n != 1 {
		t.Fatal("the new artist is not named like the old")
	}
	// The same MusicBrainz artist on both never joins them back.
	p := compactcatalog.ProviderIdentity{Provider: "musicbrainz", Kind: "artist", ID: "mb-artist"}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for _, id := range []int64{separate, artist.ID} {
			if err := compactcatalog.RecordProviderIdentityTx(ctx, tx, id, p); err != nil {
				return err
			}
		}
		_, err := AdoptProviderIdentityTx(ctx, tx, artist.ID, p)
		return err
	})
	if n := rows(t, c, `SELECT count(*) FROM catalog_entities WHERE kind=? AND retired=0`, int(compactcatalog.Artist)); n != 2 {
		t.Fatalf("artists after an identity join: %d", n)
	}
}
