package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// A Candidate is a private handle on an inert database file: a backup copy
// being checked and scrubbed, or a restore source being inspected. It is not
// the live database and must never behave like it. It has one connection with
// a 2 MiB page cache, takes no write-gate ticket, never counts as a live reader
// or writer, and makes no publication, wake or authority effect. Its file is
// held in SQLite's exclusive locking mode, so nothing else opens it meanwhile.
//
// The live gate and its admission machinery exist to share one live file
// between many callers. A candidate has exactly one caller, so going through
// them would only make inert artifact work queue behind (and look like) live
// writes. OpenCandidate takes the directory the caller owns and a name inside
// it, never an arbitrary path, so a production database cannot be opened here.
type Candidate struct {
	db   *sql.DB
	path string
}

// CandidateOptions adjust how a candidate opens. The zero value keeps foreign
// keys on, as the live database has them.
type CandidateOptions struct {
	// ForeignKeysOff opens without foreign-key enforcement, for salvage that
	// copies rows table by table in no particular dependency order.
	ForeignKeysOff bool
}

// ErrCandidatePath is a candidate name that is not a plain file inside its
// directory.
var ErrCandidatePath = errors.New("dbwork: candidate must be a regular file inside its own directory")

// candidateCacheKiB is the one connection's page cache.
const candidateCacheKiB = 2 * 1024

// OpenCandidate opens dir/name as a private candidate. dir must be absolute;
// name must be a relative path inside it with no symbolic link along the way,
// and the file must already exist.
func OpenCandidate(dir, name string, options ...CandidateOptions) (*Candidate, error) {
	path, err := candidatePath(dir, name)
	if err != nil {
		return nil, err
	}
	var option CandidateOptions
	if len(options) > 0 {
		option = options[0]
	}
	values := url.Values{}
	values.Set("_txlock", "immediate")
	if option.ForeignKeysOff {
		values.Add("_pragma", "foreign_keys(OFF)")
	} else {
		values.Add("_pragma", "foreign_keys(ON)")
	}
	values.Add("_pragma", "busy_timeout(5000)")
	values.Add("_pragma", "locking_mode(EXCLUSIVE)")
	values.Add("_pragma", "cache_size(-"+strconv.Itoa(candidateCacheKiB)+")")
	values.Add("_pragma", "mmap_size(0)")
	// The unobserved driver: a candidate is not part of the live database's
	// reader, writer or plan accounting.
	db, err := sql.Open("sqlite", sqliteFileURI(path)+"?"+values.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Candidate{db: db, path: path}, nil
}

func candidatePath(dir, name string) (string, error) {
	if !filepath.IsAbs(dir) || name == "" || filepath.IsAbs(name) || !filepath.IsLocal(filepath.FromSlash(name)) {
		return "", ErrCandidatePath
	}
	dir = filepath.Clean(dir)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	parts := strings.Split(filepath.ToSlash(filepath.Clean(filepath.FromSlash(name))), "/")
	for i := range parts {
		info, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return "", err
			}
			return "", ErrCandidatePath
		}
		last := i == len(parts)-1
		if last && !info.Mode().IsRegular() || !last && !info.IsDir() {
			return "", ErrCandidatePath
		}
	}
	return filepath.Join(dir, filepath.FromSlash(name)), nil
}

// Path is the candidate file.
func (c *Candidate) Path() string { return c.path }

// QueryRowContext reads one row from the candidate.
func (c *Candidate) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return c.db.QueryRowContext(ctx, query, args...)
}

// QueryContext reads from the candidate. Close the rows before any other call.
func (c *Candidate) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return c.db.QueryContext(ctx, query, args...)
}

// ExecContext runs one autocommit statement on the candidate.
func (c *Candidate) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return c.db.ExecContext(ctx, query, args...)
}

// WithTx runs fn in one write transaction on the candidate and commits it
// when fn succeeds.
func (c *Candidate) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Check runs PRAGMA quick_check (quick) or integrity_check and returns its
// report, at most limit lines. A healthy file reports exactly "ok".
func (c *Candidate) Check(ctx context.Context, quick bool, limit int) ([]string, error) {
	if limit < 1 {
		limit = 1
	}
	pragma := "integrity_check"
	if quick {
		pragma = "quick_check"
	}
	rows, err := c.db.QueryContext(ctx, `PRAGMA `+pragma+`(`+strconv.Itoa(limit)+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lines := []string{}
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			return lines, err
		}
		lines = append(lines, line)
	}
	return lines, rows.Err()
}

// Healthy reports whether a Check report is SQLite's single "ok".
func Healthy(lines []string) bool { return len(lines) == 1 && lines[0] == "ok" }

// Fold copies every WAL frame into the candidate file and empties its log, so
// the file alone is the whole database. A partial pass is an error.
func (c *Candidate) Fold(ctx context.Context) error {
	var busy, frames, copied int
	if err := c.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &frames, &copied); err != nil {
		return err
	}
	if busy != 0 || frames != copied {
		return errors.New("dbwork: candidate log could not be folded into its file")
	}
	return nil
}

// Close closes the candidate's connection. SQLite folds the log of a file it
// closes last, which for a candidate is always this connection.
func (c *Candidate) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	err := c.db.Close()
	c.db = nil
	return err
}
