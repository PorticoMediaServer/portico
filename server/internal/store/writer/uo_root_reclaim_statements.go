package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	uoRootOldSlotOctets stmt.ID = "uo.root.reclaim.old_slot_octets"
	uoRootOldSlotPoint  stmt.ID = "uo.root.reclaim.old_slot_point"
	uoRootChildNext     stmt.ID = "uo.root.reclaim.child_next"
	uoRootChildDelete   stmt.ID = "uo.root.reclaim.child_delete"
	uoRootReclaimSave   stmt.ID = "uo.root.reclaim.save"
)

func uoRootReclaimDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	d := func(id stmt.ID, kind stmt.Kind, sql string, p ...stmt.Param) stmt.Definition {
		return stmt.Definition{ID: id, Kind: kind, SQL: sql, Params: p, Owner: "store/writer UO root reclaim", CallSites: []string{"store/writer/uo_root_reclaim.go"}, Plan: stmt.Plan{Hot: true}}
	}
	return []stmt.Definition{
		d(uoRootOldSlotOctets, stmt.Read, `SELECT coalesce(reclaim_command_sequence,0),coalesce(octet_length(reclaim_operation_id),0),coalesce(octet_length(reclaim_request_digest),0),coalesce(octet_length(reclaim_result_digest),0),coalesce(octet_length(reclaim_result_payload),0),control_variable_bytes FROM user_mutation_command_owners WHERE root_id=? AND command_incarnation=?`, s("root"), b("incarnation")),
		d(uoRootOldSlotPoint, stmt.Read, `SELECT reclaim_operation_id,reclaim_request_digest,reclaim_result_digest,reclaim_result_payload,reclaim_command_kind,reclaim_result_version,reclaim_result_count,reclaim_captured_acknowledged_sequence,reclaim_old_after_sequence,coalesce(reclaim_removed_sequence,0),reclaim_removed_variable_bytes,reclaim_result_after_sequence,reclaim_result_no_next,reclaim_applied_ms FROM user_mutation_command_owners WHERE root_id=? AND command_incarnation=? AND reclaim_command_sequence=? AND octet_length(reclaim_result_payload)=? AND octet_length(reclaim_operation_id)=16 AND octet_length(reclaim_request_digest)=32 AND octet_length(reclaim_result_digest)=32`, s("root"), b("incarnation"), i("sequence"), i("payload_bytes")),
		d(uoRootChildNext, stmt.Read, `SELECT command_sequence,variable_bytes FROM user_mutation_command_receipts WHERE root_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`, s("root"), b("incarnation"), i("after"), i("through_ack")),
		d(uoRootChildDelete, stmt.Write, `DELETE FROM user_mutation_command_receipts WHERE root_id=? AND command_incarnation=? AND command_sequence=? AND variable_bytes=? RETURNING variable_bytes`, s("root"), b("incarnation"), i("sequence"), i("bytes")),
		d(uoRootReclaimSave, stmt.Write, `UPDATE user_mutation_command_owners SET last_command_sequence=?,receipt_rows=?,receipt_variable_bytes=?,receipt_reclaim_after_sequence=?,receipt_reclaim_due_ms=?,revision=?,reclaim_command_sequence=?,reclaim_operation_id=?,reclaim_request_digest=?,reclaim_command_kind=?,reclaim_result_version=1,reclaim_result_count=?,reclaim_result_digest=?,reclaim_result_payload=?,reclaim_captured_acknowledged_sequence=?,reclaim_old_after_sequence=?,reclaim_removed_sequence=?,reclaim_removed_variable_bytes=?,reclaim_result_after_sequence=?,reclaim_result_no_next=?,reclaim_applied_ms=? WHERE root_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND revision=? AND receipt_rows=? AND receipt_variable_bytes=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms IS ? AND coalesce(reclaim_command_sequence,0)=? AND control_variable_bytes=? RETURNING last_command_sequence`, i("next_sequence"), i("next_rows"), i("next_bytes"), i("next_cursor"), ni("next_due"), i("next_revision"), i("slot_sequence"), b("operation"), b("request"), i("kind"), i("removed_count"), b("result_digest"), b("payload"), i("captured_floor"), i("old_cursor"), ni("removed_sequence"), i("removed_bytes"), i("result_cursor"), i("no_next"), i("applied_ms"), s("root"), b("incarnation"), i("old_sequence"), i("old_floor"), i("old_revision"), i("old_rows"), i("old_bytes"), i("old_after"), ni("old_due"), i("old_slot_sequence"), i("old_control_bytes")),
	}
}
