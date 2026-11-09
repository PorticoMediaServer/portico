package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Native compile/Capture against the real embedded schema: no draft overlay and
// no stored golden is fabricated here. This proves every declared index/key, the
// complete foreign-key set and that Capture executes no effect.
func TestMGCaptureStatementPlans(t *testing.T) {
	defs := mgCaptureDefinitions()
	if len(defs) != 7 {
		t.Fatal("finite inventory", len(defs))
	}
	for _, d := range defs {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	w := mgRecordingCaptureExecutor(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	digest := sha256.Sum256([]byte(mgRecordingCaptureFixtureSQL))
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	g, err := cachedCapture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(digest[:]), Statistics: plan.Unanalyzed, Implementations: "Closed MG capture recording/source/allocation/launch points and claim/renew/checkpoint custody mutations on the embedded schema; absent-key compile cohorts only; no worker, artifact, physical-exit or performance qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Manifest) != 7 || len(g.Plans) != 7 {
		t.Fatal("incomplete native capture", len(g.Manifest), len(g.Plans))
	}
	if _, err := stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != before {
		t.Fatal("Capture executed accepting effects", before, after, err)
	}
}

// Every family member is point/index SEARCH-only with no temporary B-tree,
// before and after ANALYZE.
func TestMGCaptureNativePlans(t *testing.T) {
	w := mgRecordingCaptureExecutor(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, analyzed := range []bool{false, true} {
		if analyzed {
			if _, err := conn.ExecContext(t.Context(), `ANALYZE`); err != nil {
				t.Fatal(err)
			}
		}
		for _, d := range mgCaptureDefinitions() {
			if issues := plan.LintSQLOn(t.Context(), conn, d.SQL); len(issues) > 0 {
				t.Fatal(d.ID, issues)
			}
			rows, err := conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, d.Cohorts[0].Bindings...)
			if err != nil {
				t.Fatal(d.ID, err)
			}
			count := 0
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(detail, "SEARCH ") || strings.Contains(detail, "TEMP B-TREE") {
					t.Fatal(d.ID, detail)
				}
				count++
			}
			err = rows.Err()
			rows.Close()
			if err != nil || count == 0 {
				t.Fatal(d.ID, count, err)
			}
		}
	}
}

// Native capture facts and the launch-uniqueness point keep their exact
// comparison shapes without widening a shared registry list.
func TestMGCaptureDescriptorsAndFences(t *testing.T) {
	if err := mgCapturePurpose(nil, MGCaptureClaim); err == nil {
		t.Fatal("nil turn admitted a capture purpose")
	}
	row := mgCaptureRow{id: strings.Repeat("a", 64)}
	if row.ownerIs(mgRecordingOwner{}) {
		t.Fatal("empty owner matched a capture row")
	}
	if !mgCaptureFence(strings.Repeat("a", 48)) || mgCaptureFence(strings.Repeat("a", 47)) || mgCaptureFence("ABC") {
		t.Fatal("source fence shape")
	}
}
