package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IA-08 statement family. Every BEGIN effect is one point update/insert on the
// exact retained original or live row; there is no target listing, cascade,
// cursor or library-wide scan. Saved history reads only the immutable original
// operation/incarnation, never a live library, source, lease or name.
const (
	iaRetirementOwnerPoint       stmt.ID = "ia.retire.begin.owner"
	iaRetirementTargetPoint      stmt.ID = "ia.retire.begin.target"
	iaRetirementOperationPoint   stmt.ID = "ia.retire.begin.operation"
	iaRetirementReceiptPoint     stmt.ID = "ia.retire.begin.receipt"
	iaRetirementBodyPoint        stmt.ID = "ia.retire.begin.body"
	iaRetirementLibraryFence     stmt.ID = "ia.retire.begin.library_fence"
	iaRetirementSourceFence      stmt.ID = "ia.retire.begin.source_fence"
	iaRetirementConfigurationCAS stmt.ID = "ia.retire.begin.configuration"
	iaRetirementConfirmation     stmt.ID = "ia.retire.begin.confirmation"
	iaRetirementLiveLibraryPoint stmt.ID = "ia.retire.begin.live_library"
	iaRetirementDriverInsert     stmt.ID = "ia.retire.begin.driver"
	iaRetirementHeaderInsert     stmt.ID = "ia.retire.begin.header"
)

func iaRetirementHistoryDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: iaRetirementOwnerPoint, Kind: stmt.Read, SQL: `SELECT original_operation_id,original_request_digest,command_incarnation,
original_library_id,original_library_key,original_resolved_root,original_source_incarnation,original_source_generation,original_root_identity,
last_command_sequence,acknowledged_command_sequence,receipt_reclaim_sequence,receipt_retained_count,receipt_retained_bytes
FROM catalog_removal_progress WHERE kind=? AND target=? AND original_operation_id=? AND command_incarnation=?`,
			Params:  []stmt.Param{t("kind"), t("target"), b("original_operation"), b("incarnation")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_progress"}, Seeks: []string{"kind=? AND target=?"}},
			Cohorts: []stmt.Cohort{{Name: "original", Bindings: []any{"folder", "source", make([]byte, 16), make([]byte, 16)}}}},
		{ID: iaRetirementTargetPoint, Kind: stmt.Read, SQL: `SELECT original_operation_id,command_incarnation FROM catalog_removal_progress WHERE kind=? AND target=?`,
			Params:  []stmt.Param{t("kind"), t("target")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_progress"}, Seeks: []string{"kind=? AND target=?"}},
			Cohorts: []stmt.Cohort{{Name: "library", Bindings: []any{"library", "library"}}, {Name: "folder", Bindings: []any{"folder", "source"}}}},
		{ID: iaRetirementOperationPoint, Kind: stmt.Read, SQL: `SELECT kind,target,command_incarnation FROM catalog_removal_progress WHERE original_operation_id=?`,
			Params:  []stmt.Param{b("original_operation")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_removal_progress_1"}, Seeks: []string{"original_operation_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "original", Bindings: []any{make([]byte, 16)}}}},
		{ID: iaRetirementReceiptPoint, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,command_kind,result_version,result_count,result_digest,retained_bytes
FROM catalog_removal_receipts WHERE result_kind=? AND result_target=? AND original_operation_id=? AND command_incarnation=? AND command_sequence=?`,
			Params:  []stmt.Param{t("kind"), t("target"), b("original_operation"), b("incarnation"), i("sequence")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND result_kind=? AND result_target=? AND command_sequence=?"}},
			Cohorts: []stmt.Cohort{{Name: "original", Bindings: []any{"folder", "source", make([]byte, 16), make([]byte, 16), int64(1)}}}},
		{ID: iaRetirementBodyPoint, Kind: stmt.Read, SQL: `SELECT accepted_revision,applied_ms,result_phase,result_entity_kind,result_entity_cursor,result_object_cursor,result_walk_removed,result_walk_kept,result_state
FROM catalog_removal_receipts WHERE result_kind=? AND result_target=? AND original_operation_id=? AND command_incarnation=? AND command_sequence=?`,
			Params:  []stmt.Param{t("kind"), t("target"), b("original_operation"), b("incarnation"), i("sequence")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND result_kind=? AND result_target=? AND command_sequence=?"}},
			Cohorts: []stmt.Cohort{{Name: "original", Bindings: []any{"folder", "source", make([]byte, 16), make([]byte, 16), int64(1)}}}},
	}
	for at := range defs {
		defs[at].Owner = "store/writer IA retirement history"
		defs[at].CallSites = []string{"store/writer/ia_retirement_history.go"}
	}
	return defs
}

func iaRetirementBeginStatements() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	n := func(name string, kind stmt.Type) stmt.Param {
		return stmt.Param{Name: name, Type: kind, Nullable: true}
	}
	defs := []stmt.Definition{
		{ID: iaRetirementLibraryFence, Kind: stmt.Write, SQL: `UPDATE catalog_libraries SET retired=1 WHERE id=? AND library_id=? AND root=? AND retired=0`,
			Params:  []stmt.Param{i("library_id"), s("library_key"), s("root")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "live", Bindings: []any{int64(7), "library", "/media/original"}}}},
		{ID: iaRetirementSourceFence, Kind: stmt.Write, SQL: `UPDATE library_sources INDEXED BY sqlite_autoindex_library_sources_1 SET enabled=0,health='removing',generation=generation+1,next_scan_at=''
WHERE id=? AND library_id=? AND incarnation=? AND generation=? AND root=? AND root_identity=? AND health<>'removing' AND generation<9223372036854775807`,
			Params: []stmt.Param{s("source_id"), s("library_key"), s("source_incarnation"), i("source_generation"), s("resolved_root"), s("root_identity")},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_sources_1"}, Seeks: []string{"id=?"},
				AutoIndexes: []stmt.AutoIndexBinding{{Table: "library_sources", Name: "sqlite_autoindex_library_sources_1", Origin: "pk",
					Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}},
			Cohorts: []stmt.Cohort{{Name: "live", Bindings: []any{"source", "library", "original", int64(3), "/media/original", "device:inode"}}}},
		{ID: iaRetirementConfigurationCAS, Kind: stmt.Write, SQL: `UPDATE libraries INDEXED BY sqlite_autoindex_libraries_1 SET configuration_revision=configuration_revision+1,configuration_metadata_revision=configuration_metadata_revision+1,
data_revision=data_revision+1,data_order_revision=data_order_revision+1,playback_origin_revision=playback_origin_revision+1
WHERE id=? AND configuration_present=1 AND configuration_revision=? AND configuration_revision<9223372036854775807
AND configuration_metadata_revision<9223372036854775807 AND data_revision<9223372036854775807 AND data_order_revision<9223372036854775807 AND playback_origin_revision<9223372036854775807`,
			Params: []stmt.Param{s("library_key"), i("configuration_revision")},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_libraries_1"}, Seeks: []string{"id=?"},
				AutoIndexes: []stmt.AutoIndexBinding{{Table: "libraries", Name: "sqlite_autoindex_libraries_1", Origin: "pk",
					Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}},
			Cohorts: []stmt.Cohort{{Name: "live", Bindings: []any{"library", int64(5)}}}},
		{ID: iaRetirementConfirmation, Kind: stmt.Read, SQL: `SELECT 1 FROM libraries INDEXED BY sqlite_autoindex_libraries_1 WHERE id=? AND name=? AND root=? AND configuration_present=1 AND configuration_revision=?`,
			Params: []stmt.Param{s("library_key"), s("confirmed_current_name"), s("resolved_root"), i("configuration_revision")},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_libraries_1"}, Seeks: []string{"id=?"},
				AutoIndexes: []stmt.AutoIndexBinding{{Table: "libraries", Name: "sqlite_autoindex_libraries_1", Origin: "pk",
					Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}},
			Cohorts: []stmt.Cohort{{Name: "live", Bindings: []any{"library", "Library", "/media/original", int64(5)}}}},
		{ID: iaRetirementLiveLibraryPoint, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_libraries WHERE id=? AND library_id=? AND retired=0`,
			Params:  []stmt.Param{i("library_id"), s("library_key")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "live", Bindings: []any{int64(7), "library"}}}},
		{ID: iaRetirementDriverInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_removal_progress(kind,target,original_operation_id,original_request_digest,command_incarnation,
original_library_id,original_library_key,original_resolved_root,original_source_incarnation,original_source_generation,original_root_identity,
last_command_sequence,receipt_retained_count,receipt_retained_bytes,job_next_attempt_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{s("kind"), s("target"), b("original_operation"), b("original_request"), b("incarnation"), i("library_id"),
				s("library_key"), s("resolved_root"), n("source_incarnation", stmt.Text), n("source_generation", stmt.Integer),
				n("root_identity", stmt.Text), i("last_sequence"), i("retained_count"), i("retained_bytes"), i("due_ms")},
			Plan: stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "original", Bindings: []any{"folder", "source", make([]byte, 16), make([]byte, 32), make([]byte, 16),
				int64(7), "library", "/media/original", "original", int64(3), "device:inode", int64(1), int64(1), int64(239), int64(100)}}}},
		{ID: iaRetirementHeaderInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_removal_receipts(original_operation_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,
accepted_revision,applied_ms,result_kind,result_target,result_phase,result_entity_kind,result_entity_cursor,result_object_cursor,result_walk_removed,result_walk_kept,result_state,result_count,result_digest,retained_bytes)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{b("original_operation"), b("incarnation"), i("sequence"), b("operation"), b("request"), s("command_kind"),
				i("result_version"), i("accepted_revision"), i("applied_ms"), s("kind"), s("target"), i("phase"), i("entity_kind"),
				i("entity_cursor"), s("object_cursor"), i("walk_removed"), i("walk_kept"), s("state"), i("result_count"), b("result_digest"), i("retained_bytes")},
			Plan: stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "original", Bindings: []any{make([]byte, 16), make([]byte, 16), int64(1), make([]byte, 16), make([]byte, 32),
				"retire_source", int64(1), int64(1), int64(100), "folder", "source", int64(0), int64(0), int64(0), "", int64(0), int64(0), "retiring", int64(0), make([]byte, 32), int64(233)}}}},
	}
	for at := range defs {
		defs[at].Owner = "store/writer IA retirement BEGIN"
		defs[at].CallSites = []string{"store/writer/ia_retirement_begin.go"}
	}
	return defs
}

// Complete private family: current US owner authority, the immutable original
// history points and the finite BEGIN fences and inserts.
func iaRetirementBeginDefinitions() []stmt.Definition {
	defs := append(usAuthorityDefinitions(), iaRetirementHistoryDefinitions()...)
	return append(defs, iaRetirementBeginStatements()...)
}
