package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	ipRefreshOwner           stmt.ID = "ip.refresh.owner"
	ipRefreshOctets          stmt.ID = "ip.refresh.octets"
	ipRefreshCurrent         stmt.ID = "ip.refresh.current"
	ipRefreshReceiptIdentity stmt.ID = "ip.refresh.receipt_identity"
	ipRefreshReceiptOctets   stmt.ID = "ip.refresh.receipt_octets"
	ipRefreshReceiptResult   stmt.ID = "ip.refresh.receipt_result"
	ipRefreshReceiptInsert   stmt.ID = "ip.refresh.receipt_insert"
	ipRefreshRequestUpdate   stmt.ID = "ip.refresh.request_update"
	ipRefreshPageUpdate      stmt.ID = "ip.refresh.page_update"
	ipRefreshFinishUpdate    stmt.ID = "ip.refresh.finish_entities_update"
	ipRefreshRaw             stmt.ID = "ip.refresh.raw"
	ipRefreshNext            stmt.ID = "ip.refresh.next"
	ipRefreshWaiting         stmt.ID = "ip.refresh.waiting"
	ipRefreshCandidate       stmt.ID = "ip.refresh.candidate"
	ipRefreshSource          stmt.ID = "ip.refresh.source"
	ipRefreshSchedule        stmt.ID = "ip.refresh.schedule"
	ipRefreshProvider        stmt.ID = "ip.refresh.provider"
	ipRefreshBudget          stmt.ID = "ip.refresh.budget"
	ipRefreshBudgetDebit     stmt.ID = "ip.refresh.budget_debit"
)

func ipRefreshDefinitions() []stmt.Definition {
	return []stmt.Definition{
		ipPolicyDefinition(ipRefreshOwner, stmt.Read, `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM metadata_refresh_sweeps WHERE provider=? AND purpose=?`, "tt"),
		ipPolicyDefinition(ipRefreshOctets, stmt.Read, `SELECT octet_length(phase) FROM metadata_refresh_sweeps WHERE provider=? AND purpose=?`, "tt"),
		ipPolicyDefinition(ipRefreshCurrent, stmt.Read, `SELECT generation,revision,epoch_ms,phase,after_id,page_from_id,page_until_id,page_waiting,job_lease_owner,job_lease_generation,job_lease_expires_ms FROM metadata_refresh_sweeps WHERE provider=? AND purpose=?`, "tt"),
		ipPolicyDefinition(ipRefreshReceiptIdentity, stmt.Read, `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'request' THEN 201 WHEN 'page' THEN 202 WHEN 'finish' THEN 202 END FROM metadata_refresh_receipts WHERE provider=? AND purpose=? AND command_incarnation=? AND command_sequence=?`, "ttbi"),
		ipPolicyDefinition(ipRefreshReceiptOctets, stmt.Read, `SELECT octet_length(phase) FROM metadata_refresh_receipts WHERE provider=? AND purpose=? AND command_incarnation=? AND command_sequence=?`, "ttbi"),
		ipPolicyDefinition(ipRefreshReceiptResult, stmt.Read, `SELECT accepted_revision,generation,epoch_ms,phase,after_id,scheduled_count,page_waiting,applied_ms FROM metadata_refresh_receipts WHERE provider=? AND purpose=? AND command_incarnation=? AND command_sequence=?`, "ttbi"),
		ipPolicyDefinition(ipRefreshReceiptInsert, stmt.Write, `INSERT INTO metadata_refresh_receipts(provider,purpose,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,generation,epoch_ms,phase,after_id,scheduled_count,page_waiting) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "ttbibbibtiiitiii"),
		ipPolicyDefinition(ipRefreshRequestUpdate, stmt.Write, `UPDATE metadata_refresh_sweeps SET generation=?,revision=?,epoch_ms=?,phase='entities',after_id=0,page_from_id=0,page_until_id=0,page_waiting=0,job_lease_owner=0,job_lease_generation=job_lease_generation+1,job_lease_expires_ms=0,job_next_attempt_ms=?,last_command_sequence=? WHERE provider=? AND purpose=? AND generation=? AND revision=? AND after_id=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=?`, "iiiiittiiiiiibii"),
		ipPolicyDefinition(ipRefreshPageUpdate, stmt.Write, `UPDATE metadata_refresh_sweeps SET revision=?,after_id=?,page_from_id=?,page_until_id=?,page_waiting=?,last_command_sequence=? WHERE provider=? AND purpose=? AND generation=? AND revision=? AND after_id=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND phase='entities'`, "iiiiiittiiiiiibii"),
		ipPolicyDefinition(ipRefreshFinishUpdate, stmt.Write, `UPDATE metadata_refresh_sweeps SET revision=?,phase='people',page_waiting=0,last_command_sequence=? WHERE provider=? AND purpose=? AND generation=? AND revision=? AND after_id=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND phase='entities'`, "iittiiiiiibii"),
		ipPolicyDefinition(ipRefreshRaw, stmt.Read, `SELECT entity_id,generation,revision FROM metadata_acquisition_jobs INDEXED BY metadata_acquisition_sweep WHERE provider=? AND purpose=? AND entity_id>? ORDER BY entity_id LIMIT 16`, "tti"),
		ipPolicyDefinition(ipRefreshNext, stmt.Read, `SELECT entity_id FROM metadata_acquisition_jobs INDEXED BY metadata_acquisition_sweep WHERE provider=? AND purpose=? AND entity_id>? ORDER BY entity_id LIMIT 1`, "tti"),
		// LIMIT applies to RAW keys; filtering in a WHERE would turn excluded
		// candidates into an unbounded scan. Row 17 is an insertion-drift proof.
		ipPolicyDefinition(ipRefreshWaiting, stmt.Read, `SELECT entity_id,CASE WHEN status='pending' AND next_attempt_ms<=? AND lease='' THEN 1 ELSE 0 END FROM metadata_acquisition_jobs INDEXED BY metadata_acquisition_sweep WHERE provider=? AND purpose=? AND entity_id>? AND entity_id<=? ORDER BY entity_id LIMIT 17`, "ittii"),
		ipPolicyDefinition(ipRefreshCandidate, stmt.Read, `SELECT e.public_id,e.revision,e.metadata_incarnation,e.source_revision,e.identity_revision,e.artwork_incarnation,e.artwork_revision,p.incarnation,p.revision FROM metadata_acquisition_jobs j JOIN catalog_entities e ON e.id=j.entity_id JOIN catalog_libraries l ON l.id=e.library_id JOIN metadata_provider_policy p ON p.library_id=e.library_id AND p.provider=j.provider WHERE j.provider=? AND j.purpose=? AND j.entity_id=? AND j.generation=? AND j.revision=? AND j.status='matched' AND j.selected_id<>'' AND j.lease='' AND j.lease_until_ms<=? AND j.next_attempt_ms<=? AND e.retired=0 AND l.retired=0 AND p.enabled=1`, "ttiiiii"),
		ipPolicyDefinition(ipRefreshSource, stmt.Read, `SELECT 1 FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id JOIN metadata_provider_policy p ON p.library_id=e.library_id AND p.provider=? WHERE e.id=? AND e.public_id=? AND e.revision=? AND e.metadata_incarnation=? AND e.source_revision=? AND e.identity_revision=? AND e.artwork_incarnation=? AND e.artwork_revision=? AND p.incarnation=? AND p.revision=? AND e.retired=0 AND l.retired=0 AND p.enabled=1`, "tibibiibibi"),
		ipPolicyDefinition(ipRefreshSchedule, stmt.Write, `UPDATE metadata_acquisition_jobs SET status='pending',generation=generation+1,revision=revision+1,force_refresh=1,attempts=0,next_attempt_ms=?,error='' WHERE entity_id=? AND provider=? AND purpose=? AND generation=? AND revision=? AND status='matched' AND selected_id<>'' AND lease='' AND lease_until_ms<=? AND next_attempt_ms<=?`, "iittiiii"),
		ipPolicyDefinition(ipRefreshProvider, stmt.Read, `SELECT enabled,next_attempt_ms FROM metadata_providers WHERE code=?`, "t"),
		ipPolicyDefinition(ipRefreshBudget, stmt.Read, `SELECT window_start,spent FROM metadata_refresh_budget WHERE provider=? AND span=?`, "tt"),
		ipPolicyDefinition(ipRefreshBudgetDebit, stmt.Write, `INSERT INTO metadata_refresh_budget(provider,span,window_start,spent) VALUES(?,?,?,?) ON CONFLICT(provider,span) DO UPDATE SET window_start=excluded.window_start,spent=excluded.spent WHERE window_start=? AND spent=?`, "ttiiii"),
	}
}
