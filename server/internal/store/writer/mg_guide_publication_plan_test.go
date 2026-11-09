package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestMGGuidePublicationStatementDefinitions(t *testing.T) {
	defs := mgGuidePublicationDefinitions()
	if len(defs) != 5 {
		t.Fatal("finite publication inventory", len(defs))
	}
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	w, _ := testCanonicalExecutor(t)
	for _, d := range defs {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
		if len(d.Cohorts) == 0 {
			t.Fatal("missing cohort", d.ID)
		}
		rows, err := w.db.QueryContext(t.Context(), "EXPLAIN "+d.SQL, d.Cohorts[0].Bindings...)
		if err != nil {
			t.Fatal(d.ID, err)
		}
		n := 0
		for rows.Next() {
			n++
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil || n == 0 {
			t.Fatal("native compile", d.ID, n, err)
		}
	}
	ds := mgGuidePublicationDescriptors()
	if len(ds) != 2 || ds[0].ID != MGGuideSeal || ds[1].ID != MGGuidePublish {
		t.Fatal(ds)
	}
	for _, d := range ds {
		if d.Durability != Normal {
			t.Fatal("guide seal/publication is not NORMAL", d)
		}
	}
	if _, err := buildCommandDescriptors(commandRegistration{owner: "MG", descriptors: ds}); err != nil {
		t.Fatal(err)
	}
	// The domain registry is the only shared seam this task touches.
	closed := map[CommandID]Descriptor{}
	for _, d := range mgDomainDescriptors() {
		closed[d.ID] = d
	}
	for _, id := range []CommandID{MGGuideSeal, MGGuidePublish} {
		if d, ok := closed[id]; !ok || d.Durability != Normal {
			t.Fatal("domain descriptor missing", id, d)
		}
	}
}

// A structural native Capture of the closed seal/publication statements on the
// embedded G/013 schema. The bounded staging capture keeps its own reviewed
// golden; this capture is deliberately not recorded as a golden here, because
// the builder does not run the Capture test. The desk records it (see the task
// report) and it then compares plans verbatim.
func TestMGGuidePublicationCapture(t *testing.T) {
	defs := mgGuidePublicationDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := testCanonicalExecutor(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	for _, name := range []string{"mg_guide_publication_statements.go", "mg_guide_publication.go", "mg_guide_publication_test.go", "mg_guide_publication_fixture_test.go", "mg_guide_publication_plan_test.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(data)
	}
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	g, err := cachedCapture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(h.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "MG guide seal/publication point effects only; embedded G/013 live_operations/live_sources tables with exact CAS and RETURNING equality; no authority, admission, runtime enablement or performance qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, d := range defs {
		want += len(d.Cohorts)
	}
	if len(g.Manifest) != len(defs) || len(g.Plans) != want || g.ForeignKeys == nil {
		t.Fatal("capture inventory", len(g.Manifest), len(g.Plans), want, g.ForeignKeys == nil)
	}
	if _, err := stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != before {
		t.Fatal("Capture executed effects", before, after, err)
	}
}
