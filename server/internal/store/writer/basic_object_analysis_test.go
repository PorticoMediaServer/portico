package writer

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"strings"
	"testing"
)

func TestBasicObjectAnalysisPreparation(t *testing.T) {
	s := basicCopyPrepareSource()
	s.policyRevision, s.sourceGeneration = 29, 23
	for _, tc := range []struct {
		policy, done    string
		pending         bool
		state, revision string
	}{
		{` [ "probe" ] `, `[ "probe" ]`, false, "complete", s.objectRevision},
		{`["probe"]`, `["probe"]`, true, "pending", ""},
		{`["probe","subtitles","trickplay"]`, `["probe"]`, false, "pending", s.objectRevision},
	} {
		in, e := prepareBasicObjectAnalysis(s, "library", "actual algorithm", tc.policy, tc.done, tc.pending)
		if e != nil {
			t.Fatal(e)
		}
		p, e := validateBasicObjectAnalysis(in)
		if e != nil || p.state != tc.state || p.revision != tc.revision || p.operationsJSON != tc.done || in.policyJSON != tc.policy || p.algorithm != "actual algorithm" {
			t.Fatal(p, e)
		}
	}
	for _, tc := range [][2]string{{`null`, `["probe"]`}, {`["probe","probe"]`, `["probe"]`}, {`["probe"]`, `["probe","probe"]`}, {`["probe"]`, `["probe","subtitles"]`}, {`["probe","trickplay"]`, `["probe","trickplay"]`}, {`["probe"]`, `[]`}, {`["local_metadata"]`, `["local_metadata"]`}} {
		if in, e := prepareBasicObjectAnalysis(s, "library", "algorithm", tc[0], tc[1], false); in != nil || !errors.Is(e, ErrInvalid) {
			t.Fatal(tc, in, e)
		}
	}
	huge := strings.Repeat("x", maxCanonicalRequestBytes+1)
	if in, e := prepareBasicObjectAnalysis(s, "library", "algorithm", "invalid", huge, false); in != nil || !errors.Is(e, ErrCap) {
		t.Fatal(in, e)
	}
	if in, e := prepareBasicObjectAnalysis(s, "library", string([]byte{255}), `["probe"]`, `["probe"]`, false); in != nil || !errors.Is(e, ErrInvalid) {
		t.Fatal(in, e)
	}
	defs := basicObjectAnalysisDefinitions()
	if len(defs) != 2 {
		t.Fatal(len(defs))
	}
	if _, e := stmt.Describe(defs); e != nil {
		t.Fatal(e)
	}
	for _, d := range defs {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
}

// Native tests are added for the parent's hosted run, with actual FULL Capture
// before a protected BasicAccept frame. The fixture supplies no dispatcher.
func TestBasicObjectAnalysisNativeProjection(t *testing.T) {
	w, s, _, _ := basicControlFixture(t)
	policy := ` [ "probe", "local_metadata", "subtitles" ] `
	completed := `["local_metadata", "probe"]`
	if _, e := w.db.ExecContext(t.Context(), `UPDATE library_scan_policies SET operations_json=? WHERE library_id='basic-library'`, policy); e != nil {
		t.Fatal(e)
	}
	in, e := prepareBasicObjectAnalysis(s, "basic-library", "actual-algorithm", policy, completed, false)
	if e != nil {
		t.Fatal(e)
	}
	turn := basicHandlerTurn(t, w, BasicAccept, basicObjectAnalysisDefinitions())
	got, e := applyBasicObjectAnalysis(t.Context(), turn, in)
	want, _ := validateBasicObjectAnalysis(in)
	if e != nil || got != want {
		t.Fatal(got, want, e)
	}
	var exact bool
	if e = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT analysis_revision=? AND analysis_policy=29 AND analysis_operations_json=? AND analysis_state='pending' AND analysis_error='' AND analysis_algorithm='actual-algorithm' AND analysis_generation=23 FROM inventory_objects WHERE id='basic-object'`, s.objectRevision, completed).Scan(&exact); e != nil || !exact {
		t.Fatal(exact, e)
	}
}

func TestBasicObjectAnalysisNativeConflicts(t *testing.T) {
	for _, change := range []string{"policy_bytes", "revision", "defaults", "wrong_return"} {
		t.Run(change, func(t *testing.T) {
			w, s, _, _ := basicControlFixture(t)
			in, e := prepareBasicObjectAnalysis(s, "basic-library", "actual-algorithm", `["probe","local_metadata","subtitles"]`, `["probe"]`, true)
			if e != nil {
				t.Fatal(e)
			}
			defs := basicObjectAnalysisDefinitions()
			if change == "wrong_return" {
				defs[1].SQL = strings.Replace(defs[1].SQL, "RETURNING id,source_id", "RETURNING source_id,id", 1)
			}
			registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_object_analysis_test.go")
			switch change {
			case "policy_bytes":
				_, e = w.db.ExecContext(t.Context(), `UPDATE library_scan_policies SET operations_json='[ "probe", "local_metadata", "subtitles" ]' WHERE library_id='basic-library'`)
			case "revision":
				_, e = w.db.ExecContext(t.Context(), `UPDATE inventory_objects SET revision='different' WHERE id='basic-object'`)
			case "defaults":
				_, e = w.db.ExecContext(t.Context(), `UPDATE inventory_objects SET analysis_generation=1 WHERE id='basic-object'`)
			}
			if e != nil {
				t.Fatal(e)
			}
			turn := basicHandlerTurn(t, w, BasicAccept, defs, registry)
			got, e := applyBasicObjectAnalysis(t.Context(), turn, in)
			if !errors.Is(e, ErrConflict) || got != (basicObjectAnalysisProjection{}) {
				t.Fatal(got, e)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(e)
			var algorithm string
			if e = w.db.QueryRowContext(t.Context(), `SELECT analysis_algorithm FROM inventory_objects WHERE id='basic-object'`).Scan(&algorithm); e != nil || algorithm != "" {
				t.Fatal(algorithm, e)
			}
		})
	}
}
