package writer

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicFirstAcceptTestDefinitions() []stmt.Definition {
	groups := [][]stmt.Definition{basicControlCommandDefinitions(), basicActivationDefinitions(), basicSettleLeafDefinitions(), basicPublishLeafDefinitions(), basicObjectAnalysisDefinitions(), basicAcceptControlDefinitions(), basicFirstScalarOldDefinitions(), basicNFODefinitions(), basicArtworkDefinitions(), basicReceiptDefinitions(), basicReceiptMemberDefinitions()}
	var defs []stmt.Definition
	for _, group := range groups {
		for _, d := range group {
			found := false
			for _, old := range defs {
				if old.ID == d.ID {
					if !reflect.DeepEqual(old, d) {
						panic("different Basic literal definition")
					}
					found = true
					break
				}
			}
			if !found {
				defs = append(defs, d)
			}
		}
	}
	for _, d := range scanReceiptDefinitions() {
		switch d.ID {
		case scanResultInsert, scanReceiptOwner, scanReceiptAck, scanReceiptReclaimKey:
			defs = append(defs, d)
		}
	}
	return defs
}
func basicFirstAcceptCommandFixture(t *testing.T) (*executor, *basicAcceptCommand) {
	t.Helper()
	w, s, original, identity := basicControlFixture(t)
	seedBasicFirstOrdinaryOwner(t, w)
	for _, q := range []string{
		`INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(61,'mkv','h264',1080,1)`,
		`UPDATE catalog_assets SET container='mkv',video_codec='h264',height=1080,duration=100,info_publication_pending=1 WHERE id=41`,
		`UPDATE catalog_info_changes SET phase=3,family_mask=511,job_revision=13,target_shape_id=61,target_available=1,has_after=1,after_entity=31,upper_entity=31 WHERE id=51`,
		`UPDATE catalog_asset_links SET info_pending_change=51,part_index=2 WHERE entity_id=31 AND asset_id=41`,
		`UPDATE catalog_entities SET resolution_key=0,file_size_key=42,community_rating_key=7,critic_rating_key=8,scalar_source_revision=17,aggregate_revision=23 WHERE id=31`,
		`INSERT INTO catalog_scalar_contributions(library_id,parent_id,contributor_id,source,contributor_revision,membership_sequence,community_rating,critic_rating) VALUES(7,31,31,0,17,9,7,8)`,
		`INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,file_size,file_size_revision,authored_refs) VALUES(7,31,41,1,42,1,0)`,
		`INSERT INTO asset_stream_facts VALUES('basic-asset',31,42,-11,'probe-fingerprint',1)`,
		`INSERT INTO catalog_info_scalar_old VALUES(51,4,3,NULL,NULL,'old'),(51,5,3,NULL,NULL,'vp9'),(51,6,3,NULL,NULL,'aac'),(51,7,1,1920,NULL,NULL),(51,8,1,720,NULL,NULL),(51,9,2,NULL,90,NULL)`,
		`UPDATE catalog_info_input_budget SET used_bytes=2065 WHERE singleton=1`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	analysis, err := prepareBasicObjectAnalysis(s, "basic-library", "actual-probe-algorithm", `["probe","local_metadata","subtitles"]`, `["probe","local_metadata","subtitles"]`, false)
	if err != nil {
		t.Fatal(err)
	}
	nfo, err := prepareBasicNFOSet(s, "basic-library", nil, nil, nil, basicNFOStatus{status: "observed"}, true)
	if err != nil {
		t.Fatal(err)
	}
	command, err := newBasicAcceptCommand(identity, basicAcceptRequest{original: original, purpose: BasicPurposeAccept, operation: basicAcceptFirstOrdinaryLeaf, activation: basicActivationEffects{source: s, changeRevision: 13, familyMask: 511, shapeID: 61, invalidationRevision: 1, fileSizeRevision: 1}, entityID: 31, duration: 100, height: 1080, analysis: *analysis, nfo: nfo, oldScalars: []basicFirstOldScalar{
		{field: 4, kind: 3, text: sql.NullString{String: "old", Valid: true}},
		{field: 5, kind: 3, text: sql.NullString{String: "vp9", Valid: true}},
		{field: 6, kind: 3, text: sql.NullString{String: "aac", Valid: true}},
		{field: 7, kind: 1, integer: sql.NullInt64{Int64: 1920, Valid: true}},
		{field: 8, kind: 1, integer: sql.NullInt64{Int64: 720, Valid: true}},
		{field: 9, kind: 2, real: sql.NullFloat64{Float64: 90, Valid: true}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return w, command
}
func TestBasicFirstAcceptAtomicPublicationAndSavedHistory(t *testing.T) {
	w, command := basicFirstAcceptCommandFixture(t)
	defs := basicFirstAcceptTestDefinitions()
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_accept_handler_test.go", "basic_accept_command.go", "basic_accept_handler.go", "basic_accept_control.go")
	turn := basicHandlerTurn(t, w, BasicAccept, defs, registry)
	value, err := applyBasicAccept(t.Context(), turn, command)
	if err != nil || value.Outcome != (ReceiptOutcome{}) || value.AcceptedRunRevision != 42 || value.ExpectedEpoch != 17 || value.ResultCount != 1 || len(value.Members) != 1 || value.Members[0] != (InventoryMember{ObjectID: "basic-object", LibraryID: 7, SubjectID: 31, AssetID: 41, AcceptedAssetRevision: 18}) {
		t.Fatal(value, err)
	}
	basicHandlerCommit(t, turn, BasicPurposeAccept, command.request.original.run.leaseExpiresMS)
	var exact bool
	if err = w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT job_revision=42 AND analyzed=44 AND discovered=59 AND warnings=47 AND last_command_sequence=19 AND acknowledged_command_sequence=18 FROM inventory_runs WHERE job_id='basic-run') AND
 (SELECT processed=67 FROM jobs WHERE id='basic-run') AND
 (SELECT info_epoch=18 AND info_publication_pending=0 FROM catalog_assets WHERE id=41) AND
 (SELECT info_pending_change IS NULL AND info_shape_id=61 AND info_duration=100 AND part_index=2 FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41) AND
 (SELECT analysis_revision='object:019' AND analysis_policy=29 AND analysis_state='complete' AND analysis_algorithm='actual-probe-algorithm' AND analysis_generation=23 FROM inventory_objects WHERE id='basic-object') AND
 (SELECT phase=5 AND job_revision=15 FROM catalog_info_changes WHERE id=51) AND
 (SELECT accepted_revision=42 AND expected_epoch=17 AND result_count=1 FROM scan_command_receipts WHERE run_id='basic-run') AND
 (SELECT accepted_asset_revision=18 FROM scan_command_results WHERE run_id='basic-run') AND
 (SELECT count(*)=0 FROM catalog_info_scalar_old WHERE change_id=51) AND
 (SELECT used_bytes=1000 FROM catalog_info_input_budget WHERE singleton=1)`).Scan(&exact); err != nil || !exact {
		t.Fatal(exact, err)
	}
	q, err := command.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	// Saved history must not reconstruct from changed current source/job rows.
	for _, query := range []string{`UPDATE inventory_runs SET job_revision=9223372036854775807,job_lease_expires_ms=1 WHERE job_id='basic-run'`, `DELETE FROM inventory_objects WHERE id='basic-object'`} {
		if _, err = w.db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	turn = basicHandlerTurn(t, w, BasicAccept, defs, registry)
	lookup, err := lookupBasic(t.Context(), turn, q)
	if err != nil || lookup.decision != receiptReplay || !reflect.DeepEqual(lookup.result, value) {
		t.Fatal(lookup, value, err)
	}
	if ack, err := acknowledgeBasic(t.Context(), turn, q, lookup); err != nil || ack != 19 {
		t.Fatal(ack, err)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}
func TestBasicFirstAcceptLateFailureRollsBackEntirePublication(t *testing.T) {
	for _, missing := range []stmt.ID{basicAcceptRunCAS, scanResultInsert} {
		t.Run(string(missing), func(t *testing.T) {
			w, command := basicFirstAcceptCommandFixture(t)
			defs := basicFirstAcceptTestDefinitions()
			turn := basicHandlerTurn(t, w, BasicAccept, defs)
			delete(turn.steps, missing)
			value, err := applyBasicAccept(t.Context(), turn, command)
			if err == nil || !reflect.DeepEqual(value, BasicResult{}) {
				t.Fatal(value, err)
			}
			_ = turn.closeRows()
			if _, e := turn.transaction.rollback(err); !errors.Is(e, err) {
				t.Fatal(e)
			}
			var exact bool
			if err = w.db.QueryRowContext(t.Context(), `SELECT
  (SELECT info_epoch=17 AND info_publication_pending=1 FROM catalog_assets WHERE id=41) AND
  (SELECT info_pending_change=51 AND info_shape_id IS NULL AND info_duration=0 FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41) AND
  (SELECT phase=3 AND job_revision=13 FROM catalog_info_changes WHERE id=51) AND
  (SELECT analysis_policy=0 AND analysis_revision='' AND analysis_algorithm='' FROM inventory_objects WHERE id='basic-object') AND
  (SELECT job_revision=41 AND analyzed=43 AND last_command_sequence=18 FROM inventory_runs WHERE job_id='basic-run') AND
  (SELECT count(*)=6 FROM catalog_info_scalar_old WHERE change_id=51) AND
 (SELECT used_bytes=2065 FROM catalog_info_input_budget WHERE singleton=1) AND
 (SELECT count(*)=0 FROM video_nfo_status) AND
 (SELECT count(*)=0 FROM scan_command_receipts) AND (SELECT count(*)=0 FROM scan_command_results)`).Scan(&exact); err != nil || !exact {
				t.Fatal(exact, err)
			}
		})
	}
}
