package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// The ingest scenarios measure what one addition, change or removal costs on
// an existing fixture, so the same numbers taken at several library sizes
// show whether the cost per item stays flat. Each prints its milliseconds per
// unit and the statement shapes that took the most time, with their mean cost
// per call: a shape whose mean grows with the library is the one to fix.

func profileReport(name string, units int, top int) {
	for i, entry := range dbwork.StatementProfile() {
		if i == top {
			break
		}
		fmt.Fprintf(os.Stderr, "INGEST_SHAPE %s us_per_unit=%.1f calls_per_unit=%.2f mean_us=%.1f %s\n", name, float64(entry.Nanos)/1000/float64(units), float64(entry.Count)/float64(units), entry.MeanMicros(), entry.Shape)
	}
}

var lastCache dbwork.StatementCacheStats

// containerProfile reports the statements of the watched action and its undo
// alone, without the fixture show's creation.
var containerProfile bool

func ingestResult(name string, units int, write, derive time.Duration, gate uint64) {
	cache := dbwork.Reads().StatementCache
	fmt.Fprintf(os.Stderr, "INGEST_STATEMENT_CACHE %s hits=%d misses=%d evictions=%d bypasses=%d\n", name, cache.Hits-lastCache.Hits, cache.Misses-lastCache.Misses, cache.Evictions-lastCache.Evictions, cache.Bypasses-lastCache.Bypasses)
	lastCache = cache
	per := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) / float64(units) }
	fmt.Fprintf(os.Stderr, "INGEST %s units=%d write_ms_per_unit=%.3f derive_ms_per_unit=%.3f total_ms_per_unit=%.3f max_gate_ms=%d\n", name, units, per(write), per(derive), per(write+derive), gate)
}

func drainTimed(ctx context.Context, db *sql.DB) (time.Duration, error) {
	// The server's worker commits BatchLimit units per transaction.
	start := time.Now()
	w := compactcatalog.NewWorker(db)
	for {
		n, err := w.Step(ctx, compactcatalog.BatchLimit)
		if err != nil || n == 0 {
			return time.Since(start), err
		}
	}
}

// ingestScenarios runs the addition, progress, watched and removal scenarios.
func ingestScenarios(db *sql.DB, s *catalog.Service, removal bool) error {
	ctx := context.Background()
	if err := compactcatalog.Drain(ctx, db); err != nil {
		return err
	}
	namespace := strconv.FormatInt(time.Now().UnixNano(), 10)
	// 1. Films, 1–3 versions each, written as the scanner writes them.
	if err := ingestBatch(ctx, db, "add-films", 400, func(tx *sql.Tx, i int) error {
		library, err := compactcatalog.LibraryTx(ctx, tx, "movies")
		if err != nil {
			return err
		}
		path := fmt.Sprintf("/fixture/movies/ingest-%s-%04d.mp4", namespace, i)
		item, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/fixture/movies", path, 0), Title: fmt.Sprintf("Ingest %s %04d", namespace, i), Year: 2000 + i%25, Added: "2026-10-13T00:00:00.000Z"})
		if err != nil {
			return err
		}
		for version := 0; version < 1+i%3; version++ {
			asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: fmt.Sprintf("%s.%d", path, version), Size: 1000000, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 5400})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, item, asset, compactcatalog.Link{}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	// 2. Episodes added to the largest existing show.
	var show, season int64
	if err := db.QueryRow(`SELECT e.show_id,e.season_id FROM catalog_episodes e GROUP BY e.show_id ORDER BY count(*) DESC LIMIT 1`).Scan(&show, &season); err != nil {
		return err
	}
	if err := ingestBatch(ctx, db, "add-episodes-big-show", 200, func(tx *sql.Tx, i int) error {
		library, err := compactcatalog.LibraryTx(ctx, tx, "tv")
		if err != nil {
			return err
		}
		path := fmt.Sprintf("/fixture/tv/ingest-%s-%04d.mp4", namespace, i)
		item, _, err := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Episode, Parent: season, Key: compactcatalog.ItemKey("/fixture/tv", path, 0), Title: fmt.Sprintf("Ingest episode %s %d", namespace, i), Year: 2001, Added: "2026-10-13T00:00:00.000Z"})
		if err != nil {
			return err
		}
		if err = compactcatalog.SetFactsTx(ctx, tx, item, map[string]any{"show_id": show, "season_id": season, "numbering": "seasonal", "number": 1000000 + int(time.Now().UnixNano()/1000)%1000000*1000 + i}); err != nil {
			return err
		}
		asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000000, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 2400})
		if err != nil {
			return err
		}
		return compactcatalog.LinkAssetTx(ctx, tx, item, asset, compactcatalog.Link{})
	}); err != nil {
		return err
	}
	// 3. Songs added to an album of the artist with the most albums.
	var album, artist int64
	if err := db.QueryRow(`SELECT a.entity_id,a.artist_id FROM catalog_albums a WHERE a.artist_id=(SELECT artist_id FROM catalog_albums GROUP BY artist_id ORDER BY count(*) DESC LIMIT 1) ORDER BY a.entity_id LIMIT 1`).Scan(&album, &artist); err != nil {
		return err
	}
	if err := ingestBatch(ctx, db, "add-songs-big-artist", 200, func(tx *sql.Tx, i int) error {
		library, err := compactcatalog.LibraryTx(ctx, tx, "music")
		if err != nil {
			return err
		}
		path := fmt.Sprintf("/fixture/music/ingest-%s-%04d.mp4", namespace, i)
		item, _, err := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Track, Parent: album, Key: compactcatalog.ItemKey("/fixture/music", path, 0), Title: fmt.Sprintf("Ingest song %s %d", namespace, i), Year: 2001, Added: "2026-10-13T00:00:00.000Z"})
		if err != nil {
			return err
		}
		if err = compactcatalog.SetFactsTx(ctx, tx, item, map[string]any{"album_id": album, "disc_number": 2, "track_number": 1000 + i}); err != nil {
			return err
		}
		if err = compactcatalog.SetSongArtistsTx(ctx, tx, item, []int64{artist}); err != nil {
			return err
		}
		asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000000, ModifiedNS: 1, Container: "mp4", AudioCodec: "aac", Duration: 240})
		if err != nil {
			return err
		}
		return compactcatalog.LinkAssetTx(ctx, tx, item, asset, compactcatalog.Link{})
	}); err != nil {
		return err
	}
	// 4. Metadata arriving later: a provider's title, year, overview, genres
	//    and credits written to existing films, one at a time.
	films, err := scanIDList(db, `SELECT e.id FROM catalog_entities e WHERE e.kind=1 ORDER BY e.id LIMIT 300`)
	if err != nil {
		return err
	}
	if err = ingestBatch(ctx, db, "metadata-trickle", len(films), func(tx *sql.Tx, i int) error {
		id := films[i]
		if err := compactcatalog.SetFieldsTx(ctx, tx, id, compactcatalog.Automatic, map[string]any{"title": fmt.Sprintf("Provider title %s %d", namespace, i), "overview": "An overview " + namespace, "tagline": "A tagline"}); err != nil {
			return err
		}
		if err := compactcatalog.SetTermsTx(ctx, tx, id, compactcatalog.VocabGenre, "tmdb", []compactcatalog.Term{{SourceID: "18", Name: "Drama"}, {SourceID: strconv.Itoa(100 + i%40), Name: "Genre tmdb " + strconv.Itoa(i%40)}}); err != nil {
			return err
		}
		credits := make([]compactcatalog.Credit, 8)
		for c := range credits {
			person := fmt.Sprint("tmdb-person-", (i*7+c)%5000)
			credits[c] = compactcatalog.Credit{PersonKey: "tmdb:" + person, PersonName: "Person " + person, ProviderPersonID: person, CreditID: person, CreditedName: "Person " + person, Role: "Actor", Department: "Acting", Ordinal: c}
		}
		return compactcatalog.SetCreditsTx(ctx, tx, id, "tmdb", credits)
	}); err != nil {
		return err
	}
	// 5. Progress ticks on episodes of the largest show.
	episodes, err := scanIDList(db, `SELECT entity_id FROM catalog_episodes WHERE show_id=? ORDER BY entity_id LIMIT 100`, show)
	if err != nil {
		return err
	}
	viewer := identity.Viewer{Authority: "local", AccountID: "fixture-owner", ProfileID: "fixture-viewer", Role: "owner"}
	ticks := 0
	dbwork.ProfileStatements(true)
	dbwork.WriteGate().ResetPeak()
	start := time.Now()
	for n, id := range episodes {
		var public string
		if err = db.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE id=?`, id).Scan(&public); err != nil {
			return err
		}
		playback := fmt.Sprintf("ingest-%s-%d", namespace, n)
		// A playback session claims the resume row first, as the player does.
		if _, err = dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,0,0,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET playback_id=excluded.playback_id`, identity.PersonalKey(viewer), id, playback); err != nil {
			return err
		}
		for step, position := range []float64{60, 300, 900, 1500} {
			err = dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
				return s.AcceptPlaybackProgress(tx, viewer, playback, public, int64(step+1), position, 2400, "playing", true)
			})
			if err != nil {
				return fmt.Errorf("progress tick: %w", err)
			}
			ticks++
		}
	}
	write := time.Since(start)
	derive, err := drainTimed(ctx, db)
	if err != nil {
		return err
	}
	ingestResult("progress-tick", ticks, write, derive, dbwork.WriteGate().Stats().MaxHeldMilli)
	profileReport("progress-tick", ticks, 8)
	// 6. Marking a 500-episode show watched, and undoing it.
	containerProfile = true
	marked, err := exerciseContainer(db, s)
	if err != nil {
		return err
	}
	for _, r := range marked {
		fmt.Fprintf(os.Stderr, "INGEST %s ms=%.3f\n", r.Route, r.P50MS)
	}
	if !removal {
		return nil
	}
	return ingestRemoval(db, s)
}

func scanIDList(db *sql.DB, query string, args ...any) ([]int64, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ingestBatch writes units sixteen to a transaction, as a scan page does,
// then drains the derived work, and reports both per unit.
func ingestBatch(ctx context.Context, db *sql.DB, name string, units int, write func(*sql.Tx, int) error) error {
	before, err := writerChangeCount(ctx, db)
	if err != nil {
		return err
	}
	dbwork.ProfileStatements(true)
	dbwork.WriteGate().ResetPeak()
	start := time.Now()
	for offset := 0; offset < units; offset += 16 {
		err := dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			for i := offset; i < min(units, offset+16); i++ {
				if err := write(tx, i); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	writeTime := time.Since(start)
	derive, err := drainTimed(ctx, db)
	if err != nil {
		return err
	}
	after, err := writerChangeCount(ctx, db)
	if err != nil {
		return err
	}
	ingestResult(name, units, writeTime, derive, dbwork.WriteGate().Stats().MaxHeldMilli)
	fmt.Fprintf(os.Stderr, "INGEST_CHANGES %s changes_per_unit=%.1f\n", name, float64(after-before)/float64(units))
	profileReport(name, units, 10)
	return nil
}

// ingestRemoval removes the Movies library and reports the removal rate in
// windows, so a rate that falls as the removal proceeds (or with size) shows.
func ingestRemoval(db *sql.DB, s *catalog.Service) error {
	ctx := context.Background()
	var revision int64
	if err := db.QueryRow(`SELECT revision FROM library_configuration WHERE library_id='movies'`).Scan(&revision); err != nil {
		return err
	}
	count := func() (n int) {
		_ = db.QueryRow(`SELECT count(*) FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id AND l.library_id='movies'`).Scan(&n)
		return
	}
	total := count()
	adapter := s.LibraryRemovalAdapter()
	scheduler := operations.NewScheduler(operations.New(db))
	if err := scheduler.Register(adapter); err != nil {
		return err
	}
	if err := s.RemoveLibrary(ctx, "movies", "Movies", revision, func(*sql.Tx) error { return nil }, func(ctx context.Context, tx *sql.Tx, kind, resource string) error {
		_, err := scheduler.EnqueueDomainTx(ctx, tx, kind, resource, "fixture-owner")
		return err
	}); err != nil {
		return err
	}
	dbwork.ProfileStatements(true)
	dbwork.WriteGate().ResetPeak()
	start, windowStart, windowLeft := time.Now(), time.Now(), total
	for {
		step, cancel := context.WithTimeout(ctx, time.Second)
		observation, err := adapter.Step(step, "movies")
		cancel()
		if err != nil {
			return err
		}
		if time.Since(windowStart) > 20*time.Second || observation.State == "succeeded" {
			left := count()
			fmt.Fprintf(os.Stderr, "INGEST_REMOVAL entities_left=%d window_ms_per_entity=%.3f\n", left, float64(time.Since(windowStart))/float64(time.Millisecond)/float64(max(1, windowLeft-left)))
			windowStart, windowLeft = time.Now(), left
		}
		if observation.State == "succeeded" {
			break
		}
	}
	ingestResult("remove-movies-library", total, time.Since(start), 0, dbwork.WriteGate().Stats().MaxHeldMilli)
	profileReport("remove-movies-library", total, 12)
	return nil
}
