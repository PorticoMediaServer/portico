package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// IA22's own statement plans. Group C is not embedded yet, so these are exact
// native EQP proofs against the frozen C019 fragment (same fixture as IA20), not
// Capture goldens. No TEMP B-TREE, no unbounded SCAN, exact index pinned.
func TestIARefreshSubtitleSidecarStatementPlans(t *testing.T) {
	w, _, _, _ := iaRefreshSubtitleFixture(t)
	want := map[stmt.ID]string{
		iaRefreshSubtitlePageID:   "asset_subtitles_asset",
		iaRefreshSubtitleHeldID:   "asset_subtitles_asset",
		iaRefreshSidecarCohortID:  "asset_subtitles_asset",
		iaRefreshSidecarCurrentID: "sqlite_autoindex_subtitle_sidecars_1",
		iaRefreshSidecarPathID:    "sqlite_autoindex_subtitle_sidecars_2",
	}
	seen := map[stmt.ID]bool{}
	for _, d := range iaRefreshSubtitleSidecarDefinitions() {
		index, ok := want[d.ID]
		if !ok {
			continue
		}
		seen[d.ID] = true
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 {
			t.Fatal(d.ID, "closed hot plan required")
		}
		if !strings.HasSuffix(string(d.ID), "bytes") && len(plan.LintSQL(d.SQL)) != 0 {
			t.Fatal(d.ID, plan.LintSQL(d.SQL))
		}
		for _, cohort := range d.Cohorts {
			rows, err := w.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatal(d.ID, err)
			}
			indexed := false
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if strings.Contains(detail, "TEMP B-TREE") || strings.Contains(detail, "SCAN ") && !strings.HasPrefix(detail, "SCAN CONSTANT") {
					rows.Close()
					t.Fatal(d.ID, detail)
				}
				indexed = indexed || strings.HasPrefix(detail, "SEARCH ") && strings.Contains(detail, index)
			}
			if err = rows.Err(); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			rows.Close()
			if !indexed {
				t.Fatal(d.ID, "missing exact SEARCH on", index)
			}
		}
	}
	for id := range want {
		if !seen[id] {
			t.Fatal("IA22 statement missing from registry", id)
		}
	}
}

func TestIARefreshSubtitleSidecarStatementsAreNotAcceptingCommands(t *testing.T) {
	// IA22 adds no command ID, handler, result codec or descriptor. Every new
	// statement stays a private Read; adding a Write here would silently widen
	// the accepting surface of 125/126.
	for _, d := range append(iaRefreshSubtitleDefinitions(), iaRefreshSidecarDefinitions()...) {
		if d.Kind != stmt.Read {
			t.Fatal(d.ID, "IA22 statement must stay read-only")
		}
		if len(d.CallSites) == 0 || d.Owner == "" {
			t.Fatal(d.ID, "statement ownership/call sites required")
		}
	}
}
