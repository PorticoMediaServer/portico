package catalog

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"context"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// TestContinueListeningLargeHistoryLatency is an opt-in measurement: one
// profile with a long unfinished listening history across two music
// libraries. It reports the Continue Listening row (first page and a cursor
// page) and, for comparison, the uncapped count the row used to run.
//
//	PORTICO_LISTENING_BENCH=200000 go test ./internal/catalog -run TestContinueListeningLargeHistoryLatency -count=1 -v
func TestContinueListeningLargeHistoryLatency(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_LISTENING_BENCH"))
	if n <= 0 {
		t.Skip("set PORTICO_LISTENING_BENCH to the history size")
	}
	c := catalogtest.Open(t)
	db := c.DB
	music := c.Library("music", "Music", "music", "/music")
	music2 := c.Library("music2", "More Music", "music", "/music2")
	c.Library("movies", "Movies", "movie", "/movies")
	artist := c.Artist(music, "Bench Artist")
	album := c.Album(artist, "Bench Album", 2020)
	artist2 := c.Artist(music2, "Bench Artist 2")
	album2 := c.Album(artist2, "Bench Album 2", 2020)
	artists := map[string]int64{"music": artist.ID, "music2": artist2.ID}
	albums := map[string]int64{"music": album.ID, "music2": album2.ID}
	seedStart := time.Now()
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("s%09d", i)
			libraryID, root := music, "/music"
			if i%3 == 0 {
				libraryID, root = music2, "/music2"
			}
			library := "music"
			if libraryID == music2 {
				library = "music2"
			}
			path := root + "/" + id + ".flac"
			stamp := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute).Format("2006-01-02T15:04:05.000Z")
			entity, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: libraryID, Kind: compactcatalog.Track, Parent: albums[library], Key: compactcatalog.ItemKey(root, path, 0), Title: id, Added: "2026-03-01T00:00:00.000Z"})
			if err != nil {
				return err
			}
			if err = compactcatalog.SetFactsTx(ctx, tx, entity, map[string]any{"album_id": albums[library], "track_number": i + 1}); err != nil {
				return err
			}
			if err = compactcatalog.SetSongArtistsTx(ctx, tx, entity, []int64{artists[library]}); err != nil {
				return err
			}
			assetID, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "flac", AudioCodec: "flac", Duration: 600})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, entity, assetID, compactcatalog.Link{}); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,120000,0,?)`, entity, "play-"+id); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES('p',?,?,?,'paused')`, library, entity, stamp); err != nil {
				return err
			}
		}
		return nil
	})
	c.Drain()
	if _, err := db.Exec(`ANALYZE`); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d unfinished songs in %s", n, time.Since(seedStart).Round(time.Millisecond))
	s := New(db)
	libraries := []string{"music", "music2", "movies"}
	r := HomeRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: libraries}, ServerID: "server", Profile: "p", ViewerFence: "f", Libraries: libraries, Now: time.Now()}
	measure := func(label string, read func() error) {
		best := time.Duration(1 << 62)
		for k := 0; k < 5; k++ {
			start := time.Now()
			if err := read(); err != nil {
				t.Fatal(label, err)
			}
			best = min(best, time.Since(start))
		}
		t.Logf("%s: best of 5 %s", label, best.Round(10*time.Microsecond))
	}
	var cursor string
	measure("listen_next first page", func() error {
		row, err := s.HomeSingleRow(r, "listen_next", HomeRowPage{Limit: 12})
		if err == nil && (rowTotal(row) != 2 || len(row.Entries) != 2 || row.HasMore || row.NextCursor != "") {
			err = fmt.Errorf("row total %d entries %d", row.Total, len(row.Entries))
		}
		cursor = row.NextCursor
		return err
	})
	measure("listen_next subsequent page", func() error {
		_, err := s.HomeSingleRow(r, "listen_next", HomeRowPage{Limit: 12, Cursor: cursor})
		return err
	})
	measure("uncapped count (previous per-request cost)", func() error {
		var total int
		return db.QueryRow(`SELECT count(*) FROM (`+homeContinueBase("continue_listening")+`)`, "p", `["music","music2","movies"]`).Scan(&total)
	})
}
