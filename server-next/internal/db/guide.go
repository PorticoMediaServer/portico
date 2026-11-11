package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// guide.db holds the TV guide: every source's programmes and a copy of each Library Channel's
// schedule, so the guide reads one way. It can be deleted at any time; it is filled again from
// the sources and from channel_schedule. It has its own single writer, so a guide refresh
// never waits behind the main database's writes or holds them up.
const guideSchema = `
CREATE TABLE programmes (
  source_id INTEGER NOT NULL, guide_key TEXT NOT NULL,
  starts_at INTEGER NOT NULL, ends_at INTEGER NOT NULL,
  programme_key TEXT NOT NULL, series_key TEXT,
  title TEXT NOT NULL, episode_title TEXT, season_number INTEGER, episode_number INTEGER,
  summary TEXT, rating TEXT, rating_age INTEGER, flags INTEGER, image_url TEXT,
  item_id INTEGER,
  PRIMARY KEY (source_id, guide_key, starts_at)
) WITHOUT ROWID;
CREATE INDEX programmes_key ON programmes (source_id, guide_key, programme_key);
CREATE INDEX programmes_series ON programmes (series_key, starts_at) WHERE series_key IS NOT NULL;
CREATE INDEX programmes_title ON programmes (title, starts_at);
`

// LibraryGuideSource is the source_id of Library Channel programmes; their guide_key is the
// channel's public id.
const LibraryGuideSource = 0

type guideDB struct {
	write *sql.DB
	read  *sql.DB
	mu    sync.Mutex // one guide write at a time
}

func openGuide(ctx context.Context, path string) (*guideDB, error) {
	g, err := openGuideOnce(ctx, path)
	if err == nil {
		return g, nil
	}
	// A guide file that cannot be read is thrown away and made again.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		os.Remove(path + suffix)
	}
	return openGuideOnce(ctx, path)
}

func openGuideOnce(ctx context.Context, path string) (*guideDB, error) {
	write, err := open(path, "rwc", 1, 8<<10)
	if err != nil {
		return nil, err
	}
	var tables int
	if err := write.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'programmes'`).Scan(&tables); err != nil {
		write.Close()
		return nil, err
	}
	if tables == 0 {
		if _, err := write.ExecContext(ctx, guideSchema); err != nil {
			write.Close()
			return nil, err
		}
	}
	// The guide file is never checkpointed by a helper: let SQLite do it as it goes.
	write.ExecContext(ctx, `PRAGMA wal_autocheckpoint = 1000`)
	read, err := open(path, "ro", 4, 4<<10)
	if err != nil {
		write.Close()
		return nil, err
	}
	return &guideDB{write: write, read: read}, nil
}

func guidePath(mainPath string) string { return filepath.Join(filepath.Dir(mainPath), "guide.db") }

func (g *guideDB) close() error {
	if g == nil {
		return nil
	}
	return errors.Join(g.read.Close(), g.write.Close())
}

// GuideProgramme is one programme of the guide.
type GuideProgramme struct {
	SourceID     int64
	GuideKey     string
	StartsAt     time.Time
	EndsAt       time.Time
	ProgrammeKey string
	SeriesKey    string
	Title        string
	EpisodeTitle string
	Season       *int
	Episode      *int
	Summary      string
	Rating       string
	RatingAge    *int
	Flags        int
	ImageURL     string
	ItemID       int64 // a Library Channel's title, or 0
}

// CopyChannelGuide replaces a Library Channel's programmes in the guide from from on with
// rows, and drops those that ended before forget, in one guide transaction.
func (d *DB) CopyChannelGuide(ctx context.Context, channelPublicID string, from, forget time.Time, rows []GuideProgramme) error {
	g := d.guide
	g.mu.Lock()
	defer g.mu.Unlock()
	tx, err := g.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM programmes WHERE source_id = ? AND guide_key = ? AND (starts_at >= ? OR ends_at < ?)`,
		LibraryGuideSource, channelPublicID, from.UnixMilli(), forget.UnixMilli()); err != nil {
		return err
	}
	insert, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO programmes (source_id, guide_key, starts_at, ends_at, programme_key, series_key,
		title, episode_title, season_number, episode_number, summary, rating, rating_age, flags, image_url, item_id)
		VALUES (?, ?, ?, ?, ?, nullif(?, ''), ?, nullif(?, ''), ?, ?, nullif(?, ''), nullif(?, ''), ?, ?, nullif(?, ''), nullif(?, 0))`)
	if err != nil {
		return err
	}
	defer insert.Close()
	for _, p := range rows {
		if _, err := insert.ExecContext(ctx, LibraryGuideSource, channelPublicID, p.StartsAt.UnixMilli(), p.EndsAt.UnixMilli(), p.ProgrammeKey,
			p.SeriesKey, p.Title, p.EpisodeTitle, p.Season, p.Episode, p.Summary, p.Rating, p.RatingAge, p.Flags, p.ImageURL, p.ItemID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DropChannelGuide removes a channel's programmes from the guide.
func (d *DB) DropChannelGuide(ctx context.Context, sourceID int64, guideKey string) error {
	g := d.guide
	g.mu.Lock()
	defer g.mu.Unlock()
	_, err := g.write.ExecContext(ctx, `DELETE FROM programmes WHERE source_id = ? AND guide_key = ?`, sourceID, guideKey)
	return err
}

// GuideCopyMatches says whether the guide's copy of a Library Channel's schedule has the
// same number of programmes from since on, ending at the same time, as channel_schedule.
// Both are read through their keys.
func (d *DB) GuideCopyMatches(ctx context.Context, channelID int64, channelPublicID string, since time.Time) (bool, error) {
	var mainCount, mainEnd, guideCount, guideEnd int64
	if err := d.read.QueryRowContext(ctx, `SELECT count(*), ifnull(max(ends_at), 0) FROM channel_schedule WHERE channel_id = ? AND starts_at >= ?`,
		channelID, since.UnixMilli()).Scan(&mainCount, &mainEnd); err != nil {
		return false, err
	}
	if err := d.guide.read.QueryRowContext(ctx, `SELECT count(*), ifnull(max(ends_at), 0) FROM programmes
		WHERE source_id = ? AND guide_key = ? AND starts_at >= ?`, LibraryGuideSource, channelPublicID, since.UnixMilli()).Scan(&guideCount, &guideEnd); err != nil {
		return false, err
	}
	return mainCount == guideCount && mainEnd == guideEnd, nil
}

// GuideProgrammes reads one channel's programmes that end after from and start before to.
// It reads the channel's own key range, however large the guide.
func (d *DB) GuideProgrammes(ctx context.Context, sourceID int64, guideKey string, from, to time.Time, limit int) ([]GuideProgramme, error) {
	rows, err := d.guide.read.QueryContext(ctx, `SELECT starts_at, ends_at, programme_key, ifnull(series_key, ''), title, ifnull(episode_title, ''),
		season_number, episode_number, ifnull(summary, ''), ifnull(rating, ''), rating_age, ifnull(flags, 0), ifnull(image_url, ''), ifnull(item_id, 0)
		FROM programmes WHERE source_id = ? AND guide_key = ? AND starts_at > ? AND starts_at < ? AND ends_at > ? ORDER BY starts_at LIMIT ?`,
		sourceID, guideKey, from.Add(-48*time.Hour).UnixMilli(), to.UnixMilli(), from.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GuideProgramme
	for rows.Next() {
		p := GuideProgramme{SourceID: sourceID, GuideKey: guideKey}
		var starts, ends int64
		if err := rows.Scan(&starts, &ends, &p.ProgrammeKey, &p.SeriesKey, &p.Title, &p.EpisodeTitle, &p.Season, &p.Episode, &p.Summary,
			&p.Rating, &p.RatingAge, &p.Flags, &p.ImageURL, &p.ItemID); err != nil {
			return nil, err
		}
		p.StartsAt, p.EndsAt = time.UnixMilli(starts), time.UnixMilli(ends)
		out = append(out, p)
	}
	return out, rows.Err()
}
