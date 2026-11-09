package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The lock/restore families declare private keyset reads only: the owner row,
// the one owner-field row, the accepted catalogue snapshot and the saved
// receipt. Every read must SEARCH on a declared key without a temporary sort.
func TestIPTextFieldLockNativePlans(t *testing.T) {
	w, _, _ := ipTextLockFixture(t)
	for _, d := range ipTextLockDefinitions() {
		if !strings.HasPrefix(string(d.ID), "ip.") {
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
