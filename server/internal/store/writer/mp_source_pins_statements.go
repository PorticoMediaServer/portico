package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func mpSourcePinsDefinitions() []stmt.Definition {
	specs := []struct {
		id, sql, types string
		kind           stmt.Kind
	}{
		{"mp.pins.bump", `UPDATE playback_sessions SET mp_revision=mp_revision+1 WHERE id=? AND mp_revision=? AND generation=? AND mp_execution_id=? RETURNING 1`, "siis", stmt.Write},
		{"mp.pins.prepared_source_current", `SELECT 1 FROM prepared_media_versions AS v CROSS JOIN mp_job_inputs AS i ON i.job_id=v.job_id WHERE v.id=? AND i.library_id=? AND i.library_source_id=? AND i.asset_id=? AND i.inventory_object_id=? AND i.inventory_revision=? AND i.root_identity=? AND i.incarnation=? AND i.remote_source_id=? AND i.effective_configuration=? AND i.read_policy_witness=? AND i.read_authority_witness=? AND i.library_generation=? AND i.raw_configuration_generation=? AND i.profile_revision=? AND i.size=? AND i.modified_ns=?`, "ssssssssssssiiiii", stmt.Read},
		{"mp.pins.prepared_insert", `INSERT INTO prepared_media_session_pins(session_id,version_id,digest,size) SELECT ?,v.id,v.digest,v.size FROM prepared_media_versions AS v WHERE v.id=? AND v.digest=? AND v.size=? AND v.item_id=? AND v.asset_id=? AND v.library_id=? AND v.source_revision=? AND v.profile_id=? AND v.target_id=? AND v.state='published' RETURNING 1`, "sssiisssss", stmt.Write},
		{"mp.pins.subtitle_insert", `INSERT INTO playback_subtitle_pins(session_id,resource_id,resource_revision) SELECT ?,r.id,v.revision FROM subtitle_resources AS r CROSS JOIN subtitle_revisions AS v ON v.resource_id=r.id AND v.revision=r.current_revision WHERE r.id=? AND v.revision=? AND v.digest=? AND v.size=? AND r.item_id=? AND r.source_id=? AND r.deleted=0 AND (r.scope='shared' OR (r.scope='personal' AND r.owner=?)) AND v.source_size=? AND v.source_modified_ns=? RETURNING 1`, "ssisiissii", stmt.Write},
		{"mp.pins.disc_insert", `INSERT INTO playback_disc_pins(session_id,object_id,title_key,source_revision,fingerprint,stream_revision,chapter_revision,title_facts_revision,timeline) SELECT ?,d.object_id,?,d.source_revision,d.fingerprint,?,?,?,? FROM inventory_disc_facts AS d CROSS JOIN inventory_disc_titles AS t ON t.object_id=d.object_id CROSS JOIN inventory_disc_title_media AS m ON m.object_id=t.object_id AND m.title_key=t.title_key WHERE d.object_id=? AND d.source_revision=? AND d.fingerprint=? AND d.asset_id=? AND t.title_key=? AND m.revision=? AND m.source_revision=d.source_revision AND m.fingerprint=d.fingerprint RETURNING 1`, "ssiissssssss", stmt.Write},
		{"mp.pins.grant_rotate", `UPDATE playback_sessions SET grant_hash=?,grant_token=?,expires_at=?,mp_revision=mp_revision+1 WHERE id=? AND mp_revision=? AND generation=? AND mp_execution_id=? AND grant_hash=? AND mp_grant_retired=0 AND mp_retired_generation<generation AND mp_exited_generation<generation RETURNING 1`, "ssssiiss", stmt.Write},
		{"mp.pins.receiver_insert", `INSERT INTO playback_private_presentations(session_id,owner_session_id,expires_ms) SELECT ?,v.id,? FROM playback_sessions AS v WHERE v.id=? AND v.generation=? AND v.account_id=? AND v.profile_id=? AND v.mp_retired_generation<v.generation AND v.mp_exited_generation<v.generation AND unixepoch(v.expires_at,'subsec')*1000>=? RETURNING 1`, "sisissi", stmt.Write},
		{"mp.pins.no_readers", `SELECT 1 FROM playback_sessions AS p WHERE p.id=? AND NOT EXISTS(SELECT 1 FROM mp_media_read_pins AS r WHERE r.media_session_id=p.id)`, "s", stmt.Read},
		{"mp.pins.inventory_delete", `DELETE FROM playback_source_pins WHERE session_id=? AND asset_id=? AND size=? AND modified_ns=? AND source_id=? AND inventory_object_id=? RETURNING 1`, "ssiiss", stmt.Write},
		{"mp.pins.physical_delete", `DELETE FROM playback_physical_source_pins WHERE session_id=? AND source_id=? AND incarnation=? AND configuration_generation=? RETURNING 1`, "sssi", stmt.Write},
		{"mp.pins.prepared_delete", `DELETE FROM prepared_media_session_pins WHERE session_id=? AND version_id=? AND digest=? AND size=? RETURNING 1`, "sssi", stmt.Write},
		{"mp.pins.subtitle_delete", `DELETE FROM playback_subtitle_pins WHERE session_id=? AND resource_id=? AND resource_revision=? RETURNING 1`, "ssi", stmt.Write},
		{"mp.pins.disc_delete", `DELETE FROM playback_disc_pins WHERE session_id=? AND title_key=? AND fingerprint=? AND stream_revision=? RETURNING 1`, "sssi", stmt.Write},
		{"mp.pins.receiver_delete", `DELETE FROM playback_private_presentations WHERE session_id=? AND owner_session_id=? AND expires_ms=? RETURNING 1`, "ssi", stmt.Write},
		{"mp.pins.remote_delete", `DELETE FROM mp_remote_delivery_inputs WHERE media_session_id=? AND origin_version=? RETURNING 1`, "ss", stmt.Write},
		{"mp.pins.remote_evidence_delete", `DELETE FROM subtitle_remote_sessions WHERE session_id=? AND evidence=(SELECT evidence FROM mp_remote_delivery_inputs WHERE media_session_id=subtitle_remote_sessions.session_id AND origin_version=?) RETURNING 1`, "ss", stmt.Write},
		{"mp.pins.grant_retire", `UPDATE playback_sessions SET mp_grant_retired=1,mp_retire_request_id=CASE WHEN mp_retired_generation<generation THEN ? ELSE mp_retire_request_id END,mp_retired_generation=generation,state='stopped',mp_revision=mp_revision+1 WHERE id=? AND mp_revision=? AND generation=? AND mp_execution_id=? AND grant_hash=? AND mp_grant_retired=0 RETURNING 1`, "ssiiss", stmt.Write},
		{"mp.pins.remote_insert", `INSERT INTO mp_remote_delivery_inputs(media_session_id,origin_version,descriptor_version,evidence,policy_digest,endpoint_digest,size) VALUES(?,?,?,?,?,?,?) RETURNING 1`, "ssssssi", stmt.Write},
		{"mp.pins.remote_subtitle_evidence", `INSERT INTO subtitle_remote_sessions(session_id,evidence) VALUES(?,?) RETURNING 1`, "ss", stmt.Write},
	}
	var d []stmt.Definition
	for _, s := range specs {
		v := mpPrepDefinition(s.id, s.sql, s.types, s.kind)
		v.CallSites = []string{"store/writer/mp_source_pins.go"}
		d = append(d, v)
	}
	return d
}
