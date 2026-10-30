package persistence

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorageStatusLeavesOldEvidenceUntouched(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "portico.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Historical files remain evidence; there is no listing or deletion API.
	copies := filepath.Join(dir, "migration-backups")
	if err = os.Mkdir(copies, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(copies, "before-20261013T120000.000000000Z.sqlite")
	if err = os.WriteFile(path, []byte("old evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	status, err := ReadStorageStatus(context.Background(), db)
	if err != nil || status.DatabasePath != filepath.Join(dir, "portico.db") || status.ReclaimableBytes < 0 {
		t.Fatal(status, err)
	}
	raw, err := json.Marshal(status)
	if err != nil || strings.Contains(string(raw), "upgrade") {
		t.Fatal(string(raw), err)
	}
	preserved, err := os.ReadFile(path)
	if err != nil || string(preserved) != "old evidence" {
		t.Fatal("old evidence changed", err)
	}
}
