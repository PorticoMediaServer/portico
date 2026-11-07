package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func mpSessionDefinitions() []stmt.Definition {
	key := func(table, name, column string) stmt.AutoIndexBinding {
		return stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk", Columns: []stmt.IndexColumn{{Name: column, Collation: "BINARY"}}}
	}
	session := key("playback_v1_sessions", "sqlite_autoindex_playback_v1_sessions_1", "id")
	token := key("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "token_hash")
	family := key("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "id")
	return []stmt.Definition{{ID: readStatementID("foreground.mp.session_head"), Owner: "store/read MP", CallSites: []string{"store/read/mp_session.go:ForegroundReader.SessionHead"}, Kind: stmt.Read,
		SQL: `SELECT s.id,s.kind,s.role,s.state,s.version_id,s.queue_id,s.entry_id,s.media_session_id,s.part_index,s.revision,s.generation,s.media_generation,s.position_ms,s.start_position_ms,s.last_seq,s.lease_expires_ms,s.created_ms,s.updated_ms,s.ended_ms,s.end_reason,s.device_id,f.id,
e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,
s.account_id,s.profile_id,s.authority,e.library_id,e.kind
FROM playback_v1_sessions AS s INDEXED BY sqlite_autoindex_playback_v1_sessions_1
CROSS JOIN authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1 ON t.token_hash=?
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN catalog_entities AS e ON e.id=s.item_id
WHERE s.id=? AND s.item_id=? AND e.library_id=? AND e.kind=? AND e.retired=0
AND t.retired=0 AND t.generation=f.current_generation AND f.revoked=0 AND f.pin_gate=0 AND f.authority='local'
AND s.device_id=f.device_id AND s.account_id=f.account_id AND s.profile_id=f.profile_id AND s.authority=f.authority
AND s.role='local' AND s.kind IN('vod','audio') AND s.group_id='' AND s.transfer_id='' AND s.channel_id=''`,
		Params:  []stmt.Param{{Name: "token", Type: stmt.Text}, {Name: "session", Type: stmt.Text}, {Name: "item", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}, {Name: "kind", Type: stmt.Integer}},
		Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{"token", "mp-session", int64(1), int64(1), int64(1)}}, {Name: "empty", Bindings: []any{"token", "missing", int64(1), int64(1), int64(1)}}},
		Plan:    stmt.Plan{Hot: true, Indexes: []string{session.Name, token.Name, family.Name}, AutoIndexes: []stmt.AutoIndexBinding{session, token, family}, PrimaryKeys: []string{"e"}},
	}}
}
