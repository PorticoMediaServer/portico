package writer

import (
	"reflect"
	"testing"
)

func TestBasicHandlerProbeSavesOneNativeCoherentReceipt(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "applied_body", true: "late_receipt_rollback"}[late], func(t *testing.T) {
			e, source, original, identity := basicControlFixture(t)
			incarnation := [16]byte{0x41}
			if _, err := e.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET family_mask=511 WHERE id=51`); err != nil {
				t.Fatal(err)
			}
			if _, err := e.db.ExecContext(t.Context(), `UPDATE catalog_assets SET container='old',video_codec='vp9',audio_codec='aac',width=1920,height=1080,duration=90,playback_origin_incarnation=?,playback_origin_revision=101,prepared_source_revision=103,file_size_revision=113,info_invalidation_revision=109 WHERE id=41`, incarnation[:]); err != nil {
				t.Fatal(err)
			}
			command, err := newBasicStageCommand(identity, basicStageRequest{original: original, purpose: BasicPurposeStage, operation: basicStageProbeInput,
				probe: basicProbeEffects{source: source, changeRevision: 1, assetPath: "/media/Arrival.mkv", before: basicProbeScalars{"old", "vp9", "aac", 1920, 1080, 90}, after: basicProbeScalars{"mkv", "h264", "", 0, 0, 100}, playbackIncarnation: incarnation, playbackRevision: 101, preparedRevision: 103, fileSizeRevision: 113, invalidationRevision: 109}})
			if err != nil {
				t.Fatal(err)
			}
			defs := basicStageCommandDefinitions(command.request)
			registry := nativeInventoryFixtureRegistry(t, e, defs, "basic_probe_handler_test.go", "basic_handlers_test.go", "basic_control_test.go", "basic_stage_test.go")
			turn := basicHandlerTurn(t, e, BasicStage, defs, registry)
			if late {
				delete(turn.steps, basicControlReceipt)
			}
			result, err := applyBasicStage(t.Context(), turn, command)
			if late {
				if err == nil || !reflect.DeepEqual(result, BasicResult{}) {
					t.Fatal(result, err)
				}
				var reached bool
				if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT container='mkv' AND playback_origin_revision=102 AND prepared_source_revision=104 FROM catalog_assets WHERE id=41`).Scan(&reached); e != nil || !reached {
					t.Fatal("did not reach intended late failure", reached, e)
				}
				_ = turn.closeRows()
				_, _ = turn.transaction.rollback(err)
				var restored bool
				if e := e.db.QueryRowContext(t.Context(), `SELECT (SELECT container='old' AND info_epoch=17 AND playback_origin_revision=101 AND prepared_source_revision=103 FROM catalog_assets WHERE id=41) AND (SELECT count(*)=0 FROM catalog_info_scalar_old) AND (SELECT job_revision=41 AND last_command_sequence=18 FROM inventory_runs WHERE job_id='basic-run') AND (SELECT count(*)=0 FROM scan_command_receipts)`).Scan(&restored); e != nil || !restored {
					t.Fatal(restored, e)
				}
				return
			}
			if err != nil || result.Outcome != (ReceiptOutcome{}) || result.AcceptedRunRevision != 42 || result.ExpectedEpoch != 17 || result.ChangeID != 51 || result.SourceRevision != "object:019" || result.Continuation != (BasicContinuation{}) {
				t.Fatal(result, err)
			}
			var exact bool
			if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT container='mkv' AND video_codec='h264' AND audio_codec='' AND width=0 AND height=0 AND duration=100 AND playback_origin_revision=102 AND prepared_source_revision=104 AND file_size_revision=113 AND info_invalidation_revision=109 AND info_epoch=17 AND info_publication_pending=0 FROM catalog_assets WHERE id=41) AND (SELECT count(*)=6 FROM catalog_info_scalar_old WHERE change_id=51) AND (SELECT accepted_revision=42 AND expected_epoch=17 AND source_revision='object:019' AND result_digest=? FROM scan_command_receipts WHERE run_id='basic-run' AND command_sequence=19)`, result.Digest[:]).Scan(&exact); e != nil || !exact {
				t.Fatal(exact, e)
			}
			basicHandlerCommit(t, turn, BasicPurposeStage, original.run.leaseExpiresMS)
		})
	}
}
