package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Group E is not embedded, so no SEARCH/plan golden is recorded here. Every
// closed page/finish read still proves its native primary-key/unique-index path
// (SEARCH, no SCAN, no temp B-TREE sort) against the exact draft subset fixture.
func TestIPEditBulkApplyNativePlans(t *testing.T) {
	w, _, _ := ipBulkApplyFixture(t)
	defs := append(ipEditBulkPageDefinitions(), ipEditBulkFinishDefinitions()...)
	for _, d := range defs {
		if !strings.HasPrefix(string(d.ID), "ip.bulk.page.") && !strings.HasPrefix(string(d.ID), "ip.bulk.finish.") {
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
