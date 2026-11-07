package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"testing"
)

func basicScalarCopyStepDefinitions() []stmt.Definition {
	d := append(basicCopyChapterFactTestDefinitions(), basicCopySubtitleFactDefinitions()...)
	return append(d, basicCopyStepDefinitions()...)
}

func basicScalarCopyStepStart(t *testing.T, turn *commandTurn, source basicStreamFactStage, before basicCopyPosition, family, shape int64) basicCopyPosition {
	t.Helper()
	next := before
	next.family = family
	got, err := advanceBasicCopiedPosition(t.Context(), turn, source, before, next, shape)
	if err != nil {
		t.Fatal(got, err)
	}
	return got
}

func TestBasicScalarCopyStepsPersistNativeContinuation(t *testing.T) {
	for _, family := range []int64{4, 6, 9} {
		t.Run(string(rune('0'+family)), func(t *testing.T) {
			defs := basicScalarCopyStepDefinitions()
			var w *executor
			var turn *commandTurn
			var source basicStreamFactStage
			var before, got basicCopyPosition
			var shape int64
			var err error
			switch family {
			case 4:
				var in basicChapterFactCopyEffects
				w, turn, in = basicCopyChapterFactFixture(t, true, defs)
				source, shape = in.family.source, in.shapeID
				in.position = basicScalarCopyStepStart(t, turn, source, in.position, family, shape)
				before = in.position
				reg := turn.registry
				basicMediaCommit(t, turn)
				turn = basicHandlerTurn(t, w, BasicStage, defs, reg)
				got, err = copyBasicChapterFactStep(t.Context(), turn, in)
			case 6:
				var in basicSubtitleFactCopyEffects
				w, turn, in = basicCopySubtitleFactFixture(t, true, defs)
				source, shape = in.family.source, in.shapeID
				in.position = basicScalarCopyStepStart(t, turn, source, in.position, family, shape)
				before = in.position
				reg := turn.registry
				basicMediaCommit(t, turn)
				turn = basicHandlerTurn(t, w, BasicStage, defs, reg)
				got, err = copyBasicSubtitleFactStep(t.Context(), turn, in)
			case 9:
				var in basicSubtitleEvidenceCopyEffects
				w, turn, in = basicCopyEvidenceFixture(t, true, defs)
				source, shape = in.family.source, in.shapeID
				in.position = basicScalarCopyStepStart(t, turn, source, in.position, family, shape)
				before = in.position
				reg := turn.registry
				basicMediaCommit(t, turn)
				turn = basicHandlerTurn(t, w, BasicStage, defs, reg)
				got, err = copyBasicSubtitleEvidenceStep(t.Context(), turn, in)
			}
			want := before
			want.changeRevision++
			want.family++
			if family == 9 {
				want.family, want.phase = 0, 3
			}
			if err != nil || got != want {
				t.Fatal(got, err, want)
			}
			var native basicCopyPosition
			if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT id,job_revision,phase,after_entity,copy_family,copy_phase,copy_id,copy_aux,copy_key,copy_key2,copy_key3 FROM catalog_info_changes WHERE id=51`).Scan(&native.changeID, &native.changeRevision, &native.phase, &native.afterEntity, &native.family, &native.copyPhase, &native.id, &native.aux, &native.key, &native.key2, &native.key3); err != nil || native != want {
				t.Fatal(native, err, want)
			}
			var fenced bool
			if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT info_epoch=17 AND info_publication_pending=1 FROM catalog_assets WHERE id=41) AND (SELECT info_pending_change=51 FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41) AND (SELECT count(*)=0 FROM scan_command_receipts)`).Scan(&fenced); err != nil || !fenced {
				t.Fatal(fenced, err)
			}
			basicMediaCommit(t, turn)
		})
	}
}

func TestBasicCopyCompletionLateCursorFailureRollsBackEvidence(t *testing.T) {
	defs := basicScalarCopyStepDefinitions()
	w, turn, in := basicCopyEvidenceFixture(t, true, defs)
	in.position = basicScalarCopyStepStart(t, turn, in.family.source, in.position, 9, in.shapeID)
	reg := turn.registry
	basicMediaCommit(t, turn)
	turn = basicHandlerTurn(t, w, BasicStage, defs, reg)
	delete(turn.steps, basicPrefenceChangeCAS)
	got, err := copyBasicSubtitleEvidenceStep(t.Context(), turn, in)
	if err == nil || got != (basicCopyPosition{}) {
		t.Fatal(got, err)
	}
	var changed string
	if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT inventory_evidence FROM asset_subtitle_facts WHERE asset_id='basic-asset'`).Scan(&changed); e != nil || changed == "old evidence" {
		t.Fatal("expected actual preceding write", changed, e)
	}
	_ = turn.closeRows()
	_, _ = turn.transaction.rollback(err)
	var kept bool
	if e := w.db.QueryRowContext(t.Context(), `SELECT (SELECT inventory_evidence='old evidence' FROM asset_subtitle_facts WHERE asset_id='basic-asset') AND (SELECT phase=2 AND copy_family=9 FROM catalog_info_changes WHERE id=51) AND (SELECT info_epoch=17 AND info_publication_pending=1 FROM catalog_assets WHERE id=41)`).Scan(&kept); e != nil || !kept {
		t.Fatal(kept, e)
	}
}
