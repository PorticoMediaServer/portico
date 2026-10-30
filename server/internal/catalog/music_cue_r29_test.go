package catalog

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"testing"
)

func TestMusicCueVirtualTracksR29(t *testing.T) {
	s, db := openEmptyAlbumDB(t)
	c := catalogtest.New(t, db)
	var token string
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, v, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/music/Album/disc.flac", Size: 100, ModifiedNS: 1, Container: "flac", AudioCodec: "flac", Duration: 120})
		token = v
		return e
	})
	f := assets.Facts{AudioCodec: "flac", Container: "flac", Duration: 120, MusicCueTracks: []assets.Chapter{{Title: "One", Start: 0, End: 60}, {Title: "Two", Start: 60, End: 120}}, Streams: []assets.Stream{{Type: "audio", Codec: "flac", Channels: 6, Detail: assets.StreamDetail{SampleRate: 96000, BitsPerSample: 24}}}}
	assets.CopyEmbeddedAudioTags(&f, map[string]string{"title": "Disc", "album": "Album", "artist": "Artist"})
	commit := func(f assets.Facts) {
		t.Helper()
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		if e = s.commitAudio(context.Background(), tx, "lib", "music", "/music", token, "/music/Album/disc.flac", f); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
		c.Drain()
	}
	commit(f)
	commit(f)
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM catalog_songs s JOIN catalog_asset_links l ON l.entity_id=s.entity_id WHERE l.asset_id=(SELECT id FROM catalog_assets WHERE token=?)`, token).Scan(&n); e != nil || n != 2 {
		t.Fatal("cue replay duplicated/lost tracks", n, e)
	}
	rows, e := db.Query(`SELECT e.title,l.start_seconds,l.end_seconds FROM music_virtual_tracks v JOIN catalog_entities e ON e.id=v.entity_id JOIN catalog_asset_links l ON l.entity_id=e.id WHERE v.asset_id=? ORDER BY v.ordinal`, token)
	if e != nil {
		t.Fatal(e)
	}
	titles := []string{}
	for rows.Next() {
		var title string
		var a, b float64
		if e = rows.Scan(&title, &a, &b); e != nil {
			t.Fatal(e)
		}
		if b-a != 60 {
			t.Fatal(a, b)
		}
		titles = append(titles, title)
	}
	rows.Close()
	if len(titles) != 2 || titles[0] != "One" || titles[1] != "Two" {
		t.Fatal(titles)
	}
	var public string
	if e = db.QueryRow(`SELECT pid(e.public_id) FROM music_virtual_tracks v JOIN catalog_entities e ON e.id=v.entity_id WHERE v.asset_id=? AND v.ordinal=2`, token).Scan(&public); e != nil {
		t.Fatal(e)
	}
	facts, e := s.musicFacts(public)
	if e != nil || facts == nil || facts.Channels != 6 || facts.BitDepth != 24 || facts.SampleRate != 96000 || facts.Lossless == nil || !*facts.Lossless {
		t.Fatal(facts, e)
	}
	secondPublic := public
	f.MusicCueTracks = nil
	commit(f)
	if e = db.QueryRow(`SELECT count(*) FROM music_virtual_tracks WHERE asset_id=?`, token).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	var retired bool
	if e = db.QueryRow(`SELECT retired FROM catalog_entities WHERE public_id=pid_blob(?)`, secondPublic).Scan(&retired); e != nil || !retired {
		t.Fatal("removed cue remains in browse", retired, e)
	}
}
