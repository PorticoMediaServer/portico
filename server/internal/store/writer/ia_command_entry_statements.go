package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	iaEntryOwner       stmt.ID = "ia.entry.history_owner"
	iaEntryHeader      stmt.ID = "ia.entry.history_header"
	iaEntryNamespace   stmt.ID = "ia.entry.namespace"
	iaEntryFreshSource stmt.ID = "ia.entry.fresh_source"
)

// Only fixed integers and schema-constrained16/32-byte proofs leave SQLite.
// Variable source values are compared against owned bounded inputs, never copied
// before admission. Current authority is independent of these retained rows.
func iaEntryDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nt := t("source")
	nt.Nullable = true
	defs := []stmt.Definition{
		{ID: iaEntryOwner, Kind: stmt.Read, SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,CASE WHEN original_library_id=? AND original_library_key=? AND command_kind=? AND original_request_digest=? AND original_source_id IS ? THEN 1 ELSE 0 END FROM catalog_configuration_operations WHERE original_operation_id=?`, Params: []stmt.Param{i("library"), t("key"), t("kind"), b("request"), nt, b("operation")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_operations"}, Seeks: []string{"original_operation_id=?"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{int64(1), "library", "configure_library", make([]byte, 32), nil, make([]byte, 16)}}}},
		{ID: iaEntryHeader, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,CASE command_kind WHEN 'configure_library' THEN 100 WHEN 'configure_source' THEN 101 ELSE 0 END,result_count,result_digest,result_version FROM catalog_configuration_receipts WHERE original_operation_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{b("operation"), b("incarnation"), i("sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND command_sequence=?"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{make([]byte, 16), make([]byte, 16), int64(1)}}}},
		{ID: iaEntryNamespace, Kind: stmt.Read, SQL: `SELECT CASE WHEN library_id=? THEN 1 ELSE 0 END,retired FROM catalog_libraries WHERE id=?`, Params: []stmt.Param{t("key"), i("library")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{"library", int64(1)}}}},
		{ID: iaEntryFreshSource, Kind: stmt.Read, SQL: `SELECT 1 FROM library_sources WHERE id=? AND library_id=? AND generation=? AND incarnation=? AND root=? AND root_identity=?`, Params: []stmt.Param{t("source"), t("library"), i("generation"), t("incarnation"), t("root"), t("identity")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_sources_1"}, Seeks: []string{"id=?"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{"source", "library", int64(1), "incarnation", "/media", "native"}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer IA command entry"
		defs[n].CallSites = []string{"store/writer/ia_command_entry.go"}
	}
	return append(usAuthorityDefinitions(), defs...)
}
