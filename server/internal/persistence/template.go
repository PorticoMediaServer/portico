package persistence

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Installing the schema costs roughly half a second on a fresh database, and
// roughly sixteen seconds under the race detector, because about fifty-five
// installers each run their own DDL against a new file. A test package that
// builds a hundred fixtures pays that a hundred times, which is why
// `go test -race ./internal/httpapi` could not finish inside any sane timeout —
// and a layer whose concurrency cannot be race-tested is a layer whose
// concurrency is not being checked.
//
// The installed schema is deterministic, so it only has to be built once per
// process: every later fresh database is a byte copy of the first, and the
// handful of values that are genuinely per-database are regenerated after the
// copy.
//
// This is opt-in and only tests opt in. A server opens one database in its
// entire life, so the cache could never hit in production; leaving it off there
// means production carries no test-shaped behaviour at all.

var (
	templateEnabled atomic.Bool
	templateOnce    sync.Once
	templateBytes   []byte
	templateErr     error
)

// UseSchemaTemplate makes Open reuse one installed schema for every fresh
// database opened afterwards in this process. Call it from TestMain.
func UseSchemaTemplate() { templateEnabled.Store(true) }

// openFromTemplate answers a fresh database from the cached schema. It reports
// handled=false for anything it declines — an existing database, a template that
// could not be built — so Open falls through to a real install.
func openFromTemplate(path string) (*sql.DB, bool, error) {
	if !templateEnabled.Load() {
		return nil, false, nil
	}
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		// An existing database verifies its baseline: the template says
		// what a *new* database looks like, never what an old one should become.
		return nil, false, nil
	}
	data, err := schemaTemplate()
	if err != nil || len(data) == 0 {
		return nil, false, nil
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, false, nil
	}
	// A stale sidecar would be read as this database's log.
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	if err = os.WriteFile(path, data, 0600); err != nil {
		return nil, false, nil
	}
	db, err := dbwork.OpenHandle(path, dbwork.DefaultPolicy())
	if err != nil {
		return nil, true, err
	}
	if err = regenerateDatabaseSecrets(db); err != nil {
		db.Close()
		return nil, true, err
	}
	return db, true, nil
}

// schemaTemplate installs the schema once, into a throwaway file, and keeps the
// bytes.
func schemaTemplate() ([]byte, error) {
	templateOnce.Do(func() {
		directory, err := os.MkdirTemp(os.TempDir(), "portico-schema-template-")
		if err != nil {
			templateErr = err
			return
		}
		defer os.RemoveAll(directory)
		file := filepath.Join(directory, "template.sqlite")
		db, err := OpenFresh(file)
		if err != nil {
			templateErr = err
			return
		}
		// Fold the log back into the main file so one file is the whole database.
		if _, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			db.Close()
			templateErr = err
			return
		}
		if err = db.Close(); err != nil {
			templateErr = err
			return
		}
		templateBytes, templateErr = os.ReadFile(file)
	})
	return templateBytes, templateErr
}

// regenerateDatabaseSecrets restores the values a fresh install would have
// generated for this database alone. Copying them would make every database in
// the process share one cursor-signing key, which is true of no real install.
func regenerateDatabaseSecrets(db *sql.DB) error {
	return dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		for _, name := range []string{"chapter_cursor_key", "activity_cursor_key", "catalog_cursor_key"} {
			key := make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE configuration SET value=? WHERE key=?`, hex.EncodeToString(key), name); err != nil {
				return err
			}
		}
		return nil
	})
}
