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
	"os"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaCoreSQL string

//go:embed schema_areas.sql
var schemaAreasSQL string

// schemaSQL is the whole schema: the core tables, then the tables of the areas built on them.
var schemaSQL = schemaCoreSQL + "\n" + schemaAreasSQL

// DB is the main database. All writes go through the single write connection, so SQLite
// never sees two writers. Reads use a small pool and never block on the writer in WAL mode.
type DB struct {
	writeConn *sql.DB
	read      *sql.DB
	queues    [2]chan writeRequest
	closed    chan struct{}
	marks     tokenMarks
	undo      undoOffers
	// sessionWatch is told about ended and changed sign-ins. See WatchSessions.
	sessionWatch func(ended, changed []int64)
	// changed is signalled after every commit, to wake the search indexer. See search_index.go.
	changed chan struct{}
	search  *searchIndex

	// See checkpoint.go.
	path       string
	helper     *sql.DB
	helperDone chan struct{}
	// backingUp is set while an online backup runs. See checkpointHelper.
	backingUp atomic.Bool

	// titleData is the title data file the reads use, or nil. See titledata.go.
	titleData  atomic.Pointer[titleData]
	nextInsist time.Time

	// upgradesDone closes when the upgrade's background work has stopped. See upgrade.go.
	upgradesDone chan struct{}
	draining     atomic.Bool // background work is held back while the WAL is emptied
	holdUntil    time.Time   // when the present hold ends whether or not the file emptied
	holdAgain    time.Time   // after a hold that ran out: when the next may begin

	// file is the database file, held only so the helper can sync it, and copied is how many
	// WAL pages its last copy had reached. The file is closed after SQLite's own connections:
	// closing it sooner would drop the locks SQLite holds on the same file.
	file   *os.File
	copied int
}

// Open opens the database at path, creating it with the current schema if it is new and
// upgrading it if an earlier version of Portico made it (see upgrade.go).
// Every commit is synced to disk before it returns (synchronous=FULL).
func Open(ctx context.Context, path string) (*DB, error) {
	if err := checkIsOurs(ctx, path); err != nil {
		return nil, err
	}
	write, err := open(path, "rwc", 1, writeCacheKiB)
	if err != nil {
		return nil, err
	}
	d := &DB{writeConn: write, closed: make(chan struct{}), path: path, helperDone: make(chan struct{}), changed: make(chan struct{}, 1)}
	d.upgradesDone = make(chan struct{})
	d.queues[User], d.queues[Background] = make(chan writeRequest), make(chan writeRequest)
	if err := d.ensureSchema(ctx); err != nil {
		write.Close()
		return nil, err
	}
	if d.read, err = open(path, "ro", 8, readCacheKiB); err != nil {
		write.Close()
		return nil, err
	}
	d.marks.boot = time.Now().UnixNano()
	if d.helper, err = open(path, "rw", 1, 0); err != nil {
		write.Close()
		d.read.Close()
		return nil, err
	}
	if d.file, err = os.OpenFile(path, os.O_RDWR, 0); err != nil {
		write.Close()
		d.read.Close()
		d.helper.Close()
		return nil, err
	}
	// The helper never waits long for anyone: what it cannot do now it does next time.
	d.helper.Exec(`PRAGMA busy_timeout = 50`)
	go d.writer()
	go d.checkpointHelper()
	go d.finishUpgrades()
	return d, nil
}

// Page caches, per connection. The writer's is the one that matters to an import: every row
// touches several index pages, and a page it has to read back costs far more than one it holds.
// The eight read connections together hold up to 128 MiB.
const (
	writeCacheKiB = 64 << 10
	readCacheKiB  = 16 << 10
)

// checkIsOurs refuses a file that is there and is not a Portico database, before anything
// is opened for writing: opening a database the way Portico does switches it to WAL mode,
// and a file from another program must be left exactly as it was.
func checkIsOurs(ctx context.Context, path string) error {
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		return nil // not there yet, or empty: Open makes it
	}
	look, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?mode=ro")
	if err != nil {
		return err
	}
	defer look.Close()
	var ours bool
	if err := look.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations')`).Scan(&ours); err != nil {
		return fmt.Errorf("%s could not be read as a database, and nothing in it has been changed: %w", path, err)
	}
	if !ours {
		return fmt.Errorf("%s is a database Portico did not make; nothing in it has been changed. Choose another data folder, or move the file away", path)
	}
	return nil
}

func open(path, mode string, conns, cacheKiB int) (*sql.DB, error) {
	q := url.Values{}
	q.Set("mode", mode)
	q.Set("_txlock", "immediate")
	for _, pragma := range []string{"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)", "foreign_keys(1)",
		"wal_autocheckpoint(0)", fmt.Sprintf("journal_size_limit(%d)", walKeepBytes)} {
		q.Add("_pragma", pragma)
	}
	if cacheKiB > 0 {
		q.Add("_pragma", fmt.Sprintf("cache_size(-%d)", cacheKiB))
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
	if err := d.writeConn.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tables); err != nil {
		return err
	}
	if tables == 0 {
		tx, err := d.writeConn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			schemaVersion(), time.Now().UnixMilli()); err != nil {
			return err
		}
		return tx.Commit()
	}
	return d.upgrade(ctx)
}

// Close stops the writer and the checkpoint helper and closes the connections. SQLite copies
// what is left in the WAL into the database as the last connection closes.
func (d *DB) Close() error {
	if d.search != nil {
		d.search.stop()
	}
	close(d.closed)
	<-d.helperDone
	<-d.upgradesDone
	return errors.Join(d.UseTitleData(""), d.read.Close(), d.helper.Close(), d.writeConn.Close(), d.file.Close())

}

// SkipDiskSync makes commits return without waiting for the disk. It is for the load tool
// alone, to time everything a write waits for except the disk. A power cut then loses data.
func (d *DB) SkipDiskSync() {
	d.writeConn.Exec(`PRAGMA synchronous = OFF`)
	d.helper.Exec(`PRAGMA synchronous = OFF`)
}
