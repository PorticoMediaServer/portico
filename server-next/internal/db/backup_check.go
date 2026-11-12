package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
)

// CheckCopy checks a copy of the database whole before it is kept as a backup or restored:
// SQLite's own check finds nothing wrong, its accounts can be read, and CheckSchema accepts it
// (this program's version and exactly what a new database holds, or an older version the next
// start upgrades, holding no trigger and no view; never a newer one). It is opened read-only
// and as a file that cannot change, so checking it never writes beside it.
func CheckCopy(ctx context.Context, path string) error {
	copied, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?mode=ro&immutable=1")
	if err != nil {
		return err
	}
	defer copied.Close()
	copied.SetMaxOpenConns(1)
	var check string
	if err := copied.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&check); err != nil {
		return fmt.Errorf("the database cannot be read: %w", err)
	}
	if check != "ok" {
		return fmt.Errorf("the database is damaged: %s", check)
	}
	if err := CheckSchema(ctx, copied); err != nil {
		return err
	}
	// A database that cannot answer who its people are will not serve as this server.
	var people int
	if err := copied.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&people); err != nil {
		return fmt.Errorf("its accounts cannot be read: %w", err)
	}
	return nil
}

// UploadedPictures are the pictures people uploaded (artwork and profile pictures), by the
// name the picture store keeps them under: what a backup holds of the picture store.
func (d *DB) UploadedPictures(ctx context.Context) ([]string, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT DISTINCT file_hash FROM images WHERE source = ? AND file_hash IS NOT NULL`, ImageFromUpload)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		out = append(out, hash)
	}
	return out, rows.Err()
}

// HasRemoteSources says whether any remote storage source is kept, with its sign-in sealed.
func (d *DB) HasRemoteSources(ctx context.Context) (bool, error) {
	var has bool
	err := d.read.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM remote_sources)`).Scan(&has)
	return has, err
}

// BackupSubtitles are the downloaded subtitles named by the copied database. Reading the
// copy matters: a subtitle added or removed while a backup is made must not change its set.
func BackupSubtitles(ctx context.Context, path string) ([]string, error) {
	copied, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	defer copied.Close()
	rows, err := copied.QueryContext(ctx, `SELECT DISTINCT file_hash FROM subtitles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hashes []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		hashes = append(hashes, hash)
	}
	return hashes, rows.Err()
}
