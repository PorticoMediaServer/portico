package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Groups D and E are not embedded yet, so this is a draft-fixture EXPLAIN
// control, not an HN Capture receipt or golden. The source read stays on the
// exact evidence/build/receipt PKs, the page/member raw keysets use their own
// PK order, and no read may use a temp B-tree.
func TestIPTVSetNativePlans(t *testing.T) {
	w, _, _, _ := ipTVSetFixture(t)
	for _, d := range ipTVSetDefinitions() {
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
