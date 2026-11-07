package writer

import (
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	iaConfigurationGCSlot         stmt.ID = "ia.configuration.receipt_reclaim.slot"
	iaConfigurationGCSourceFirst  stmt.ID = "ia.configuration.receipt_reclaim.source_first"
	iaConfigurationGCHeaderFirst  stmt.ID = "ia.configuration.receipt_reclaim.header_first"
	iaConfigurationGCSourceDelete stmt.ID = "ia.configuration.receipt_reclaim.source_delete"
	iaConfigurationGCHeaderDelete stmt.ID = "ia.configuration.receipt_reclaim.header_delete"
)

// Configuration keeps its own typed scalar on the actual retained owner. The
// three ordinal fields supplement the common sequence/result shape; they are
// never aliases for a scan/directory cursor or retirement result.
func iaConfigurationGCColumns() []stmt.Param {
	columns := append([]stmt.Param(nil), iaRetirementGCColumns...)
	return append(columns, stmt.Param{Name: "reclaim_old_ordinal", Type: stmt.Integer},
		stmt.Param{Name: "reclaim_removed_ordinal", Type: stmt.Integer, Nullable: true},
		stmt.Param{Name: "reclaim_result_ordinal", Type: stmt.Integer})
}

func iaConfigurationGCStoreID(source bool, shape int) stmt.ID {
	return stmt.ID(fmt.Sprintf("ia.configuration.receipt_reclaim.store_%t_%d", source, shape))
}
func iaConfigurationGCAckID(source bool) stmt.ID {
	return stmt.ID(fmt.Sprintf("ia.configuration.receipt_reclaim.ack_%t", source))
}

// Inert definitions only. Native mutation, current authority, actual byte
// admission and terminal COMMIT truth remain the owning command issuer's job.
func iaConfigurationReceiptReclaimDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	key := []stmt.Param{b("original_operation"), b("incarnation"), t("original_kind")}
	point := stmt.Plan{Hot: true, Indexes: []string{"catalog_configuration_operation_kind"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND command_kind=?"}}
	sourcePlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_sources"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?"}}
	headerPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?"}}
	ownerKey := `original_operation_id=? AND command_incarnation=? AND command_kind=?`
	childKey := `original_operation_id=? AND command_incarnation=? AND command_sequence=1 AND command_kind=?`
	names := []string{}
	for _, p := range iaConfigurationGCColumns() {
		names = append(names, p.Name)
	}
	defs := []stmt.Definition{
		{ID: iaConfigurationGCSlot, Kind: stmt.Read, SQL: "SELECT receipt_reclaim_due_ms," + strings.Join(names, ",") + ",reclaim_slot_bytes FROM catalog_configuration_operations INDEXED BY catalog_configuration_operation_kind WHERE " + ownerKey, Params: key, Plan: point},
		{ID: iaConfigurationGCSourceFirst, Kind: stmt.Read, SQL: "SELECT ordinal,retained_bytes FROM catalog_configuration_sources WHERE " + childKey + " AND ordinal>? ORDER BY ordinal LIMIT 1", Params: append(append([]stmt.Param(nil), key...), i("after_ordinal")), Plan: sourcePlan},
		{ID: iaConfigurationGCHeaderFirst, Kind: stmt.Read, SQL: "SELECT result_count,retained_bytes FROM catalog_configuration_receipts WHERE " + childKey, Params: key, Plan: headerPlan},
		{ID: iaConfigurationGCSourceDelete, Kind: stmt.Write, SQL: "DELETE FROM catalog_configuration_sources WHERE " + childKey + " AND ordinal=? AND retained_bytes=? RETURNING retained_bytes", Params: append(append([]stmt.Param(nil), key...), i("ordinal"), i("bytes")), Plan: sourcePlan},
		{ID: iaConfigurationGCHeaderDelete, Kind: stmt.Write, SQL: "DELETE FROM catalog_configuration_receipts WHERE " + childKey + " AND result_count=? AND retained_bytes=? RETURNING retained_bytes", Params: append(append([]stmt.Param(nil), key...), i("source_count"), i("bytes")), Plan: headerPlan},
	}
	for _, source := range []bool{false, true} {
		scope := ownerKey + " AND original_request_digest=? AND original_library_id=? AND original_library_key=?"
		scopeParams := append(append([]stmt.Param(nil), key...), b("original_request"), i("library"), t("library_key"))
		if source {
			scope += " AND original_source_id=?"
			scopeParams = append(scopeParams, t("source"))
		} else {
			scope += " AND original_source_id IS NULL"
		}
		ackParams := append([]stmt.Param{i("next_ack")}, scopeParams...)
		ackParams = append(ackParams, i("last"), i("ack"), i("slot_sequence"), b("slot_operation"), b("slot_request"), b("slot_digest"), i("slot_bytes"))
		defs = append(defs, stmt.Definition{ID: iaConfigurationGCAckID(source), Kind: stmt.Write, SQL: "UPDATE catalog_configuration_operations INDEXED BY catalog_configuration_operation_kind SET acknowledged_command_sequence=? WHERE " + scope + ` AND last_command_sequence=? AND acknowledged_command_sequence=? AND reclaim_command_sequence=? AND reclaim_operation_id=? AND reclaim_request_digest=? AND reclaim_result_digest=? AND reclaim_slot_bytes=?`, Params: ackParams, Plan: point})
		// OLD shape: empty, no-removal, source-removal, header-removal. SQL
		// NULL/equality predicates are closed; no nullable wildcard IS ?.
		for shape := 0; shape < 4; shape++ {
			params := []stmt.Param{i("next_last"), i("next_cursor_sequence"), i("next_cursor_ordinal"), i("next_rows"), i("next_bytes"), {Name: "next_due", Type: stmt.Integer, Nullable: true}}
			set := []string{"last_command_sequence=?", "receipt_reclaim_sequence=?", "receipt_reclaim_ordinal=?", "receipt_retained_count=?", "receipt_retained_bytes=?", "receipt_reclaim_due_ms=?"}
			for _, p := range iaConfigurationGCColumns() {
				set = append(set, p.Name+"=?")
				p.Name = "next_" + p.Name
				params = append(params, p)
			}
			old := scope + ` AND last_command_sequence=? AND acknowledged_command_sequence=? AND receipt_reclaim_sequence=? AND receipt_reclaim_ordinal=? AND receipt_retained_count=? AND receipt_retained_bytes=? AND receipt_reclaim_due_ms=? AND reclaim_slot_bytes=?`
			params = append(params, scopeParams...)
			params = append(params, i("old_last"), i("old_ack"), i("old_cursor_sequence"), i("old_cursor_ordinal"), i("old_rows"), i("old_bytes"), i("old_due"), i("old_slot_bytes"))
			for _, p := range iaConfigurationGCColumns() {
				isNull := shape == 0 || shape == 1 && p.Nullable || shape == 3 && p.Name == "reclaim_removed_ordinal"
				if isNull {
					old += " AND " + p.Name + " IS NULL"
					continue
				}
				old += " AND " + p.Name + "=?"
				p.Nullable = false
				p.Name = "old_" + p.Name
				params = append(params, p)
			}
			defs = append(defs, stmt.Definition{ID: iaConfigurationGCStoreID(source, shape), Kind: stmt.Write, SQL: "UPDATE catalog_configuration_operations INDEXED BY catalog_configuration_operation_kind SET " + strings.Join(set, ",") + " WHERE " + old + " RETURNING reclaim_slot_bytes", Params: params, Plan: point})
		}
	}
	for n := range defs {
		d := &defs[n]
		d.Owner = "store/writer IA configuration receipt reclaim"
		d.CallSites = []string{"store/writer/ia_configuration_reclaim_native.go"}
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
