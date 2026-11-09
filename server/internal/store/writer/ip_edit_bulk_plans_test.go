package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Group E is embedded, so no SEARCH/plan golden is recorded here. Every
// closed read still proves its native keyset path (SEARCH, no SCAN, no temp
// B-TREE sort) against the exact embedded schema.
func TestIPEditBulkNativePlans(t *testing.T) {
	w, _, _ := ipBulkFixture(t)
	for _, d := range ipEditBulkDefinitions() {
		if !strings.HasPrefix(string(d.ID), "ip.bulk.") {
			continue
		}
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
				if !strings.Contains(detail, "SEARCH ") || strings.Contains(detail, "TEMP B-TREE") || strings.Contains(detail, "SCAN ") {
					t.Fatal(detail)
				}
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
