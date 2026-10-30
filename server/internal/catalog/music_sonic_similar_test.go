package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

func sonicStoredFixture(t *testing.T, c *catalogtest.Catalog, item catalogtest.Item, x, y float32) {
	t.Helper()
	c.SonicEmbedding(item, x, y)
}

func TestMusicSonicMeansUseOnlyCurrentProfilePermittedSongs(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(lib, "Artist")
	album := c.Album(artist, "Album", 2026)
	one := c.Song(album, 1, "/music/one.flac", "One")
	hidden := c.Song(album, 2, "/music/private.flac", "Private")
	sonicStoredFixture(t, c, one, 1, 0)
	sonicStoredFixture(t, c, hidden, 0, 1)
	c.Attributes(hidden.ID, "label", "private")
	c.Drain()
	queue := func() int {
		t.Helper()
		c.Exec(`DELETE FROM inventory_policy_pending`)
		c.Write(func(ctx context.Context, tx *sql.Tx) error { return QueueSonicRefreshTx(ctx, tx) })
		var count int
		if err := c.DB.QueryRow(`SELECT count(*) FROM inventory_policy_pending WHERE library_id='music'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if queue() != 0 {
		t.Fatal("complete library requeued")
	}
	s := New(c.DB)
	read := func(viewer Viewer, kind int) []sonicGroup {
		t.Helper()
		out := []sonicGroup{}
		err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
			return s.WithContext(ctx).sonicGroups(viewer, kind, 0, func(g sonicGroup) { out = append(out, g) })
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	viewer := Viewer{Profile: "local:a:open", Libraries: []string{"music"}}
	open := read(viewer, 6)
	if len(open) != 1 || open[0].vector[0] < .70 || open[0].vector[1] < .70 {
		t.Fatal("not an equal-track mean", open)
	}
	viewer.Profile = "local:a:restricted"
	viewer.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"private"}}
	private := read(viewer, 6)
	if len(private) != 1 || private[0].vector[0] != 1 || private[0].vector[1] != 0 {
		t.Fatal("hidden song influenced album", private)
	}
	artists := read(viewer, 5)
	if len(artists) != 1 || artists[0].id != artist.ID || artists[0].vector != private[0].vector {
		t.Fatal("artist mean crossed visibility", artists)
	}
	c.Exec(`UPDATE music_sonic_embeddings SET model_sha256='old' WHERE entity_id=?`, one.ID)
	if queue() != 1 {
		t.Fatal("one completed song hid a missing current-model song")
	}
	if actual := read(viewer, 6); len(actual) != 0 {
		t.Fatal("partially analyzed album claimed a current mean", actual)
	}
	c.Exec(`UPDATE music_sonic_embeddings SET model_sha256=? WHERE entity_id=?`, sonic.ModelSHA256, one.ID)
	c.Exec(`UPDATE inventory_objects SET revision='replaced' WHERE id=?`, fmt.Sprint(one.ID))
	if actual := read(viewer, 7); len(actual) != 0 {
		t.Fatal("replaced source vector survived", actual)
	}
	c.Exec(`UPDATE inventory_objects SET revision='r' WHERE id=?`, fmt.Sprint(one.ID))
	c.Exec(`INSERT INTO library_sonic_settings(library_id,enabled,revision) VALUES('music',0,1)`)
	if queue() != 0 {
		t.Fatal("disabled library requeued")
	}
	if actual := read(viewer, 6); len(actual) != 0 {
		t.Fatal("off library influenced sound selection", actual)
	}
	viewer.Libraries = []string{"other"}
	if actual := read(viewer, 7); len(actual) != 0 {
		t.Fatal("scope widened", actual)
	}
	if _, err := s.ListeningSimilarBySound(viewer, one.Public, 12); err != sql.ErrNoRows {
		t.Fatal("seed outside profile admitted", err)
	}
}

func TestMusicSonicSimilarRanksExactCosineAndReportsGenreFallback(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("music", "Music", "music", "/music")
	seed := c.Album(c.Artist(lib, "Seed artist"), "Seed", 2026)
	near := c.Album(c.Artist(lib, "Near artist"), "Near", 2026)
	far := c.Album(c.Artist(lib, "Far artist"), "Far", 2026)
	one := c.Song(seed, 1, "/music/seed.flac", "Seed song")
	two := c.Song(near, 1, "/music/near.flac", "Near song")
	three := c.Song(far, 1, "/music/far.flac", "Far song")
	sonicStoredFixture(t, c, one, 1, 0)
	sonicStoredFixture(t, c, two, .9, .1)
	sonicStoredFixture(t, c, three, 0, 1)
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "local:account:profile", Libraries: []string{"music"}}
	got, err := s.ListeningSimilarBySound(viewer, seed.Public, 12)
	if err != nil || !got.BySound || len(got.Items.Entries) != 2 || got.Items.Entries[0].ID != near.Public || got.Items.Entries[1].ID != far.Public {
		t.Fatalf("exact cosine rank: %+v %v", got, err)
	}
	for _, item := range got.Items.Entries {
		if item.Playback != nil {
			t.Fatal("browse result minted playback", item)
		}
	}
	c.Exec(`INSERT INTO library_sonic_settings(library_id,enabled,revision) VALUES('music',0,1)`)
	got, err = s.ListeningSimilarBySound(viewer, seed.Public, 12)
	if err != nil || got.BySound {
		t.Fatal("off claimed sound rather than existing genre/co-listening", got, err)
	}
}

// Synthetic vectors exercise catalogue/profile grouping, not model inference.
func TestMusicSonicArtistMeanIncludesActualGuestCreditsAndFencesHiddenSongs(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("credited-music", "Music", "music", "/credits")
	main := c.Artist(library, "Album artist")
	guest := c.Artist(library, "Guest artist")
	album := c.Album(main, "Album", 2026)
	one := c.Song(album, 1, "/credits/one.flac", "One")
	hidden := c.Song(album, 2, "/credits/hidden.flac", "Hidden")
	sonicStoredFixture(t, c, one, 1, 0)
	sonicStoredFixture(t, c, hidden, 0, 1)
	for _, item := range []catalogtest.Item{one, hidden} {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.SetSongArtistsTx(ctx, tx, item.ID, []int64{main.ID, guest.ID})
		})
	}
	c.Attributes(hidden.ID, "label", "private")
	c.Drain()
	viewer := Viewer{Profile: identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}), Libraries: []string{"credited-music"}}
	s := New(c.DB)
	read := func() []sonicGroup {
		t.Helper()
		var out []sonicGroup
		if err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
			return s.WithContext(ctx).sonicGroups(viewer, 5, guest.ID, func(group sonicGroup) { out = append(out, group) })
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	groups := read()
	if len(groups) != 1 || groups[0].vector[0] < .70 || groups[0].vector[1] < .70 {
		t.Fatal("guest credits missing from artist mean", groups)
	}
	viewer.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"private"}}
	groups = read()
	if len(groups) != 1 || groups[0].vector[0] != 1 || groups[0].vector[1] != 0 {
		t.Fatal("hidden guest credit affected mean", groups)
	}
	batch, err := s.MusicRadioBatch(viewer, RadioBatchRequest{Seed: guest.Public, Start: true, Limit: 1})
	if err != nil || len(batch.IDs) != 1 || batch.IDs[0] != one.Public || !batch.BySound {
		t.Fatal("credited artist radio did not start a permitted credited track", batch, err)
	}
}
