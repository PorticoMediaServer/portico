package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicCopyStepTestDefinitions() []stmt.Definition {
	defs := append(basicCopyStreamTestDefinitions(), basicCopyFactDefinitions()...)
	return append(defs, basicCopyStepDefinitions()...)
}
func basicCopyStepStart(t *testing.T, turn *commandTurn, fence basicStreamCopyFence) (basicCopyPosition, error) {
	t.Helper()
	return copyBasicStreamFactStep(t.Context(), turn, basicStreamCopyEffects{source: fence.family.source, position: fence.position, shapeID: fence.shapeID, fileSizeRevision: fence.fileSizeRevision, invalidation: fence.invalidation, hadBefore: true, before: basicCanonicalStreamFact{31, 42, -11, 1, "probe-fingerprint"}})
}
func TestBasicCopyStepNativeRowsAndContinuationAreOneUnit(t *testing.T) {
	w, turn, fence := basicCopyStreamFixture(t, 2, false, basicCopyStepTestDefinitions())
	p, err := basicCopyStepStart(t, turn, fence)
	if err != nil || p != (basicCopyPosition{changeID: 51, changeRevision: 16, phase: 2, afterEntity: 31, family: 2}) {
		t.Fatal(p, err)
	}
	for ordinal := int64(0); ordinal < 2; ordinal++ {
		fence.position, fence.ordinal = p, ordinal
		p, err = copyBasicMediaRowStep(t.Context(), turn, basicMediaCopyStep{fence: fence, before: basicStreamRow{}})
		if err != nil {
			t.Fatal(p, err)
		}
		if p.changeRevision != 17+ordinal || p.family != 2 || (ordinal == 0 && (p.id != 1 || p.copyPhase != 0)) || (ordinal == 1 && (p.id != 0 || p.copyPhase != 1)) {
			t.Fatal(p)
		}
	}
	basicMediaCommit(t, turn)
	var exact bool
	if err = w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT phase=2 AND job_revision=18 AND copy_family=2 AND copy_phase=1 AND copy_id=0 AND copy_aux=0 AND copy_key='' AND copy_key2='' AND copy_key3='' FROM catalog_info_changes WHERE id=51)
 AND (SELECT info_epoch=17 AND info_publication_pending=1 FROM catalog_assets WHERE id=41)
 AND (SELECT info_pending_change=51 FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)
 AND (SELECT count(*)=3 FROM asset_streams)
 AND (SELECT count(*)=0 FROM scan_command_receipts)`).Scan(&exact); err != nil || !exact {
		t.Fatal(exact, err)
	}
	if got := basicCopyStreamReadCore(t, w.db, 9); got.title != "retained sibling" {
		t.Fatal(got)
	}
}

func TestBasicCopyStepLateCursorFailuresRollbackCanonicalRow(t *testing.T) {
	for _, fault := range []string{"missing_step", "returning_swap"} {
		t.Run(fault, func(t *testing.T) {
			defs := basicCopyStepTestDefinitions()
			if fault == "returning_swap" {
				for n := range defs {
					if defs[n].ID == basicCopyPositionCAS {
						defs[n].SQL = strings.Replace(defs[n].SQL, "RETURNING id,job_revision,phase,after_entity,copy_family,copy_phase,", "RETURNING id,job_revision,phase,after_entity,copy_phase,copy_family,", 1)
					}
				}
			}
			w, turn, fence := basicCopyStreamFixture(t, 2, false, defs)
			if fault == "missing_step" {
				delete(turn.steps, basicCopyPositionCAS)
			}
			got, err := basicCopyStepStart(t, turn, fence)
			if err == nil || got != (basicCopyPosition{}) {
				t.Fatal(got, err)
			}
			if fault == "returning_swap" && !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			// The fact write occurred; the frame's original phase1→2 prefence must
			// also roll back, not leave a durable prefix without its receipt.
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
			var exact bool
			if err = w.db.QueryRowContext(t.Context(), `SELECT
    (SELECT phase=0 AND job_revision=13 AND copy_family=1 FROM catalog_info_changes WHERE id=51)
    AND (SELECT info_publication_pending=0 AND info_epoch=17 FROM catalog_assets WHERE id=41)
    AND (SELECT info_pending_change IS NULL FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)
    AND (SELECT count(*)=0 FROM scan_command_receipts)`).Scan(&exact); err != nil || !exact {
				t.Fatal(exact, err)
			}
		})
	}
}
