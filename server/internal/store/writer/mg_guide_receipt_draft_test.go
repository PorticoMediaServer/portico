package writer

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// Exact empty-table overlay from owner draft candidate SHA256
// 4e0751989357d1aed85467ba81f5c24ce79aa3a26f947e0fd222070891bb9262.
// Native guide fixture owns the existing operation/generation declarations.
// No wholesale schema copy or product/canonical DDL mutation.
const mgGuideReceiptDraftSQL = `CREATE TABLE live_operation_command_receipts(
 request_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 operation_id BLOB NOT NULL CHECK(length(operation_id)=16),
 request_digest BLOB NOT NULL CHECK(length(request_digest)=32),
 result_digest BLOB NOT NULL CHECK(length(result_digest)=32),
 applied_ms INTEGER NOT NULL CHECK(applied_ms>=0),
 command_kind TEXT NOT NULL CHECK(command_kind IN('begin','channels','programmes','choices','seal','publish','discard','reclaim')),
 source_id TEXT NOT NULL REFERENCES live_source_identities(id),
 generation_id TEXT NOT NULL,
 accepted_revision INTEGER NOT NULL CHECK(accepted_revision>0),
 accepted_acknowledged_sequence INTEGER NOT NULL CHECK(accepted_acknowledged_sequence>=0 AND accepted_acknowledged_sequence<command_sequence),
 result_channel_count INTEGER NOT NULL CHECK(result_channel_count>=0),
 result_programme_count INTEGER NOT NULL CHECK(result_programme_count>=0),
 result_choice_count INTEGER NOT NULL CHECK(result_choice_count>=0),
 result_staged_bytes INTEGER NOT NULL CHECK(result_staged_bytes>=0),
 result_stage_digest BLOB NOT NULL CHECK(length(result_stage_digest)=32),
 result_sealed INTEGER NOT NULL CHECK(result_sealed IN(0,1)),
 result_source_name TEXT NOT NULL,
 result_tuner_count INTEGER NOT NULL CHECK(result_tuner_count BETWEEN 0 AND 256),
 result_capacity_known INTEGER NOT NULL CHECK(result_capacity_known IN(0,1)),
 result_planning_estimate INTEGER NOT NULL CHECK(result_planning_estimate>=1),
 result_tuner_count_mode TEXT NOT NULL CHECK(result_tuner_count_mode IN('','configured','defaulted','discovered')),
 result_delivery_validation TEXT NOT NULL,
 result_acquisition TEXT NOT NULL CHECK(result_acquisition IN('','upload','remote')),
 result_skipped_channels INTEGER NOT NULL CHECK(result_skipped_channels>=0),
 result_skipped_programmes INTEGER NOT NULL CHECK(result_skipped_programmes>=0),
 result_adjusted_programmes INTEGER NOT NULL CHECK(result_adjusted_programmes>=0),
 result_diagnostic_example_count INTEGER NOT NULL CHECK(result_diagnostic_example_count BETWEEN 0 AND 10),
 result_source_revision INTEGER NOT NULL CHECK(result_source_revision>=0),
 result_source_state TEXT NOT NULL CHECK(result_source_state IN('','active','disabled')),
 result_published_at TEXT NOT NULL DEFAULT '',
 result_reclaim_family TEXT NOT NULL CHECK(result_reclaim_family IN('none','programmes','channels','choices','generation','complete')),
 result_reclaim_cursor TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(request_id,command_incarnation,command_sequence),
 UNIQUE(request_id,command_incarnation,operation_id),
 CONSTRAINT live_receipt_publish CHECK(command_kind<>'publish' OR (result_sealed=1 AND result_channel_count>0 AND result_source_revision>0 AND result_source_state<>'' AND result_published_at<>'')),
 CHECK(typeof("request_id") IN('null','text')),
 CHECK(typeof("command_incarnation") IN('null','blob')),
 CHECK(typeof("command_sequence") IN('null','integer')),
 CHECK(typeof("operation_id") IN('null','blob')),
 CHECK(typeof("request_digest") IN('null','blob')),
 CHECK(typeof("result_digest") IN('null','blob')),
 CHECK(typeof("applied_ms") IN('null','integer')),
 CHECK(typeof("command_kind") IN('null','text')),
 CHECK(typeof("source_id") IN('null','text')),
 CHECK(typeof("generation_id") IN('null','text')),
 CHECK(typeof("accepted_revision") IN('null','integer')),
 CHECK(typeof("accepted_acknowledged_sequence") IN('null','integer')),
 CHECK(typeof("result_channel_count") IN('null','integer')),
 CHECK(typeof("result_programme_count") IN('null','integer')),
 CHECK(typeof("result_choice_count") IN('null','integer')),
 CHECK(typeof("result_staged_bytes") IN('null','integer')),
 CHECK(typeof("result_stage_digest") IN('null','blob')),
 CHECK(typeof("result_sealed") IN('null','integer')),
 CHECK(typeof("result_source_name") IN('null','text')),
 CHECK(typeof("result_tuner_count") IN('null','integer')),
 CHECK(typeof("result_capacity_known") IN('null','integer')),
 CHECK(typeof("result_planning_estimate") IN('null','integer')),
 CHECK(typeof("result_tuner_count_mode") IN('null','text')),
 CHECK(typeof("result_delivery_validation") IN('null','text')),
 CHECK(typeof("result_acquisition") IN('null','text')),
 CHECK(typeof("result_skipped_channels") IN('null','integer')),
 CHECK(typeof("result_skipped_programmes") IN('null','integer')),
 CHECK(typeof("result_adjusted_programmes") IN('null','integer')),
 CHECK(typeof("result_diagnostic_example_count") IN('null','integer')),
 CHECK(typeof("result_source_revision") IN('null','integer')),
 CHECK(typeof("result_source_state") IN('null','text')),
 CHECK(typeof("result_published_at") IN('null','text')),
 CHECK(typeof("result_reclaim_family") IN('null','text')),
 CHECK(typeof("result_reclaim_cursor") IN('null','text'))
) WITHOUT ROWID;

CREATE TABLE live_operation_receipt_diagnostics(
 request_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 9),
 message TEXT NOT NULL,
 PRIMARY KEY(request_id,command_incarnation,command_sequence,ordinal),
 FOREIGN KEY(request_id,command_incarnation,command_sequence) REFERENCES live_operation_command_receipts(request_id,command_incarnation,command_sequence) ON DELETE CASCADE,
 CHECK(typeof("request_id") IN('null','text')),
 CHECK(typeof("command_incarnation") IN('null','blob')),
 CHECK(typeof("command_sequence") IN('null','integer')),
 CHECK(typeof("ordinal") IN('null','integer')),
 CHECK(typeof("message") IN('null','text'))
) WITHOUT ROWID;

CREATE TABLE live_operation_retirements(
 request_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 create_operation_id BLOB NOT NULL CHECK(length(create_operation_id)=16),
 birth_request_digest BLOB NOT NULL CHECK(length(birth_request_digest)=32),
 source_id TEXT NOT NULL REFERENCES live_source_identities(id),
 final_sequence INTEGER NOT NULL CHECK(final_sequence>0),
 final_result_digest BLOB NOT NULL CHECK(length(final_result_digest)=32),
 retired_ms INTEGER NOT NULL CHECK(retired_ms>=0),
 PRIMARY KEY(request_id),
 CHECK(typeof("request_id") IN('null','text')),
 CHECK(typeof("command_incarnation") IN('null','blob')),
 CHECK(typeof("create_operation_id") IN('null','blob')),
 CHECK(typeof("birth_request_digest") IN('null','blob')),
 CHECK(typeof("source_id") IN('null','text')),
 CHECK(typeof("final_sequence") IN('null','integer')),
 CHECK(typeof("final_result_digest") IN('null','blob')),
 CHECK(typeof("retired_ms") IN('null','integer'))
) WITHOUT ROWID;

CREATE INDEX live_operation_command_receipts_scope ON live_operation_command_receipts(source_id,request_id);

CREATE INDEX live_operation_retirements_scope ON live_operation_retirements(source_id,request_id);`

func mgGuideReceiptFixture(t *testing.T, sealed bool) (*executor, mgGuideNative) {
	t.Helper()
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(mgGuideReceiptDraftSQL))); got != "287c898fd9bacd6dbb07c5ad238fdb545421a269c85085030d01f764ef334f26" {
		t.Fatal("pinned guide receipt overlay changed", got)
	}
	w, before := mgGuideNativeFixture(t, sealed, "active")
	if _, err := w.db.ExecContext(t.Context(), mgGuideReceiptDraftSQL); err != nil {
		t.Fatal(err)
	}
	var fk int
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatal("FK enforcement disabled", fk, err)
	}
	return w, before
}
