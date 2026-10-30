package persistence

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// A fresh install costs one to three seconds (the whole schema), and many tests
// only need "a current, empty database". They get a byte copy of one fresh
// install made once per test binary; opening the copy is the ordinary reopen of
// a current database (TestASecondOpenRunsNoSchemaWork), so the state is the same.
var freshInstall struct {
	once sync.Once
	raw  []byte
	err  error
}

// freshDatabaseCopy writes a copy of a fresh install to dir/name and returns its path.
func freshDatabaseCopy(t *testing.T, dir, name string) string {
	t.Helper()
	freshInstall.once.Do(func() {
		scratch, err := os.MkdirTemp("", "portico-fresh-install-")
		if err != nil {
			freshInstall.err = err
			return
		}
		defer os.RemoveAll(scratch)
		path := filepath.Join(scratch, "server.sqlite")
		db, err := Open(path)
		if err != nil {
			freshInstall.err = err
			return
		}
		if err = db.Close(); err != nil {
			freshInstall.err = err
			return
		}
		freshInstall.raw, freshInstall.err = os.ReadFile(path)
	})
	if freshInstall.err != nil {
		t.Fatal(freshInstall.err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, freshInstall.raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
