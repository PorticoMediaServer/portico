package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	ipBulkOwner        stmt.ID = "ip.bulk.begin.owner"
	ipBulkReceipt      stmt.ID = "ip.bulk.begin.receipt"
	ipBulkReceiptHead  stmt.ID = "ip.bulk.begin.receipt_head"
	ipBulkEntity       stmt.ID = "ip.bulk.capture.entity"
	ipBulkInsert       stmt.ID = "ip.bulk.begin.insert"
	ipBulkFieldWrite   stmt.ID = "ip.bulk.capture.field"
	ipBulkFieldMatch   stmt.ID = "ip.bulk.capture.field_match"
	ipBulkFieldRead    stmt.ID = "ip.bulk.capture.field_read"
	ipBulkTargetWrite  stmt.ID = "ip.bulk.capture.target"
	ipBulkTargetMatch  stmt.ID = "ip.bulk.capture.target_match"
	ipBulkTargetRead   stmt.ID = "ip.bulk.capture.target_read"
	ipBulkFieldCount   stmt.ID = "ip.bulk.capture.field_count"
	ipBulkTargetCount  stmt.ID = "ip.bulk.capture.target_count"
	ipBulkReceiptWrite stmt.ID = "ip.bulk.begin.receipt_insert"
)

// Birth/capture is a closed native statement set over E's descriptive repair
// owner and its exact-intent field/target/receipt children. Page apply and the
// terminal are deliberately absent.
func ipEditBulkDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	defs := []stmt.Definition{
		{ID: ipBulkOwner, Kind: stmt.Read, SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,job_revision,descriptive_expected_count,descriptive_captured_count,descriptive_input_complete,descriptive_manifest_algorithm,descriptive_manifest_digest,descriptive_expected_bytes,descriptive_captured_bytes,descriptive_captured_digest,descriptive_reclaim_after_ordinal,processed,failed,status,intent,kind,entity_id,library_id,actor,base_revision FROM metadata_repair_cascades WHERE id=?`, Params: []stmt.Param{s("owner")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_repair_cascades"}}},
		{ID: ipBulkReceipt, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'begin' THEN 1 ELSE 0 END,accepted_revision,after_ordinal,captured_count,processed_count,failed_count,input_complete,manifest_algorithm,manifest_digest,applied_ms FROM metadata_edit_batch_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{s("owner"), b("incarnation"), i("sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_batch_receipts"}}},
		{ID: ipBulkReceiptHead, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'begin' THEN 1 ELSE 0 END FROM metadata_edit_batch_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{s("owner"), b("incarnation"), i("sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_batch_receipts"}}},
		{ID: ipBulkEntity, Kind: stmt.Read, SQL: `SELECT e.kind FROM catalog_entities AS e CROSS JOIN catalog_subjects AS s ON s.id=e.id AND s.library_id=e.library_id AND s.kind=e.kind CROSS JOIN catalog_libraries AS l ON l.id=e.library_id WHERE e.id=? AND e.library_id=? AND e.metadata_incarnation=? AND e.revision=? AND e.retired=0 AND l.library_id=? AND l.retired=0`, Params: []stmt.Param{i("entity"), i("library"), b("incarnation"), i("revision"), s("library_key")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "s", "l"}}},
		{ID: ipBulkInsert, Kind: stmt.Write, SQL: `INSERT INTO metadata_repair_cascades(id,kind,entity_id,library_id,actor,intent,base_revision,status,command_incarnation,last_command_sequence,descriptive_expected_count,descriptive_captured_count,descriptive_input_complete,descriptive_manifest_algorithm,descriptive_manifest_digest,descriptive_expected_bytes,descriptive_captured_bytes,descriptive_captured_digest,descriptive_reclaim_after_ordinal,descriptive_receipt_reclaim_after_sequence,created_at) VALUES(?,?,?,?,?,'descriptive-bulk-v1',?,'ready',?,?,?,?,1,?,?,?,?,?,-1,0,?) RETURNING job_revision`, Params: []stmt.Param{s("owner"), s("kind"), i("entity"), s("library"), s("actor"), s("base"), b("incarnation"), i("sequence"), i("expected_count"), i("captured_count"), s("algorithm"), b("manifest"), i("expected_bytes"), i("captured_bytes"), b("captured_digest"), s("created")}},
		{ID: ipBulkFieldWrite, Kind: stmt.Write, SQL: `INSERT INTO metadata_edit_batch_fields(operation_id,field,action,value,lock_override) VALUES(?,?,?,?,?)`, Params: []stmt.Param{s("owner"), s("field"), s("action"), nt("value"), i("lock")}},
		{ID: ipBulkFieldMatch, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_edit_batch_fields WHERE operation_id=? AND field=? AND action=? AND value IS ? AND lock_override=?`, Params: []stmt.Param{s("owner"), s("field"), s("action"), nt("value"), i("lock")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_batch_fields"}}},
		{ID: ipBulkFieldRead, Kind: stmt.Read, SQL: `SELECT field,action,value,lock_override FROM metadata_edit_batch_fields WHERE operation_id=? ORDER BY field`, Params: []stmt.Param{s("owner")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_batch_fields"}}},
		{ID: ipBulkTargetWrite, Kind: stmt.Write, SQL: `INSERT INTO metadata_edit_batch_targets(operation_id,ordinal,entity_id,expected_entity_incarnation,expected_revision,state) VALUES(?,?,?,?,?,'captured')`, Params: []stmt.Param{s("owner"), i("ordinal"), i("entity"), b("incarnation"), i("revision")}},
		{ID: ipBulkTargetMatch, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_edit_batch_targets WHERE operation_id=? AND ordinal=? AND entity_id=? AND expected_entity_incarnation=? AND expected_revision=? AND state='captured' AND result_revision IS NULL`, Params: []stmt.Param{s("owner"), i("ordinal"), i("entity"), b("incarnation"), i("revision")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_batch_targets"}}},
		{ID: ipBulkTargetRead, Kind: stmt.Read, SQL: `SELECT ordinal,entity_id,expected_entity_incarnation,expected_revision FROM metadata_edit_batch_targets WHERE operation_id=? ORDER BY ordinal`, Params: []stmt.Param{s("owner")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_batch_targets"}}},
		{ID: ipBulkFieldCount, Kind: stmt.Read, SQL: `SELECT count(*) FROM metadata_edit_batch_fields WHERE operation_id=?`, Params: []stmt.Param{s("owner")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_batch_fields"}}},
		{ID: ipBulkTargetCount, Kind: stmt.Read, SQL: `SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id=?`, Params: []stmt.Param{s("owner")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_batch_targets"}}},
		{ID: ipBulkReceiptWrite, Kind: stmt.Write, SQL: `INSERT INTO metadata_edit_batch_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,after_ordinal,captured_count,processed_count,failed_count,input_complete,manifest_algorithm,manifest_digest) VALUES(?,?,?,?,?,?,?,'begin',?,?,?,?,?,1,?,?) RETURNING 1`, Params: []stmt.Param{s("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("applied"), b("result"), i("revision"), i("after"), i("count"), i("processed"), i("failed"), s("algorithm"), b("manifest")}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer IP descriptive bulk"
		defs[n].CallSites = []string{"store/writer/ip_edit_bulk_native.go"}
		var args []any
		for _, p := range defs[n].Params {
			switch {
			case p.Nullable:
				args = append(args, nil)
			case p.Type == stmt.Text:
				args = append(args, "")
			case p.Type == stmt.Blob:
				args = append(args, make([]byte, 16))
			default:
				args = append(args, int64(0))
			}
		}
		defs[n].Cohorts = []stmt.Cohort{{Name: "missing_native_owner", Bindings: args}}
	}
	return append(usAuthorityDefinitions(), defs...)
}
