package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func mpQueueDefinitions() []stmt.Definition {
	key := func(table, name, column string) stmt.AutoIndexBinding {
		return stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk", Columns: []stmt.IndexColumn{{Name: column, Collation: "BINARY"}}}
	}
	queue := key("queues_v1", "sqlite_autoindex_queues_v1_1", "id")
	token := key("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "token_hash")
	family := key("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "id")
	plan := func() stmt.Plan {
		return stmt.Plan{Hot: true, Indexes: []string{queue.Name, token.Name, family.Name}, AutoIndexes: []stmt.AutoIndexBinding{queue, token, family}}
	}
	params := []stmt.Param{{Name: "token", Type: stmt.Text}, {Name: "queue", Type: stmt.Text}}
	cohorts := []stmt.Cohort{{Name: "present", Bindings: []any{"token", "mp-queue"}}, {Name: "empty", Bindings: []any{"token", "missing"}}}
	return []stmt.Definition{{
		ID: readStatementID("foreground.mp.queue_source"), Owner: "store/read MP", CallSites: []string{"store/read/mp_queue.go:MediaReader.QueueHead"}, Kind: stmt.Read,
		SQL: mpQueueSourceSQL, Params: params, Cohorts: cohorts, Plan: plan(),
	}, {
		ID: readStatementID("foreground.mp.queue_head_octets"), Owner: "store/read MP", CallSites: []string{"store/read/mp_queue.go:MediaReader.QueueHead"}, Kind: stmt.Read,
		SQL: mpQueueOctetsSQL, Params: params, Cohorts: cohorts, Plan: plan(),
	}, {
		ID: readStatementID("foreground.mp.queue_head"), Owner: "store/read MP", CallSites: []string{"store/read/mp_queue.go:MediaReader.QueueHead"}, Kind: stmt.Read,
		SQL: `SELECT q.id,q.revision,q.custody_generation,q.current_entry_id,q.current_position,q.repeat,q.shuffle_on,q.shuffle_seed,q.shuffle_lap,q.shuffle_domain,q.shuffle_first,q.session_id,q.automatic_advances,q.created_ms,q.updated_ms,
q.owner_kind,q.owner_id,q.authority,q.account_id,q.profile_id,f.id,f.device_id
FROM queues_v1 AS q INDEXED BY sqlite_autoindex_queues_v1_1
CROSS JOIN authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1 ON t.token_hash=?
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
WHERE q.id=? AND t.retired=0 AND t.generation=f.current_generation
AND f.revoked=0 AND f.pin_gate=0 AND f.authority='local'
AND q.owner_kind='device' AND q.owner_id=f.device_id AND q.authority=f.authority AND q.account_id=f.account_id AND q.profile_id=f.profile_id`,
		Params: params, Cohorts: cohorts, Plan: plan(),
	}}
}
