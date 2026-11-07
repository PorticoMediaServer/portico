package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	iaRetirementLibraryFence      stmt.ID = "ia.retirement.begin.library_fence"
	iaRetirementSourceFence       stmt.ID = "ia.retirement.begin.source_fence"
	iaRetirementConfigurationCAS  stmt.ID = "ia.retirement.begin.configuration"
	iaRetirementConfirmationPoint stmt.ID = "ia.retirement.begin.confirmation"
	iaRetirementLiveLibraryPoint  stmt.ID = "ia.retirement.begin.live_library"
	iaRetirementDriverInsert      stmt.ID = "ia.retirement.begin.driver"
	iaRetirementHeaderInsert      stmt.ID = "ia.retirement.begin.header"
)

// Fixed native effects only. No whole-library source/job update or target
// cascade occurs in BEGIN; the retained fence denies access immediately and
// future bounded STEP units own physical cleanup and existing lease handling.
func iaRetirementBeginDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	n := func(n string, k stmt.Type) stmt.Param { return stmt.Param{Name: n, Type: k, Nullable: true} }
	defs := []stmt.Definition{
		{ID: iaRetirementLibraryFence, Kind: stmt.Write, SQL: `UPDATE catalog_libraries SET retired=1 WHERE id=? AND library_id=? AND root=? AND retired=0`,
			Params: []stmt.Param{i("library_id"), s("library_key"), s("root")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}}},
		{ID: iaRetirementSourceFence, Kind: stmt.Write, SQL: `UPDATE library_sources INDEXED BY sqlite_autoindex_library_sources_1 SET enabled=0,health='removing',generation=generation+1,next_scan_at=''
WHERE id=? AND library_id=? AND incarnation=? AND generation=? AND root=? AND root_identity=? AND health<>'removing' AND generation<9223372036854775807`,
			Params: []stmt.Param{s("source_id"), s("library_key"), s("source_incarnation"), i("source_generation"), s("resolved_root"), s("root_identity")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_sources_1"}, Seeks: []string{"id=?"}}},
		{ID: iaRetirementConfigurationCAS, Kind: stmt.Write, SQL: `UPDATE libraries SET configuration_revision=configuration_revision+1,configuration_metadata_revision=configuration_metadata_revision+1,
data_revision=data_revision+1,data_order_revision=data_order_revision+1,playback_origin_revision=playback_origin_revision+1
WHERE id=? AND configuration_present=1 AND configuration_revision=? AND configuration_revision<9223372036854775807
AND configuration_metadata_revision<9223372036854775807 AND data_revision<9223372036854775807 AND data_order_revision<9223372036854775807 AND playback_origin_revision<9223372036854775807`,
			Params: []stmt.Param{s("library_key"), i("configuration_revision")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_libraries_1"}, Seeks: []string{"id=?"}}},
		{ID: iaRetirementConfirmationPoint, Kind: stmt.Read, SQL: `SELECT 1 FROM libraries WHERE id=? AND name=? AND root=? AND configuration_present=1 AND configuration_revision=?`,
			Params: []stmt.Param{s("library_key"), s("confirmed_current_name"), s("resolved_root"), i("configuration_revision")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_libraries_1"}, Seeks: []string{"id=?"}}},
		{ID: iaRetirementLiveLibraryPoint, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_libraries WHERE id=? AND library_id=? AND retired=0`,
			Params: []stmt.Param{i("library_id"), s("library_key")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}}},
		{ID: iaRetirementDriverInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_removal_progress(kind,target,original_operation_id,original_request_digest,command_incarnation,
original_library_id,original_library_key,original_resolved_root,original_source_incarnation,original_source_generation,original_root_identity,
last_command_sequence,receipt_retained_count,receipt_retained_bytes,job_next_attempt_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{s("kind"), s("target"), b("original_operation"), b("original_request"), b("incarnation"), i("library_id"), s("library_key"), s("resolved_root"),
				n("source_incarnation", stmt.Text), n("source_generation", stmt.Integer), n("root_identity", stmt.Text), i("last_sequence"), i("retained_count"), i("retained_bytes"), i("due_ms")}, Plan: stmt.Plan{Hot: true}},
		{ID: iaRetirementHeaderInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_removal_receipts(original_operation_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,
accepted_revision,applied_ms,result_kind,result_target,result_phase,result_entity_kind,result_entity_cursor,result_object_cursor,result_walk_removed,result_walk_kept,result_state,result_count,result_digest,retained_bytes)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{b("original_operation"), b("incarnation"), i("sequence"), b("operation"), b("request"), s("command_kind"), i("result_version"), i("accepted_revision"), i("applied_ms"), s("kind"), s("target"), i("phase"), i("entity_kind"), i("entity_cursor"), s("object_cursor"), i("walk_removed"), i("walk_kept"), s("state"), i("result_count"), b("result_digest"), i("retained_bytes")},
			Plan:   stmt.Plan{Hot: true}},
	}
	inc, operation, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
	inc[0], operation[0], digest[0] = 1, 2, 9
	for at := range defs {
		d := &defs[at]
		d.Owner, d.CallSites = "store/writer IA retirement BEGIN", []string{"store/writer/ia_retirement_begin.go"}
		var args []any
		switch d.ID {
		case iaRetirementLibraryFence:
			args = []any{int64(7), "library", "/media/original"}
		case iaRetirementSourceFence:
			args = []any{"source", "library", "original", int64(3), "/media/original", "device:inode"}
		case iaRetirementConfigurationCAS:
			args = []any{"library", int64(5)}
		case iaRetirementConfirmationPoint:
			args = []any{"library", "Library", "/media/original", int64(5)}
		case iaRetirementLiveLibraryPoint:
			args = []any{int64(7), "library"}
		case iaRetirementDriverInsert:
			args = []any{"folder", "fresh-source", operation, digest, inc, int64(7), "library", "/media/original", "original", int64(3), "device:inode", int64(1), int64(1), int64(239), int64(100)}
		case iaRetirementHeaderInsert:
			args = []any{operation, inc, int64(1), operation, digest, "retire_source", int64(1), int64(1), int64(100), "folder", "source", int64(0), int64(0), int64(0), "", int64(0), int64(0), "retiring", int64(0), digest, int64(233)}
		}
		d.Cohorts = []stmt.Cohort{{Name: "original", Bindings: args}}
	}
	return defs
}
