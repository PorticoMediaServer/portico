package metadata

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"testing"
)

func TestMusicBrainzPublicationRestoresRetainedWatchedState(t *testing.T) {
	db, _ := mbDB(t)
	defer db.Close()
	c := catalogtest.New(t, db)
	// Keep an old recording's state, with a different locator from the new song.
	artist := c.Artist(c.Handle("lib"), "Old Artist")
	album := c.Album(artist, "Old Album", 1999)
	old := c.Song(album, 1, "/music/old.flac", "Old Song")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFieldsTx(ctx, tx, old.ID, compactcatalog.Automatic, map[string]any{"recording_id": mbRecord})
	})
	c.Exec(`INSERT INTO personal_watched_intents VALUES('p',?,1,'2026-01-01T00:00:00.000000000Z')`, old.ID)
	c.Write(func(ctx context.Context, tx *sql.Tx) error { return catalog.RemoveTitleTx(ctx, tx, old.ID) })
	asset := mbAssetToken(t, db)
	c.Exec(`INSERT INTO audio_tag_evidence VALUES('lib',?,'musicbrainz_trackid','embedded',?)`, asset, mbRecord)
	// Isolate the recording job; the album does not need a provider match.
	c.Exec(`UPDATE mb_jobs SET status='unresolved' WHERE kind='album'`)
	s := New(db, "")
	s.mb = mbFixture{record: func(_ context.Context, id string) (metadataprovider.RecordingLookup, error) {
		return metadataprovider.RecordingLookup{RequestedID: id, Recording: mbRecordingFixture()}, nil
	}}
	for i := 0; i < 3; i++ {
		if err := s.MusicBrainzStep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	item := mbIntID(t, db, "Local Song")
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM personal_watched_intents WHERE profile_id='p' AND item_id=? AND watched=1`, item).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("accepted recording did not restore watched state: %d", n)
	}
}
