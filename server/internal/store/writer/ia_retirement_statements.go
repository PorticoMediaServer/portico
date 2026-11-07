package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	iaRetirementOwnerPoint    stmt.ID = "ia.retirement.owner"
	iaRetirementReceiptPoint  stmt.ID = "ia.retirement.receipt"
	iaRetirementBodyPoint     stmt.ID = "ia.retirement.body"
	iaRetirementAckCAS        stmt.ID = "ia.retirement.ack"
	iaRetirementReclaimPoint  stmt.ID = "ia.retirement.reclaim_point"
	iaRetirementReclaimDelete stmt.ID = "ia.retirement.reclaim_delete"
	iaRetirementReclaimCAS    stmt.ID = "ia.retirement.reclaim_debt"
)

// IA-SCHEMA-001 draft storage. This closed family is inert until INT adopts
// group C and the owning dispatcher composes actual Capture/byte/custody data.
// All points use the ORIGINAL operation and actual kind+target. Saved history
// never joins a live source, lease, mutable library name, or current root.
func iaRetirementHistoryDefinitions() []stmt.Definition {
	p := func(n string, k stmt.Type) stmt.Param { return stmt.Param{Name: n, Type: k} }
	b := func(n string) stmt.Param { return p(n, stmt.Blob) }
	s := func(n string) stmt.Param { return p(n, stmt.Text) }
	i := func(n string) stmt.Param { return p(n, stmt.Integer) }
	owner := []stmt.Param{s("kind"), s("target"), b("original_operation"), b("incarnation")}
	key := append(append([]stmt.Param(nil), owner...), i("sequence"))
	inc, operation := make([]byte, 16), make([]byte, 16)
	inc[0], operation[0] = 1, 2
	args := []any{"folder", "source", operation, inc}
	defs := []stmt.Definition{
		{ID: iaRetirementOwnerPoint, Kind: stmt.Read, SQL: `SELECT original_operation_id,original_request_digest,command_incarnation,
original_library_id,original_library_key,original_resolved_root,original_source_incarnation,original_source_generation,original_root_identity,
last_command_sequence,acknowledged_command_sequence,receipt_reclaim_sequence,receipt_retained_count,receipt_retained_bytes
FROM catalog_removal_progress WHERE kind=? AND target=? AND original_operation_id=? AND command_incarnation=?`, Params: owner,
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_progress"}, Seeks: []string{"kind=? AND target=?"}}},
		{ID: iaRetirementReceiptPoint, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,command_kind,result_version,result_count,result_digest,retained_bytes
FROM catalog_removal_receipts WHERE result_kind=? AND result_target=? AND original_operation_id=? AND command_incarnation=? AND command_sequence=?`, Params: key,
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND result_kind=? AND result_target=? AND command_sequence=?"}}},
		{ID: iaRetirementBodyPoint, Kind: stmt.Read, SQL: `SELECT accepted_revision,applied_ms,result_phase,result_entity_kind,result_entity_cursor,result_object_cursor,result_walk_removed,result_walk_kept,result_state
FROM catalog_removal_receipts WHERE result_kind=? AND result_target=? AND original_operation_id=? AND command_incarnation=? AND command_sequence=?`, Params: key,
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND result_kind=? AND result_target=? AND command_sequence=?"}}},
		{ID: iaRetirementAckCAS, Kind: stmt.Write, SQL: `UPDATE catalog_removal_progress SET acknowledged_command_sequence=?
WHERE kind=? AND target=? AND original_operation_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=?`,
			Params: append([]stmt.Param{i("next_ack")}, append(append([]stmt.Param(nil), owner...), i("expected_last"), i("expected_ack"))...),
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_progress"}, Seeks: []string{"kind=? AND target=?"}}},
		{ID: iaRetirementReclaimPoint, Kind: stmt.Read, SQL: `SELECT command_sequence,retained_bytes FROM catalog_removal_receipts
WHERE result_kind=? AND result_target=? AND original_operation_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=?
ORDER BY command_sequence LIMIT 1`, Params: append(append([]stmt.Param(nil), owner...), i("after_sequence"), i("acknowledged_sequence")),
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND result_kind=? AND result_target=? AND command_sequence>? AND command_sequence<?"}}},
		{ID: iaRetirementReclaimDelete, Kind: stmt.Write, SQL: `DELETE FROM catalog_removal_receipts
WHERE result_kind=? AND result_target=? AND original_operation_id=? AND command_incarnation=? AND command_sequence=? AND retained_bytes=?`,
			Params: append(append([]stmt.Param(nil), key...), i("retained_bytes")),
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND result_kind=? AND result_target=? AND command_sequence=?"}}},
		{ID: iaRetirementReclaimCAS, Kind: stmt.Write, SQL: `UPDATE catalog_removal_progress SET receipt_reclaim_sequence=?,receipt_retained_count=?,receipt_retained_bytes=?
WHERE kind=? AND target=? AND original_operation_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=?
AND receipt_reclaim_sequence=? AND receipt_retained_count=? AND receipt_retained_bytes=?`,
			Params: append([]stmt.Param{i("next_reclaimed"), i("next_count"), i("next_bytes")}, append(append([]stmt.Param(nil), owner...), i("expected_last"), i("expected_ack"), i("expected_reclaimed"), i("expected_count"), i("expected_bytes"))...),
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_progress"}, Seeks: []string{"kind=? AND target=?"}}},
	}
	for at := range defs {
		d := &defs[at]
		d.Owner, d.CallSites = "store/writer IA retirement history", []string{"store/writer/ia_retirement_native.go"}
		bindings := append([]any(nil), args...)
		switch d.ID {
		case iaRetirementReceiptPoint, iaRetirementBodyPoint:
			bindings = append(bindings, int64(1))
		case iaRetirementAckCAS:
			bindings = append([]any{int64(1)}, append(bindings, int64(1), int64(0))...)
		case iaRetirementReclaimPoint:
			bindings = append(bindings, int64(0), int64(1))
		case iaRetirementReclaimDelete:
			bindings = append(bindings, int64(1), int64(233))
		case iaRetirementReclaimCAS:
			bindings = append([]any{int64(1), int64(0), int64(0)}, append(bindings, int64(1), int64(1), int64(0), int64(1), int64(233))...)
		}
		missing := append([]any(nil), bindings...)
		target := 1
		if d.ID == iaRetirementAckCAS {
			target = 2
		}
		if d.ID == iaRetirementReclaimCAS {
			target = 4
		}
		missing[target] = "not-the-original-target"
		d.Cohorts = []stmt.Cohort{{Name: "original", Bindings: bindings}, {Name: "wrong_target", Bindings: missing}}
	}
	return defs
}
