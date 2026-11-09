package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IA-07 statement family. Group C is not embedded yet, so this family is the
// exact draft shape: the live source point reads, the PK/UNIQUE-guarded update,
// the whole-root rescan intent and the immutable operation/header/cohort rows.
// Every statement is one indexed point or one finite single-row write; there is
// no OFFSET, temp sort, library-wide scan or caller-provided SQL.
const (
	iaSourceConfigureStatePoint   stmt.ID = "ia.source.configure.state_point"
	iaSourceConfigureRootPoint    stmt.ID = "ia.source.configure.root_point"
	iaSourceConfigureUpdate       stmt.ID = "ia.source.configure.update"
	iaSourceConfigureIntent       stmt.ID = "ia.source.configure.intent"
	iaSourceConfigureOwnerPoint   stmt.ID = "ia.source.configure.owner_point"
	iaSourceConfigureHeaderPoint  stmt.ID = "ia.source.configure.header_point"
	iaSourceConfigureCohortPoint  stmt.ID = "ia.source.configure.cohort_point"
	iaSourceConfigureOwnerInsert  stmt.ID = "ia.source.configure.owner_insert"
	iaSourceConfigureReceipt      stmt.ID = "ia.source.configure.receipt_insert"
	iaSourceConfigureCohortInsert stmt.ID = "ia.source.configure.cohort_insert"
)

// The owner/header/cohort points read only the exact original operation and
// incarnation. They never join a mutable library or source row, so a saved
// cohort survives a later rename, disable, root move or retirement.
func iaConfigureSourceStatements() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	boolean := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Boolean} }
	nullableText := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	defs := []stmt.Definition{
		// Current scheduling/state facts for the cohort snapshot. The CAS itself
		// is IA-05's fresh-source point; this read never authorizes a write.
		{ID: iaSourceConfigureStatePoint, Kind: stmt.Read, SQL: `SELECT enabled,health,identity_confirmed,next_scan_at,last_complete_at,progress_last_progress_at FROM library_sources INDEXED BY sqlite_autoindex_library_sources_1 WHERE id=? AND library_id=?`, Params: []stmt.Param{t("source"), t("library")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_sources_1"}, Seeks: []string{"id=?"}},
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{"source", "library"}}}},
		// The live library-root UNIQUE key is library_sources(library_id,root).
		{ID: iaSourceConfigureRootPoint, Kind: stmt.Read, SQL: `SELECT id FROM library_sources INDEXED BY sqlite_autoindex_library_sources_2 WHERE library_id=? AND root=?`, Params: []stmt.Param{t("library"), t("root")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_sources_2"}, Seeks: []string{"library_id=? AND root=?"}},
			Cohorts: []stmt.Cohort{{Name: "absent", Bindings: []any{"library", "/media"}}}},
		// The update repeats every CAS witness, including the live health and
		// identity confirmation it carried forward, so a scanner state change
		// after the state point yields zero affected rows (ErrConflict) instead
		// of overwriting newer evidence.
		{ID: iaSourceConfigureUpdate, Kind: stmt.Write, SQL: `UPDATE library_sources INDEXED BY sqlite_autoindex_library_sources_1 SET name=?,configured_root=?,root=?,classification=?,root_identity=?,follow_symlinks=?,interval_seconds=?,incarnation=?,generation=?,health=?,enabled=?,identity_confirmed=?,next_scan_at=?,last_complete_at=?,kind=? WHERE id=? AND library_id=? AND generation=? AND incarnation=? AND root=? AND root_identity=? AND health=? AND identity_confirmed=?`,
			Params:  []stmt.Param{t("name"), t("configured"), t("root"), t("classification"), t("identity"), boolean("follow"), i("interval"), t("incarnation"), i("generation"), t("health"), boolean("enabled"), boolean("confirmed"), t("next"), t("complete"), t("kind"), t("where_source"), t("where_library"), i("where_generation"), t("where_incarnation"), t("where_root"), t("where_identity"), t("where_health"), boolean("where_confirmed")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_sources_1"}, Seeks: []string{"id=?"}},
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{"Library", "/media", "/media", "local", "native", false, int64(0), "incarnation", int64(2), "ready", true, true, "", "", "local", "source", "library", int64(1), "incarnation", "/media", "native", "ready", true}}}},
		// One whole-root incremental intent per source. A newer generation replaces
		// the older pending intent rather than stacking a second scan.
		{ID: iaSourceConfigureIntent, Kind: stmt.Write, SQL: `INSERT INTO inventory_strict_intents(source_id,relative_path,library_id,source_generation,root_incarnation,root_identity,root,configured_root,source_kind,follow_symlinks,mode,created_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source_id,relative_path) DO UPDATE SET library_id=excluded.library_id,source_generation=excluded.source_generation,root_incarnation=excluded.root_incarnation,root_identity=excluded.root_identity,root=excluded.root,configured_root=excluded.configured_root,source_kind=excluded.source_kind,follow_symlinks=excluded.follow_symlinks,mode=excluded.mode,created_ms=excluded.created_ms`,
			Params:  []stmt.Param{t("source"), t("relative"), t("library"), i("generation"), t("incarnation"), t("identity"), t("root"), t("configured"), t("kind"), boolean("follow"), t("mode"), i("created")},
			Plan:    stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "one_intent", Bindings: []any{"source", ".", "library", int64(2), "incarnation", "native", "/media", "/media", "local", false, "incremental", int64(1)}}}},
		{ID: iaSourceConfigureOwnerPoint, Kind: stmt.Read, SQL: `SELECT original_request_digest,command_kind,original_library_id,original_library_key,original_source_id,last_command_sequence,acknowledged_command_sequence,receipt_retained_count,receipt_retained_bytes FROM catalog_configuration_operations WHERE original_operation_id=? AND command_incarnation=?`, Params: []stmt.Param{b("operation"), b("incarnation")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_operations"}, Seeks: []string{"original_operation_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "absent", Bindings: []any{make([]byte, 16), make([]byte, 16)}}}},
		{ID: iaSourceConfigureHeaderPoint, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,result_version,result_count,result_digest,retained_bytes,accepted_revision,applied_ms,result_library_name,result_library_kind FROM catalog_configuration_receipts WHERE original_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?`, Params: []stmt.Param{b("operation"), b("incarnation"), i("sequence"), t("kind")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND command_sequence=?"}},
			Cohorts: []stmt.Cohort{{Name: "absent", Bindings: []any{make([]byte, 16), make([]byte, 16), int64(1), "configure_source"}}}},
		{ID: iaSourceConfigureCohortPoint, Kind: stmt.Read, SQL: `SELECT ordinal,source_id,source_name,source_incarnation,source_generation,configured_root,resolved_root,root_identity,source_kind,classification,enabled,health,follow_symlinks,interval_seconds,identity_confirmed,next_scan_at,last_complete_at,last_progress_at,retained_bytes FROM catalog_configuration_sources WHERE original_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=? ORDER BY ordinal LIMIT 2`, Params: []stmt.Param{b("operation"), b("incarnation"), i("sequence"), t("kind")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_sources"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND command_sequence=?"}},
			Cohorts: []stmt.Cohort{{Name: "absent", Bindings: []any{make([]byte, 16), make([]byte, 16), int64(1), "configure_source"}}}},
		{ID: iaSourceConfigureOwnerInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_configuration_operations(original_operation_id,original_request_digest,command_incarnation,command_kind,original_library_id,original_library_key,original_source_id,last_command_sequence,receipt_retained_count,receipt_retained_bytes) VALUES(?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{b("operation"), b("request"), b("incarnation"), t("kind"), i("library"), t("key"), t("source"), i("last"), i("rows"), i("bytes")},
			Plan:    stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "one_owner", Bindings: []any{make([]byte, 16), make([]byte, 32), make([]byte, 16), "configure_source", int64(7), "library", "source", int64(1), int64(2), int64(400)}}}},
		{ID: iaSourceConfigureReceipt, Kind: stmt.Write, SQL: `INSERT INTO catalog_configuration_receipts(original_operation_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,accepted_revision,applied_ms,result_library_name,result_library_kind,result_count,result_digest,retained_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{b("operation"), b("incarnation"), i("sequence"), b("header_operation"), b("request"), t("kind"), i("version"), i("revision"), i("applied"), nullableText("name"), nullableText("library_kind"), i("count"), b("digest"), i("bytes")},
			Plan:    stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "one_receipt", Bindings: []any{make([]byte, 16), make([]byte, 16), int64(1), make([]byte, 16), make([]byte, 32), "configure_source", int64(1), int64(1), int64(1), nil, nil, int64(1), make([]byte, 32), int64(200)}}}},
		{ID: iaSourceConfigureCohortInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_configuration_sources(original_operation_id,command_incarnation,command_sequence,command_kind,ordinal,source_id,source_name,source_incarnation,source_generation,configured_root,resolved_root,root_identity,source_kind,classification,enabled,health,follow_symlinks,interval_seconds,identity_confirmed,next_scan_at,last_complete_at,last_progress_at,retained_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{b("operation"), b("incarnation"), i("sequence"), t("kind"), i("ordinal"), t("source"), t("name"), t("source_incarnation"), i("generation"), t("configured"), t("root"), t("identity"), t("source_kind"), t("classification"), boolean("enabled"), t("health"), boolean("follow"), i("interval"), boolean("confirmed"), t("next"), t("complete"), t("progress"), i("bytes")},
			Plan:    stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "one_source", Bindings: []any{make([]byte, 16), make([]byte, 16), int64(1), "configure_source", int64(0), "source", "Library", "incarnation", int64(2), "/media", "/media", "native", "local", "local", true, "ready", false, int64(0), true, "", "", "", int64(220)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer IA configure source"
		defs[n].CallSites = []string{"store/writer/ia_configure_source.go"}
	}
	return defs
}

// Complete private family: the IA-05 entry (with US current-owner authority),
// this task's finite source points/writes and the one canonical event append.
func iaConfigureSourceDefinitions() []stmt.Definition {
	defs := append(iaEntryDefinitions(), iaConfigureSourceStatements()...)
	return append(defs, wcAPIEventDefinitions()...)
}
