package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	ipBulkOwner        stmt.ID = "ip.edit_bulk.owner"
	ipBulkReceipt      stmt.ID = "ip.edit_bulk.receipt"
	ipBulkRoot         stmt.ID = "ip.edit_bulk.root"
	ipBulkInitialize   stmt.ID = "ip.edit_bulk.initialize"
	ipBulkFieldInsert  stmt.ID = "ip.edit_bulk.field_insert"
	ipBulkFieldMatch   stmt.ID = "ip.edit_bulk.field_match"
	ipBulkTargetMatch  stmt.ID = "ip.edit_bulk.target_match"
	ipBulkTargetInsert stmt.ID = "ip.edit_bulk.target_insert"
	ipBulkAdvance      stmt.ID = "ip.edit_bulk.advance"
	ipBulkSeal         stmt.ID = "ip.edit_bulk.seal"
	ipBulkSave         stmt.ID = "ip.edit_bulk.save"
)

func ipEditBulkDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	n := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	defs := []stmt.Definition{
		{ID: ipBulkOwner, Kind: stmt.Read, SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,job_revision,descriptive_expected_count,descriptive_captured_count,descriptive_input_complete,descriptive_expected_bytes,descriptive_captured_bytes,descriptive_manifest_digest,descriptive_captured_digest,processed,failed,CASE status WHEN 'pending' THEN 1 WHEN 'ready' THEN 2 WHEN 'running' THEN 3 WHEN 'complete' THEN 4 ELSE 0 END FROM metadata_repair_cascades WHERE id=?`, Params: []stmt.Param{s("owner")}},
		{ID: ipBulkReceipt, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'begin' THEN 1 WHEN 'capture' THEN 2 WHEN 'seal' THEN 3 WHEN 'page' THEN 4 WHEN 'finish' THEN 5 ELSE 0 END,accepted_revision,after_ordinal,captured_count,processed_count,failed_count,input_complete,manifest_digest,applied_ms,(manifest_algorithm='portico-ip-descriptive-input-sha256-v1') FROM metadata_edit_batch_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{s("owner"), b("incarnation"), i("sequence")}},
		{ID: ipBulkRoot, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_repair_cascades WHERE id=? AND kind=? AND entity_id=? AND library_id=? AND actor=? AND base_revision=? AND intent='descriptive-bulk-v1'`, Params: []stmt.Param{s("owner"), s("kind"), i("entity"), s("library"), s("actor"), s("base")}},
		{ID: ipBulkInitialize, Kind: stmt.Write, SQL: `UPDATE metadata_repair_cascades SET descriptive_expected_count=?,descriptive_captured_count=0,descriptive_input_complete=0,descriptive_manifest_algorithm='portico-ip-descriptive-input-sha256-v1',descriptive_manifest_digest=?,descriptive_expected_bytes=?,descriptive_captured_bytes=?,descriptive_captured_digest=?,descriptive_reclaim_after_ordinal=-1,job_revision=job_revision+1,last_command_sequence=? WHERE id=? AND job_revision=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND status='pending' AND descriptive_expected_count IS NULL AND descriptive_captured_count IS NULL AND descriptive_input_complete IS NULL AND descriptive_manifest_algorithm IS NULL AND descriptive_manifest_digest IS NULL AND descriptive_expected_bytes IS NULL AND descriptive_captured_bytes IS NULL AND descriptive_captured_digest IS NULL AND descriptive_reclaim_after_ordinal IS NULL RETURNING 1`, Params: []stmt.Param{i("count"), b("manifest"), i("expected_bytes"), i("field_bytes"), b("seed"), i("sequence"), s("owner"), i("revision"), b("incarnation"), i("last"), i("ack")}},
		{ID: ipBulkFieldInsert, Kind: stmt.Write, SQL: `INSERT INTO metadata_edit_batch_fields(operation_id,field,action,value,lock_override) VALUES(?,?,?,?,?)`, Params: []stmt.Param{s("owner"), s("field"), s("action"), n("value"), i("lock")}},
		{ID: ipBulkFieldMatch, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_edit_batch_fields WHERE operation_id=? AND field=? AND action=? AND value IS ? AND lock_override=?`, Params: []stmt.Param{s("owner"), s("field"), s("action"), n("value"), i("lock")}},
		{ID: ipBulkTargetInsert, Kind: stmt.Write, SQL: `INSERT INTO metadata_edit_batch_targets(operation_id,ordinal,entity_id,expected_entity_incarnation,expected_revision,state) VALUES(?,?,?,?,?,'captured')`, Params: []stmt.Param{s("owner"), i("ordinal"), i("entity"), b("incarnation"), i("revision")}},
		{ID: ipBulkTargetMatch, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_edit_batch_targets WHERE operation_id=? AND ordinal=? AND entity_id=? AND expected_entity_incarnation=? AND expected_revision=? AND state='captured' AND result_revision IS NULL`, Params: []stmt.Param{s("owner"), i("ordinal"), i("entity"), b("incarnation"), i("revision")}},
		{ID: ipBulkAdvance, Kind: stmt.Write, SQL: `UPDATE metadata_repair_cascades SET descriptive_captured_count=?,descriptive_captured_bytes=?,descriptive_captured_digest=?,job_revision=job_revision+1,last_command_sequence=? WHERE id=? AND command_incarnation=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND status='pending' AND descriptive_input_complete=0 AND descriptive_captured_count=? AND descriptive_captured_bytes=? AND descriptive_captured_digest=? RETURNING 1`, Params: []stmt.Param{i("count_next"), i("bytes_next"), b("chain_next"), i("sequence"), s("owner"), b("incarnation"), i("revision"), i("last"), i("ack"), i("count"), i("bytes"), b("chain")}},
		{ID: ipBulkSeal, Kind: stmt.Write, SQL: `UPDATE metadata_repair_cascades SET descriptive_input_complete=1,status='ready',job_revision=job_revision+1,last_command_sequence=? WHERE id=? AND command_incarnation=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND status='pending' AND descriptive_input_complete=0 AND descriptive_captured_count=descriptive_expected_count AND descriptive_captured_bytes=descriptive_expected_bytes AND descriptive_captured_digest=descriptive_manifest_digest AND NOT EXISTS(SELECT 1 FROM metadata_edit_batch_targets t WHERE t.operation_id=metadata_repair_cascades.id AND t.ordinal>=descriptive_expected_count) RETURNING 1`, Params: []stmt.Param{i("sequence"), s("owner"), b("incarnation"), i("revision"), i("last"), i("ack")}},
		{ID: ipBulkSave, Kind: stmt.Write, SQL: `INSERT INTO metadata_edit_batch_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,after_ordinal,captured_count,processed_count,failed_count,input_complete,manifest_algorithm,manifest_digest) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'portico-ip-descriptive-input-sha256-v1',?)`, Params: []stmt.Param{s("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("applied"), b("result"), s("kind"), i("revision"), i("after"), i("count"), i("processed"), i("failed"), i("complete"), b("manifest")}},
	}
	for j := range defs {
		defs[j].Owner = "writer.ip.edit_bulk"
		defs[j].CallSites = []string{"writer/ip_edit_bulk_native.go"}
		var bindings []any
		for _, p := range defs[j].Params {
			if p.Nullable {
				bindings = append(bindings, nil)
			} else {
				switch p.Type {
				case stmt.Text:
					bindings = append(bindings, "")
				case stmt.Blob:
					bindings = append(bindings, make([]byte, 16))
				default:
					bindings = append(bindings, int64(0))
				}
			}
		}
		defs[j].Cohorts = []stmt.Cohort{{Name: "missing_native_owner", Bindings: bindings}}
	}
	return defs
}
