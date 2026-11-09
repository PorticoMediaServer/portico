package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Group D is embedded, so this is an exact-schema EXPLAIN control, not
// an HN Capture receipt or golden. The build read stays on the exact build and
// evidence PKs, the exact-seal reverse lookup uses the partial evidence index,
// every child count uses its closed family PK, and no read may use a temp
// B-tree.
func TestIPEntitySealNativePlans(t *testing.T) {
	w, _, _, _ := ipEntitySealFixture(t)
	for _, d := range ipEntitySealDefinitions() {
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
