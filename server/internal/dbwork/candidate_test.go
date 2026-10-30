package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A candidate opens only a regular file inside the directory its caller owns,
// never an arbitrary path or one reached through a link, and writes to it
// without the live gate or the live writer accounting.
func TestCandidateIsPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "copy", "server.sqlite")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = seed.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE sample(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	seed.Close()
	if err = os.Symlink(filepath.Join(dir, "copy"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	for _, refused := range []struct{ dir, name string }{
		{"relative", "copy/server.sqlite"},
		{dir, path},
		{dir, "../server.sqlite"},
		{dir, "link/server.sqlite"},
		{dir, "copy"},
	} {
		if c, err := OpenCandidate(refused.dir, refused.name); err == nil {
			c.Close()
			t.Fatalf("opened %q in %q", refused.name, refused.dir)
		}
	}
	if _, err = OpenCandidate(dir, "copy/absent.sqlite"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("absent file: %v", err)
	}
	c, err := OpenCandidate(dir, "copy/server.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	writerBefore, readerBefore := UngatedWrites()
	publishedBefore := Publications()
	if err = c.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO sample VALUES(1)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if writer, reader := UngatedWrites(); writer != writerBefore || reader != readerBefore || Publications() != publishedBefore || gate.Held() {
		t.Fatal("candidate work counted as live database work")
	}
	if lines, err := c.Check(ctx, true, 1); err != nil || !Healthy(lines) {
		t.Fatal(lines, err)
	}
	if err = c.Fold(ctx); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path + "-wal"); err == nil && info.Size() != 0 {
		t.Fatal("fold left log frames")
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
}
