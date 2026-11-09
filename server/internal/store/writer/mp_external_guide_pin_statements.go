package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// F's one-row install/release CTE recipes are decomposed into point guards and
// one native mutation in the SAME single-writer transaction, so the existing
// closed SQL grammar remains unchanged. Programme.channel_id equality, original
// source request and exact physical occurrence all remain mandatory. The frozen
// F SQL artifacts themselves are unchanged; reclaim is verbatim in its own file.
func mpExternalGuidePinDefinitions() []stmt.Definition {
	out := append(mpPhysicalDefinitions(), mpGuidePinReclaimDefinitions()...)
	pk := func(table string, cols ...string) stmt.Plan {
		index := "sqlite_autoindex_" + table + "_1"
		c := []stmt.IndexColumn{}
		for _, col := range cols {
			c = append(c, stmt.IndexColumn{Name: col, Collation: "BINARY"})
		}
		return stmt.Plan{Hot: true, Indexes: []string{index}, AutoIndexes: []stmt.AutoIndexBinding{{Table: table, Name: index, Origin: "pk", Columns: c}}}
	}
	add := func(id stmt.ID, sql, kinds string, p stmt.Plan) {
		d := stmt.Definition{ID: id, SQL: sql, Kind: stmt.Read, Owner: "store/writer MP guide", CallSites: []string{"store/writer/mp_external_guide_pin.go"}, Plan: p}
		if id == mpGuideInstall || id == mpGuideRelease || id == mpGuideRecordExit {
			d.Kind = stmt.Write
		}
		args := []any{}
		for i, c := range kinds {
			param := stmt.Param{Name: string(rune('a' + i)), Type: stmt.Text}
			var v any = "missing"
			switch c {
			case 'i':
				param.Type = stmt.Integer
				v = int64(1)
			case 'n':
				param.Nullable = true
				v = nil
			case 'j':
				param.Type = stmt.Integer
				param.Nullable = true
				v = nil
			case 'b':
				param.Type = stmt.Blob
				v = make([]byte, 16)
			}
			d.Params = append(d.Params, param)
			args = append(args, v)
		}
		d.Cohorts = []stmt.Cohort{{Name: "empty", Bindings: args}}
		out = append(out, d)
	}
	physical := pk("playback_sessions", "id")
	add(mpGuideInstallPhysical, `SELECT 1 FROM playback_sessions INDEXED BY sqlite_autoindex_playback_sessions_1 WHERE id=? AND generation=? AND mp_execution_id=? AND request_id=? AND mp_retired_generation<generation AND mp_exited_generation<generation AND mp_cleanup_complete=0`, "siss", physical)
	source := pk("live_sources", "id")
	g := pk("live_generations", "id")
	source.Indexes = append(source.Indexes, g.Indexes...)
	source.AutoIndexes = append(source.AutoIndexes, g.AutoIndexes...)
	add(mpGuideInstallSource, `SELECT 1 FROM live_sources AS s INDEXED BY sqlite_autoindex_live_sources_1 CROSS JOIN live_generations AS g INDEXED BY sqlite_autoindex_live_generations_1 ON g.id=s.active_generation AND g.source_id=s.id WHERE s.id=? AND s.state='active' AND s.active_generation=? AND g.id=? AND g.source_id=?`, "ssss", source)
	add(mpGuideInstallChannel, `SELECT 1 FROM live_channel_versions INDEXED BY sqlite_autoindex_live_channel_versions_1 WHERE generation_id=? AND channel_id=?`, "ss", pk("live_channel_versions", "generation_id", "channel_id"))
	add(mpGuideInstallProgramme, `SELECT 1 FROM live_programmes INDEXED BY sqlite_autoindex_live_programmes_1 WHERE generation_id=? AND id=? AND channel_id=?`, "sss", pk("live_programmes", "generation_id", "id"))
	pin := stmt.Plan{Hot: true, PrimaryKeys: []string{"mp_external_guide_pins"}}
	add(mpGuideInstall, `INSERT INTO mp_external_guide_pins(media_session_id,occurrence_generation,execution_id,source_request_id,captured_source_id,captured_generation_id,captured_channel_id,captured_programme_id,captured_ms,current_occurrence_generation,current_execution_id,current_source_id,current_generation_id,current_channel_id,current_programme_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, "sisssssniissssn", stmt.Plan{Hot: true})
	add(mpGuideReleaseHeader, `SELECT variable_bytes,released_ms,captured_ms FROM mp_external_guide_pins WHERE media_session_id=? AND occurrence_generation=? AND execution_id=? AND source_request_id=?`, "siss", pin)
	add(mpGuideReleaseReaders, `SELECT 1 FROM mp_media_read_pins INDEXED BY mp_media_read_pins_media WHERE media_session_id=? LIMIT 1`, "s", stmt.Plan{Hot: true, Indexes: []string{"mp_media_read_pins_media"}})
	add(mpGuideReleaseWindow, `SELECT start_index,mp_exit_confirmed,mp_cleaned FROM playback_hls_windows INDEXED BY mp_hls_window_generation WHERE session_id=? AND start_index>? ORDER BY start_index LIMIT 1`, "si", stmt.Plan{Hot: true, Indexes: []string{"mp_hls_window_generation"}})
	add(mpGuideReleasePhysical, `SELECT mp_media_variable_bytes,mp_exited_generation,mp_exited_ms,mp_exit_supervisor_incarnation,mp_exit_digest FROM playback_sessions INDEXED BY sqlite_autoindex_playback_sessions_1 WHERE id=? AND generation=? AND mp_execution_id=? AND request_id=?`, "siss", physical)
	add(mpGuideRecordExit, `UPDATE playback_sessions INDEXED BY sqlite_autoindex_playback_sessions_1 SET mp_exit_supervisor_incarnation=?,mp_exited_ms=?,mp_exit_digest=?,mp_cleanup_cursor=?,mp_exited_generation=generation,mp_exit_request_id=mp_retire_request_id,mp_cleanup_complete=1,mp_revision=mp_revision+1 WHERE id=? AND mp_revision=? AND generation=? AND mp_execution_id=? AND mp_retire_request_id=? AND mp_retired_generation=generation AND mp_exited_generation=0 AND mp_cleanup_complete=0 AND state='stopped' AND mp_grant_retired=1 RETURNING mp_media_variable_bytes`, "bisisiiss", physical)
	add(mpGuideRelease, `UPDATE mp_external_guide_pins SET current_occurrence_generation=?,current_execution_id=?,current_source_id=?,current_generation_id=?,current_channel_id=?,current_programme_id=?,released_ms=? WHERE media_session_id=? AND occurrence_generation=? AND execution_id=? AND source_request_id=? AND released_ms=0 RETURNING variable_bytes`, "jnnnnnisiss", pin)
	return out
}
