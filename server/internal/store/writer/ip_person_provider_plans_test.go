package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Group D is not embedded yet, so this is a draft-fixture EXPLAIN control, not
// an HN Capture receipt or golden. Reads must stay on the exact person token,
// provider-subject tuple, person operation PK or the unique build-owner index,
// with no temp B-tree; writes are bounded CAS statements.
func TestIPPersonProviderNativePlans(t *testing.T) {
	w, _, _, _ := ipPersonFixture(t)
	for _, d := range ipPersonDefinitions() {
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
