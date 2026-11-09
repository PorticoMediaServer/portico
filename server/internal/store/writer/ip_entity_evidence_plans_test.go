package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Group D is embedded, so this is an exact-schema EXPLAIN control, not
// an HN Capture receipt or golden. The build read stays on the exact evidence
// and build PKs, the reverse receipt lookup uses the exact partial evidence
// index, and no read may use a temp B-tree.
func TestIPEntityEvidenceNativePlans(t *testing.T) {
	w, _, _, _ := ipEntityEvidenceFixture(t)
	for _, d := range ipEntityEvidenceDefinitions() {
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
