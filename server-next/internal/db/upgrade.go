package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/dirsync"
)

// How a later version of Portico changes a database an earlier version made.
//
// The database records a version number. The schema files always describe the newest
// version, and a new database is made straight from them. A database from an earlier
// release is brought up to date by the steps in upgrades, in order.
//
// Each step has a quick part, applied while the server starts in one transaction together
// with the new version number, so the database is always at a whole version however the
// server stops. A step may also have a background part for work that touches every row of a
// large table: it runs after the server is up, in small transactions behind people's own
// writes, and remembers how far it has got, so it carries on where it stopped.
//
// Before the first step of a start the whole database is copied to a file beside it, so
// there is always a way back to the version the person came from.
//
// Not here, on purpose: steps that go back a version, and anything that rebuilds a table
// (SQLite needs foreign keys switched off for that; add it with the first step that needs it).

// firstVersion is the version of the schema files at the first release. Until then the
// schema files are edited in place, upgrades stays empty and every database is version 1.
const firstVersion = 1

// upgrade is one step: it takes a database from one version to the next.
type upgrade struct {
	// what says what the step changes, for the log.
	what string
	// sql is the quick part. It must cost the same at any library size: a new table, a new
	// column on a table that is not STRICT, a change to a small table. It may not rely on the
	// background part of any step having finished.
	sql string
	// background, if set, is the slow part, one small batch at a time. It is given the
	// position the batch before it returned (0 the first time) and returns its own, and done
	// once nothing is left. A batch must not hold the writer for long: about 2,000 rows of a
	// large table. One statement that cannot be split, such as creating an index, is a batch
	// of its own. Until it is done the server runs with the work half finished, so whatever
	// reads the new column or index must work without it.
	background func(tx *sql.Tx, after int64) (next int64, done bool, err error)
}

// upgrades is every step since the first release, oldest first. The step at position i
// takes a database from version firstVersion+i to the next. Add at the end only: a step that
// has been released is never changed, moved or removed, because databases have already run it.
var upgrades = []upgrade{}

// schemaVersion is the version this program makes and reads.
func schemaVersion() int { return firstVersion + len(upgrades) }

// upgradePoint is called at each moment an upgrade could be cut short. Tests kill the
// process there.
var upgradePoint = func(string) {}

// backupPath is where the copy made before upgrading from a version is kept.
func backupPath(path string, version int) string {
	return fmt.Sprintf("%s.before-upgrade-%d", path, version)
}

// upgrade brings an existing database up to this program's version, or refuses it.
func (d *DB) upgrade(ctx context.Context) error {
	var version int
	if err := d.writeConn.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil || version < firstVersion {
		return errors.New("this database was made by a different build of Portico; before the first release, delete it and start again")
	}
	current := schemaVersion()
	if version > current {
		problem := fmt.Sprintf("this database was last used by a newer version of Portico (the database is at version %d, this program reads up to version %d). "+
			"Nothing in it has been changed. Install the newer version of Portico again to use it", version, current)
		if copied := backupPath(d.path, current); fileExists(copied) {
			problem += fmt.Sprintf(". To go back instead, losing everything since the upgrade: stop Portico, delete %s-wal and %s-shm if they are there, and put a copy of %s in place of %s",
				d.path, d.path, copied, d.path)
		}
		return errors.New(problem)
	}
	if version == current {
		return nil
	}
	slog.Info("upgrading the database; the server starts when this is done", "from", version, "to", current)
	copied, err := d.backupBeforeUpgrade(ctx, version)
	if err != nil {
		return fmt.Errorf("the database needs upgrading and could not be copied first, so nothing in it has been changed: %w", err)
	}
	slog.Info("the database as it was before the upgrade is kept; it can be deleted once this version is working well", "file", copied)
	for ; version < current; version++ {
		step := upgrades[version-firstVersion]
		if err := d.applyStep(ctx, version+1, step); err != nil {
			return fmt.Errorf("upgrading the database to version %d (%s) failed, and it stays at version %d; the database as it was before the upgrade is at %s: %w",
				version+1, step.what, version, copied, err)
		}
		slog.Info("database upgraded", "version", version+1, "change", step.what)
	}
	return nil
}

// applyStep runs a step's quick part and records the new version, both or neither.
func (d *DB) applyStep(ctx context.Context, version int, step upgrade) error {
	tx, err := d.writeConn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if step.sql != "" {
		if _, err := tx.ExecContext(ctx, step.sql); err != nil {
			return err
		}
	}
	var position any
	if step.background != nil {
		position = 0
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at, background_after) VALUES (?, ?, ?)`,
		version, time.Now().UnixMilli(), position); err != nil {
		return err
	}
	upgradePoint("step applied, not yet committed")
	if err := tx.Commit(); err != nil {
		return err
	}
	upgradePoint("step committed")
	return nil
}

// backupBeforeUpgrade copies the whole database to a file beside it and returns the file's
// name. The copy gets its name only once it is complete and on disk, so a file with that
// name is always a whole database.
func (d *DB) backupBeforeUpgrade(ctx context.Context, version int) (string, error) {
	final := backupPath(d.path, version)
	partial := final + ".partial"
	if err := os.Remove(partial); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := d.copyTo(ctx, partial); err != nil {
		os.Remove(partial)
		return "", err
	}
	upgradePoint("copy written, not yet named")
	if err := os.Rename(partial, final); err != nil {
		return "", err
	}
	return final, dirsync.Sync(filepath.Dir(final))
}

// copyTo writes the database, with everything committed so far, to a new file and syncs it.
func (d *DB) copyTo(ctx context.Context, path string) error {
	if _, err := d.writeConn.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// finishUpgrades runs the background parts that are not finished, oldest step first, until
// they are done or the database closes. What is left is picked up at the next start.
func (d *DB) finishUpgrades() {
	defer close(d.upgradesDone)
	ctx := context.Background()
	var left bool
	if err := d.read.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE background_after IS NOT NULL)`).Scan(&left); err == nil && !left {
		return
	}
	started := false
	for {
		more := false
		err := d.write(ctx, Background, func(tx *sql.Tx) (err error) {
			more, err = upgradeBatch(tx)
			return err
		})
		if errors.Is(err, errClosed) {
			return
		}
		if err != nil {
			slog.Error("the upgrade's background work stopped; it is tried again when the server next starts", "error", err)
			return
		}
		upgradePoint("batch committed")
		if !more {
			if started {
				slog.Info("the upgrade's background work is finished")
			}
			return
		}
		if !started {
			started = true
			slog.Info("the server is ready; the rest of the upgrade runs in the background")
		}
	}
}

// upgradeBatch runs one batch of the oldest unfinished background part and records how far
// it got, in the caller's transaction. It reports false when no step has any left.
func upgradeBatch(tx *sql.Tx) (bool, error) {
	var version int
	var after int64
	err := tx.QueryRow(`SELECT version, background_after FROM schema_migrations WHERE background_after IS NOT NULL ORDER BY version LIMIT 1`).Scan(&version, &after)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	at := version - firstVersion - 1
	if at < 0 || at >= len(upgrades) || upgrades[at].background == nil {
		return false, fmt.Errorf("version %d has background work left and this program has none for it", version)
	}
	next, done, err := upgrades[at].background(tx, after)
	if err != nil {
		return false, fmt.Errorf("version %d (%s): %w", version, upgrades[at].what, err)
	}
	var position any = next
	if done {
		position = nil
	}
	if _, err := tx.Exec(`UPDATE schema_migrations SET background_after = ? WHERE version = ?`, position, version); err != nil {
		return false, err
	}
	upgradePoint("batch run, not yet committed")
	return true, nil
}
