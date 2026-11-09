package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func mpSessionStopDefinitions() []stmt.Definition {
	out := mpSessionParentDrainDefinitions()
	for _, d := range wcProgressProtocolDefinitions() {
		if d.ID == wcProgressNativeScope || d.ID == wcProgressNativeProfileScope || d.ID == wcProgressNativeToken {
			out = append(out, d)
		}
	}
	out = append(out, mpPhysicalDefinitions()...)
	add := func(id stmt.ID, sql, kinds string, index, table, key string) {
		d := stmt.Definition{ID: id, SQL: sql, Kind: stmt.Read, Owner: "store/writer MP retirement", CallSites: []string{"store/writer/mp_session_stop.go"}, Plan: stmt.Plan{Hot: true, Indexes: []string{index}}}
		if id == mpSessionStopRequest || id == mpSessionStopLogical {
			d.Kind = stmt.Write
		}
		args := []any{}
		for i, c := range kinds {
			p := stmt.Param{Name: string(rune('a' + i)), Type: stmt.Text}
			var v any = "missing"
			if c == 'i' {
				p.Type = stmt.Integer
				v = int64(1)
			}
			d.Params = append(d.Params, p)
			args = append(args, v)
		}
		d.Cohorts = []stmt.Cohort{{Name: "empty", Bindings: args}}
		if key != "" {
			d.Plan.AutoIndexes = []stmt.AutoIndexBinding{{Table: table, Name: index, Origin: "pk", Columns: []stmt.IndexColumn{{Name: key, Collation: "BINARY"}}}}
		}
		out = append(out, d)
	}
	add(mpSessionStopCurrent, `SELECT ended_ms,lease_expires_ms FROM playback_v1_sessions INDEXED BY sqlite_autoindex_playback_v1_sessions_1 WHERE id=? AND revision=? AND generation=? AND media_session_id=? AND media_generation=? AND account_id=? AND profile_id=?`, "siisiss", "sqlite_autoindex_playback_v1_sessions_1", "playback_v1_sessions", "id")
	add(mpSessionStopPhysical, `SELECT mp_retired_generation,mp_exited_generation,substr(mp_retire_request_id,1,129),substr(state,1,129),mp_grant_retired FROM playback_sessions INDEXED BY sqlite_autoindex_playback_sessions_1 WHERE id=? AND mp_revision=? AND generation=? AND mp_execution_id=? AND account_id=? AND profile_id=?`, "siisss", "sqlite_autoindex_playback_sessions_1", "playback_sessions", "id")
	add(mpSessionStopRequest, `UPDATE playback_sessions INDEXED BY sqlite_autoindex_playback_sessions_1 SET state='stopped',mp_grant_retired=1,mp_retired_generation=generation,mp_retire_request_id=?,mp_revision=mp_revision+1 WHERE id=? AND mp_revision=? AND generation=? AND mp_execution_id=? AND account_id=? AND profile_id=? AND mp_retired_generation=0 AND mp_exited_generation=0 AND mp_cleanup_complete=0 RETURNING mp_revision`, "ssiisss", "sqlite_autoindex_playback_sessions_1", "playback_sessions", "id")
	add(mpSessionStopLogical, `UPDATE playback_v1_sessions INDEXED BY sqlite_autoindex_playback_v1_sessions_1 SET state='stopped',ended_ms=?,end_reason=?,updated_ms=?,revision=revision+1 WHERE id=? AND revision=? AND generation=? AND media_session_id=? AND media_generation=? AND account_id=? AND profile_id=? AND ended_ms=0 RETURNING revision`, "isisiisiss", "sqlite_autoindex_playback_v1_sessions_1", "playback_v1_sessions", "id")
	add(mpSessionStopAccountNext, `SELECT id FROM playback_v1_sessions INDEXED BY playback_v1_sessions_authority_account_id_active WHERE authority='local' AND account_id=? AND ended_ms=0 AND id>? ORDER BY id LIMIT 1`, "ss", "playback_v1_sessions_authority_account_id_active", "playback_v1_sessions", "")
	add(mpSessionStopExpiryNext, `SELECT substr(id,1,129) FROM playback_v1_sessions INDEXED BY playback_v1_sessions_lease WHERE ended_ms=0 AND lease_expires_ms<=? ORDER BY lease_expires_ms LIMIT 1`, "i", "playback_v1_sessions_lease", "playback_v1_sessions", "")
	return out
}
