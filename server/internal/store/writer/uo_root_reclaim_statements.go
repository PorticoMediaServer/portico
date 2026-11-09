package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	uoRootAckPoint      stmt.ID = "uo.root.ack.point"
	uoRootAckNull       stmt.ID = "uo.root.ack.null"
	uoRootOldSlotOctets stmt.ID = "uo.root.reclaim.old_slot_octets"
	uoRootOldSlotPoint  stmt.ID = "uo.root.reclaim.old_slot_point"
	uoRootChildNext     stmt.ID = "uo.root.reclaim.child_next"
	uoRootChildDelete   stmt.ID = "uo.root.reclaim.child_delete"
	uoRootReclaimSave   stmt.ID = "uo.root.reclaim.save"
	uoRootReclaimDue    stmt.ID = "uo.root.reclaim.due"
)

// Every statement is a keyed native access path: the owner PK, the receipt PK
// range, the partial reclaim_due index or the receipts PK delete. No statement
// scans a table, sorts with a temporary B-tree or enumerates a whole graph.
func uoRootAckDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	ownerIndex := "sqlite_autoindex_user_mutation_command_owners_2"
	base := []stmt.Param{i("floor"), ni("due"), i("next_revision"), s("root"), b("incarnation"), i("last"), i("old_floor"), i("old_revision"), i("rows"), i("bytes"), i("cursor")}
	ack := func(id stmt.ID, due string, params []stmt.Param, bindings []any) stmt.Definition {
		return stmt.Definition{
			ID: id, Kind: stmt.Write, Owner: "store/writer UO root ACK",
			CallSites: []string{"store/writer/uo_root_ack.go"},
			SQL:       `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=?,receipt_reclaim_due_ms=?,revision=? WHERE root_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND revision=? AND receipt_rows=? AND receipt_variable_bytes=? AND receipt_reclaim_after_sequence=? AND ` + due + ` RETURNING acknowledged_command_sequence`,
			Params:    params,
			Plan:      stmt.Plan{Hot: true, Indexes: []string{ownerIndex}, Seeks: []string{"root_id=?"}},
			Cohorts:   []stmt.Cohort{{Name: "ack", Bindings: bindings}},
		}
	}
	// Closed non-NULL/NULL variants: the DML parser accepts IS NULL, never a
	// nullable `IS ?` parameter.
	nonNull := append(append([]stmt.Param{}, base...), ni("old_due"))
	nullParams := append([]stmt.Param{}, base...)
	nonNullBindings := []any{int64(1), nil, int64(2), "abcdefghijklmnopqrstuv", make([]byte, 16), int64(1), int64(0), int64(1), int64(1), int64(160), int64(0), int64(100)}
	nullBindings := []any{int64(1), nil, int64(2), "abcdefghijklmnopqrstuv", make([]byte, 16), int64(1), int64(0), int64(1), int64(1), int64(160), int64(0)}
	return []stmt.Definition{ack(uoRootAckPoint, `receipt_reclaim_due_ms=?`, nonNull, nonNullBindings), ack(uoRootAckNull, `receipt_reclaim_due_ms IS NULL`, nullParams, nullBindings)}
}

func uoRootReclaimDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	d := func(id stmt.ID, kind stmt.Kind, sql string, plan stmt.Plan, cohort stmt.Cohort, p ...stmt.Param) stmt.Definition {
		return stmt.Definition{ID: id, Kind: kind, SQL: sql, Params: p, Owner: "store/writer UO root reclaim",
			CallSites: []string{"store/writer/uo_root_reclaim.go"}, Plan: plan, Cohorts: []stmt.Cohort{cohort}}
	}
	ownerIndex := "sqlite_autoindex_user_mutation_command_owners_2"
	receipts := "user_mutation_command_receipts"
	return []stmt.Definition{
		d(uoRootOldSlotOctets, stmt.Read, `SELECT coalesce(reclaim_command_sequence,0),coalesce(length(reclaim_operation_id),0),coalesce(length(reclaim_request_digest),0),coalesce(length(reclaim_result_digest),0),coalesce(length(reclaim_result_payload),0),control_variable_bytes FROM user_mutation_command_owners WHERE root_id=? AND command_incarnation=?`,
			stmt.Plan{Hot: true, Indexes: []string{ownerIndex}, Seeks: []string{"root_id=? AND command_incarnation=?"}},
			stmt.Cohort{Name: "old_octets", Bindings: []any{"abcdefghijklmnopqrstuv", make([]byte, 16)}}, s("root"), b("incarnation")),
		d(uoRootOldSlotPoint, stmt.Read, `SELECT reclaim_operation_id,reclaim_request_digest,reclaim_result_digest,reclaim_result_payload,reclaim_command_kind,reclaim_result_version,reclaim_result_count,reclaim_captured_acknowledged_sequence,reclaim_old_after_sequence,coalesce(reclaim_removed_sequence,0),reclaim_removed_variable_bytes,reclaim_result_after_sequence,reclaim_result_no_next,reclaim_applied_ms FROM user_mutation_command_owners WHERE root_id=? AND command_incarnation=? AND reclaim_command_sequence=? AND length(reclaim_result_payload)=? AND length(reclaim_operation_id)=16 AND length(reclaim_request_digest)=32 AND length(reclaim_result_digest)=32`,
			stmt.Plan{Hot: true, Indexes: []string{ownerIndex}, Seeks: []string{"root_id=? AND command_incarnation=?"}},
			stmt.Cohort{Name: "old_point", Bindings: []any{"abcdefghijklmnopqrstuv", make([]byte, 16), int64(1), int64(160)}},
			s("root"), b("incarnation"), i("sequence"), i("payload_bytes")),
		d(uoRootChildNext, stmt.Read, `SELECT command_sequence,variable_bytes FROM user_mutation_command_receipts WHERE root_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`,
			stmt.Plan{Hot: true, PrimaryKeys: []string{receipts}, Seeks: []string{"root_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<?"}},
			stmt.Cohort{Name: "child_next", Bindings: []any{"abcdefghijklmnopqrstuv", make([]byte, 16), int64(0), int64(4)}},
			s("root"), b("incarnation"), i("after"), i("through_ack")),
		// The child delete repeats the eligibility range so a row that moved
		// above the ACK floor between probe and delete cannot be removed, and a
		// live successor sorting after the eligible range is never a victim.
		d(uoRootChildDelete, stmt.Write, `DELETE FROM user_mutation_command_receipts WHERE root_id=? AND command_incarnation=? AND command_sequence=? AND command_sequence>? AND command_sequence<=? AND variable_bytes=? RETURNING variable_bytes`,
			stmt.Plan{Hot: true, PrimaryKeys: []string{receipts}, Seeks: []string{"root_id=? AND command_incarnation=? AND command_sequence=?"}},
			stmt.Cohort{Name: "child_delete", Bindings: []any{"abcdefghijklmnopqrstuv", make([]byte, 16), int64(1), int64(0), int64(4), int64(160)}},
			s("root"), b("incarnation"), i("sequence"), i("after"), i("through_ack"), i("bytes")),
		d(uoRootReclaimSave, stmt.Write, `UPDATE user_mutation_command_owners SET last_command_sequence=?,receipt_rows=?,receipt_variable_bytes=?,receipt_reclaim_after_sequence=?,receipt_reclaim_due_ms=?,revision=?,reclaim_command_sequence=?,reclaim_operation_id=?,reclaim_request_digest=?,reclaim_command_kind=?,reclaim_result_version=1,reclaim_result_count=?,reclaim_result_digest=?,reclaim_result_payload=?,reclaim_captured_acknowledged_sequence=?,reclaim_old_after_sequence=?,reclaim_removed_sequence=?,reclaim_removed_variable_bytes=?,reclaim_result_after_sequence=?,reclaim_result_no_next=?,reclaim_applied_ms=? WHERE root_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND revision=? AND receipt_rows=? AND receipt_variable_bytes=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms=? AND coalesce(reclaim_command_sequence,0)=? AND control_variable_bytes=? RETURNING last_command_sequence`,
			stmt.Plan{Hot: true, Indexes: []string{ownerIndex}, Seeks: []string{"root_id=? AND command_incarnation=?"}},
			stmt.Cohort{Name: "reclaim_save", Bindings: []any{int64(2), int64(1), int64(160), int64(1), nil, int64(2), int64(2), make([]byte, 16), make([]byte, 32), int64(654), int64(0), make([]byte, 32), []byte{1}, int64(1), int64(0), nil, int64(0), int64(1), int64(0), int64(100), "abcdefghijklmnopqrstuv", make([]byte, 16), int64(1), int64(1), int64(1), int64(1), int64(160), int64(0), int64(100), int64(0), int64(38)}},
			i("next_sequence"), i("next_rows"), i("next_bytes"), i("next_cursor"), ni("next_due"), i("next_revision"), i("slot_sequence"), b("operation"), b("request"), i("kind"), i("removed_count"), b("result_digest"), b("payload"), i("captured_floor"), i("old_cursor"), ni("removed_sequence"), i("removed_bytes"), i("result_cursor"), i("no_next"), i("applied_ms"), s("root"), b("incarnation"), i("old_sequence"), i("old_floor"), i("old_revision"), i("old_rows"), i("old_bytes"), i("old_after"), i("old_due"), i("old_slot_sequence"), i("old_control_bytes")),
		// One bounded candidate probe over the partial due index. The caller
		// runs at most one query per quantum and never enumerates owners.
		d(uoRootReclaimDue, stmt.Read, `SELECT root_id,receipt_reclaim_due_ms FROM user_mutation_command_owners INDEXED BY user_mutation_command_reclaim_due WHERE receipt_reclaim_due_ms IS NOT NULL AND receipt_reclaim_due_ms<=? ORDER BY receipt_reclaim_due_ms,root_id LIMIT 1`,
			stmt.Plan{Hot: true, Indexes: []string{"user_mutation_command_reclaim_due"}, Seeks: []string{"receipt_reclaim_due_ms<?"}},
			stmt.Cohort{Name: "due", Bindings: []any{int64(100)}}, i("now")),
	}
}
