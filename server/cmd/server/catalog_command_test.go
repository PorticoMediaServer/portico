package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

func TestCatalogRebuildCommandUsesTheInstanceLockAndPersistsRequest(t *testing.T) {
	state := t.TempDir()
	layout := storagepaths.At(state)
	if err := os.MkdirAll(layout.Database, 0700); err != nil {
		t.Fatal(err)
	}
	path := storagepaths.Path(state, "server.sqlite")
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = compactcatalog.NewWorker(db).Rebuild(context.Background(), compactcatalog.DomainBrowseRows); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE catalog_derivations SET rebuilding=0,first_build=0,rebuild_cursor=77 WHERE domain=18`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	lock, err := acquireInstanceLock(storagepaths.Path(state, "server.lock"))
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"catalog", "rebuild", "18", "--data-dir", state}
	var out bytes.Buffer
	handled, err := catalogCommand(context.Background(), args, &out)
	if !handled || !errors.Is(err, errInstanceLockHeld) {
		t.Fatal(handled, err)
	}
	lock.Close()
	handled, err = catalogCommand(context.Background(), args, &out)
	if !handled || err != nil {
		t.Fatal(handled, err)
	}
	db, err = persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rebuilding, first, cursor int
	if err = db.QueryRow(`SELECT rebuilding,first_build,rebuild_cursor FROM catalog_derivations WHERE domain=18`).Scan(&rebuilding, &first, &cursor); err != nil || rebuilding != 1 || first != 0 || cursor != 0 {
		t.Fatal(rebuilding, first, cursor, err)
	}
}

func TestCatalogRebuildCommandRefusesBadInputBeforeCreatingState(t *testing.T) {
	for _, args := range [][]string{
		{"catalog"}, {"catalog", "rebuild", "999"}, {"catalog", "rebuild", "browse"},
		{"catalog", "rebuild", "18", "--data-dir", "relative"},
		{"catalog", "rebuild", "18", "unexpected"},
		{"catalog", "rebuild", "18", "--data-dir", filepath.Join(t.TempDir(), "absent")},
	} {
		if handled, err := catalogCommand(context.Background(), args, &bytes.Buffer{}); !handled || err == nil {
			t.Fatal(args, handled, err)
		}
	}
	if handled, err := catalogCommand(context.Background(), []string{"--version"}, &bytes.Buffer{}); handled || err != nil {
		t.Fatal(handled, err)
	}
}
