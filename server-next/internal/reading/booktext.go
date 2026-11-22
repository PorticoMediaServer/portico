package reading

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/searchtext"
)

// The book-text index is its own file, booktext.db, beside the main database. It holds the
// words of every book the reader can open as an EPUB, so "search all books" finds a line in
// any of them. Nothing in it is an original: each book's text is read again from its file,
// so the file can be deleted at any time, and it is rebuilt from nothing when it was written
// by another format or under other word rules.
//
// One pass at a time brings it up to date with the books on file: a new or changed file is
// read and its rows replaced in one transaction, a file gone from the library loses its rows.
// A changed or gone book raises the revision readers are told, so a page of hits from before
// such a change is never taken for one after it.

// bookTextFormat is raised when the tables below change, or how rows are written into them.
const bookTextFormat = "1"

const bookTextSchema = `
CREATE TABLE meta (key TEXT NOT NULL PRIMARY KEY, value TEXT NOT NULL) STRICT, WITHOUT ROWID;
-- One row per book file read: its fingerprint when read. A file that could not be read has
-- a row and no sections, so it is not read again until it changes.
CREATE TABLE books (file_id INTEGER NOT NULL PRIMARY KEY, fingerprint TEXT NOT NULL) STRICT;
-- One row per section with text: its place in the reading order, its path and its text,
-- normalised as the reader normalises it.
CREATE TABLE sections (id INTEGER NOT NULL PRIMARY KEY, file_id INTEGER NOT NULL, spine INTEGER NOT NULL,
  path TEXT NOT NULL, text TEXT NOT NULL) STRICT;
CREATE INDEX sections_file ON sections (file_id);
-- The section's words, folded by searchtext and joined by spaces; the rowid is the section's id.
CREATE VIRTUAL TABLE section_words USING fts5 (words, content = '', contentless_delete = 1, tokenize = 'ascii');
`

// BookText is the book-text index.
type BookText struct {
	path    string
	reading *Service
	log     *slog.Logger

	mu       sync.Mutex // one update at a time
	conn     *sql.DB
	revision atomic.Int64
	// later holds the books that could not be read for a reason that may pass (a file out of
	// reach, a read that failed, a book too slow to read within a pass), with when, so one
	// such book does not take every pass's time from the books after it.
	later map[int64]tried
}

type tried struct {
	fingerprint string
	at          time.Time
}

// retryLater is how long a book that could not be read waits to be tried again, unless its
// file changes first.
const retryLater = 30 * time.Minute

// OpenBookText opens the index at path, or starts a new one there when it is missing or was
// written another way. It answers at once with what it holds; Update fills it, a book at a time.
func OpenBookText(path string, reading *Service, log *slog.Logger) (*BookText, error) {
	t := &BookText{path: path, reading: reading, log: log, later: map[int64]tried{}}
	conn, err := openBookTextFile(path)
	if err == nil {
		if t.current(conn) {
			t.conn = conn
			return t, nil
		}
		conn.Close()
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		os.Remove(path + suffix)
	}
	if conn, err = openBookTextFile(path); err != nil {
		return nil, err
	}
	if _, err := conn.Exec(bookTextSchema); err != nil {
		conn.Close()
		return nil, err
	}
	// A new file's revision starts from the clock, so a page from a file it replaced is never
	// taken for one of this file's.
	start := time.Now().UnixMilli()
	if _, err := conn.Exec(`INSERT INTO meta (key, value) VALUES ('format', ?), ('folding', ?), ('revision', ?)`,
		bookTextFormat, searchtext.Stamp(), strconv.FormatInt(start, 10)); err != nil {
		conn.Close()
		return nil, err
	}
	t.conn = conn
	t.revision.Store(start)
	return t, nil
}

func openBookTextFile(path string) (*sql.DB, error) {
	q := url.Values{}
	q.Set("mode", "rwc")
	q.Set("_txlock", "immediate")
	for _, pragma := range []string{"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(NORMAL)", "cache_size(-8192)"} {
		q.Add("_pragma", pragma)
	}
	conn, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(4)
	return conn, conn.Ping()
}

// current reports whether the file was written by this format under today's word rules, and
// takes its revision.
func (t *BookText) current(conn *sql.DB) bool {
	meta := map[string]string{}
	rows, err := conn.Query(`SELECT key, value FROM meta`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) != nil {
			return false
		}
		meta[k] = v
	}
	revision, err := strconv.ParseInt(meta["revision"], 10, 64)
	if rows.Err() != nil || err != nil || meta["format"] != bookTextFormat || meta["folding"] != searchtext.Stamp() {
		return false
	}
	t.revision.Store(revision)
	return true
}

// Close closes the file.
func (t *BookText) Close() error { return t.conn.Close() }

// Revision is the index's revision: raised by every change to what it holds.
func (t *BookText) Revision() int64 { return t.revision.Load() }

// Update brings the index up to date with the books on file. It stops early, keeping what it
// has done, when ctx ends.
func (t *BookText) Update(ctx context.Context, files []db.BookTextFile) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	have := map[int64]string{}
	rows, err := t.conn.QueryContext(ctx, `SELECT file_id, fingerprint FROM books`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var fingerprint string
		if err := rows.Scan(&id, &fingerprint); err != nil {
			rows.Close()
			return err
		}
		have[id] = fingerprint
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	keep := map[int64]bool{}
	for _, f := range files {
		keep[f.FileID] = true
		fingerprint := Fingerprint(f.Size, f.ModifiedNS)
		old, held := have[f.FileID]
		if last, waiting := t.later[f.FileID]; old == fingerprint || waiting && last.fingerprint == fingerprint && time.Since(last.at) < retryLater {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		sections, err := t.reading.BookSections(ctx, f)
		switch {
		case err == nil:
		case ctx.Err() != nil:
			// The pass ran out of time inside this book. The next pass starts with the books
			// after it, so a book too slow to read never holds up the rest.
			t.later[f.FileID] = tried{fingerprint, time.Now()}
			t.log.Info("book text: a book took too long to read; trying again later", "file", f.FileID)
			return ctx.Err()
		case errors.Is(err, ErrChanged):
			continue // the scanner records the new file; it is read then
		case errors.Is(err, errDamaged) || errors.Is(err, ErrLocked) || errors.Is(err, ErrFormatLater):
			sections = nil // recorded as having no text, until the file changes
		default:
			// Out of reach, or a read that failed (a network share that dropped, a busy
			// database): nothing says the book is bad, so it is tried again later.
			t.later[f.FileID] = tried{fingerprint, time.Now()}
			t.log.Info("book text: a book could not be read; trying again later", "file", f.FileID, "err", err)
			continue
		}
		delete(t.later, f.FileID)
		if err := t.replace(ctx, f.FileID, fingerprint, sections, held); err != nil {
			return err
		}
	}
	for id := range have {
		if !keep[id] {
			delete(t.later, id)
			if err := t.replace(ctx, id, "", nil, true); err != nil {
				return err
			}
		}
	}
	return nil
}

// replace puts a file's sections in place of what the index held for it; with no
// fingerprint, the file is forgotten. Only a change to a book the index held (held) raises the
// revision: a new book's sections come after every place a cursor can name, so the pages a
// reader is part way through stay good while a library is read for the first time.
func (t *BookText) replace(ctx context.Context, fileID int64, fingerprint string, sections []Section, held bool) error {
	tx, err := t.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM section_words WHERE rowid IN (SELECT id FROM sections WHERE file_id = ?)`, fileID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sections WHERE file_id = ?`, fileID); err != nil {
		return err
	}
	if fingerprint == "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM books WHERE file_id = ?`, fileID); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx, `INSERT INTO books (file_id, fingerprint) VALUES (?, ?)
		ON CONFLICT (file_id) DO UPDATE SET fingerprint = excluded.fingerprint`, fileID, fingerprint); err != nil {
		return err
	}
	for _, s := range sections {
		res, err := tx.ExecContext(ctx, `INSERT INTO sections (file_id, spine, path, text) VALUES (?, ?, ?, ?)`, fileID, s.Index, s.Path, s.Text)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		spans := searchtext.Spans(s.Text)
		words := make([]string, len(spans))
		for n, span := range spans {
			words[n] = span.Word
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO section_words (rowid, words) VALUES (?, ?)`, id, strings.Join(words, " ")); err != nil {
			return err
		}
	}
	if held {
		revision := t.revision.Load() + 1
		if _, err := tx.ExecContext(ctx, `UPDATE meta SET value = ? WHERE key = 'revision'`, strconv.FormatInt(revision, 10)); err != nil {
			return err
		}
		// Raised before the change can be seen: a search that reads the new text is never
		// labelled with the old revision. If the commit fails, the revision is only higher
		// than it needs to be, which asks a reader to start again and nothing worse.
		t.revision.Store(revision)
	}
	return tx.Commit()
}
