package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Opening a database must not cost time proportional to the catalogue. This is
// the gate on that, and it is deliberately a comparison rather than an absolute:
// a developer laptop, a CI runner and a NAS disagree about how long an open
// takes, and none of them disagree about whether it got slower because there
// are more items.
//
// A few thousand real catalogue items are enough to expose startup work that
// scans each row. The large case runs in the release and deep performance tiers;
// the default suite also checks that a reopen runs no schema statements.
func TestOpenCostIsIndependentOfCatalogueSize(t *testing.T) {
	requireScaleTier(t)
	empty := filepath.Join(t.TempDir(), "empty.sqlite")
	db, err := Open(empty)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	start := time.Now()
	if db, err = Open(empty); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	baseline := time.Since(start)

	loaded := filepath.Join(t.TempDir(), "loaded.sqlite")
	if db, err = Open(loaded); err != nil {
		t.Fatal(err)
	}
	library := persistenceLibrary(t, db, "lib", "Films", "movie", "/films")
	const items = 3000
	persistenceWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < items; i++ {
			name := fmt.Sprintf("Film %06d", i)
			path := fmt.Sprintf("/films/%06d.mp4", i)
			if _, _, _, err := persistenceMovieTx(ctx, tx, library, "/films", path, name, 2001); err != nil {
				return err
			}
		}
		return nil
	})
	persistenceDrain(t, db)
	_ = db.Close()

	start = time.Now()
	if db, err = Open(loaded); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	loadedOpen := time.Since(start)
	t.Logf("open empty=%s open %d items=%s", baseline.Round(time.Millisecond), items, loadedOpen.Round(time.Millisecond))
	// Four times the empty open is generous — it leaves room for a larger
	// sqlite_master and a colder page cache — and it is still an order of
	// magnitude below what one full-catalogue scan costs at this size.
	if loadedOpen > 4*baseline+250*time.Millisecond {
		t.Fatalf("opening a %d-item database took %s against %s: startup work is proportional to the library again", items, loadedOpen, baseline)
	}
}
