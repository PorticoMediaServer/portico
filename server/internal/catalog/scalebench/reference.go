package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The reference catalogue: 2% films, 39% episodes across shows of 250, 59% songs
// across 12-track albums, six credits and three genres per item, a content
// rating on one item in five, and progress for three profiles on one in a hundred. Every row is
// written through the catalogue write API, exactly as a scan and a metadata
// publication would, and the derived-data worker drains after each batch, so a
// batch's cost per item is the real per-item cost of cataloguing: it must stay
// flat from the first batch to the last.

type referenceShape struct {
	movies, episodes, songs, shows, albums, people int
}

func shapeOf(n int) referenceShape {
	s := referenceShape{movies: max(1, n*25/1275), episodes: n * 500 / 1275}
	s.songs = n - s.movies - s.episodes
	s.shows = max(2, s.episodes/250)
	s.albums = max(2, s.songs/12)
	s.people = max(1, n*300/1275)
	return s
}

// seedReference writes items [startItem, stopItem) in batches and reports the
// first and last batch's milliseconds per item (write plus derivation).
func seedReference(db *sql.DB, n, startItem, stopItem, batch int, derive bool) (first, last float64, err error) {
	if startItem < 0 || stopItem > n || startItem > stopItem {
		return 0, 0, errors.New("invalid scale seed range")
	}
	ctx := context.Background()
	shape := shapeOf(n)
	if startItem == 0 {
		if _, err = db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('movies','Movies','movie','/fixture/movies'),('tv','TV','tv','/fixture/tv'),('music','Music','music','/fixture/music')`); err != nil {
			return
		}
	}
	worker := compactcatalog.NewWorker(db)
	var window seedWindow
	window.begin(ctx, db, startItem)
	for offset := startItem; offset < stopItem; offset += batch {
		if err = checkScratchBudget(); err != nil {
			return
		}
		start := time.Now()
		end := min(stopItem, offset+batch)
		err = dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			for j := offset; j < end; j++ {
				i := seedOrder(j, n)
				if e := seedItem(ctx, tx, shape, i); e != nil {
					return fmt.Errorf("item %d: %w", i, e)
				}
			}
			return nil
		})
		if err != nil {
			return
		}
		window.write += time.Since(start)
		deriveStart := time.Now()
		for derive {
			var done int
			if done, err = worker.Step(ctx, 500); err != nil {
				return
			}
			if done == 0 {
				break
			}
		}
		window.derive += time.Since(deriveStart)
		last = float64(time.Since(start)) / float64(time.Millisecond) / float64(end-offset)
		if offset == startItem {
			first = last
		}
		if curveWindow > 0 && (end-window.from >= curveWindow || end == stopItem) {
			window.report(ctx, db, end)
			window.begin(ctx, db, end)
		} else if curveWindow == 0 && (end%10000 < batch || end == n) {
			fmt.Fprintf(os.Stderr, "seed %d/%d latest %.3f ms/item\n", end, n, last)
		}
	}
	return
}

// Curve instrumentation: -curve-window reports, for every window of seeded
// items, the write and derivation milliseconds per item, the writer's row
// changes per item and (with -curve-shapes) the statement shapes whose cost
// per item grew. -interleave seeds the reference shape in a scattered order so
// films, episodes and songs arrive throughout, as a growing library's would,
// and each window measures the same mix at a larger size.
var (
	curveWindow  int
	curveShapes  int
	interleaving bool
)

// seedOrder maps the j-th seeded position to a reference item. Interleaving
// multiplies by a prime larger than any fixture, a bijection on [0,n).
func seedOrder(j, n int) int {
	if !interleaving {
		return j
	}
	return int((int64(j) * 1000003) % int64(n))
}

type seedWindow struct {
	from          int
	write, derive time.Duration
	changes       int64
}

func (w *seedWindow) begin(ctx context.Context, db *sql.DB, from int) {
	*w = seedWindow{from: from}
	if curveWindow == 0 {
		return
	}
	w.changes, _ = writerChangeCount(ctx, db)
	dbwork.ProfileStatements(curveShapes > 0)
}

func (w *seedWindow) report(ctx context.Context, db *sql.DB, end int) {
	items := float64(end - w.from)
	changes, _ := writerChangeCount(ctx, db)
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) / items }
	fmt.Fprintf(os.Stderr, "CURVE items=%d write_ms_per_item=%.3f derive_ms_per_item=%.3f total_ms_per_item=%.3f changes_per_item=%.1f rss_kb=%d\n",
		end, ms(w.write), ms(w.derive), ms(w.write+w.derive), float64(changes-w.changes)/items, processRSSKB())
	for i, entry := range dbwork.StatementProfile() {
		if i == curveShapes {
			break
		}
		fmt.Fprintf(os.Stderr, "CURVE_SHAPE items=%d us_per_item=%.1f calls_per_item=%.2f mean_us=%.1f %s\n",
			end, float64(entry.Nanos)/1000/items, float64(entry.Count)/items, entry.MeanMicros(), entry.Shape)
	}
}

// seedItem writes item i and, the first time one is needed, its container.
func seedItem(ctx context.Context, tx *sql.Tx, s referenceShape, i int) error {
	library, kind := "movies", compactcatalog.Movie
	if i >= s.movies {
		library, kind = "tv", compactcatalog.Episode
	}
	if i >= s.movies+s.episodes {
		library, kind = "music", compactcatalog.Track
	}
	handle, err := compactcatalog.LibraryTx(ctx, tx, library)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/fixture/%s/item%09d.mp4", library, i)
	var parent int64
	facts := map[string]any{}
	switch kind {
	case compactcatalog.Episode:
		num := i - s.movies
		show, number := 0, num+1
		if num >= 1100 {
			show = 1 + (num-1100)%(s.shows-1)
			number = 1 + (num-1100)/(s.shows-1)
		}
		showID, seasonID, err := seedShow(ctx, tx, handle, show)
		if err != nil {
			return err
		}
		parent = seasonID
		facts = map[string]any{"show_id": showID, "season_id": seasonID, "numbering": "seasonal", "number": number}
	case compactcatalog.Track:
		num := i - s.movies - s.episodes
		album := min(s.albums-1, num/12)
		artist := album/5 + 1
		if album < 417 {
			artist = 0
		}
		albumID, _, err := seedAlbum(ctx, tx, handle, album, artist)
		if err != nil {
			return err
		}
		parent = albumID
		facts = map[string]any{"album_id": albumID, "disc_number": 1, "track_number": num%12 + 1}
	}
	item, _, err := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: kind, Parent: parent, Key: compactcatalog.ItemKey("/fixture/"+library, path, 0),
		Title: fmt.Sprintf("Title %09d", i), Year: 1950 + i%76, Added: time.Date(2026, 1, 1, 0, 0, i%86400, 0, time.UTC).Format("2006-01-02T15:04:05.000Z")})
	if err != nil {
		return err
	}
	if len(facts) > 0 {
		if err = compactcatalog.SetFactsTx(ctx, tx, item, facts); err != nil {
			return err
		}
	}
	if kind == compactcatalog.Track {
		var artist int64
		if err = tx.QueryRowContext(ctx, `SELECT artist_id FROM catalog_albums WHERE entity_id=?`, parent).Scan(&artist); err != nil {
			return err
		}
		if err = compactcatalog.SetSongArtistsTx(ctx, tx, item, []int64{artist}); err != nil {
			return err
		}
	}
	asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1048576, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 3600})
	if err != nil {
		return err
	}
	if err = compactcatalog.LinkAssetTx(ctx, tx, item, asset, compactcatalog.Link{}); err != nil {
		return err
	}
	if i%10 < 2 {
		rating := "G"
		if i%10 == 1 {
			rating = "R"
		}
		if err = compactcatalog.SetAttributesTx(ctx, tx, item, "contentRating", []string{rating}); err != nil {
			return err
		}
	}
	credits := make([]compactcatalog.Credit, 6)
	for c := range credits {
		person := fmt.Sprint("person", (i*3+c)%s.people)
		credits[c] = compactcatalog.Credit{PersonKey: "fixture:" + person, PersonName: "Person " + person, ProviderPersonID: person, CreditID: person, CreditedName: "Person " + person, Role: "Actor", Department: "Acting", Ordinal: c}
	}
	if err = compactcatalog.SetCreditsTx(ctx, tx, item, "fixture", credits); err != nil {
		return err
	}
	genres := make([]compactcatalog.Term, 3)
	for g := range genres {
		genre := fmt.Sprint((i + g) % 5000)
		genres[g] = compactcatalog.Term{SourceID: genre, Name: "Genre " + genre}
	}
	if err = compactcatalog.SetTermsTx(ctx, tx, item, compactcatalog.VocabGenre, "fixture", genres); err != nil {
		return err
	}
	if i%100 == 0 {
		for _, profile := range []string{"viewer", "viewer-two", "viewer-pg13"} {
			completed := 0
			state := "paused"
			if profile != "viewer" && i%300 == 0 {
				completed = 1
				state = "ended"
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES(?,?,120000,0,?,'fixture')`, profile, item, completed); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES(?,?,?,'2026-09-22T12:00:00Z',?)`, profile, library, item, state); err != nil {
				return err
			}
		}
	}
	return nil
}

func seedShow(ctx context.Context, tx *sql.Tx, library int64, n int) (int64, int64, error) {
	key := fmt.Sprint(n)
	show, created, err := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Show, Key: compactcatalog.ShowKey(key), Title: fmt.Sprintf("Show %06d", n)})
	if err != nil {
		return 0, 0, err
	}
	if created {
		if err = compactcatalog.SetFactsTx(ctx, tx, show, map[string]any{"local_key": key}); err != nil {
			return 0, 0, err
		}
	}
	season, created, err := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Season, Parent: show, Key: compactcatalog.SeasonKey(key, 1), Title: "Season 1"})
	if err != nil {
		return 0, 0, err
	}
	if created {
		err = compactcatalog.SetFactsTx(ctx, tx, season, map[string]any{"show_id": show, "number": 1})
	}
	return show, season, err
}

func seedAlbum(ctx context.Context, tx *sql.Tx, library int64, album, artist int) (int64, int64, error) {
	artistKey := fmt.Sprint(artist)
	artistID, created, err := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Artist, Key: compactcatalog.ArtistKey(artistKey), Title: fmt.Sprintf("Artist %06d", artist)})
	if err != nil {
		return 0, 0, err
	}
	if created {
		if err = compactcatalog.SetFactsTx(ctx, tx, artistID, map[string]any{"local_key": artistKey}); err != nil {
			return 0, 0, err
		}
	}
	key := fmt.Sprint(album)
	albumID, created, err := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Album, Parent: artistID, Key: compactcatalog.AlbumKey(key), Title: fmt.Sprintf("Album %06d", album), Year: 2000})
	if err != nil {
		return 0, 0, err
	}
	if created {
		err = compactcatalog.SetFactsTx(ctx, tx, albumID, map[string]any{"artist_id": artistID, "local_key": key})
	}
	return albumID, artistID, err
}
