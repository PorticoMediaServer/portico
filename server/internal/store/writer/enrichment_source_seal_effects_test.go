package writer

import (
	"errors"
	"testing"
)

func TestEvidenceSealCASRequiresCompletePrefixAndNoCandidates(t *testing.T) {
	for _, candidate := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "candidate"}[candidate], func(t *testing.T) {
			w, scope := sealEffectFixture(t)
			if candidate {
				populateSealCandidates(t, w)
			}
			if _, err := w.db.ExecContext(t.Context(), `UPDATE metadata_evidence_builds SET cursor=3,materialized_rows=3 WHERE id='build-original:211'`); err != nil {
				t.Fatal(err)
			}
			turn := sealEffectTurn(t, w, enrichmentSealAdvanceDefinitions())
			root := evidenceSealRoot{buildID: "build-original:211", evidenceID: "evidence-original:197", parser: "fixture-parser", buildGeneration: 47, revision: 1, subjectID: 193, rows: 3}
			got, err := advanceEvidenceSeal(t.Context(), turn, scope, root)
			if candidate {
				if !errors.Is(err, ErrBusy) || got != (sealMutationFacts{}) {
					t.Fatal("unretired candidates sealed", got, err)
				}
				return
			}
			if err != nil || got.revision != 2 || got.next != 3 || got.observedCatalogRevision != 203 || got.afterItem != scope.afterItem || got.appliedMS <= 0 {
				t.Fatal("native CAS facts", got, err)
			}
			if got, err := advanceEvidenceSeal(t.Context(), turn, scope, root); !errors.Is(err, ErrConflict) || got != (sealMutationFacts{}) {
				t.Fatal("stale content R accepted", got, err)
			}
		})
	}
}
