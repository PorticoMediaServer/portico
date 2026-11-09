package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

// Include this union in the outer owner's complete Capture and exact W plan.
// No registry installation, purpose opening, source inference or parent DELETE.
func mpSessionParentDrainDefinitions() []stmt.Definition {
	d := stmt.Definition{ID: mpSessionDrained, Kind: stmt.Read, Owner: "store/writer MP retirement", CallSites: []string{"store/writer/mp_session_stop_guard.go:requireMPSessionParentDrained"}, SQL: `SELECT 1 FROM playback_sessions INDEXED BY sqlite_autoindex_playback_sessions_1 WHERE id=? AND generation>0 AND state='stopped' AND mp_grant_retired=1 AND mp_retired_generation=generation AND mp_exited_generation=generation AND mp_retire_request_id<>'' AND mp_exit_request_id=mp_retire_request_id AND length(mp_exit_supervisor_incarnation)=16 AND mp_exited_ms>0 AND length(mp_exit_digest)=64 AND mp_cleanup_complete=1 AND mp_receipt_rows=0 AND mp_receipt_variable_bytes=0 AND mp_command_last_sequence=mp_command_acknowledged_sequence`, Params: []stmt.Param{{Name: "session", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "empty", Bindings: []any{"missing"}}}, Plan: mpSessionCustodyPlan("playback_sessions", "id")}
	out := []stmt.Definition{d}
	for _, p := range mpSessionCustodyPoints {
		d.ID = p.id
		d.SQL = "SELECT 1 FROM " + p.table + " WHERE " + p.key + "=? LIMIT 1"
		d.Plan = mpSessionCustodyPlan(p.table, p.key)
		if len(d.Plan.Indexes) > 0 {
			order := p.key
			if len(d.Plan.AutoIndexes) > 0 {
				cols := []string{}
				for _, col := range d.Plan.AutoIndexes[0].Columns {
					cols = append(cols, col.Name)
				}
				order = strings.Join(cols, ",")
			}
			d.SQL = "SELECT 1 FROM " + p.table + " INDEXED BY " + d.Plan.Indexes[0] + " WHERE " + p.key + "=? ORDER BY " + order + " LIMIT 1"
		}
		out = append(out, d)
	}
	return out
}

func mpSessionCustodyPlan(table, key string) stmt.Plan {
	switch table {
	case "playback_sessions":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_sessions_1"}, Seeks: []string{"id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_sessions", Name: "sqlite_autoindex_playback_sessions_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}
	case "lyric_selections":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_lyric_selections_1"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "lyric_selections", Name: "sqlite_autoindex_lyric_selections_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}}}}}
	case "playback_airplay_presentations":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_airplay_presentations_2"}, Seeks: []string{"media_session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_airplay_presentations", Name: "sqlite_autoindex_playback_airplay_presentations_2", Origin: "u", Columns: []stmt.IndexColumn{{Name: "media_session_id", Collation: "BINARY"}}}}}
	case "playback_audio_plans":
		return stmt.Plan{Hot: true, PrimaryKeys: []string{"playback_audio_plans"}, Seeks: []string{"session_id=?"}}
	case "playback_conversion_observations":
		return stmt.Plan{Hot: true, PrimaryKeys: []string{"playback_conversion_observations"}, Seeks: []string{"session_id=?"}}
	case "playback_delivery_plans":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_delivery_plans_1"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_delivery_plans", Name: "sqlite_autoindex_playback_delivery_plans_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}}}}}
	case "playback_disc_pins":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_disc_pins_1"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_disc_pins", Name: "sqlite_autoindex_playback_disc_pins_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}}}}}
	case "playback_disc_snapshots":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_disc_snapshots_2"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_disc_snapshots", Name: "sqlite_autoindex_playback_disc_snapshots_2", Origin: "u", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}, {Name: "generation", Collation: "BINARY"}}}}}
	case "playback_hls_demand":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_hls_demand_1"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_hls_demand", Name: "sqlite_autoindex_playback_hls_demand_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}}}}}
	case "playback_hls_stages":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_hls_stages_1"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_hls_stages", Name: "sqlite_autoindex_playback_hls_stages_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}}}}}
	case "playback_hls_windows":
		return stmt.Plan{Hot: true, Indexes: []string{"mp_hls_window_generation"}, Seeks: []string{"session_id=?"}}
	case "playback_manifest_subtitles":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_manifest_subtitles_1"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_manifest_subtitles", Name: "sqlite_autoindex_playback_manifest_subtitles_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}, {Name: "resource_id", Collation: "BINARY"}, {Name: "resource_revision", Collation: "BINARY"}}}}}
	case "playback_observations":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_observations_1"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_observations", Name: "sqlite_autoindex_playback_observations_1", Origin: "u", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}}}}}
	case "playback_physical_source_pins":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_physical_source_pins_1"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_physical_source_pins", Name: "sqlite_autoindex_playback_physical_source_pins_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}}}}}
	case "playback_private_presentations":
		return stmt.Plan{Hot: true, PrimaryKeys: []string{"playback_private_presentations"}, Seeks: []string{"session_id=?"}}
	case "playback_source_pins":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_source_pins_1"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_source_pins", Name: "sqlite_autoindex_playback_source_pins_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}}}}}
	case "playback_subtitle_pins":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_subtitle_pins_1"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_subtitle_pins", Name: "sqlite_autoindex_playback_subtitle_pins_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}, {Name: "resource_id", Collation: "BINARY"}, {Name: "resource_revision", Collation: "BINARY"}}}}}
	case "playback_subtitle_presentations":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_subtitle_presentations_1"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_subtitle_presentations", Name: "sqlite_autoindex_playback_subtitle_presentations_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}}}}}
	case "playback_subtitle_state":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_subtitle_state_1"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_subtitle_state", Name: "sqlite_autoindex_playback_subtitle_state_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}}}}}
	case "playback_vod_plans":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_vod_plans_1"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_vod_plans", Name: "sqlite_autoindex_playback_vod_plans_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}}}}}
	case "prepared_media_session_pins":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_prepared_media_session_pins_1"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "prepared_media_session_pins", Name: "sqlite_autoindex_prepared_media_session_pins_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}}}}}
	case "remote_playback":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_remote_playback_1"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "remote_playback", Name: "sqlite_autoindex_remote_playback_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}}}}}
	case "subtitle_remote_sessions":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_subtitle_remote_sessions_1"}, Seeks: []string{"session_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "subtitle_remote_sessions", Name: "sqlite_autoindex_subtitle_remote_sessions_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "session_id", Collation: "BINARY"}}}}}
	case "subtitle_selection_operations":
		return stmt.Plan{Hot: true, Indexes: []string{"subtitle_selection_operations_session_id_fk"}, Seeks: []string{"session_id=?"}}
	case "mp_physical_command_receipts":
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_mp_physical_command_receipts_1"}, Seeks: []string{"owner_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "mp_physical_command_receipts", Name: "sqlite_autoindex_mp_physical_command_receipts_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "owner_id", Collation: "BINARY"}, {Name: "command_incarnation", Collation: "BINARY"}, {Name: "command_sequence", Collation: "BINARY"}}}}}
	case "mp_queue_stop_obligations":
		return stmt.Plan{Hot: true, Indexes: []string{"mp_queue_stop_media_fk"}, Seeks: []string{"media_session_id=?"}}
	case "mp_next_stop_obligations":
		return stmt.Plan{Hot: true, Indexes: []string{"mp_next_stop_media_fk"}, Seeks: []string{"media_session_id=?"}}
	case "mp_media_inputs":
		return stmt.Plan{Hot: true, PrimaryKeys: []string{"mp_media_inputs"}, Seeks: []string{"media_session_id=?"}}
	case "mp_session_replacements":
		return stmt.Plan{Hot: true, Indexes: []string{"mp_session_replacement_old_media_fk"}, Seeks: []string{"old_media_id=?"}}
	case "mp_remote_delivery_inputs":
		return stmt.Plan{Hot: true, PrimaryKeys: []string{"mp_remote_delivery_inputs"}, Seeks: []string{"media_session_id=?"}}
	case "mp_direct_delivery":
		return stmt.Plan{Hot: true, PrimaryKeys: []string{"mp_direct_delivery"}, Seeks: []string{"session_id=?"}}
	case "mp_media_read_pins":
		return stmt.Plan{Hot: true, Indexes: []string{"mp_media_read_pins_media"}, Seeks: []string{"media_session_id=?"}}
	case "mp_hls_plans":
		return stmt.Plan{Hot: true, PrimaryKeys: []string{"mp_hls_plans"}, Seeks: []string{"session_id=?"}}
	case "mp_external_guide_pins":
		return stmt.Plan{Hot: true, PrimaryKeys: []string{"mp_external_guide_pins"}, Seeks: []string{"media_session_id=?"}}
	case "playback_v1_sessions":
		return stmt.Plan{Hot: true, Indexes: []string{"playback_v1_sessions_media"}, Seeks: []string{"media_session_id=?"}}
	default:
		panic("unknown compiled MP session custody point")
	}
}
