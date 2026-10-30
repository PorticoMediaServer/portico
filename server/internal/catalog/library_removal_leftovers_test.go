package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// libraryLeftovers counts, per table, the rows that still carry a removed
// library's catalogue handle.
func libraryLeftovers(t *testing.T, c *catalogtest.Catalog, handle int64) map[string]int {
	t.Helper()
	tables, err := c.DB.Query(`SELECT m.name FROM sqlite_schema m JOIN pragma_table_info(m.name) i WHERE m.type='table' AND i.name='library_id' AND i.type='INTEGER' AND m.name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for tables.Next() {
		var name string
		if err = tables.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	tables.Close()
	if len(names) < 20 {
		t.Fatalf("only %d tables carry a library handle; the schema walk is broken", len(names))
	}
	left := map[string]int{}
	for _, name := range names {
		var n int
		if err = c.DB.QueryRow(`SELECT count(*) FROM `+name+` WHERE library_id=?`, handle).Scan(&n); err != nil {
			t.Fatal(name, err)
		}
		if n > 0 {
			left[name] = n
		}
	}
	return left
}

func removeLibraryFully(t *testing.T, c *catalogtest.Catalog, library string, drainBetween bool) {
	t.Helper()
	for i := 0; ; i++ {
		done := false
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var e error
			done, e = removeLibraryBatchTx(ctx, tx, library)
			return e
		})
		if drainBetween {
			c.Drain()
		}
		if done {
			return
		}
		if i > 2000 {
			t.Fatal("removal did not finish")
		}
	}
}

// A removed library leaves nothing behind in any table that names it, whether
// the background catalogue work keeps up with the removal or runs after it,
// and its handle is never given to a later library.
func TestRemovedLibraryLeavesNoRowsUnderItsHandle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, drainBetween := range []bool{true, false} {
		t.Run(fmt.Sprintf("workKeepsUp=%v", drainBetween), func(t *testing.T) {
			c := catalogtest.Open(t)
			c.Library("keep", "Kept", "movie", "/keep")
			keptAsset, _ := identityFile(t, c, "keep", "/keep/Kept (1999).mkv", 1080)
			attachMovie(t, c, "keep", keptAsset, "/keep/Kept (1999).mkv", "Kept", 1999)
			handle := c.Library("m", "Movies", "movie", "/m")
			for i := 0; i < 30; i++ {
				path := fmt.Sprintf("/m/Film %02d (2001).mkv", i)
				a, _ := identityFile(t, c, "m", path, 1080)
				film := attachMovie(t, c, "m", a, path, fmt.Sprintf("Film %02d", i), 2001)
				if i < 3 {
					c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,not_interested,rating,revision,watched,last_played_at) VALUES('p',?,0,1,0,8,1,1,'2026-09-01T00:00:00Z')`, film)
				}
			}
			c.Drain()
			if len(libraryLeftovers(t, c, handle)) == 0 {
				t.Fatal("the fixture derived nothing for the library")
			}
			c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE library_id='m'`)
			removeLibraryFully(t, c, "m", drainBetween)
			c.Drain()
			drainRemovedLibraryMutationReceipts(t, c)
			if n := rows(t, c, `SELECT count(*) FROM catalog_libraries WHERE library_id='m'`); n != 0 {
				t.Fatalf("the library row lingers: %d", n)
			}
			if left := libraryLeftovers(t, c, handle); len(left) != 0 {
				t.Fatalf("rows left under the removed library's handle: %v", left)
			}
			if n := rows(t, c, `SELECT count(*) FROM catalog_derivation_failures`); n != 0 {
				t.Fatalf("catalogue work failed after the removal: %d", n)
			}
			if again := c.Library("again", "Movies again", "movie", "/m"); again == handle {
				t.Fatalf("a new library was given the removed library's handle %d", handle)
			}
			if n := rows(t, c, `SELECT count(*) FROM catalog_entities`); n != 1 {
				t.Fatalf("the other library's title was touched: %d", n)
			}
		})
	}
}

// Catalogue derivation and acknowledged command cleanup are separate production
// rails. Run the actual cleanup adapter before the all-tables lifetime oracle;
// c.Drain only advances compact catalogue derivation, not operations jobs.
func drainRemovedLibraryMutationReceipts(t *testing.T, c *catalogtest.Catalog) {
	t.Helper()
	var pending, debt int
	if err := c.DB.QueryRow(`SELECT count(*),COALESCE(sum(status<>'terminal' OR acknowledged<>1 OR child_count<>0),0) FROM catalog_entity_mutations`).Scan(&pending, &debt); err != nil {
		t.Fatal(err)
	}
	if debt != 0 {
		t.Fatalf("library removal left unacknowledged or live command custody: %d", debt)
	}
	adapter := New(c.DB).MutationAdapter()
	// This fixture has one acknowledged terminal leaf per removed film. The
	// production cleanup deletes one leaf per turn, then proves exhaustion.
	for turn := 0; turn <= pending; turn++ {
		out, err := adapter.Step(context.Background(), mutationCleanupResource)
		if err != nil {
			t.Fatal("actual mutation cleanup", err)
		}
		if out.State == "succeeded" {
			return
		}
	}
	t.Fatalf("acknowledged command cleanup did not exhaust %d leaves", pending)
}
