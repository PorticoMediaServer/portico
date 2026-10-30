package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

func TestMusicMixesSeparateHistoryCoverageAndExpirePrivateSelections(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("music", "Music", "music", "/music")
	album := c.Album(c.Artist(library, "Actual artist"), "Album", 2026)
	at := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	items := []catalogtest.Item{}
	for n := 0; n < 8; n++ {
		item := c.Song(album, n+1, fmt.Sprintf("/music/%d.flac", n), fmt.Sprintf("Song %d", n))
		sonicStoredFixture(t, c, item, float32(8-n), float32(n+1))
		items = append(items, item)
	}
	c.Attributes(items[7].ID, "label", "private")
	c.Drain()
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for n, item := range append(append([]catalogtest.Item{}, items[:6]...), items[:6]...) {
			_, err := tx.ExecContext(ctx, `INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,watched_ms,completed) VALUES(?,'local','account','profile','Fixture',?,'music',7,?,?,?,100000,1)`, fmt.Sprint(n), item.ID, item.Public, at.Add(-time.Hour).UnixMilli(), at.Add(-time.Hour).UnixMilli())
			if err != nil {
				return err
			}
		}
		return nil
	})
	viewer := Viewer{Profile: identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}), Libraries: []string{"music"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"private"}}}
	s := New(c.DB)
	got, err := s.MusicMixes(viewer, at)
	if err != nil || got.HistoryTracks != 6 || got.AnalyzedHistoryTracks != 6 || got.LibraryTracks != 7 || got.AnalyzedLibraryTracks != 7 || len(got.Mixes) != 1 {
		t.Fatal(got, err)
	}
	id := got.Mixes[0].ID
	keys, err := s.MusicMixKeys(viewer, at, id)
	if err != nil || !reflect.DeepEqual(keys, got.Mixes[0].Cluster.Tracks) {
		t.Fatal(keys, got, err)
	}
	row, err := s.MusicMixRow(viewer, at)
	if err != nil || len(row) != 1 || row[0].ID != id || row[0].NameSource.Artist == nil || row[0].NameSource.Style != nil || len(row[0].Covers) != 3 || len(row[0].Artists) != 1 || row[0].SongCount != 6 || row[0].More {
		t.Fatal("missing-head artist fallback or finite row facts changed", row, err)
	}
	for _, cover := range row[0].Covers {
		if cover.Album.ID != got.Mixes[0].Cluster.CoverAlbums[0].ID || row[0].Tint != musicMixTint(cover.Album.ID) {
			t.Fatal("fabricated cover sample or tint", row)
		}
	}
	actor := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "profile"}
	operation := identity.Token()
	resolves := 0
	current := func(*sql.Tx) (Viewer, error) { resolves++; return viewer, nil }
	authorize := func(*sql.Tx) error { return nil }
	saved, err := s.SaveMusicMix(context.Background(), actor, id, operation, "Actual artist mix", at, current, authorize)
	if err != nil || saved.PlaylistID == "" || resolves != 1 {
		t.Fatal(saved, resolves, err)
	}
	stored := []string{}
	rows, err := c.DB.Query(`SELECT pid(item.public_id) FROM catalog_playlist_entries entry JOIN catalog_playlists playlist ON playlist.id=entry.playlist_id JOIN catalog_entities item ON item.id=entry.item_id WHERE playlist.token=? ORDER BY entry.order_key,entry.id`, saved.PlaylistID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			break
		}
		stored = append(stored, key)
	}
	rows.Close()
	if err != nil || !reflect.DeepEqual(stored, keys) {
		t.Fatal("playlist did not copy exactly captured keys", stored, keys, err)
	}
	other := viewer
	other.Profile = identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "other"})
	if _, err := s.MusicMixKeys(other, at, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("another profile resolved private mix", err)
	}
	other = viewer
	other.Fence = "queue-transport-fence"
	if got, err := s.MusicMixKeys(other, at, id); err != nil || !reflect.DeepEqual(got, keys) {
		t.Fatal("equivalent permitted content changed with transport fence", got, err)
	}
	other.Libraries = []string{}
	if _, err := s.MusicMixKeys(other, at, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("current library revocation admitted mix", err)
	}
	c.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,watched_ms,completed) VALUES('new-play','local','account','profile','Fixture',?,'music',7,?,?,?,100000,1)`, items[0].ID, items[0].Public, at.Add(-time.Minute).UnixMilli(), at.Add(-time.Minute).UnixMilli())
	if _, err := s.MusicMixKeys(viewer, at, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("changed listening retained old mix identity", err)
	}
	replay, err := s.SaveMusicMix(context.Background(), actor, id, operation, "Actual artist mix", at, current, authorize)
	if err != nil || replay.PlaylistID != saved.PlaylistID || resolves != 1 {
		t.Fatal("stale recommendation prevented ordinary creation replay", replay, resolves, err)
	}
	if _, err := s.SaveMusicMix(context.Background(), actor, id, operation, "Different name", at, current, authorize); !errors.Is(err, ErrOperationConflict) {
		t.Fatal("changed copy request replayed", err)
	}
	if _, err := s.SaveMusicMix(context.Background(), actor, id, operation, "Actual artist mix", at, current, func(*sql.Tx) error { return identity.ErrUnauthorized }); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatal("receipt bypassed current actor authorization", err)
	}
	got, err = s.MusicMixes(viewer, at)
	if err != nil || len(got.Mixes) != 1 || got.Mixes[0].ID == id {
		t.Fatal("new play did not address new selection", got, err)
	}
	id = got.Mixes[0].ID
	// Even when played tracks remain analyzed, unplayed stale source facts
	// reduce actual library coverage. Never substitute history's 100 percent.
	c.Exec(`UPDATE music_sonic_embeddings SET model_sha256='old' WHERE entity_id=?`, items[6].ID)
	got, err = s.MusicMixes(viewer, at)
	if err != nil || got.AnalyzedHistoryTracks != 6 || got.AnalyzedLibraryTracks != 6 || got.LibraryTracks != 7 {
		t.Fatal(got, err)
	}
	c.Exec(`UPDATE music_sonic_embeddings SET model_sha256='old' WHERE entity_id=?`, items[0].ID)
	if _, err := s.MusicMixKeys(viewer, at, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("changed membership retained old identity", err)
	}
	c.Exec(`UPDATE music_sonic_embeddings SET model_sha256=?`, sonic.ModelSHA256)
	c.Exec(`INSERT INTO library_sonic_settings(library_id,enabled,revision) VALUES('music',0,1)`)
	got, err = s.MusicMixes(viewer, at)
	if err != nil || got.Model != nil || len(got.Mixes) != 0 || got.LibraryTracks != 0 {
		t.Fatal("Off produced mixes", got, err)
	}
	if _, err := s.MusicMixKeys(viewer, at, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("Off resolved old selection", err)
	}
}

func TestMusicMixIDsBindOrderedMembersAndExactModel(t *testing.T) {
	v := Viewer{Profile: "profile", Fence: "fence"}
	model := sonic.Identity{ID: sonic.ModelID, Version: sonic.ModelVersion, SHA256: sonic.ModelSHA256}
	first := musicMixID(v, model, []string{"one", "two"}, "history")
	if len(first) != 47 || first != musicMixID(v, model, []string{"one", "two"}, "history") || first == musicMixID(v, model, []string{"two", "one"}, "history") || first == musicMixID(v, model, []string{"one", "two"}, "new play") {
		t.Fatal("unstable or order-insensitive identity", first)
	}
	model.Version = "next"
	if first == musicMixID(v, model, []string{"one", "two"}, "history") {
		t.Fatal("model change retained mix identity")
	}
}
