package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// The descriptive-bulk terminal owns this closed statement set: the exact
// retained owner header, its own finish receipt, the raw-tail emptiness check
// and the truthful applied/stale summary. No page or per-target edit statement
// is repeated here.
const (
	ipBulkFinishOwner        stmt.ID = "ip.bulk.finish.owner"
	ipBulkFinishReceipt      stmt.ID = "ip.bulk.finish.receipt"
	ipBulkFinishReceiptHead  stmt.ID = "ip.bulk.finish.receipt_head"
	ipBulkFinishRaw          stmt.ID = "ip.bulk.finish.raw_targets"
	ipBulkFinishTargets      stmt.ID = "ip.bulk.finish.targets"
	ipBulkFinishWrite        stmt.ID = "ip.bulk.finish.terminal"
	ipBulkFinishReceiptWrite stmt.ID = "ip.bulk.finish.receipt_insert"
)

func ipEditBulkFinishDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: ipBulkFinishOwner, Kind: stmt.Read, SQL: `SELECT command_incarnation,job_revision,last_command_sequence,acknowledged_command_sequence,after_item,processed,failed,status,intent,kind,entity_id,library_id,actor,base_revision,descriptive_expected_count,descriptive_captured_count,descriptive_input_complete,descriptive_manifest_algorithm,descriptive_manifest_digest,descriptive_expected_bytes,descriptive_captured_bytes,descriptive_captured_digest FROM metadata_repair_cascades WHERE id=?`, Params: []stmt.Param{s("owner")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_repair_cascades"}}},
		{ID: ipBulkFinishReceipt, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'finish' THEN 1 ELSE 0 END,accepted_revision,after_ordinal,captured_count,processed_count,failed_count,input_complete,manifest_algorithm,manifest_digest,applied_ms FROM metadata_edit_batch_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{s("owner"), b("incarnation"), i("sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_batch_receipts"}}},
		{ID: ipBulkFinishReceiptHead, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'finish' THEN 1 ELSE 0 END FROM metadata_edit_batch_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{s("owner"), b("incarnation"), i("sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_batch_receipts"}}},
		{ID: ipBulkFinishRaw, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_edit_batch_targets WHERE operation_id=? AND ordinal>=? ORDER BY ordinal LIMIT 1`, Params: []stmt.Param{s("owner"), i("after")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_batch_targets"}}},
		{ID: ipBulkFinishTargets, Kind: stmt.Read, SQL: `SELECT count(*),coalesce(sum(CASE state WHEN 'applied' THEN 1 ELSE 0 END),0),coalesce(sum(CASE state WHEN 'stale' THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN state='captured' THEN 1 ELSE 0 END),0) FROM metadata_edit_batch_targets WHERE operation_id=?`, Params: []stmt.Param{s("owner")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_batch_targets"}}},
		{ID: ipBulkFinishWrite, Kind: stmt.Write, SQL: `UPDATE metadata_repair_cascades SET status='complete',last_command_sequence=?,job_revision=job_revision+1 WHERE id=? AND command_incarnation=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND after_item=? AND processed=? AND failed=? AND status IN('ready','running') AND intent='descriptive-bulk-v1' AND descriptive_input_complete=1 AND processed+failed=descriptive_expected_count AND NOT EXISTS(SELECT 1 FROM metadata_edit_batch_targets AS t WHERE t.operation_id=metadata_repair_cascades.id AND t.ordinal>=processed+failed) RETURNING 1`, Params: []stmt.Param{i("sequence"), s("owner"), b("incarnation"), i("revision"), i("last"), i("ack"), s("after"), i("processed"), i("failed")}},
		{ID: ipBulkFinishReceiptWrite, Kind: stmt.Write, SQL: `INSERT INTO metadata_edit_batch_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,after_ordinal,captured_count,processed_count,failed_count,input_complete,manifest_algorithm,manifest_digest) VALUES(?,?,?,?,?,?,?,'finish',?,?,?,?,?,1,?,?) RETURNING 1`, Params: []stmt.Param{s("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("applied"), b("result"), i("revision"), i("after"), i("captured"), i("processed"), i("failed"), s("algorithm"), b("manifest")}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer IP descriptive bulk finish"
		defs[n].CallSites = []string{"store/writer/ip_edit_bulk_finish.go"}
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
