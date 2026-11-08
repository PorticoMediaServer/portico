package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	mpRepeatExistingStop  stmt.ID = "mp.repeat.existing_stop"
	mpRepeatPreparedKey   stmt.ID = "mp.repeat.next_key"
	mpRepeatStopBytes     stmt.ID = "mp.repeat.stop_bytes"
	mpWorkerOwner         stmt.ID = "mp.worker.owner"
	mpWorkerHeader        stmt.ID = "mp.worker.header"
	mpWorkerPayload       stmt.ID = "mp.worker.payload"
	mpWorkerInsert        stmt.ID = "mp.worker.insert"
	mpWorkerDebt          stmt.ID = "mp.worker.debt"
	mpRepeatCurrent       stmt.ID = "mp.repeat.current"
	mpRepeatPreparedBytes stmt.ID = "mp.repeat.next_bytes"
	mpRepeatPrepared      stmt.ID = "mp.repeat.next"
	mpRepeatStop          stmt.ID = "mp.repeat.stop"
	mpRepeatObligation    stmt.ID = "mp.repeat.obligation"
	mpRepeatCancel        stmt.ID = "mp.repeat.cancel"
	mpRepeatUpdate        stmt.ID = "mp.repeat.update"
)

func mpWorkerDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: mpWorkerOwner, Kind: stmt.Read, SQL: `SELECT q.mp_command_incarnation,q.mp_command_last_sequence,q.mp_command_acknowledged_sequence,q.mp_owner_revision,q.mp_receipt_rows,q.mp_receipt_variable_bytes,q.mp_control_variable_bytes,q.mp_retirement_state FROM queues_v1 AS q INDEXED BY sqlite_autoindex_queues_v1_1 INNER JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=? WHERE q.id=? AND q.authority='local' AND q.account_id=? AND q.profile_id=? AND q.owner_kind='device' AND q.owner_id=? AND f.device_id=q.owner_id AND q.mp_owner_member_incarnation=? AND q.mp_owner_profile_lifetime=?`, Params: []stmt.Param{t("family"), t("queue"), t("account"), t("profile"), t("device"), t("member"), t("profile_lifetime")}},
		{ID: mpWorkerHeader, Kind: stmt.Read, SQL: `SELECT operation_id,command_kind,request_digest,result_version,result_count,result_digest,applied_ms,length(result_payload),variable_bytes FROM mp_queue_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{t("queue"), b("incarnation"), i("sequence")}},
		{ID: mpWorkerPayload, Kind: stmt.Read, SQL: `SELECT result_payload FROM mp_queue_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{t("queue"), b("incarnation"), i("sequence")}},
		{ID: mpWorkerInsert, Kind: stmt.Write, SQL: `INSERT INTO mp_queue_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,command_kind,request_digest,result_version,result_count,result_digest,result_payload,applied_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, Params: []stmt.Param{t("queue"), b("incarnation"), i("sequence"), b("operation"), i("kind"), b("request"), i("version"), i("count"), b("result"), b("payload"), i("applied")}},
		{ID: mpWorkerDebt, Kind: stmt.Write, SQL: `UPDATE queues_v1 INDEXED BY mp_queue_command_owner SET mp_command_last_sequence=?,mp_owner_revision=mp_owner_revision+1,mp_receipt_rows=mp_receipt_rows+1,mp_receipt_variable_bytes=mp_receipt_variable_bytes+? WHERE id=? AND mp_command_incarnation=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_owner_revision=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_control_variable_bytes=? AND mp_retirement_state='live' RETURNING mp_command_last_sequence,mp_owner_revision,mp_receipt_rows,mp_receipt_variable_bytes,mp_control_variable_bytes`, Params: []stmt.Param{i("sequence"), i("bytes"), t("queue"), b("incarnation"), i("last"), i("ack"), i("owner_revision"), i("rows"), i("old_bytes"), i("control_bytes")}},
		{ID: mpRepeatCurrent, Kind: stmt.Read, SQL: `SELECT repeat,updated_ms FROM queues_v1 WHERE id=? AND revision=? AND custody_generation=? AND mp_retirement_state='live'`, Params: []stmt.Param{t("queue"), i("revision"), i("custody")}},
		{ID: mpRepeatPreparedKey, Kind: stmt.Read, SQL: `SELECT substr(token,1,129) FROM playback_v1_prepared INDEXED BY playback_v1_prepared_queue WHERE queue_id=? AND state='prepared' LIMIT 2`, Params: []stmt.Param{t("queue")}},
		{ID: mpRepeatPreparedBytes, Kind: stmt.Read, SQL: `SELECT octet_length(token),octet_length(media_session_id) FROM playback_v1_prepared INDEXED BY sqlite_autoindex_playback_v1_prepared_1 WHERE token=? AND queue_id=? AND state='prepared'`, Params: []stmt.Param{t("next"), t("queue")}},
		{ID: mpRepeatPrepared, Kind: stmt.Read, SQL: `SELECT token,media_session_id,mp_media_generation FROM playback_v1_prepared WHERE token=? AND queue_id=? AND state='prepared'`, Params: []stmt.Param{t("next"), t("queue")}},
		{ID: mpRepeatStopBytes, Kind: stmt.Read, SQL: `SELECT octet_length(mp_retire_request_id),mp_retired_generation,mp_exited_generation FROM playback_sessions INDEXED BY sqlite_autoindex_playback_sessions_1 WHERE id=? AND generation=?`, Params: []stmt.Param{t("media"), i("generation")}},
		{ID: mpRepeatStop, Kind: stmt.Write, SQL: `UPDATE playback_sessions SET mp_retired_generation=generation,mp_retire_request_id=? WHERE id=? AND generation=? AND account_id=? AND profile_id=? AND mp_retired_generation=0 AND mp_exited_generation=0 AND mp_cleanup_complete=0 RETURNING mp_retire_request_id,mp_exited_generation`, Params: []stmt.Param{t("stop_request"), t("media"), i("generation"), t("account"), t("profile")}},
		{ID: mpRepeatExistingStop, Kind: stmt.Read, SQL: `SELECT mp_retire_request_id,mp_exited_generation FROM playback_sessions WHERE id=? AND generation=? AND account_id=? AND profile_id=? AND mp_retired_generation=generation AND mp_retire_request_id<>''`, Params: []stmt.Param{t("media"), i("generation"), t("account"), t("profile")}},
		{ID: mpRepeatObligation, Kind: stmt.Write, SQL: `INSERT INTO mp_next_stop_obligations(next_id,media_session_id,media_generation,request_id,reason,requested_ms) VALUES(?,?,?,?,?,?) RETURNING variable_bytes`, Params: []stmt.Param{t("next"), t("media"), i("generation"), t("stop_request"), t("reason"), i("at")}},
		{ID: mpRepeatCancel, Kind: stmt.Write, SQL: `UPDATE playback_v1_prepared SET state='canceled',cancel_reason='queue_changed' WHERE token=? AND queue_id=? AND media_session_id=? AND mp_media_generation=? AND state='prepared' RETURNING token`, Params: []stmt.Param{t("next"), t("queue"), t("media"), i("generation")}},
		{ID: mpRepeatUpdate, Kind: stmt.Write, SQL: `UPDATE queues_v1 SET repeat=?,updated_ms=?,revision=revision+1 WHERE id=? AND revision=? AND custody_generation=? AND repeat=? RETURNING revision,updated_ms`, Params: []stmt.Param{t("repeat"), i("at"), t("queue"), i("revision"), i("custody"), t("old_repeat")}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer MP worker repeat"
		defs[n].CallSites = []string{"store/writer/mp_worker_receipt.go", "store/writer/mp_queue_repeat.go"}
		defs[n].Plan = stmt.Plan{Hot: true}
		switch defs[n].ID {
		case mpWorkerOwner:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_queues_v1_1", "sqlite_autoindex_authorization_session_families_1"}
			for _, table := range []string{"queues_v1", "authorization_session_families"} {
				defs[n].Plan.AutoIndexes = append(defs[n].Plan.AutoIndexes, stmt.AutoIndexBinding{Table: table, Name: "sqlite_autoindex_" + table + "_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}})
			}
		case mpWorkerHeader, mpWorkerPayload:
			defs[n].Plan.PrimaryKeys = []string{"mp_queue_command_receipts"}
		case mpWorkerDebt:
			defs[n].Plan.Indexes = []string{"mp_queue_command_owner"}
		case mpRepeatCurrent, mpRepeatUpdate:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_queues_v1_1"}
		case mpRepeatPreparedKey:
			defs[n].Plan.Indexes = []string{"playback_v1_prepared_queue"}
		case mpRepeatPreparedBytes, mpRepeatPrepared:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_playback_v1_prepared_1"}
		case mpRepeatStopBytes, mpRepeatStop, mpRepeatExistingStop:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_playback_sessions_1"}
		case mpRepeatCancel:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_playback_v1_prepared_1"}
		}

		if defs[n].ID == mpRepeatPreparedBytes || defs[n].ID == mpRepeatStopBytes {
			table, key := "playback_v1_prepared", "token"
			if defs[n].ID == mpRepeatStopBytes {
				table, key = "playback_sessions", "id"
			}
			defs[n].Plan.AutoIndexes = []stmt.AutoIndexBinding{{Table: table, Name: "sqlite_autoindex_" + table + "_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: key, Collation: "BINARY"}}}}
		}

		args := []any{}
		for _, p := range defs[n].Params {
			switch p.Type {
			case stmt.Text:
				args = append(args, "mp-fixture")
			case stmt.Blob:
				args = append(args, make([]byte, 16))
			default:
				args = append(args, int64(1))
			}
		}
		defs[n].Cohorts = []stmt.Cohort{{Name: "bounded-key", Bindings: args}}
	}
	// Reuse the reviewed current native actor/family proof verbatim. No WC
	// playback/personal scope or mutable source eligibility is borrowed.
	for _, d := range wcProgressProtocolDefinitions() {
		switch d.ID {
		case wcProgressNativeScope, wcProgressNativeProfileScope, wcProgressNativeToken:
			defs = append(defs, d)
		}
	}
	return append(defs, wcAPIEventDefinitions()...)
}
func mpWorkerStatementMaxUses(id stmt.ID) int {
	if id == wcAPIEventInsert || id == wcAPIEventOldest || id == wcAPIEventDelete {
		return 2
	}
	for _, d := range mpWorkerDefinitions() {
		if d.ID == id {
			return 1
		}
	}
	return 0
}
