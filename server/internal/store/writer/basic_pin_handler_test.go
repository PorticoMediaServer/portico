package writer

import (
	"errors"
	"reflect"
	"testing"
)

func TestBasicHandlerShapePinRequiresEverySealedFamily(t *testing.T) {
	e, source, original, identity := basicControlFixture(t)
	incarnation := [16]byte{0x41}
	if _, err := e.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET family_mask=511 WHERE id=51`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(t.Context(), `UPDATE catalog_assets SET container='mkv',video_codec='h264',audio_codec='',width=0,height=0,duration=100,playback_origin_incarnation=?,playback_origin_revision=101,prepared_source_revision=103,file_size_revision=113,info_invalidation_revision=109 WHERE id=41`, incarnation[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(t.Context(), `DELETE FROM catalog_info_input_families WHERE change_id=51`); err != nil {
		t.Fatal(err)
	}
	counts := [9]int64{1, 0, 0, 1, 0, 1, 0, 0, 1}
	for n, count := range counts {
		sealed := int64(1)
		if n == 4 {
			sealed = 0
		} // a complete EMPTY family still needs its native seal
		if _, err := e.db.ExecContext(t.Context(), `INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected,rows_staged,sealed) VALUES(51,?,0,?,?,?)`, int64(n+1), count, count, sealed); err != nil {
			t.Fatal(err)
		}
	}
	tuple := basicProbeScalars{"mkv", "h264", "", 0, 0, 100}
	command, err := newBasicStageCommand(identity, basicStageRequest{original: original, purpose: BasicPurposeStage, operation: basicStagePinShapeInput,
		probe: basicProbeEffects{source: source, changeRevision: 1, assetPath: "/media/Arrival.mkv", before: tuple, after: tuple, playbackIncarnation: incarnation, playbackRevision: 101, preparedRevision: 103, fileSizeRevision: 113, invalidationRevision: 109}})
	if err != nil {
		t.Fatal(err)
	}
	turn := basicHandlerTurn(t, e, BasicStage, basicStageCommandDefinitions(command.request))
	result, err := applyBasicStage(t.Context(), turn, command)
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(result, BasicResult{}) {
		t.Fatal(result, err)
	}
	_ = turn.closeRows()
	_, _ = turn.transaction.rollback(err)
	var intact bool
	if e := e.db.QueryRowContext(t.Context(), `SELECT (SELECT target_shape_id IS NULL AND copy_family=0 AND job_revision=1 FROM catalog_info_changes WHERE id=51) AND (SELECT job_revision=41 AND last_command_sequence=18 FROM inventory_runs WHERE job_id='basic-run') AND (SELECT count(*)=0 FROM scan_command_receipts)`).Scan(&intact); e != nil || !intact {
		t.Fatal(intact, e)
	}
}
