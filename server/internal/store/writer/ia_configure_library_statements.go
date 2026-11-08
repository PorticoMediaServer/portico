package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IA-06 statement family. Every statement is a point or one finite insert on
// the adopted or exact group C draft relations; there is no OFFSET, temp sort or
// library-wide scan. Each insert must change exactly one new row: a pre-existing
// namespace or policy row makes wc008DraftChanged return ErrConflict, so the
// ON CONFLICT clauses never settle or rewrite a second library.
const (
	iaLibraryNamespaceKey    stmt.ID = "ia.library.namespace_key"
	iaLibraryLivePoint       stmt.ID = "ia.library.live_point"
	iaLibrarySourcePoint     stmt.ID = "ia.library.source_point"
	iaLibraryNamespaceInsert stmt.ID = "ia.library.namespace_insert"
	iaLibraryLiveInsert      stmt.ID = "ia.library.live_insert"
	iaLibraryPolicyInsert    stmt.ID = "ia.library.policy_insert"
	iaLibrarySourceInsert    stmt.ID = "ia.library.source_insert"
	iaLibraryIntentInsert    stmt.ID = "ia.library.intent_insert"
	iaLibraryOwnerInsert     stmt.ID = "ia.library.owner_insert"
	iaLibraryReceiptInsert   stmt.ID = "ia.library.receipt_insert"
	iaLibraryCohortInsert    stmt.ID = "ia.library.cohort_insert"
	iaLibraryOwnerPoint      stmt.ID = "ia.library.owner_point"
	iaLibraryHeaderPoint     stmt.ID = "ia.library.header_point"
	iaLibrarySourcesPoint    stmt.ID = "ia.library.sources_point"
)

// The owner/header/cohort points read only the exact original operation and
// incarnation. They never join a mutable live library or source row, so a saved
// cohort survives a later rename, disable or retirement.
func iaLibraryDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	boolean := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Boolean} }
	defs := []stmt.Definition{
		{ID: iaLibraryNamespaceKey, Kind: stmt.Read, SQL: `SELECT id,retired FROM catalog_libraries WHERE library_id=?`, Params: []stmt.Param{t("key")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_libraries_1"}, Seeks: []string{"library_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "absent", Bindings: []any{"library"}}}},
		{ID: iaLibraryLivePoint, Kind: stmt.Read, SQL: `SELECT name,kind,root FROM libraries WHERE id=?`, Params: []stmt.Param{t("library")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_libraries_1"}, Seeks: []string{"id=?"}},
			Cohorts: []stmt.Cohort{{Name: "absent", Bindings: []any{"library"}}}},
		{ID: iaLibrarySourcePoint, Kind: stmt.Read, SQL: `SELECT library_id FROM library_sources WHERE id=?`, Params: []stmt.Param{t("source")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_sources_1"}, Seeks: []string{"id=?"}},
			Cohorts: []stmt.Cohort{{Name: "absent", Bindings: []any{"source"}}}},
		{ID: iaLibraryOwnerPoint, Kind: stmt.Read, SQL: `SELECT original_request_digest,command_kind,original_library_id,original_library_key,last_command_sequence,acknowledged_command_sequence,receipt_retained_count,receipt_retained_bytes FROM catalog_configuration_operations WHERE original_operation_id=? AND command_incarnation=?`, Params: []stmt.Param{b("operation"), b("incarnation")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_operations"}, Seeks: []string{"original_operation_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "absent", Bindings: []any{make([]byte, 16), make([]byte, 16)}}}},
		{ID: iaLibraryHeaderPoint, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,result_version,result_count,result_digest,retained_bytes,accepted_revision,applied_ms,result_library_name,result_library_kind FROM catalog_configuration_receipts WHERE original_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?`, Params: []stmt.Param{b("operation"), b("incarnation"), i("sequence"), t("kind")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND command_sequence=?"}},
			Cohorts: []stmt.Cohort{{Name: "absent", Bindings: []any{make([]byte, 16), make([]byte, 16), int64(1), "configure_library"}}}},
		{ID: iaLibrarySourcesPoint, Kind: stmt.Read, SQL: `SELECT ordinal,source_id,source_name,source_incarnation,source_generation,configured_root,resolved_root,root_identity,source_kind,classification,enabled,health,follow_symlinks,interval_seconds,identity_confirmed,next_scan_at,last_complete_at,last_progress_at,retained_bytes FROM catalog_configuration_sources WHERE original_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=? ORDER BY ordinal LIMIT 65`, Params: []stmt.Param{b("operation"), b("incarnation"), i("sequence"), t("kind")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_sources"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND command_sequence=?"}},
			Cohorts: []stmt.Cohort{{Name: "absent", Bindings: []any{make([]byte, 16), make([]byte, 16), int64(1), "configure_library"}}}},
		{ID: iaLibraryNamespaceInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_libraries(id,library_id,root) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING`, Params: []stmt.Param{i("id"), t("library"), t("root")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}},
			Cohorts: []stmt.Cohort{{Name: "one_library", Bindings: []any{int64(7), "library", "/media"}}}},
		{ID: iaLibraryLiveInsert, Kind: stmt.Write, SQL: `INSERT INTO libraries(id,name,kind,root,configuration_present,configuration_revision,configuration_metadata_revision) VALUES(?,?,?,?,?,?,?)`, Params: []stmt.Param{t("id"), t("name"), t("kind"), t("root"), i("present"), i("revision"), i("metadata")},
			Plan:    stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "one_library", Bindings: []any{"library", "Library", "movie", "/media", int64(1), int64(1), int64(1)}}}},
		{ID: iaLibraryPolicyInsert, Kind: stmt.Write, SQL: `INSERT INTO library_scan_policies(library_id,revision,tier) VALUES(?,?,?) ON CONFLICT(library_id) DO NOTHING`, Params: []stmt.Param{t("library"), i("revision"), t("tier")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_scan_policies_1"}},
			Cohorts: []stmt.Cohort{{Name: "one_library", Bindings: []any{"library", int64(1), "basic"}}}},
		{ID: iaLibrarySourceInsert, Kind: stmt.Write, SQL: `INSERT INTO library_sources(id,library_id,name,configured_root,root,classification,root_identity,follow_symlinks,interval_seconds,next_scan_at,identity_confirmed,incarnation,generation,health,enabled,kind) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{t("id"), t("library"), t("name"), t("configured"), t("root"), t("classification"), t("identity"), boolean("follow"), i("interval"), t("next"), boolean("confirmed"), t("incarnation"), i("generation"), t("health"), boolean("enabled"), t("kind")},
			Plan:    stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "one_source", Bindings: []any{"source", "library", "Library", "/media", "/media", "local", "native", false, int64(0), "", true, "incarnation", int64(1), "ready", true, "local"}}}},
		{ID: iaLibraryIntentInsert, Kind: stmt.Write, SQL: `INSERT INTO inventory_strict_intents(source_id,relative_path,library_id,source_generation,root_incarnation,root_identity,root,configured_root,source_kind,follow_symlinks,mode,created_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{t("source"), t("relative"), t("library"), i("generation"), t("incarnation"), t("identity"), t("root"), t("configured"), t("kind"), boolean("follow"), t("mode"), i("created")},
			Plan:    stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "one_intent", Bindings: []any{"source", ".", "library", int64(1), "incarnation", "native", "/media", "/media", "local", false, "incremental", int64(1)}}}},
		{ID: iaLibraryOwnerInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_configuration_operations(original_operation_id,original_request_digest,command_incarnation,command_kind,original_library_id,original_library_key,last_command_sequence,receipt_retained_count,receipt_retained_bytes) VALUES(?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{b("operation"), b("request"), b("incarnation"), t("kind"), i("library"), t("key"), i("last"), i("rows"), i("bytes")},
			Plan:    stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "one_owner", Bindings: []any{make([]byte, 16), make([]byte, 32), make([]byte, 16), "configure_library", int64(7), "library", int64(1), int64(2), int64(400)}}}},
		{ID: iaLibraryReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_configuration_receipts(original_operation_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,accepted_revision,applied_ms,result_library_name,result_library_kind,result_count,result_digest,retained_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{b("operation"), b("incarnation"), i("sequence"), b("header_operation"), b("request"), t("kind"), i("version"), i("revision"), i("applied"), t("name"), t("library_kind"), i("count"), b("digest"), i("bytes")},
			Plan:    stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "one_receipt", Bindings: []any{make([]byte, 16), make([]byte, 16), int64(1), make([]byte, 16), make([]byte, 32), "configure_library", int64(1), int64(1), int64(1), "Library", "movie", int64(1), make([]byte, 32), int64(200)}}}},
		{ID: iaLibraryCohortInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_configuration_sources(original_operation_id,command_incarnation,command_sequence,command_kind,ordinal,source_id,source_name,source_incarnation,source_generation,configured_root,resolved_root,root_identity,source_kind,classification,enabled,health,follow_symlinks,interval_seconds,identity_confirmed,next_scan_at,last_complete_at,last_progress_at,retained_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{b("operation"), b("incarnation"), i("sequence"), t("kind"), i("ordinal"), t("source"), t("name"), t("source_incarnation"), i("generation"), t("configured"), t("root"), t("identity"), t("source_kind"), t("classification"), boolean("enabled"), t("health"), boolean("follow"), i("interval"), boolean("confirmed"), t("next"), t("complete"), t("progress"), i("bytes")},
			Plan:    stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "one_source", Bindings: []any{make([]byte, 16), make([]byte, 16), int64(1), "configure_library", int64(0), "source", "Library", "incarnation", int64(1), "/media", "/media", "native", "local", "local", true, "ready", false, int64(0), true, "", "", "", int64(220)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer IA configure library"
		defs[n].CallSites = []string{"store/writer/ia_configure_library.go", "store/writer/ia_configuration_history.go"}
	}
	return defs
}

// Complete private family: the IA-05 entry (with US current-owner authority),
// the finite configuration points and the one canonical event append owner.
func iaConfigureLibraryDefinitions() []stmt.Definition {
	defs := append(iaEntryDefinitions(), iaLibraryDefinitions()...)
	return append(defs, wcAPIEventDefinitions()...)
}
