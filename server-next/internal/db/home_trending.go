package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// This is provider input, not a ranking derived from this server's activity or catalogue.
type TMDBTrendingTitle struct {
	Kind ItemKind
	ID   string
}

const trendingChecked = "metadata.tmdb.trending.checked"
const tmdbLibrary = `l.kind IN ('movies','shows','anime') AND ifnull(nullif(l.metadata_provider,''),'tmdb')='tmdb'`

func (d *DB) TMDBTrendingEnabled(ctx context.Context) (bool, error) {
	var enabled bool
	err := d.read.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM libraries l WHERE `+tmdbLibrary+`)`).Scan(&enabled)
	return enabled, err
}

func (d *DB) TMDBTrendingCheckedAt(ctx context.Context) (time.Time, error) {
	var at int64
	err := d.read.QueryRowContext(ctx, `SELECT updated_at FROM settings WHERE key=?`, trendingChecked).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	return time.UnixMilli(at), err
}

// BeginTMDBTrending records an attempt before contacting the provider. Failed attempts
// wait the same day as successful ones; the last successful input version stays unchanged.
func (d *DB) BeginTMDBTrending(ctx context.Context, now time.Time) (bool, error) {
	const attempted = "metadata.tmdb.trending.attempted"
	due := false
	err := d.write(ctx, Background, func(tx *sql.Tx) error {
		var at int64
		err := tx.QueryRowContext(ctx, `SELECT updated_at FROM settings WHERE key=?`, attempted).Scan(&at)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && now.Before(time.UnixMilli(at).Add(24*time.Hour)) {
			return nil
		}
		due = true
		return putSetting(tx, attempted, "{}", now.UnixMilli())
	})
	return due, err
}

func (d *DB) SaveTMDBTrending(ctx context.Context, titles []TMDBTrendingTitle, now time.Time) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM tmdb_trending`); err != nil {
			return err
		}
		insert, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO tmdb_trending(kind,external_id,position) VALUES(?,?,?)`)
		if err != nil {
			return err
		}
		defer insert.Close()
		for n, t := range titles {
			if t.ID == "" || (t.Kind != KindMovie && t.Kind != KindShow) {
				continue
			}
			if _, err := insert.ExecContext(ctx, t.Kind, t.ID, n); err != nil {
				return err
			}
		}
		if err := putSetting(tx, trendingChecked, "{}", now.UnixMilli()); err != nil {
			return err
		}
		libraries, err := ids(ctx, tx, `SELECT l.id FROM libraries l WHERE `+tmdbLibrary)
		if err != nil {
			return err
		}
		for _, id := range libraries {
			if err := appendChange(tx, Change{At: now.UnixMilli(), Topic: TopicLibrary, Op: OpUpdated, RefID: id, LibraryID: id}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (d *DB) TrendingPage(ctx context.Context, v Viewer, q ItemQuery) (ItemPage, error) {
	q.homeTrending = true
	if q.Sort == "" {
		q.Sort, q.Desc = SortOrder, false
	}
	checked, err := d.TMDBTrendingCheckedAt(ctx)
	if err != nil {
		return ItemPage{}, err
	}
	if !checked.IsZero() {
		q.trendingVersion = checked.UnixMilli()
	}
	after, err := decodeCursor(q.Cursor, q.Sort, q.Desc)
	if err != nil {
		return ItemPage{}, err
	}
	if after != nil && after.Version != q.trendingVersion {
		return ItemPage{}, ErrCursorExpired
	}
	return d.Items(ctx, v, q)
}

func (q ItemQuery) trendingSource(w *query) {
	source := `tmdb_trending trending JOIN item_external_ids x ON x.provider='tmdb' AND x.external_id=trending.external_id
  JOIN libraries l ON ` + tmdbLibrary + ` JOIN items matched ON matched.id=x.item_id AND matched.kind=trending.kind AND matched.library_id=l.id`
	w.tagTotal, w.plainKinds, w.plainLibrary, w.whole = "", "", 0, false
	w.scope = "SELECT matched.id FROM " + source
	w.from += " JOIN (" + source + ") ON matched.id=i.id"
	w.countFrom += " JOIN (" + source + ") ON matched.id=i.id"
	w.add(` AND ifnull(`, ownState("hidden"), `,0)=0`)
}
