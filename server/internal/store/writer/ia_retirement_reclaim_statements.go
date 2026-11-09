package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IA-10 retirement statement family. The driver and slot are one primary-key
// point; the receipt keyset is one PK range with no OFFSET or temporary sort;
// each fresh effect deletes at most one exact acknowledged header and writes
// the one CAS slot. No statement lists a receipt family or a catalogue.
const (
	iaRetirementReclaimOwnerPoint   stmt.ID = "ia.retirement.gc.owner"
	iaRetirementReclaimNextPoint    stmt.ID = "ia.retirement.gc.next"
	iaRetirementReclaimDelete       stmt.ID = "ia.retirement.gc.delete"
	iaRetirementReclaimAdvance      stmt.ID = "ia.retirement.gc.advance"
	iaRetirementReclaimAdvanceFirst stmt.ID = "ia.retirement.gc.advance_first"
)

func iaRetirementReclaimStatements() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	nb := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob, Nullable: true} }
	advanceParams := func(first bool) []stmt.Param {
		params := []stmt.Param{
			i("next_last"), i("next_ack"), i("next_reclaim"), i("next_rows"), i("next_bytes"), nt("next_due"),
			i("slot_sequence"), b("slot_operation"), b("slot_request"), t("slot_kind"), i("slot_version"),
			i("slot_captured"), i("slot_old_sequence"), nt("slot_removed_sequence"), t("slot_removed_kind"),
			i("slot_removed_count"), i("slot_removed_bytes"), i("slot_result_sequence"), i("slot_no_next"),
			i("slot_result_rows"), i("slot_result_bytes"), i("slot_applied"), i("slot_result_count"), b("slot_digest"),
			t("kind"), t("target"), b("incarnation"), i("old_last"), i("old_ack"), i("old_reclaim"),
			i("old_rows"), i("old_bytes"),
		}
		if !first {
			params = append(params, nb("old_slot_operation"), i("old_slot_sequence"))
		}
		return params
	}
	advanceSQL := `UPDATE catalog_removal_progress SET
last_command_sequence=?,acknowledged_command_sequence=?,receipt_reclaim_sequence=?,receipt_retained_count=?,receipt_retained_bytes=?,
receipt_reclaim_due_ms=?,
reclaim_command_sequence=?,reclaim_operation_id=?,reclaim_request_digest=?,reclaim_command_kind=?,reclaim_result_version=?,
reclaim_captured_acknowledged_sequence=?,reclaim_old_sequence=?,reclaim_removed_sequence=?,reclaim_removed_kind=?,
reclaim_removed_count=?,reclaim_removed_bytes=?,reclaim_result_sequence=?,reclaim_result_no_next=?,reclaim_result_rows=?,
reclaim_result_bytes=?,reclaim_applied_ms=?,reclaim_result_count=?,reclaim_result_digest=?
WHERE kind=? AND target=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=?
AND receipt_reclaim_sequence=? AND receipt_retained_count=? AND receipt_retained_bytes=?`
	advanceCohort := []any{int64(4), int64(4), int64(2), int64(1), int64(473), int64(90),
		int64(4), make([]byte, 16), make([]byte, 32), "retirement_receipt_reclaim", int64(1), int64(3), int64(1),
		int64(2), "header", int64(1), int64(472), int64(2), int64(0), int64(1), int64(473), int64(90), int64(1),
		make([]byte, 32), "library", "library", make([]byte, 16), int64(3), int64(3), int64(1), int64(2), int64(945)}
	defs := []stmt.Definition{
		{ID: iaRetirementReclaimOwnerPoint, Kind: stmt.Read, SQL: `SELECT original_operation_id,original_request_digest,command_incarnation,
original_library_id,original_library_key,original_resolved_root,original_source_incarnation,original_source_generation,original_root_identity,
last_command_sequence,acknowledged_command_sequence,receipt_reclaim_sequence,receipt_retained_count,receipt_retained_bytes,
retirement_state,phase,entity_kind,entity_cursor,object_cursor,walk_removed,walk_kept,receipt_reclaim_due_ms,
reclaim_command_sequence,reclaim_operation_id,reclaim_request_digest,reclaim_command_kind,reclaim_result_version,
reclaim_captured_acknowledged_sequence,reclaim_old_sequence,reclaim_removed_sequence,reclaim_removed_kind,reclaim_removed_count,
reclaim_removed_bytes,reclaim_result_sequence,reclaim_result_no_next,reclaim_result_rows,reclaim_result_bytes,reclaim_applied_ms,
reclaim_result_count,reclaim_result_digest
FROM catalog_removal_progress WHERE kind=? AND target=?`,
			Params:  []stmt.Param{t("kind"), t("target")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_progress"}, Seeks: []string{"kind=? AND target=?"}},
			Cohorts: []stmt.Cohort{{Name: "completed_retirement", Bindings: []any{"library", "library"}}}},
		{ID: iaRetirementReclaimNextPoint, Kind: stmt.Read, SQL: `SELECT command_sequence,retained_bytes
FROM catalog_removal_receipts WHERE original_operation_id=? AND command_incarnation=? AND result_kind=? AND result_target=? AND command_sequence>?
ORDER BY command_sequence LIMIT 1`,
			Params:  []stmt.Param{b("operation"), b("incarnation"), t("kind"), t("target"), i("after")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND result_kind=? AND result_target=? AND command_sequence>?"}},
			Cohorts: []stmt.Cohort{{Name: "after_reclaim_watermark", Bindings: []any{make([]byte, 16), make([]byte, 16), "library", "library", int64(1)}}}},
		{ID: iaRetirementReclaimDelete, Kind: stmt.Write, SQL: `DELETE FROM catalog_removal_receipts
WHERE original_operation_id=? AND command_incarnation=? AND result_kind=? AND result_target=? AND command_sequence=?
RETURNING retained_bytes`,
			Params:  []stmt.Param{b("operation"), b("incarnation"), t("kind"), t("target"), i("sequence")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND result_kind=? AND result_target=? AND command_sequence=?"}},
			Cohorts: []stmt.Cohort{{Name: "exact_acknowledged_header", Bindings: []any{make([]byte, 16), make([]byte, 16), "library", "library", int64(2)}}}},
		{ID: iaRetirementReclaimAdvance, Kind: stmt.Write, SQL: advanceSQL + `
AND reclaim_operation_id=? AND reclaim_command_sequence=?
RETURNING last_command_sequence`,
			Params:  advanceParams(false),
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_progress"}, Seeks: []string{"kind=? AND target=?"}},
			Cohorts: []stmt.Cohort{{Name: "overwrite_acknowledged_slot", Bindings: append(append([]any{}, advanceCohort...), make([]byte, 16), int64(3))}}},
		{ID: iaRetirementReclaimAdvanceFirst, Kind: stmt.Write, SQL: advanceSQL + `
AND reclaim_command_sequence IS NULL
RETURNING last_command_sequence`,
			Params:  advanceParams(true),
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_progress"}, Seeks: []string{"kind=? AND target=?"}},
			Cohorts: []stmt.Cohort{{Name: "first_acknowledged_turn", Bindings: advanceCohort}}},
	}
	for at := range defs {
		defs[at].Owner = "store/writer IA retirement receipt reclaim"
		defs[at].CallSites = []string{"store/writer/ia_retirement_reclaim.go"}
	}
	return defs
}
