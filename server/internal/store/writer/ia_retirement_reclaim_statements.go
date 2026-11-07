package writer

import (
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	iaRetirementGCSlot   stmt.ID = "ia.retirement.receipt_reclaim.slot"
	iaRetirementGCFirst  stmt.ID = "ia.retirement.receipt_reclaim.first"
	iaRetirementGCDelete stmt.ID = "ia.retirement.receipt_reclaim.delete"
)

// Exact closed native slot order, shared only by this106 adapter's SQL/bindings.
// All OLD nullable shapes are compiled separately; IS ? and dynamic predicates
// are deliberately absent from the closed SQL grammar.
var iaRetirementGCColumns = []stmt.Param{
	{Name: "reclaim_command_sequence", Type: stmt.Integer},
	{Name: "reclaim_operation_id", Type: stmt.Blob},
	{Name: "reclaim_request_digest", Type: stmt.Blob},
	{Name: "reclaim_command_kind", Type: stmt.Text},
	{Name: "reclaim_result_version", Type: stmt.Integer},
	{Name: "reclaim_captured_acknowledged_sequence", Type: stmt.Integer},
	{Name: "reclaim_old_sequence", Type: stmt.Integer},
	{Name: "reclaim_removed_sequence", Type: stmt.Integer, Nullable: true},
	{Name: "reclaim_removed_kind", Type: stmt.Text},
	{Name: "reclaim_removed_count", Type: stmt.Integer},
	{Name: "reclaim_removed_bytes", Type: stmt.Integer},
	{Name: "reclaim_result_sequence", Type: stmt.Integer},
	{Name: "reclaim_result_no_next", Type: stmt.Integer},
	{Name: "reclaim_result_rows", Type: stmt.Integer},
	{Name: "reclaim_result_bytes", Type: stmt.Integer},
	{Name: "reclaim_applied_ms", Type: stmt.Integer},
	{Name: "reclaim_result_count", Type: stmt.Integer},
	{Name: "reclaim_result_digest", Type: stmt.Blob},
}

func iaRetirementGCStoreID(source bool, shape int) stmt.ID {
	return stmt.ID(fmt.Sprintf("ia.retirement.receipt_reclaim.store_%t_%d", source, shape))
}
func iaRetirementGCAckID(source bool) stmt.ID {
	return stmt.ID(fmt.Sprintf("ia.retirement.receipt_reclaim.ack_%t", source))
}

// The caller separately composes iaRetirementOwnerPoint, already owned by the
// retained history family. This finite set cannot register a handler/profile.
func iaRetirementReceiptReclaimDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	key := []stmt.Param{t("kind"), t("target"), b("original_operation"), b("incarnation")}
	point := stmt.Plan{Hot: true, Indexes: []string{"catalog_removal_operation"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND kind=? AND target=?"}}
	child := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND result_kind=? AND result_target=?"}}
	names := make([]string, len(iaRetirementGCColumns))
	for i, p := range iaRetirementGCColumns {
		names[i] = p.Name
	}
	defs := []stmt.Definition{
		{ID: iaRetirementGCSlot, Kind: stmt.Read, SQL: "SELECT receipt_reclaim_due_ms," + strings.Join(names, ",") + ",reclaim_slot_bytes FROM catalog_removal_progress INDEXED BY catalog_removal_operation WHERE kind=? AND target=? AND original_operation_id=? AND command_incarnation=?", Params: key, Plan: point},
		{ID: iaRetirementGCFirst, Kind: stmt.Read, SQL: `SELECT command_sequence,command_kind,retained_bytes FROM catalog_removal_receipts
WHERE original_operation_id=? AND command_incarnation=? AND result_kind=? AND result_target=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`,
			Params: []stmt.Param{b("original_operation"), b("incarnation"), t("kind"), t("target"), i("after"), i("through_ack")}, Plan: child},
		{ID: iaRetirementGCDelete, Kind: stmt.Write, SQL: `DELETE FROM catalog_removal_receipts WHERE original_operation_id=? AND command_incarnation=? AND result_kind=? AND result_target=?
AND command_sequence=? AND command_kind=? AND retained_bytes=? RETURNING retained_bytes`,
			Params: []stmt.Param{b("original_operation"), b("incarnation"), t("kind"), t("target"), i("sequence"), t("command_kind"), i("bytes")}, Plan: child},
	}
	for _, source := range []bool{false, true} {
		scopeSQL := `kind=? AND target=? AND original_operation_id=? AND command_incarnation=? AND original_request_digest=? AND original_library_id=? AND original_library_key=? AND original_resolved_root=?`
		scopeParams := append(append([]stmt.Param(nil), key...), b("original_request"), i("library"), t("library_key"), t("resolved_root"))
		if source {
			scopeSQL += " AND original_source_incarnation=? AND original_source_generation=? AND original_root_identity=?"
			scopeParams = append(scopeParams, t("source_incarnation"), i("source_generation"), t("root_identity"))
		} else {
			scopeSQL += " AND original_source_incarnation IS NULL AND original_source_generation IS NULL AND original_root_identity IS NULL"
		}
		ackParams := append([]stmt.Param{i("next_ack")}, scopeParams...)
		ackParams = append(ackParams, i("last"), i("ack"), i("slot_sequence"), b("slot_operation"), b("slot_request"), b("slot_result"), i("slot_bytes"))
		defs = append(defs, stmt.Definition{ID: iaRetirementGCAckID(source), Kind: stmt.Write,
			SQL: "UPDATE catalog_removal_progress INDEXED BY catalog_removal_operation SET acknowledged_command_sequence=? WHERE " + scopeSQL + ` AND last_command_sequence=? AND acknowledged_command_sequence=?
AND reclaim_command_sequence=? AND reclaim_operation_id=? AND reclaim_request_digest=? AND reclaim_result_digest=? AND reclaim_slot_bytes=?`, Params: ackParams, Plan: point})
		for shape := 0; shape < 3; shape++ {
			params := []stmt.Param{i("next_last"), i("next_cursor"), i("next_rows"), i("next_bytes"), {Name: "next_due", Type: stmt.Integer, Nullable: true}}
			set := []string{"last_command_sequence=?", "receipt_reclaim_sequence=?", "receipt_retained_count=?", "receipt_retained_bytes=?", "receipt_reclaim_due_ms=?"}
			for _, p := range iaRetirementGCColumns {
				set = append(set, p.Name+"=?")
				p.Name = "next_" + p.Name
				params = append(params, p)
			}
			params = append(params, scopeParams...)
			old := scopeSQL + ` AND last_command_sequence=? AND acknowledged_command_sequence=? AND receipt_reclaim_sequence=? AND receipt_retained_count=? AND receipt_retained_bytes=? AND receipt_reclaim_due_ms=? AND reclaim_slot_bytes=?`
			params = append(params, i("old_last"), i("old_ack"), i("old_cursor"), i("old_rows"), i("old_bytes"), i("old_due"), i("old_slot_bytes"))
			for _, p := range iaRetirementGCColumns {
				if shape == 0 || shape == 1 && p.Nullable {
					old += " AND " + p.Name + " IS NULL"
					continue
				}
				old += " AND " + p.Name + "=?"
				p.Nullable = false
				p.Name = "old_" + p.Name
				params = append(params, p)
			}
			defs = append(defs, stmt.Definition{ID: iaRetirementGCStoreID(source, shape), Kind: stmt.Write,
				SQL: "UPDATE catalog_removal_progress INDEXED BY catalog_removal_operation SET " + strings.Join(set, ",") + " WHERE " + old + " RETURNING reclaim_slot_bytes", Params: params, Plan: point})
		}
	}
	// Capture bindings are detached illustrative scalars. Runtime binds the
	// complete actual originals/OLD slot, never these inert catalogue cohorts.
	for n := range defs {
		d := &defs[n]
		d.Owner = "store/writer IA retirement receipt reclaim"
		d.CallSites = []string{"store/writer/ia_retirement_reclaim_native.go"}
		args := make([]any, len(d.Params))
		for i, p := range d.Params {
			switch p.Type {
			case stmt.Text:
				args[i] = "scope"
			case stmt.Integer:
				args[i] = int64(1)
			case stmt.Blob:
				args[i] = make([]byte, 16)
			}
			if p.Nullable {
				args[i] = nil
			}
		}
		d.Cohorts = []stmt.Cohort{{Name: "bounded_original", Bindings: args}}
	}
	return defs
}
