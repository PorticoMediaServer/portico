package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
)

// CheckCopy checks a copy of the database whole before it is kept as a backup or restored:
// SQLite's own check finds nothing wrong, it was made by this build's schema or an older one
// it can read, and it holds nothing the schema does not define (a trigger, a view or a table
// someone added could run or hide something once the copy is in use). It is opened read-only
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
	var version sql.NullInt64
	if err := copied.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil || !version.Valid {
		return fmt.Errorf("this is not a Portico database")
	}
	if version.Int64 > SchemaVersion {
		return fmt.Errorf("it was made by a newer version of Portico")
	}
	// A database that cannot answer who its people are will not serve as this server.
	var people int
	if err := copied.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&people); err != nil {
		return fmt.Errorf("its accounts cannot be read: %w", err)
	}
	want, err := schemaObjects(ctx, nil)
	if err != nil {
		return err
	}
	have, err := schemaObjects(ctx, copied)
	if err != nil {
		return err
	}
	// A copy with a table or an index missing would open and then fail on the first request
	// that needs it, with nothing to put back.
	for name := range want {
		if _, held := have[name]; !held {
			return fmt.Errorf("it lacks %s, which Portico's schema defines", name)
		}
	}
	for name, sql := range have {
		// SQLite's own tables for row numbers and query statistics appear by themselves.
		if name == "table sqlite_sequence" || name == "table sqlite_stat1" || name == "table sqlite_stat4" {
			continue
		}
		if have, defined := want[name]; !defined || have != sql {
			return fmt.Errorf("it holds %s, which Portico's schema does not define", name)
		}
	}
	return nil
}

// schemaObjects lists a database's tables, indexes, triggers and views by kind and name, with
// the SQL that made each. With no database it is those of a new one, made in memory.
func schemaObjects(ctx context.Context, from *sql.DB) (map[string]string, error) {
	if from == nil {
		fresh, err := sql.Open("sqlite", "file::memory:")
		if err != nil {
			return nil, err
		}
		defer fresh.Close()
		fresh.SetMaxOpenConns(1)
		if _, err := fresh.ExecContext(ctx, schemaSQL); err != nil {
			return nil, err
		}
		from = fresh
	}
	// Every row is read, SQLite's own names included: a trigger or a view can be given a name
	// beginning sqlite_ by writing to the schema table directly, and SQLite runs it.
	rows, err := from.QueryContext(ctx, `SELECT type, name, ifnull(sql, '') FROM sqlite_master`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var kind, name, text string
		if err := rows.Scan(&kind, &name, &text); err != nil {
			return nil, err
		}
		out[kind+" "+name] = strings.Join(strings.Fields(text), " ")
	}
	return out, rows.Err()
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
