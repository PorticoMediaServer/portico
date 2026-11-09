package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IA-09 statement family. Every selection is one exact keyset page on an
// adopted index; every fresh effect is one point DELETE plus the driver CAS and
// the immutable step receipt. The step never lists or cascades a whole
// catalogue, and it cannot mint a lease, an ACK or a public result.
const (
	iaRetireStepOwner         stmt.ID = "ia.retire.step.owner"
	iaRetireStepReceipt       stmt.ID = "ia.retire.step.receipt"
	iaRetireStepBody          stmt.ID = "ia.retire.step.body"
	iaRetireStepLibrary       stmt.ID = "ia.retire.step.library"
	iaRetireStepNextMember    stmt.ID = "ia.retire.step.next_member"
	iaRetireStepMemberExists  stmt.ID = "ia.retire.step.member_exists"
	iaRetireStepNextEntity    stmt.ID = "ia.retire.step.next_entity"
	iaRetireStepEntityHold    stmt.ID = "ia.retire.step.entity_hold"
	iaRetireStepEntityExists  stmt.ID = "ia.retire.step.entity_exists"
	iaRetireStepAssetExists   stmt.ID = "ia.retire.step.asset_exists"
	iaRetireStepObjectExists  stmt.ID = "ia.retire.step.object_exists"
	iaRetireStepMemberDelete  stmt.ID = "ia.retire.step.member_delete"
	iaRetireStepEntityDelete  stmt.ID = "ia.retire.step.entity_delete"
	iaRetireStepAdvance       stmt.ID = "ia.retire.step.advance"
	iaRetireStepReceiptInsert stmt.ID = "ia.retire.step.receipt_insert"
)

func iaRetirementStepStatements() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: iaRetireStepOwner, Kind: stmt.Read, SQL: `SELECT original_operation_id,original_request_digest,command_incarnation,
original_library_id,original_library_key,original_resolved_root,original_source_incarnation,original_source_generation,original_root_identity,
last_command_sequence,acknowledged_command_sequence,receipt_reclaim_sequence,receipt_retained_count,receipt_retained_bytes,
retirement_state,phase,entity_kind,entity_cursor,object_cursor,walk_removed,walk_kept,
job_revision,job_lease_owner,job_lease_generation,job_lease_expires_ms
FROM catalog_removal_progress WHERE kind=? AND target=?`,
			Params:  []stmt.Param{t("kind"), t("target")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_progress"}, Seeks: []string{"kind=? AND target=?"}},
			Cohorts: []stmt.Cohort{{Name: "library", Bindings: []any{"library", "library"}}, {Name: "folder", Bindings: []any{"folder", "source"}}}},
		{ID: iaRetireStepReceipt, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,command_kind,result_version,result_count,result_digest,retained_bytes
FROM catalog_removal_receipts WHERE result_kind=? AND result_target=? AND original_operation_id=? AND command_incarnation=? AND command_sequence=?`,
			Params:  []stmt.Param{t("kind"), t("target"), b("original_operation"), b("incarnation"), i("sequence")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND result_kind=? AND result_target=? AND command_sequence=?"}},
			Cohorts: []stmt.Cohort{{Name: "step", Bindings: []any{"library", "library", make([]byte, 16), make([]byte, 16), int64(2)}}}},
		{ID: iaRetireStepBody, Kind: stmt.Read, SQL: `SELECT accepted_revision,applied_ms,result_phase,result_entity_kind,result_entity_cursor,result_object_cursor,result_walk_removed,result_walk_kept,result_state
FROM catalog_removal_receipts WHERE result_kind=? AND result_target=? AND original_operation_id=? AND command_incarnation=? AND command_sequence=?`,
			Params:  []stmt.Param{t("kind"), t("target"), b("original_operation"), b("incarnation"), i("sequence")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND result_kind=? AND result_target=? AND command_sequence=?"}},
			Cohorts: []stmt.Cohort{{Name: "step", Bindings: []any{"library", "library", make([]byte, 16), make([]byte, 16), int64(2)}}}},
		{ID: iaRetireStepLibrary, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_libraries WHERE id=? AND library_id=? AND root=? AND retired=1`,
			Params:  []stmt.Param{i("library_id"), t("library_key"), t("resolved_root")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "retired", Bindings: []any{int64(7), "library", "/media/original"}}}},
		// One indexed child unit: the exact link row for the current entity, after
		// the last removed asset. A single-column keyset keeps the native grammar
		// closed (no parenthesized disjunction) and needs no temporary sort.
		{ID: iaRetireStepNextMember, Kind: stmt.Read, SQL: `SELECT entity_id,asset_id FROM catalog_asset_links
WHERE library_id=? AND entity_id=? AND asset_id>?
ORDER BY asset_id LIMIT 1`,
			Params:  []stmt.Param{i("library_id"), i("entity_id"), i("after_asset")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=? AND asset_id>?"}},
			Cohorts: []stmt.Cohort{{Name: "after_cursor", Bindings: []any{int64(7), int64(10), int64(0)}}}},
		{ID: iaRetireStepMemberExists, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_asset_links WHERE library_id=? AND entity_id>0 ORDER BY entity_id,asset_id LIMIT 1`,
			Params:  []stmt.Param{i("library_id")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id>?"}},
			Cohorts: []stmt.Cohort{{Name: "any", Bindings: []any{int64(7)}}}},
		// Entity keyset on the adopted (library_id,id) index. The title bytes are
		// measured in Go as the actual per-unit variable charge, never inferred.
		{ID: iaRetireStepNextEntity, Kind: stmt.Read, SQL: `SELECT id,kind,title
FROM catalog_entities INDEXED BY catalog_entities_library_id
WHERE library_id=? AND id>?
ORDER BY id LIMIT ?`,
			Params:  []stmt.Param{i("library_id"), i("after_id"), i("limit")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library_id"}, Seeks: []string{"library_id=? AND id>?"}},
			Cohorts: []stmt.Cohort{{Name: "after_cursor", Bindings: []any{int64(7), int64(0), int64(8)}}}},
		{ID: iaRetireStepEntityHold, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_collection_members INDEXED BY catalog_collection_members_item WHERE item_id=? LIMIT 1`,
			Params:  []stmt.Param{i("item")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"catalog_collection_members_item"}, Seeks: []string{"item_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "held", Bindings: []any{int64(7)}}}},
		{ID: iaRetireStepEntityExists, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_entities INDEXED BY catalog_entities_library WHERE library_id=? LIMIT 1`,
			Params:  []stmt.Param{i("library_id")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library"}, Seeks: []string{"library_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "any", Bindings: []any{int64(7)}}}},
		{ID: iaRetireStepAssetExists, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_assets INDEXED BY sqlite_autoindex_catalog_assets_2 WHERE library_id=? LIMIT 1`,
			Params: []stmt.Param{i("library_id")},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_assets_2"}, Seeks: []string{"library_id=?"},
				AutoIndexes: []stmt.AutoIndexBinding{{Table: "catalog_assets", Name: "sqlite_autoindex_catalog_assets_2", Origin: "u",
					Columns: []stmt.IndexColumn{{Name: "library_id", Collation: "BINARY"}, {Name: "id", Collation: "BINARY"}}}}},
			Cohorts: []stmt.Cohort{{Name: "any", Bindings: []any{int64(7)}}}},
		{ID: iaRetireStepObjectExists, Kind: stmt.Read, SQL: `SELECT 1 FROM library_sources AS s INDEXED BY library_sources_library
INNER JOIN inventory_objects AS o INDEXED BY inventory_objects_source ON o.source_id=s.id
WHERE s.library_id=? LIMIT 1`,
			Params: []stmt.Param{t("library_key")},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"library_sources_library", "inventory_objects_source"},
				Seeks: []string{"library_id=?", "source_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "library", Bindings: []any{"library"}}}},
		{ID: iaRetireStepMemberDelete, Kind: stmt.Write, SQL: `DELETE FROM catalog_asset_links WHERE library_id=? AND entity_id=? AND asset_id=?`,
			Params:  []stmt.Param{i("library_id"), i("entity_id"), i("asset_id")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=? AND asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "exact", Bindings: []any{int64(7), int64(10), int64(41)}}}},
		{ID: iaRetireStepEntityDelete, Kind: stmt.Write, SQL: `DELETE FROM catalog_entities INDEXED BY catalog_entities_library_id WHERE library_id=? AND id=?`,
			Params:  []stmt.Param{i("library_id"), i("id")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library_id"}, Seeks: []string{"library_id=? AND id=?"}},
			Cohorts: []stmt.Cohort{{Name: "exact", Bindings: []any{int64(7), int64(10)}}}},
		{ID: iaRetireStepAdvance, Kind: stmt.Write, SQL: `UPDATE catalog_removal_progress SET last_command_sequence=?,retirement_state=?,phase=?,entity_kind=?,entity_cursor=?,object_cursor=?,
walk_removed=?,walk_kept=?,job_revision=?,receipt_retained_count=?,receipt_retained_bytes=?
WHERE kind=? AND target=? AND original_operation_id=? AND command_incarnation=? AND original_request_digest=?
AND original_library_id=? AND original_library_key=? AND original_resolved_root=?
AND last_command_sequence=? AND acknowledged_command_sequence=? AND receipt_reclaim_sequence=?
AND receipt_retained_count=? AND receipt_retained_bytes=?
AND retirement_state='retiring' AND phase=? AND entity_kind=? AND entity_cursor=? AND object_cursor=? AND walk_removed=? AND walk_kept=?
AND job_revision=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_revision<9223372036854775807`,
			Params: []stmt.Param{i("next_sequence"), t("next_state"), i("next_phase"), i("next_entity_kind"), i("next_entity_cursor"), t("next_object_cursor"),
				i("next_walk_removed"), i("next_walk_kept"), i("next_revision"), i("next_retained_count"), i("next_retained_bytes"),
				t("kind"), t("target"), b("original_operation"), b("incarnation"), b("original_request"),
				i("original_library_id"), t("original_library_key"), t("original_resolved_root"),
				i("last_sequence"), i("ack_sequence"), i("reclaim_sequence"),
				i("retained_count"), i("retained_bytes"),
				i("phase"), i("entity_kind"), i("entity_cursor"), t("object_cursor"), i("walk_removed"), i("walk_kept"),
				i("revision"), i("lease_owner"), i("lease_generation"), i("lease_expires_ms")},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_progress"}, Seeks: []string{"kind=? AND target=?"}},
			Cohorts: []stmt.Cohort{{Name: "exact", Bindings: []any{int64(2), "retiring", int64(1), int64(0), int64(0), "0",
				int64(1), int64(0), int64(3), int64(2), int64(470),
				"library", "library", make([]byte, 16), make([]byte, 16), make([]byte, 32),
				int64(7), "library", "/media/original",
				int64(1), int64(1), int64(0),
				int64(1), int64(239),
				int64(1), int64(0), int64(0), "0", int64(0), int64(0),
				int64(2), int64(1), int64(1), int64(4000000000000)}}}},
		{ID: iaRetireStepReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_removal_receipts(original_operation_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,
accepted_revision,applied_ms,result_kind,result_target,result_phase,result_entity_kind,result_entity_cursor,result_object_cursor,result_walk_removed,result_walk_kept,result_state,result_count,result_digest,retained_bytes)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{b("original_operation"), b("incarnation"), i("sequence"), b("operation"), b("request"), t("command_kind"),
				i("result_version"), i("accepted_revision"), i("applied_ms"), t("kind"), t("target"), i("phase"), i("entity_kind"),
				i("entity_cursor"), t("object_cursor"), i("walk_removed"), i("walk_kept"), t("state"), i("result_count"), b("result_digest"), i("retained_bytes")},
			Plan: stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "step", Bindings: []any{make([]byte, 16), make([]byte, 16), int64(2), make([]byte, 16), make([]byte, 32),
				"retire_step", int64(1), int64(3), int64(100), "library", "library", int64(1), int64(0), int64(0), "0",
				int64(1), int64(0), "retiring", int64(0), make([]byte, 32), int64(472)}}}},
	}
	for at := range defs {
		defs[at].Owner = "store/writer IA retirement STEP"
		defs[at].CallSites = []string{"store/writer/ia_retirement_step.go"}
	}
	return defs
}

// Complete private family: current US owner authority plus the finite step
// points. History reuses its own exact receipt/body reads, never a live row.
func iaRetirementStepDefinitions() []stmt.Definition {
	return append(usAuthorityDefinitions(), iaRetirementStepStatements()...)
}
