package writer

import (
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Closed statement catalog for MP-08 (commands 703-705). These IDs are owned by
// this task and are never appended to MP-07's mp.start.* set. Every statement is
// a point operation on the sealed preparation/execution/source rows or on the
// one MP start receipt root; no scan, sort or second receipt root is introduced.

func mpAcceptDefinition(id, sqlText, types string, kind stmt.Kind) stmt.Definition {
	p := make([]stmt.Param, len(types))
	b := make([]any, len(types))
	for i, c := range types {
		p[i].Name = "p" + strconv.Itoa(i)
		switch c {
		case 's':
			p[i].Type = stmt.Text
			b[i] = "fixture"
		case 'i':
			p[i].Type = stmt.Integer
			b[i] = int64(1)
		case 'b':
			p[i].Type = stmt.Blob
			b[i] = make([]byte, 16)
		}
	}
	return stmt.Definition{ID: stmt.ID(id), SQL: sqlText, Kind: kind, Params: p, Owner: "store/writer MP start accept",
		CallSites: []string{"store/writer/mp_start_accept.go", "store/writer/mp_start_accept_receipts.go", "store/writer/mp_start_accept_statements.go", "store/writer/mp_expiry.go"},
		Cohorts:   []stmt.Cohort{{Name: "bounded-terminal-input", Bindings: b}}, Plan: stmt.Plan{Hot: true}}
}

func mpStartAcceptDefinitions() []stmt.Definition {
	defs := []stmt.Definition{
		// One joined point read of the sealed preparation, actor, staged source,
		// queue custody, stable result IDs and (after claim) execution custody.
		mpAcceptDefinition("mp.accept.owner", `SELECT l.state,l.revision,l.execution_id,l.sealed,p.mp_command_incarnation,p.deadline_ms,p.token_expires_ms,p.device_id,p.item_id,p.part_index,p.created_ms,coalesce(p.mp_user_root_id,''),p.replaces_session_id,p.replaces_generation,p.mp_command_last_sequence,p.mp_command_acknowledged_sequence,p.mp_owner_revision,p.mp_receipt_rows,p.mp_receipt_variable_bytes,p.mp_control_variable_bytes,p.mp_retirement_state,a.server_id,a.authority,a.account_id,a.profile_id,a.device_id,a.family_id,a.grant_id,a.session_hash,a.epoch,a.token_generation,a.original_horizon_ms,s.library_id,s.library_source_id,s.asset_id,s.inventory_object_id,s.inventory_revision,s.root_identity,s.incarnation,s.remote_source_id,s.effective_configuration,s.read_policy_witness,s.read_authority_witness,s.library_generation,s.raw_configuration_generation,s.profile_revision,s.size,s.modified_ns,q.queue_id,q.queue_entry_id,q.queue_owner_kind,q.queue_owner_id,q.queue_revision,q.queue_custody_generation,r.session_id,r.legacy_session_id,r.legacy_generation,r.snapshot_id,coalesce(x.custody_media_session_id,''),coalesce(x.physical_exited,0),coalesce(x.cleaned,0) FROM playback_start_preparations AS p INNER JOIN playback_start_preparation_lifecycle AS l ON l.operation_id=p.id INNER JOIN playback_start_preparation_actor AS a ON a.operation_id=p.id INNER JOIN playback_start_preparation_source AS s ON s.operation_id=p.id INNER JOIN playback_start_preparation_queue AS q ON q.operation_id=p.id INNER JOIN playback_start_preparation_results AS r ON r.operation_id=p.id LEFT JOIN mp_start_execution AS x ON x.operation_id=p.id WHERE p.id=? AND p.device_id=?`, "ss", stmt.Read),
		mpAcceptDefinition("mp.accept.actor_current", `SELECT 1 FROM authorization_family_tokens AS t INNER JOIN authorization_session_families AS f ON f.id=t.family_id WHERE t.token_hash=? AND f.id=? AND f.server_id=? AND f.authority=? AND f.account_id=? AND f.profile_id=? AND f.device_id=? AND f.epoch=? AND t.generation=? AND t.generation=f.current_generation AND t.retired=0 AND f.revoked=0 AND f.pin_gate=0`, "sssssssii", stmt.Read),
		mpAcceptDefinition("mp.accept.public_item_current", `SELECT 1 FROM catalog_entities AS e CROSS JOIN catalog_libraries AS c ON c.id=e.library_id CROSS JOIN catalog_assets AS a ON a.library_id=e.library_id CROSS JOIN catalog_asset_links AS l ON l.entity_id=e.id AND l.asset_id=a.id AND l.library_id=e.library_id WHERE e.public_id=? AND c.library_id=? AND a.token=? AND l.part_index=? AND e.retired=0 AND c.retired=0 AND a.available=1 AND l.available=1 AND l.info_pending_change IS NULL`, "bssi", stmt.Read),
		mpAcceptDefinition("mp.accept.source_current", `SELECT 1 FROM inventory_objects AS o INNER JOIN library_sources AS s ON s.id=o.source_id INNER JOIN catalog_assets AS a ON a.token=o.asset_id WHERE o.id=? AND o.source_id=? AND o.asset_id=? AND o.revision=? AND o.root_incarnation=? AND o.size=? AND o.modified_ns=? AND o.retired=0 AND o.state='available' AND s.library_id=? AND s.root_identity=? AND s.generation=? AND coalesce(s.remote_source_id,'')=? AND s.incarnation=o.root_incarnation AND s.enabled=1 AND s.identity_confirmed=1 AND s.health='ready' AND a.available=1 AND a.info_publication_pending=0 AND a.size=o.size AND a.modified_ns=o.modified_ns`, "sssssiissis", stmt.Read),
		mpAcceptDefinition("mp.accept.queue_current", `SELECT 1 FROM queues_v1 WHERE id=? AND owner_kind=? AND owner_id=? AND revision=? AND custody_generation=? AND current_entry_id=? AND authority=? AND account_id=? AND profile_id=?`, "sssiissss", stmt.Read),
		mpAcceptDefinition("mp.accept.replacement", `SELECT 1 FROM playback_v1_sessions WHERE id=? AND authority=? AND account_id=? AND profile_id=? AND device_id=? AND generation=? AND ended_ms=0`, "sssssi", stmt.Read),
		// One physical custody transfer from the claimed execution into the stable
		// session id already reserved by the preparation. No playback session,
		// mp_media_inputs row or second public 710 birth is created here.
		mpAcceptDefinition("mp.accept.custody", `UPDATE mp_start_execution SET custody_media_session_id=? WHERE operation_id=? AND execution_id=? AND custody_media_session_id='' AND physical_exited=0 AND cleaned=0 AND LENGTH(result_digest)=64 AND staged_rows>0 AND staged_bytes>0`, "sss", stmt.Write),
		mpAcceptDefinition("mp.accept.lifecycle", `UPDATE playback_start_preparation_lifecycle SET state='accepted',revision=revision+1 WHERE operation_id=? AND revision=? AND execution_id=? AND state='prepared' AND sealed=1`, "sis", stmt.Write),
		mpAcceptDefinition("mp.accept.retire", `UPDATE playback_start_preparation_lifecycle SET state='retired',revision=revision+1 WHERE operation_id=? AND revision=? AND execution_id=? AND state IN('pending','preparing','prepared','failed')`, "sis", stmt.Write),
		// Expiry retires the preparation only. The execution row and its staged
		// debt survive untouched: no timeout frees physical ownership.
		mpAcceptDefinition("mp.accept.timeout", `UPDATE playback_start_preparation_lifecycle SET state='retired',revision=revision+1 WHERE operation_id=? AND revision=? AND execution_id=? AND state IN('pending','preparing','prepared','failed')`, "sis", stmt.Write),
		mpAcceptDefinition("mp.accept.timeout_due", `SELECT deadline_ms,id FROM playback_start_preparations INDEXED BY playback_start_preparations_deadline WHERE deadline_ms=? AND id=? LIMIT 1`, "is", stmt.Read),
		mpAcceptDefinition("mp.accept.native", `SELECT l.state,l.revision,l.execution_id,l.sealed,p.deadline_ms,coalesce(x.custody_media_session_id,''),coalesce(x.physical_exited,0),coalesce(x.cleaned,0),r.session_id,r.legacy_session_id,r.legacy_generation,r.snapshot_id FROM playback_start_preparations AS p INNER JOIN playback_start_preparation_lifecycle AS l ON l.operation_id=p.id INNER JOIN playback_start_preparation_results AS r ON r.operation_id=p.id LEFT JOIN mp_start_execution AS x ON x.operation_id=p.id WHERE p.id=?`, "s", stmt.Read),
		mpAcceptDefinition("mp.accept.receipt_header", `SELECT operation_id,command_kind,request_digest,result_version,result_count,result_digest,applied_ms,length(result_payload),variable_bytes FROM mp_start_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read),
		mpAcceptDefinition("mp.accept.receipt_payload", `SELECT result_payload FROM mp_start_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read),
		mpAcceptDefinition("mp.accept.receipt_insert", `INSERT INTO mp_start_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,command_kind,request_digest,result_version,result_count,result_digest,result_payload,applied_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, "sbibibiibbi", stmt.Write),
		mpAcceptDefinition("mp.accept.debt", `UPDATE playback_start_preparations INDEXED BY mp_start_command_owner SET mp_command_last_sequence=?,mp_owner_revision=mp_owner_revision+1,mp_receipt_rows=mp_receipt_rows+1,mp_receipt_variable_bytes=mp_receipt_variable_bytes+? WHERE id=? AND mp_command_incarnation=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_owner_revision=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_control_variable_bytes=? AND mp_retirement_state='live' RETURNING mp_command_last_sequence,mp_owner_revision,mp_receipt_rows,mp_receipt_variable_bytes,mp_control_variable_bytes`, "iisbiiiiii", stmt.Write),
	}
	for i := range defs {
		switch defs[i].ID {
		case "mp.accept.owner", "mp.accept.native":
			defs[i].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_start_preparations_1"}, Seeks: []string{"id=?"}}
		case "mp.accept.actor_current":
			defs[i].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_family_tokens_1"}}
		case "mp.accept.public_item_current":
			defs[i].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_entities_1"}, Seeks: []string{"public_id=?"}}
		case "mp.accept.source_current":
			defs[i].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_assets_1"}}
		case "mp.accept.queue_current":
			defs[i].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_queues_v1_1"}}
		case "mp.accept.replacement":
			defs[i].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_v1_sessions_1"}, Seeks: []string{"id=?"}}
		case "mp.accept.custody":
			defs[i].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_mp_start_execution_2"}, Seeks: []string{"execution_id=?"}}
		case "mp.accept.lifecycle", "mp.accept.retire", "mp.accept.timeout":
			defs[i].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_start_preparation_lifecycle_1"}, Seeks: []string{"operation_id=?"}}
		case "mp.accept.timeout_due":
			defs[i].Plan = stmt.Plan{Hot: true, Indexes: []string{"playback_start_preparations_deadline"}, Seeks: []string{"deadline_ms=? AND id=?"}}
		case "mp.accept.receipt_header", "mp.accept.receipt_payload":
			defs[i].Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"mp_start_command_receipts"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=?"}}
		case "mp.accept.debt":
			defs[i].Plan = stmt.Plan{Hot: true, Indexes: []string{"mp_start_command_owner"}}
		}
	}
	return defs
}
