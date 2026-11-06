package sqliteconn

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExistingFileDoesNotCreateMissingWriterDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing %#&.sqlite")
	cfg := Config{Role: Writer, Connections: 1, CacheKiB: 1024, Synchronous: Full}
	db, _, err := Open(t.Context(), path, cfg, ExistingFile)
	if db != nil {
		_ = db.Close()
		t.Fatal("missing file returned a writer")
	}
	if err == nil {
		t.Fatal("missing file was accepted")
	}
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("restart created a file", name, err)
		}
	}
	// Fresh-file startup remains explicitly available to its existing owners.
	db, _, err = Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal("default fresh opening changed", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExistingFileKeepsActualPolicyAndContent(t *testing.T) {
	path, first, cfg := fixture(t)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	db, actual, err := Open(t.Context(), path, cfg, ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if actual.Synchronous != int(cfg.Synchronous) || actual.DQSDDL != 0 || actual.DQSDML != 0 {
		t.Fatal("existing open changed policy", actual)
	}
	conn := assertConn(t, db, cfg)
	defer conn.Close()
	requireFK787(t, conn)
	var rows int
	if err := conn.QueryRowContext(t.Context(), "SELECT count(*) FROM child").Scan(&rows); err != nil || rows != 1 {
		t.Fatal("existing data changed", rows, err)
	}
}

func TestExistingFileRejectsUnknownAndRepeatedModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.sqlite")
	cfg := Config{Role: Writer, Connections: 1, CacheKiB: 1024, Synchronous: Full}
	for _, modes := range [][]FileMode{{0}, {2}, {255}, {ExistingFile, ExistingFile}} {
		if value, err := Connector(path, cfg, modes...); value != nil || !errors.Is(err, ErrPolicy) {
			t.Fatal("unknown opening mode", modes, value != nil, err)
		}
		if value, _, err := Open(t.Context(), path, cfg, modes...); value != nil || !errors.Is(err, ErrPolicy) {
			if value != nil {
				_ = value.Close()
			}
			t.Fatal("unknown opening mode accepted by Open", modes, value != nil, err)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid mode touched storage", err)
	}
}

func TestExistingFileReplacementDoesNotRecreateDatabase(t *testing.T) {
	path, first, cfg := fixture(t)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	db, _, err := Open(t.Context(), path, cfg, ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Force the next use to acquire a new physical connection without a raw
	// driver escape. Existing-only mode belongs to the immutable DSN, not only
	// a one-time Stat before the first successful open.
	db.SetMaxIdleConns(0)
	if db.Stats().OpenConnections != 0 {
		t.Fatal("old physical connection is still resident")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := db.PingContext(t.Context()); err == nil {
		t.Fatal("replacement opened a missing file")
	}
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("replacement created a file", name, err)
		}
	}
}
