package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	iaConfigurationOwnerPoint   stmt.ID = "ia.configuration.owner"
	iaConfigurationHeaderPoint  stmt.ID = "ia.configuration.header"
	iaConfigurationBodyPoint    stmt.ID = "ia.configuration.body"
	iaConfigurationSourcesPoint stmt.ID = "ia.configuration.sources"
	iaConfigurationAckCAS       stmt.ID = "ia.configuration.ack"
)

// Inert group C definitions, not product registration or resource authority.
// The original operation/incarnation/kind keys never join a mutable live root.
func iaConfigurationHistoryDefinitions() []stmt.Definition {
	b := func(s string) stmt.Param { return stmt.Param{Name: s, Type: stmt.Blob} }
	i := func(s string) stmt.Param { return stmt.Param{Name: s, Type: stmt.Integer} }
	s := func(s string) stmt.Param { return stmt.Param{Name: s, Type: stmt.Text} }
	owner := []stmt.Param{b("original_operation"), b("incarnation")}
	header := append(append([]stmt.Param(nil), owner...), s("kind"))
	inc, op := make([]byte, 16), make([]byte, 16)
	inc[0], op[0] = 1, 2
	defs := []stmt.Definition{
		{ID: iaConfigurationOwnerPoint, Kind: stmt.Read, SQL: `SELECT original_request_digest,command_kind,original_library_id,original_library_key,original_source_id,
last_command_sequence,acknowledged_command_sequence,receipt_reclaim_sequence,receipt_reclaim_ordinal,receipt_retained_count,receipt_retained_bytes
FROM catalog_configuration_operations WHERE original_operation_id=? AND command_incarnation=?`, Params: owner,
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_operations"}, Seeks: []string{"original_operation_id=?"}}},
		{ID: iaConfigurationHeaderPoint, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,result_version,result_count,result_digest,retained_bytes
FROM catalog_configuration_receipts WHERE original_operation_id=? AND command_incarnation=? AND command_sequence=1 AND command_kind=?`, Params: header,
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?"}}},
		{ID: iaConfigurationBodyPoint, Kind: stmt.Read, SQL: `SELECT accepted_revision,applied_ms,result_library_name,result_library_kind
FROM catalog_configuration_receipts WHERE original_operation_id=? AND command_incarnation=? AND command_sequence=1 AND command_kind=?`, Params: header,
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?"}}},
		{ID: iaConfigurationSourcesPoint, Kind: stmt.Read, SQL: `SELECT ordinal,source_id,source_name,source_incarnation,source_generation,configured_root,resolved_root,root_identity,
source_kind,classification,enabled,health,follow_symlinks,interval_seconds,identity_confirmed,next_scan_at,last_complete_at,last_progress_at,retained_bytes
FROM catalog_configuration_sources WHERE original_operation_id=? AND command_incarnation=? AND command_sequence=1 AND command_kind=? ORDER BY ordinal LIMIT 65`, Params: header,
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_sources"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?"}}},
		{ID: iaConfigurationAckCAS, Kind: stmt.Write, SQL: `UPDATE catalog_configuration_operations SET acknowledged_command_sequence=1,receipt_reclaim_due_ms=?
WHERE original_operation_id=? AND command_incarnation=? AND command_kind=? AND original_request_digest=? AND original_library_id=? AND original_library_key=?
AND original_source_id IS ? AND last_command_sequence=1 AND acknowledged_command_sequence=0
AND receipt_reclaim_sequence=0 AND receipt_reclaim_ordinal=-1 AND receipt_retained_count=? AND receipt_retained_bytes=? AND receipt_reclaim_due_ms IS NULL`,
			Params: []stmt.Param{i("due"), b("original_operation"), b("incarnation"), s("kind"), b("original_request"), i("library"), s("library_key"), {Name: "source", Type: stmt.Text, Nullable: true}, i("rows"), i("bytes")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_operations"}, Seeks: []string{"original_operation_id=?"}}},
	}
	for n := range defs {
		d := &defs[n]
		d.Owner, d.CallSites = "store/writer IA configuration history", []string{"store/writer/ia_configuration_native.go"}
		args := []any{op, inc}
		if d.ID != iaConfigurationOwnerPoint {
			args = append(args, "configure_library")
		}
		if d.ID == iaConfigurationAckCAS {
			args = []any{int64(123), op, inc, "configure_library", make([]byte, 32), int64(7), "lib", nil, int64(2), int64(400)}
		}
		empty := append([]any(nil), args...)
		at := 0
		if d.ID == iaConfigurationAckCAS {
			at = 1
		}
		empty[at] = make([]byte, 16)
		d.Cohorts = []stmt.Cohort{{Name: "original", Bindings: args}, {Name: "absent", Bindings: empty}}
	}
	return defs
}
