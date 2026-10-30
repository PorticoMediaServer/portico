package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// stateDirectoryKey records the state folder a database was last opened from,
// so a copied state folder rebases its absolute paths on first start.
const stateDirectoryKey = "state_directory"

// rebasedColumn is one stored path that lives under the state folder.
type rebasedColumn struct {
	table, column string
}

// rebasedColumns are Portico's own files, referenced absolute to the state
// folder and rewritten when the state folder moves. Media library paths are
// absolute too, but they describe the owner's media rather than Portico's own
// files, so they keep pointing where they point until the owner changes them
// in the console. Every table here is a rowid table; the rebase walks it in
// rowid order.
//
// A rebase in progress records its position in this list, so changing the
// list changes rebaseRegistryVersion.
var rebasedColumns = []rebasedColumn{
	{"libraries", "root"},
	{"library_sources", "root"},
	{"library_sources", "configured_root"},
	{"managed_mounts", "mount_path"},
	{"mount_backend_configs", "config_file"},
	{"playback_artifacts", "directory"},
	{"subtitle_sidecars", "path"},
	{"subtitle_inventory_entries", "directory"},
	{"subtitle_inventory_entries", "path"},
	{"scan_queue", "path"},
	{"metadata_operation_sources", "path"},
	{"mb_operation_sources", "path"},
}

const rebaseRegistryVersion = 1

// Rebase bounds: rows read per window, rows changed per transaction, and how
// many windows without a match pass before the position is saved anyway.
//
// The write plan counts trigger changes too. Library root publication costs
// 21 rows per rewritten root in the regression fixture, plus the saved cursor.
// Four roots cost 85 rows; the former 32-root chunk cost 673 against a 160-row
// maintenance plan. Bound the source quantum instead of increasing the plan.
const (
	rebaseWindow       = 128
	rebaseChanges      = 4
	rebaseQuietWindows = 64
)

// ErrRebaseRegistry is a rebase left unfinished by a build that walked a
// different list of columns. Its position means nothing to this build, and
// redoing it from the start could rewrite a path twice.
var ErrRebaseRegistry = errors.New("an unfinished move of the state folder was started by a different version of Portico; start that version once to finish it")

type rebaseJob struct {
	old, new string
	phase    int
	after    int64
}

// RebaseStatePaths runs after Open, before the server serves anything: when
// the state folder moved since this database last started, it rewrites the
// old prefix to the new one in every registered column, only for values equal
// to the old folder or starting with the old folder plus a separator. Storage
// stays absolute; the move is what repairs it. A database that never recorded
// its folder only records this one.
//
// The rewrite is a durable job (state_rebase_jobs): it reads at most 128 rows
// at a time, changes at most rebaseChanges per transaction, and saves its
// position in the same transaction as the change. A crash resumes the exact
// same move from that position, so no path is rewritten twice. A job left by
// an earlier start is finished first, to the folder it was moving to; if the
// folder has moved again since, a second job then moves it here.
func RebaseStatePaths(ctx context.Context, db *sql.DB, state string) error {
	ctx = dbwork.WithClass(ctx, dbwork.ClassMaintenance)
	state = filepath.Clean(state)
	job, pending, err := readRebaseJob(ctx, db)
	if err != nil {
		return err
	}
	if pending {
		if err = runRebase(ctx, db, job); err != nil {
			return err
		}
	}
	var stored string
	err = db.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key=?`, stateDirectoryKey).Scan(&stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && filepath.Clean(stored) == state {
		return nil
	}
	if err != nil || stored == "" {
		_, err = dbwork.ExecWrite(ctx, db, dbwork.ClassMaintenance, `INSERT INTO configuration(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, stateDirectoryKey, state)
		return err
	}
	job = rebaseJob{old: filepath.Clean(stored), new: state}
	if _, err = dbwork.ExecWrite(ctx, db, dbwork.ClassMaintenance,
		`INSERT INTO state_rebase_jobs(singleton,old_prefix,new_prefix,registry_version,phase,after_rowid,started_ms) VALUES(1,?,?,?,0,0,?)`,
		job.old, job.new, rebaseRegistryVersion, time.Now().UnixMilli()); err != nil {
		return err
	}
	return runRebase(ctx, db, job)
}

func readRebaseJob(ctx context.Context, db *sql.DB) (rebaseJob, bool, error) {
	var job rebaseJob
	var version int
	err := db.QueryRowContext(ctx, `SELECT old_prefix,new_prefix,registry_version,phase,after_rowid FROM state_rebase_jobs WHERE singleton=1`).Scan(&job.old, &job.new, &version, &job.phase, &job.after)
	if errors.Is(err, sql.ErrNoRows) {
		return job, false, nil
	}
	if err != nil {
		return job, false, err
	}
	if version != rebaseRegistryVersion || job.phase > len(rebasedColumns) {
		return job, false, ErrRebaseRegistry
	}
	return job, true, nil
}

// runRebase walks every registered column from the job's position, then
// records the new folder and ends the job in one transaction.
func runRebase(ctx context.Context, db *sql.DB, job rebaseJob) error {
	for ; job.phase < len(rebasedColumns); job.phase, job.after = job.phase+1, 0 {
		if err := rebaseColumn(ctx, db, &job, rebasedColumns[job.phase]); err != nil {
			return err
		}
		if _, err := dbwork.ExecWrite(ctx, db, dbwork.ClassMaintenance, `UPDATE state_rebase_jobs SET phase=?,after_rowid=0 WHERE singleton=1`, job.phase+1); err != nil {
			return err
		}
	}
	return dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO configuration(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, stateDirectoryKey, job.new); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM state_rebase_jobs WHERE singleton=1`)
		return err
	})
}

type rebaseRow struct {
	rowid int64
	value string
}

// rebaseColumn rewrites one column from job.after onwards. The identifiers
// come from the fixed list above, never from a caller.
func rebaseColumn(ctx context.Context, db *sql.DB, job *rebaseJob, col rebasedColumn) error {
	read := fmt.Sprintf(`SELECT rowid,%q FROM %q WHERE rowid>? ORDER BY rowid LIMIT %d`, col.column, col.table, rebaseWindow)
	write := fmt.Sprintf(`UPDATE %q SET %q=? WHERE rowid=? AND %q=?`, col.table, col.column, col.column)
	prefix := job.old + string(filepath.Separator)
	quiet := 0
	for {
		window, last, err := readRebaseWindow(ctx, db, read, job.after)
		if err != nil {
			return err
		}
		if last == 0 {
			return nil
		}
		matches := window[:0]
		for _, row := range window {
			if row.value == job.old || strings.HasPrefix(row.value, prefix) {
				matches = append(matches, row)
			}
		}
		if len(matches) == 0 {
			job.after = last
			if quiet++; quiet%rebaseQuietWindows == 0 {
				if err = saveRebasePosition(ctx, db, job.after); err != nil {
					return err
				}
			}
			continue
		}
		for len(matches) > 0 {
			chunk := matches[:min(rebaseChanges, len(matches))]
			matches = matches[len(chunk):]
			// The chunk that ends the window carries the position past the
			// window's unmatched tail too.
			through := chunk[len(chunk)-1].rowid
			if len(matches) == 0 {
				through = last
			}
			err = dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
				for _, row := range chunk {
					if _, err := tx.ExecContext(ctx, write, job.new+row.value[len(job.old):], row.rowid, row.value); err != nil {
						return err
					}
				}
				_, err := tx.ExecContext(ctx, `UPDATE state_rebase_jobs SET after_rowid=? WHERE singleton=1`, through)
				return err
			})
			if err != nil {
				return err
			}
			job.after = through
		}
	}
}

func readRebaseWindow(ctx context.Context, db *sql.DB, query string, after int64) ([]rebaseRow, int64, error) {
	rows, err := db.QueryContext(ctx, query, after)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	window := make([]rebaseRow, 0, rebaseWindow)
	var last int64
	for rows.Next() {
		var row rebaseRow
		var value sql.NullString
		if err = rows.Scan(&row.rowid, &value); err != nil {
			return nil, 0, err
		}
		last = row.rowid
		if value.Valid {
			row.value = value.String
			window = append(window, row)
		}
	}
	return window, last, rows.Err()
}

func saveRebasePosition(ctx context.Context, db *sql.DB, after int64) error {
	_, err := dbwork.ExecWrite(ctx, db, dbwork.ClassMaintenance, `UPDATE state_rebase_jobs SET after_rowid=? WHERE singleton=1`, after)
	return err
}
