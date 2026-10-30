package metadata

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// drainCatalogMutations drives the actual catalogue-domain jobs to terminal:
// the same per-job Step the operations scheduler calls, over the real
// console_operations rows. It re-reads the runnable jobs every turn, so a root
// that enqueues another root while it runs (an MB album adoption handing a
// frozen artist to its own adoption) is picked up instead of being left
// non-terminal. The bound is a fixture guard, not a production cap; it fails
// loudly with the remaining custody rather than weakening an assertion.
func drainCatalogMutations(t *testing.T, db *sql.DB, adapter operations.Adapter) {
	t.Helper()
	for turn := 0; turn < 10000; turn++ {
		var roots int
		if err := db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations WHERE status<>'terminal'`).Scan(&roots); err != nil {
			t.Fatal(err)
		}
		rows, err := db.Query(`SELECT resource FROM console_operations WHERE kind='catalog-mutation' AND state IN('queued','running','reconciling') ORDER BY sequence LIMIT 64`)
		if err != nil {
			t.Fatal(err)
		}
		var resources []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			resources = append(resources, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if roots == 0 && len(resources) == 0 {
			return
		}
		if len(resources) == 0 {
			t.Fatalf("%d catalogue mutation root(s) have no runnable operation", roots)
		}
		for _, id := range resources {
			out, err := adapter.Step(context.Background(), id)
			if err != nil {
				t.Fatal(id, err)
			}
			if out.State == "failed" || out.State == "cancelled" {
				t.Fatalf("catalogue mutation %s ended %s: %s", id, out.State, out.Phase)
			}
		}
	}
	t.Fatal("catalogue mutations did not drain within the bounded fixture turns")
}
