package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Group E is embedded, so this is an exact-schema EXPLAIN control, not
// an HN Capture receipt or golden. Reads must stay on an exact primary key
// (receipt, subject job, source, blob) or a reviewed reverse index; writes are
// two exact retirement CASes/deletes, one owner CAS and one receipt insert, with
// no temp B-tree.
func TestIPArtworkGCNativePlans(t *testing.T) {
	w, _, _ := ipArtworkGCFixture(t)
	for _, d := range ipArtworkGCDefinitions() {
		d := d
		t.Run(string(d.ID), func(t *testing.T) {
			rows, err := w.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, d.Cohorts[0].Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				if d.Kind != stmt.Read {
					continue
				}
				if !strings.Contains(detail, "SEARCH ") || strings.Contains(detail, "TEMP B-TREE") {
					t.Fatal(detail)
				}
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
