package read

import (
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"os"
	"testing"
)

func TestRSTechnicalCapture(t *testing.T) {
	f := rsTechnicalSource(t)
	contract, e := schema.ReadContract("INT-INDEX-POLICY.json")
	if e != nil {
		t.Fatal(e)
	}
	policy, e := plan.ReadIndexPolicy(contract)
	if e != nil {
		t.Fatal(e)
	}
	db, _, e := sqliteconn.Open(t.Context(), f.path, browseReaderConfig())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	conn, e := db.Conn(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	defs := TechnicalFactsDefinitions()
	cat, e := stmt.Describe(defs)
	if e != nil {
		t.Fatal(e)
	}
	var captures []plan.Golden
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		g, e := plan.Capture(t.Context(), conn, cat, browsePlanFixture(browseReaderConfig(), rsTechnicalHash(t), stats), policy)
		if e != nil {
			for _, d := range defs {
				if d.ID == readStatementID("rs.technical.source") {
					rows, _ := conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, int64(1), int64(1), int64(1), int64(1), int64(1), "movies")
					if rows != nil {
						for rows.Next() {
							var a, b, c int
							var text string
							rows.Scan(&a, &b, &c, &text)
							t.Log(text)
						}
						rows.Close()
					}
				}
			}
			t.Fatal(e)
		}
		if _, e = stmt.New(defs, g.Receipt()); e != nil {
			t.Fatal(e)
		}
		captures = append(captures, g)
	}
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		if e := os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
		b, e := json.Marshal(captures)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(dir+"/rs-technical-capture.json", b, 0600); e != nil {
			t.Fatal(e)
		}
	}
}
