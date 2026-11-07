package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"testing"
)

// Disposable diagnostic only: explain the exact finite source definitions on
// the owned native startup connection; this mints no Capture receipt.
func TestBasicSubtitleActualPlanDiagnostic(t *testing.T) {
	w, _ := basicStreamFixture(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = sqliteconn.Assert(t.Context(), conn, w.config); err != nil {
		t.Fatal(err)
	}
	for _, d := range basicCopySubtitlesDefinitions() {
		for _, cohort := range d.Cohorts {
			rows, err := conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatal(d.ID, err)
			}
			for rows.Next() {
				var id, parent, unused int64
				var detail string
				if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				t.Log(d.ID, id, parent, detail)
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err = rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}
