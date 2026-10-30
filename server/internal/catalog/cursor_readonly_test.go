package catalog

import (
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestFirstCatalogCursorReadDoesNotWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.OpenFresh(filepath.Join(t.TempDir(), "cursor.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	before := dbwork.Publications()
	key, err := New(db).cursorKey()
	if err != nil || len(key) != 32 {
		t.Fatalf("missing migration-owned cursor key: %d bytes, %v", len(key), err)
	}
	if after := dbwork.Publications(); after != before {
		t.Fatalf("a catalogue cursor read published a write: before=%d after=%d", before, after)
	}
}
