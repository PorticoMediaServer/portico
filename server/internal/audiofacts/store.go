package audiofacts

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Querier is a *sql.DB or *sql.Tx.
type Querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Read returns an asset's facts when they are fresh: measured from the file the
// asset row describes now (size and mtime). Stale or missing: ok is false.
func Read(ctx context.Context, q Querier, asset string) (Facts, bool, error) {
	var f Facts
	err := q.QueryRowContext(ctx, `SELECT f.container,f.codec,f.sample_rate,f.channels,f.bit_depth,f.raw_frames,f.duration_frames,f.start_frames,f.end_frames,f.trim_source,f.decoder_config,f.codec_profile,f.bit_rate,
 EXISTS(SELECT 1 FROM audio_seek_index x WHERE x.asset_id=f.asset_id AND x.size=a.size AND x.modified_ns=a.modified_ns)
 FROM audio_render_facts f JOIN catalog_assets a ON a.token=f.asset_id AND a.size=f.size AND a.modified_ns=f.modified_ns WHERE f.asset_id=?`, asset).Scan(&f.Container, &f.Codec, &f.SampleRate, &f.Channels, &f.BitDepth, &f.RawFrames, &f.DurationFrames, &f.StartFrames, &f.EndFrames, &f.TrimSource, &f.DecoderConfig, &f.Profile, &f.BitRate, &f.HasSeekIndex)
	if errors.Is(err, sql.ErrNoRows) {
		return f, false, nil
	}
	return f, err == nil, err
}

// Failed reports a fresh recorded failure (the file hasn't changed since).
func Failed(ctx context.Context, q Querier, asset string) (bool, error) {
	var failed bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audio_render_fact_failures x JOIN catalog_assets a ON a.token=x.asset_id AND a.size=x.size AND a.modified_ns=x.modified_ns WHERE x.asset_id=?)`, asset).Scan(&failed)
	return failed, err
}

// Save records a measurement against the asset's current size and mtime and
// clears any recorded failure.
func Save(ctx context.Context, db *sql.DB, asset string, f Facts) error {
	if f.TrimSource == "mpc-header-pending" {
		return ErrInconsistent
	}
	gated, err := dbwork.Begin(ctx, db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	res, err := tx.ExecContext(ctx, `INSERT INTO audio_render_facts(asset_id,size,modified_ns,container,codec,sample_rate,channels,bit_depth,raw_frames,duration_frames,start_frames,end_frames,trim_source,decoder_config,codec_profile,bit_rate,measured_ms)
 SELECT token,size,modified_ns,?,?,?,?,?,?,?,?,?,?,?,?,?,? FROM catalog_assets WHERE token=?
 ON CONFLICT(asset_id) DO UPDATE SET size=excluded.size,modified_ns=excluded.modified_ns,container=excluded.container,codec=excluded.codec,sample_rate=excluded.sample_rate,channels=excluded.channels,bit_depth=excluded.bit_depth,raw_frames=excluded.raw_frames,duration_frames=excluded.duration_frames,start_frames=excluded.start_frames,end_frames=excluded.end_frames,trim_source=excluded.trim_source,decoder_config=excluded.decoder_config,codec_profile=excluded.codec_profile,bit_rate=excluded.bit_rate,measured_ms=excluded.measured_ms`,
		f.Container, f.Codec, f.SampleRate, f.Channels, f.BitDepth, f.RawFrames, f.DurationFrames, f.StartFrames, f.EndFrames, f.TrimSource, f.DecoderConfig, f.Profile, f.BitRate, time.Now().UnixMilli(), asset)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM audio_render_fact_failures WHERE asset_id=?`, asset); err != nil {
		return err
	}
	// The seek index belongs to the same measurement: replaced or removed with it.
	if len(f.SeekIndex) > 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO audio_seek_index(asset_id,size,modified_ns,entries) SELECT token,size,modified_ns,? FROM catalog_assets WHERE token=?
 ON CONFLICT(asset_id) DO UPDATE SET size=excluded.size,modified_ns=excluded.modified_ns,entries=excluded.entries`, f.SeekIndex, asset)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM audio_seek_index WHERE asset_id=?`, asset)
	}
	if err != nil {
		return err
	}
	return gated.Commit()
}

// ReadSeekIndex returns an asset's seek index when it is fresh (made from the
// file the asset row describes now); ok is false when there is none.
func ReadSeekIndex(ctx context.Context, q Querier, asset string) (*SeekIndex, bool, error) {
	var data []byte
	err := q.QueryRowContext(ctx, `SELECT x.entries FROM audio_seek_index x JOIN catalog_assets a ON a.token=x.asset_id AND a.size=x.size AND a.modified_ns=x.modified_ns WHERE x.asset_id=?`, asset).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	x, err := DecodeSeekIndex(data)
	if err != nil {
		return nil, false, err
	}
	return x, true, nil
}

// Fail records that an asset couldn't be measured, until its file changes.
func Fail(ctx context.Context, db *sql.DB, asset, code string) error {
	_, err := dbwork.ExecWrite(ctx, db, dbwork.ClassBackgroundMedia, `INSERT INTO audio_render_fact_failures(asset_id,size,modified_ns,code,failed_ms) SELECT token,size,modified_ns,?,? FROM catalog_assets WHERE token=?
 ON CONFLICT(asset_id) DO UPDATE SET size=excluded.size,modified_ns=excluded.modified_ns,code=excluded.code,failed_ms=excluded.failed_ms`, code, time.Now().UnixMilli(), asset)
	return err
}

// Measure runs one confined measurement of an item's asset.
type Measure func(ctx context.Context, item, asset string) (Facts, error)

// Ensure returns fresh facts or queues a missing measurement for the background
// worker. A playback request never waits for a full decode. Recorded failures
// for unchanged files are not retried here.
func Ensure(ctx context.Context, db *sql.DB, measure Measure, item, asset string, budget time.Duration) (Facts, bool, error) {
	if f, ok, err := Read(ctx, db, asset); err != nil || ok {
		return f, ok, err
	}
	if failed, err := Failed(ctx, db, asset); err != nil || failed || measure == nil {
		return Facts{}, false, err
	}
	// A foreground start queues facts instead of repeatedly throwing away a
	// full decode when its short request deadline expires.
	_, err := dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive, `INSERT INTO audio_render_fact_requests(asset_id,item_id,requested_ms)VALUES(?,?,?) ON CONFLICT(asset_id)DO UPDATE SET item_id=excluded.item_id`, asset, item, time.Now().UnixMilli())
	return Facts{}, false, err
}

func code(err error) string {
	switch {
	case errors.Is(err, ErrNoAudio):
		return "no_audio"
	case errors.Is(err, ErrInconsistent):
		return "inconsistent"
	case errors.Is(err, ErrTooLong):
		return "too_long"
	case errors.Is(err, ErrSourceChanged):
		return "source_changed"
	}
	return "unreadable"
}

// Backfill measures the library's audio assets in the background (spec §18.7),
// so a play finds its facts. One pass walks the assets in id order in bounded
// windows (each query reads at most window rows); a later pass starts when the
// catalog changes, at most once per rest. Measurements run one at a time.
type Backfill struct {
	DB      *sql.DB
	Measure Measure
	// Window bounds the rows one selection reads; Rest spaces passes.
	Window int
	Rest   time.Duration

	cursor   string
	passing  bool
	finished time.Time
	// retried is whether this process has run the one-time retry of failures
	// recorded by an older measurement (FailureVersion).
	retried bool
	// logged counts this pass's logged failures (the first few say why).
	logged int
}

// FailureVersion is the measurement version failures are recorded under.
// Failures recorded before it are retried once: version 2 (NEW-29) retries
// every "unreadable" row, because the confined ffprobe couldn't load its own
// libraries and every measurement failed for that reason alone.
const FailureVersion = 2

const failureVersionKey = "audio_facts.failure_version"

// retryOldFailures clears "unreadable" failures recorded before
// FailureVersion, a bounded batch per write, then records the version, so the
// backfill measures those files again. It runs once per process and does
// nothing once the version is recorded.
func retryOldFailures(ctx context.Context, db *sql.DB) error {
	var stored int
	err := db.QueryRowContext(ctx, `SELECT CAST(value AS INTEGER) FROM configuration WHERE key=?`, failureVersionKey).Scan(&stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if stored >= FailureVersion {
		return nil
	}
	for {
		result, err := dbwork.ExecWrite(ctx, db, dbwork.ClassBackgroundMedia, `DELETE FROM audio_render_fact_failures WHERE asset_id IN (SELECT asset_id FROM audio_render_fact_failures WHERE code='unreadable' LIMIT 500)`)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n < 500 {
			break
		}
		if !dbwork.Yield(ctx) {
			return ctx.Err()
		}
	}
	_, err = dbwork.ExecWrite(ctx, db, dbwork.ClassBackgroundMedia, `INSERT INTO configuration(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, failureVersionKey, FailureVersion)
	return err
}

// Step is a worker.Run step: the delay before the next one (0 waits for a wake).
func (b *Backfill) Step(ctx context.Context) time.Duration {
	window, rest := b.Window, b.Rest
	if window <= 0 {
		window = 2048
	}
	if rest <= 0 {
		rest = 10 * time.Minute
	}
	if !b.retried {
		if err := retryOldFailures(ctx, b.DB); err != nil {
			if ctx.Err() == nil {
				log.Printf("Audio facts: retrying older failures: %v", err)
			}
			return time.Minute
		}
		b.retried = true
	}
	// Recently requested listening files run ahead of the library backfill.
	var requestedAsset, requestedItem string
	if e := b.DB.QueryRowContext(ctx, `SELECT r.asset_id,r.item_id FROM audio_render_fact_requests r JOIN catalog_assets a ON a.token=r.asset_id WHERE a.available=1 ORDER BY r.requested_ms LIMIT 1`).Scan(&requestedAsset, &requestedItem); e == nil {
		if _, ok, readErr := Read(ctx, b.DB, requestedAsset); readErr != nil {
			return time.Second
		} else if !ok && b.Measure != nil {
			bounded, cancel := context.WithTimeout(ctx, 10*time.Minute)
			f, err := b.Measure(bounded, requestedItem, requestedAsset)
			cancel()
			if ctx.Err() != nil {
				return 0
			}
			if err == nil {
				if err = Save(ctx, b.DB, requestedAsset, f); err != nil {
					return time.Second
				}
			} else if !errors.Is(err, context.DeadlineExceeded) {
				_ = Fail(ctx, b.DB, requestedAsset, code(err))
			}
			if err != nil && errors.Is(err, context.Canceled) {
				return time.Second
			}
		}
		_, _ = dbwork.ExecWrite(ctx, b.DB, dbwork.ClassBackgroundMedia, `DELETE FROM audio_render_fact_requests WHERE asset_id=?`, requestedAsset)
		return time.Millisecond
	}
	if !b.passing {
		if !b.finished.IsZero() && time.Since(b.finished) < rest {
			return rest - time.Since(b.finished)
		}
		b.passing, b.cursor, b.logged = true, "", 0
	}
	var upper sql.NullString
	err := b.DB.QueryRowContext(ctx, `SELECT token FROM catalog_assets WHERE token>? ORDER BY token LIMIT 1 OFFSET ?`, b.cursor, window-1).Scan(&upper)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("Audio facts: %v", err)
		return time.Minute
	}
	rows, err := b.DB.QueryContext(ctx, `SELECT a.token,MIN(pid(e.public_id)) FROM catalog_assets a JOIN catalog_asset_links link ON link.asset_id=a.id JOIN catalog_entities e ON e.id=link.entity_id JOIN catalog_kinds k ON k.id=e.kind
 LEFT JOIN audio_render_facts f ON f.asset_id=a.token AND f.size=a.size AND f.modified_ns=a.modified_ns
 LEFT JOIN audio_render_fact_failures x ON x.asset_id=a.token AND x.size=a.size AND x.modified_ns=a.modified_ns
 WHERE a.token>? AND (? IS NULL OR a.token<=?) AND a.available=1 AND a.video_codec='' AND k.playable=1 AND k.listening=1 AND f.asset_id IS NULL AND x.asset_id IS NULL
 GROUP BY a.token ORDER BY a.token`, b.cursor, upper, upper)
	if err != nil {
		log.Printf("Audio facts: %v", err)
		return time.Minute
	}
	type pending struct{ asset, item string }
	var todo []pending
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.asset, &p.item); err != nil {
			break
		}
		todo = append(todo, p)
	}
	rows.Close()
	for _, p := range todo {
		if ctx.Err() != nil {
			return 0
		}
		f, err := b.Measure(ctx, p.item, p.asset)
		if err != nil {
			if ctx.Err() == nil {
				// The stored code stays coarse and stable; the log says why,
				// for the first few of a pass.
				if b.logged < 5 {
					b.logged++
					log.Printf("Audio facts: %s couldn't be measured (%s): %v", p.asset, code(err), err)
				}
				_ = Fail(ctx, b.DB, p.asset, code(err))
			}
			continue
		}
		if err = Save(ctx, b.DB, p.asset, f); err != nil && ctx.Err() == nil {
			log.Printf("Audio facts: saving %s: %v", p.asset, err)
		}
	}
	if !upper.Valid {
		b.passing, b.finished = false, time.Now()
		return 0
	}
	b.cursor = upper.String
	return time.Millisecond
}
