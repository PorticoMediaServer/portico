package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IA-10 configuration statement family. The owner read is one primary-key
// point; the two children are closed keysets with no OFFSET or temporary sort;
// each fresh effect deletes at most one exact child/header row and writes the
// one CAS slot. No statement lists a library or scans a receipt family.
const (
	iaConfigurationReclaimOwnerPoint   stmt.ID = "ia.configuration.gc.owner"
	iaConfigurationReclaimHeaderPoint  stmt.ID = "ia.configuration.gc.header"
	iaConfigurationReclaimSourcesPoint stmt.ID = "ia.configuration.gc.sources"
	iaConfigurationReclaimSourceDelete stmt.ID = "ia.configuration.gc.source_delete"
	iaConfigurationReclaimHeaderDelete stmt.ID = "ia.configuration.gc.header_delete"
	iaConfigurationReclaimAdvance      stmt.ID = "ia.configuration.gc.advance"
	iaConfigurationReclaimAdvanceFirst stmt.ID = "ia.configuration.gc.advance_first"
)

func iaConfigurationReclaimStatements() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	nb := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob, Nullable: true} }
	advanceParams := func(first bool) []stmt.Param {
		params := []stmt.Param{
			i("next_last"), i("next_ack"), i("next_reclaim"), i("next_rows"), i("next_bytes"), nt("next_due"), i("next_ordinal"),
			i("slot_sequence"), b("slot_operation"), b("slot_request"), t("slot_kind"), i("slot_version"),
			i("slot_captured"), i("slot_old_sequence"), nt("slot_removed_sequence"), t("slot_removed_kind"),
			i("slot_removed_count"), i("slot_removed_bytes"), i("slot_result_sequence"), i("slot_no_next"),
			i("slot_result_rows"), i("slot_result_bytes"), i("slot_applied"), i("slot_result_count"), b("slot_digest"),
			nt("slot_old_ordinal"), nt("slot_removed_ordinal"), nt("slot_result_ordinal"),
			b("operation"), b("incarnation"), i("old_last"), i("old_ack"), i("old_reclaim"),
			i("old_rows"), i("old_bytes"), i("old_ordinal"),
		}
		if !first {
			params = append(params, nb("old_slot_operation"), i("old_slot_sequence"))
		}
		return params
	}
	advanceSQL := `UPDATE catalog_configuration_operations SET
last_command_sequence=?,acknowledged_command_sequence=?,receipt_reclaim_sequence=?,receipt_retained_count=?,receipt_retained_bytes=?,
receipt_reclaim_due_ms=?,receipt_reclaim_ordinal=?,
reclaim_command_sequence=?,reclaim_operation_id=?,reclaim_request_digest=?,reclaim_command_kind=?,reclaim_result_version=?,
reclaim_captured_acknowledged_sequence=?,reclaim_old_sequence=?,reclaim_removed_sequence=?,reclaim_removed_kind=?,
reclaim_removed_count=?,reclaim_removed_bytes=?,reclaim_result_sequence=?,reclaim_result_no_next=?,reclaim_result_rows=?,
reclaim_result_bytes=?,reclaim_applied_ms=?,reclaim_result_count=?,reclaim_result_digest=?,
reclaim_old_ordinal=?,reclaim_removed_ordinal=?,reclaim_result_ordinal=?
WHERE original_operation_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=?
AND receipt_reclaim_sequence=? AND receipt_retained_count=? AND receipt_retained_bytes=? AND receipt_reclaim_ordinal=?`
	advanceCohort := []any{int64(2), int64(2), int64(0), int64(2), int64(420), int64(100), int64(0),
		int64(2), make([]byte, 16), make([]byte, 32), "configuration_receipt_reclaim", int64(1), int64(1), int64(0),
		int64(1), "source", int64(1), int64(220), int64(0), int64(0), int64(1), int64(200), int64(100), int64(1),
		make([]byte, 32), int64(-1), int64(0), int64(0),
		make([]byte, 16), make([]byte, 16), int64(1), int64(1), int64(0), int64(3), int64(660), int64(-1)}
	defs := []stmt.Definition{
		{ID: iaConfigurationReclaimOwnerPoint, Kind: stmt.Read, SQL: `SELECT original_request_digest,command_kind,original_library_id,original_library_key,original_source_id,command_incarnation,
last_command_sequence,acknowledged_command_sequence,receipt_reclaim_sequence,receipt_retained_count,receipt_retained_bytes,
receipt_reclaim_due_ms,receipt_reclaim_ordinal,
reclaim_command_sequence,reclaim_operation_id,reclaim_request_digest,reclaim_command_kind,reclaim_result_version,
reclaim_captured_acknowledged_sequence,reclaim_old_sequence,reclaim_removed_sequence,reclaim_removed_kind,reclaim_removed_count,
reclaim_removed_bytes,reclaim_result_sequence,reclaim_result_no_next,reclaim_result_rows,reclaim_result_bytes,reclaim_applied_ms,
reclaim_result_count,reclaim_result_digest,reclaim_old_ordinal,reclaim_removed_ordinal,reclaim_result_ordinal
FROM catalog_configuration_operations WHERE original_operation_id=?`,
			Params:  []stmt.Param{b("operation")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_operations"}, Seeks: []string{"original_operation_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "configuration_owner", Bindings: []any{make([]byte, 16)}}}},
		{ID: iaConfigurationReclaimHeaderPoint, Kind: stmt.Read, SQL: `SELECT retained_bytes,result_count,result_library_name,result_library_kind
FROM catalog_configuration_receipts WHERE original_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?`,
			Params:  []stmt.Param{b("operation"), b("incarnation"), i("sequence"), t("kind")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND command_sequence=?"}},
			Cohorts: []stmt.Cohort{{Name: "configuration_header", Bindings: []any{make([]byte, 16), make([]byte, 16), int64(1), "configure_library"}}}},
		{ID: iaConfigurationReclaimSourcesPoint, Kind: stmt.Read, SQL: `SELECT ordinal,retained_bytes
FROM catalog_configuration_sources WHERE original_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?
ORDER BY ordinal LIMIT 65`,
			Params:  []stmt.Param{b("operation"), b("incarnation"), i("sequence"), t("kind")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_sources"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND command_sequence=?"}},
			Cohorts: []stmt.Cohort{{Name: "configuration_cohort", Bindings: []any{make([]byte, 16), make([]byte, 16), int64(1), "configure_library"}}}},
		{ID: iaConfigurationReclaimSourceDelete, Kind: stmt.Write, SQL: `DELETE FROM catalog_configuration_sources
WHERE original_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=? AND ordinal=?
RETURNING retained_bytes`,
			Params:  []stmt.Param{b("operation"), b("incarnation"), i("sequence"), t("kind"), i("ordinal")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_sources"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=? AND ordinal=?"}},
			Cohorts: []stmt.Cohort{{Name: "exact_source_child", Bindings: []any{make([]byte, 16), make([]byte, 16), int64(1), "configure_library", int64(0)}}}},
		{ID: iaConfigurationReclaimHeaderDelete, Kind: stmt.Write, SQL: `DELETE FROM catalog_configuration_receipts
WHERE original_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?
RETURNING retained_bytes`,
			Params:  []stmt.Param{b("operation"), b("incarnation"), i("sequence"), t("kind")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND command_sequence=?"}},
			Cohorts: []stmt.Cohort{{Name: "exact_header", Bindings: []any{make([]byte, 16), make([]byte, 16), int64(1), "configure_library"}}}},
		{ID: iaConfigurationReclaimAdvance, Kind: stmt.Write, SQL: advanceSQL + `
AND reclaim_operation_id=? AND reclaim_command_sequence=?
RETURNING last_command_sequence`,
			Params:  advanceParams(false),
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_operations"}, Seeks: []string{"original_operation_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "overwrite_acknowledged_slot", Bindings: append(append([]any{}, advanceCohort...), make([]byte, 16), int64(2))}}},
		{ID: iaConfigurationReclaimAdvanceFirst, Kind: stmt.Write, SQL: advanceSQL + `
AND reclaim_command_sequence IS NULL
RETURNING last_command_sequence`,
			Params:  advanceParams(true),
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_operations"}, Seeks: []string{"original_operation_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "first_acknowledged_turn", Bindings: advanceCohort}}},
	}
	for at := range defs {
		defs[at].Owner = "store/writer IA configuration receipt reclaim"
		defs[at].CallSites = []string{"store/writer/ia_configuration_reclaim.go"}
	}
	return defs
}
