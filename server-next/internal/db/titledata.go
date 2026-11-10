package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
)

// Title data is Portico's published file of mood, theme and similar-title facts about films and
// shows, keyed by the ids providers give them. The server keeps one copy in its data folder (see
// internal/metadata/titledata). This file is the only code that reads it with SQL: the file is a
// plain SQLite database in the shape of the data set's contract.

// TitleDataSchema is the contract's schema version this server reads: the file's
// PRAGMA user_version and the signed list's schema_version.
const TitleDataSchema = 3

// ErrTitleDataShape is a title data file this server cannot use: damaged, of another schema
// version, kind or domain, or not the version its signed list named.
var ErrTitleDataShape = errors.New("the title data file is not in the expected shape")

// The tables and columns the reads use. Asking for them with LIMIT 0 proves the file has them.
var titleDataColumns = []string{
	`SELECT key, value FROM meta`,
	`SELECT qid, kind, tagged FROM works`,
	`SELECT provider, provider_id, qid FROM work_ids`,
	`SELECT id, tag, family, row_label, retired_in FROM vocabulary`,
	`SELECT qid, tag, strength FROM work_tags`,
	`SELECT qid, rank, similar FROM work_similar`,
}

func openTitleData(path, mode string) (*sql.DB, error) {
	q := url.Values{}
	q.Set("mode", mode)
	conn, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(2)
	return conn, nil
}

// PrepareTitleData checks a downloaded title data file before it is used: SQLite's own check,
// the schema version, a full file of the domain and version the signed list named, and the
// tables the reads use. Then it adds the one index the reads need (a work's ids by its qid),
// which the published file leaves out to stay small. It adds no rows and changes none.
func PrepareTitleData(ctx context.Context, path, domain, version string) error {
	conn, err := openTitleData(path, "rw")
	if err != nil {
		return err
	}
	defer conn.Close()
	var check string
	if err := conn.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&check); err != nil || check != "ok" {
		return fmt.Errorf("%w: SQLite's check failed", ErrTitleDataShape)
	}
	var schema int
	if err := conn.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&schema); err != nil {
		return err
	}
	if schema != TitleDataSchema {
		return fmt.Errorf("%w: schema %d, this server reads %d", ErrTitleDataShape, schema, TitleDataSchema)
	}
	for _, statement := range titleDataColumns {
		if _, err := conn.ExecContext(ctx, statement+` LIMIT 0`); err != nil {
			return fmt.Errorf("%w: %v", ErrTitleDataShape, err)
		}
	}
	meta := map[string]string{}
	rows, err := conn.QueryContext(ctx, `SELECT key, value FROM meta WHERE key IN ('kind', 'domain', 'dataset_version')`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return err
		}
		meta[key] = value
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if meta["kind"] != "full" || meta["domain"] != domain || meta["dataset_version"] != version {
		return fmt.Errorf("%w: it is a %q file of %q version %q, the signed list names a full file of %q version %q",
			ErrTitleDataShape, meta["kind"], meta["domain"], meta["dataset_version"], domain, version)
	}
	_, err = conn.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS work_ids_by_qid ON work_ids (qid, provider, provider_id)`)
	return err
}

// titleData is the title data file in use, or nil.
type titleData struct {
	conn *sql.DB
}

// UseTitleData makes the prepared file at path the one every read of title data uses, and
// closes the one used before once the reads under way on it finish. An empty path stops using
// any: reads then answer as if no title has any title data.
func (d *DB) UseTitleData(path string) error {
	var next *titleData
	if path != "" {
		conn, err := openTitleData(path, "ro")
		if err != nil {
			return err
		}
		if err := conn.Ping(); err != nil {
			conn.Close()
			return err
		}
		next = &titleData{conn: conn}
	}
	if old := d.titleData.Swap(next); old != nil {
		return old.conn.Close()
	}
	return nil
}
