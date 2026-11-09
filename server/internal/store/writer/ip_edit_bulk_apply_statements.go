package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// One bounded descriptive-bulk page owns this closed statement set: the retained
// repair owner, its captured field/target cohort, the exact atomic edit-owner
// pin and its own page receipt. The page never scans the whole captured cohort;
// it reads one raw primary-key segment and advances the durable cursor.
const (
	ipBulkPageOwner         stmt.ID = "ip.bulk.page.owner"
	ipBulkPageReceipt       stmt.ID = "ip.bulk.page.receipt"
	ipBulkPageReceiptHead   stmt.ID = "ip.bulk.page.receipt_head"
	ipBulkPageRaw           stmt.ID = "ip.bulk.page.raw_targets"
	ipBulkPageFields        stmt.ID = "ip.bulk.page.fields"
	ipBulkPageEntity        stmt.ID = "ip.bulk.page.entity"
	ipBulkPageEditOwner     stmt.ID = "ip.bulk.page.edit_owner"
	ipBulkPageTargetApplied stmt.ID = "ip.bulk.page.target_applied"
	ipBulkPageTargetStale   stmt.ID = "ip.bulk.page.target_stale"
	ipBulkPageAdvance       stmt.ID = "ip.bulk.page.advance"
	ipBulkPageReceiptWrite  stmt.ID = "ip.bulk.page.receipt_insert"
)

func ipEditBulkPageDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: ipBulkPageOwner, Kind: stmt.Read, SQL: `SELECT command_incarnation,job_revision,last_command_sequence,acknowledged_command_sequence,after_item,processed,failed,status,intent,kind,entity_id,library_id,actor,base_revision,descriptive_expected_count,descriptive_captured_count,descriptive_input_complete,descriptive_manifest_algorithm,descriptive_manifest_digest,descriptive_expected_bytes,descriptive_captured_bytes,descriptive_captured_digest FROM metadata_repair_cascades WHERE id=?`, Params: []stmt.Param{s("owner")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_repair_cascades"}}},
		{ID: ipBulkPageReceipt, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'page' THEN 1 ELSE 0 END,accepted_revision,after_ordinal,captured_count,processed_count,failed_count,input_complete,manifest_algorithm,manifest_digest,applied_ms FROM metadata_edit_batch_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{s("owner"), b("incarnation"), i("sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_batch_receipts"}}},
		{ID: ipBulkPageReceiptHead, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'page' THEN 1 ELSE 0 END FROM metadata_edit_batch_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{s("owner"), b("incarnation"), i("sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_batch_receipts"}}},
		{ID: ipBulkPageRaw, Kind: stmt.Read, SQL: `SELECT ordinal,entity_id,expected_entity_incarnation,expected_revision,CASE state WHEN 'captured' THEN 1 ELSE 0 END FROM metadata_edit_batch_targets WHERE operation_id=? AND ordinal>=? ORDER BY ordinal LIMIT 17`, Params: []stmt.Param{s("owner"), i("after")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_batch_targets"}}},
		{ID: ipBulkPageFields, Kind: stmt.Read, SQL: `SELECT field,action,value,lock_override FROM metadata_edit_batch_fields WHERE operation_id=? ORDER BY field`, Params: []stmt.Param{s("owner")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_batch_fields"}}},
		{ID: ipBulkPageEntity, Kind: stmt.Read, SQL: `SELECT e.kind,e.library_id,e.metadata_incarnation,e.source_revision,e.identity_revision,e.revision,e.metadata_revision,e.title,e.sort_title,e.metadata_language,e.sort_key,e.sort_head,e.sort_key_keep_articles,e.sort_head_keep_articles FROM catalog_entities AS e CROSS JOIN catalog_libraries AS l ON l.id=e.library_id WHERE e.id=? AND e.library_id=? AND e.retired=0 AND l.retired=0 AND l.library_id=?`, Params: []stmt.Param{i("entity"), i("library"), s("library_key")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "l"}}},
		{ID: ipBulkPageEditOwner, Kind: stmt.Read, SQL: `SELECT id,command_incarnation,revision,last_command_sequence,acknowledged_command_sequence FROM metadata_edit_owners WHERE kind=? AND entity_id=? AND person_id IS NULL AND retired_ms IS NULL`, Params: []stmt.Param{s("kind"), i("entity")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_edit_owners_1"}}},
		{ID: ipBulkPageTargetApplied, Kind: stmt.Write, SQL: `UPDATE metadata_edit_batch_targets SET state='applied',result_revision=?,edit_owner_id=?,edit_command_incarnation=?,edit_command_sequence=? WHERE operation_id=? AND ordinal=? AND entity_id=? AND expected_entity_incarnation=? AND expected_revision=? AND state='captured' AND result_revision IS NULL AND edit_owner_id IS NULL RETURNING 1`, Params: []stmt.Param{i("result_revision"), i("edit_owner"), b("edit_incarnation"), i("edit_sequence"), s("owner"), i("ordinal"), i("entity"), b("incarnation"), i("revision")}},
		{ID: ipBulkPageTargetStale, Kind: stmt.Write, SQL: `UPDATE metadata_edit_batch_targets SET state='stale' WHERE operation_id=? AND ordinal=? AND entity_id=? AND expected_entity_incarnation=? AND expected_revision=? AND state='captured' AND result_revision IS NULL AND edit_owner_id IS NULL RETURNING 1`, Params: []stmt.Param{s("owner"), i("ordinal"), i("entity"), b("incarnation"), i("revision")}},
		{ID: ipBulkPageAdvance, Kind: stmt.Write, SQL: `UPDATE metadata_repair_cascades SET processed=?,failed=?,after_item=?,last_command_sequence=?,job_revision=job_revision+1,status='running' WHERE id=? AND command_incarnation=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND after_item=? AND processed=? AND failed=? AND status IN('ready','running') AND intent='descriptive-bulk-v1' AND descriptive_input_complete=1 AND descriptive_captured_count=descriptive_expected_count AND descriptive_captured_bytes=descriptive_expected_bytes RETURNING 1`, Params: []stmt.Param{i("processed"), i("failed"), s("after"), i("sequence"), s("owner"), b("incarnation"), i("revision"), i("last"), i("ack"), s("old_after"), i("old_processed"), i("old_failed")}},
		{ID: ipBulkPageReceiptWrite, Kind: stmt.Write, SQL: `INSERT INTO metadata_edit_batch_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,after_ordinal,captured_count,processed_count,failed_count,input_complete,manifest_algorithm,manifest_digest) VALUES(?,?,?,?,?,?,?,'page',?,?,?,?,?,1,?,?) RETURNING 1`, Params: []stmt.Param{s("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("applied"), b("result"), i("revision"), i("after"), i("captured"), i("processed"), i("failed"), s("algorithm"), b("manifest")}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer IP descriptive bulk page"
		defs[n].CallSites = []string{"store/writer/ip_edit_bulk_apply.go"}
		var args []any
		for _, p := range defs[n].Params {
			switch p.Type {
			case stmt.Text:
				args = append(args, "")
			case stmt.Blob:
				args = append(args, make([]byte, 16))
			default:
				args = append(args, int64(0))
			}
		}
		defs[n].Cohorts = []stmt.Cohort{{Name: "missing_native_keys", Bindings: args}}
	}
	return defs
}
