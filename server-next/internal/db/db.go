// Package db owns Portico's main database: the schema, the one write connection and the
// read pool. Nothing outside this package runs SQL or holds a transaction.
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite"
)

// SchemaVersion is recorded in schema_migrations. Before the first release the schema file is
// edited in place and this stays 1; a database made by an older build is deleted, not upgraded.
const SchemaVersion = 1

//go:embed schema.sql
var schemaSQL string

// DB is the main database. All writes go through the single write connection, so SQLite
// never sees two writers. Reads use a small pool and never block on the writer in WAL mode.
type DB struct {
	write *sql.DB
	read  *sql.DB
}

// Open opens the database at path, creating it with the current schema if it is new.
// Every commit is synced to disk before it returns (synchronous=FULL).
func Open(ctx context.Context, path string) (*DB, error) {
	write, err := open(path, "rwc", 1)
	if err != nil {
		return nil, err
	}
	d := &DB{write: write}
	if err := d.ensureSchema(ctx); err != nil {
		write.Close()
		return nil, err
	}
	if d.read, err = open(path, "ro", 8); err != nil {
		write.Close()
		return nil, err
	}
	return d, nil
}

func open(path, mode string, conns int) (*sql.DB, error) {
	q := url.Values{}
	q.Set("mode", mode)
	q.Set("_txlock", "immediate")
	for _, pragma := range []string{"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)", "foreign_keys(1)"} {
		q.Add("_pragma", pragma)
	}
	conn, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(conns)
	return conn, nil
}

func (d *DB) ensureSchema(ctx context.Context) error {
	var tables int
	if err := d.write.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tables); err != nil {
		return err
	}
	if tables == 0 {
		tx, err := d.write.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			SchemaVersion, time.Now().UnixMilli()); err != nil {
			return err
		}
		return tx.Commit()
	}
	var version int
	err := d.write.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&version)
	if err != nil || version != SchemaVersion {
		return errors.New("this database was made by a different build of Portico; before the first release, delete it and start again")
	}
	return nil
}

// Close closes both connections.
func (d *DB) Close() error {
	return errors.Join(d.read.Close(), d.write.Close())
}
