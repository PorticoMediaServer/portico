package read

import (
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Group C is not embedded, so plan.Capture cannot mint a receipt for a schema
// that carries the C2 relations and no golden may be recorded yet. These native
// EXPLAIN receipts are the same standard the merged IA-05/06/11/12/13 draft
// families apply: every read is one indexed SEARCH with no temp B-tree and no
// table scan. The functional read test waits for the group C embed.
func TestIABookSeriesPlans(t *testing.T) {
	source := readfixture.Foreground(t)
	ddl, err := os.ReadFile("testdata/ia-book-series-draft.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.Conn().ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	db, _, err := sqliteconn.Open(t.Context(), source.Path(), readfixture.ReaderConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defs := IABookSeriesHarnessDefinitions()
	if len(defs) != 7 {
		t.Fatal("series family inventory changed", len(defs))
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(plan.LintSQL(d.SQL)) != 0 {
			t.Fatal("closed hot grammar", d.ID, plan.LintSQL(d.SQL))
		}
		if len(d.Cohorts) == 0 {
			t.Fatal("statement lacks a native plan cohort", d.ID)
		}
		for _, cohort := range d.Cohorts {
			rows, err := conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatal(d.ID, cohort.Name, err)
			}
			searched := false
			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				details = append(details, detail)
				if strings.Contains(detail, "TEMP B-TREE") || strings.Contains(detail, "SCAN ") && !strings.HasPrefix(detail, "SCAN CONSTANT") {
					rows.Close()
					t.Fatal(d.ID, cohort.Name, detail)
				}
				if strings.HasPrefix(detail, "SEARCH ") {
					searched = true
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			rows.Close()
			if d.Kind == stmt.Read && !searched {
				t.Fatal(d.ID, cohort.Name, "read has no indexed SEARCH")
			}
			if cohort.Name != "present" {
				continue
			}
			want := ""
			switch d.ID {
			case readStatementID("foreground.ia.book_series.head"), readStatementID("foreground.ia.book_series.head_octets"):
				want = "SEARCH catalog_book_series_heads USING INTEGER PRIMARY KEY (rowid=?)"
			case readStatementID("foreground.ia.book_series.members"):
				want = "SEARCH catalog_book_series_members USING PRIMARY KEY (book_id=?)"
			case readStatementID("foreground.ia.book_series.member"), readStatementID("foreground.ia.book_series.member_octets"):
				want = "SEARCH catalog_book_series_members USING PRIMARY KEY (book_id=? AND ordinal=?)"
			}
			if want != "" && (len(details) != 1 || details[0] != want) {
				t.Fatal(d.ID, cohort.Name, details, want)
			}
		}
	}
	t.Logf("%d hot book-series statements on the group C draft; EXPLAIN-only, no Capture claim", len(defs))
}
