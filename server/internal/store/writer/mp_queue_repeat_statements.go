package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	mpQueueRepeatStopFact   stmt.ID = "mp.queue_repeat.stop_fact"
	mpQueueRepeatStopInsert stmt.ID = "mp.queue_repeat.stop_insert"
	mpQueueRepeatCurrent    stmt.ID = "mp.queue_repeat.current"
	mpQueueRepeatPrepared   stmt.ID = "mp.queue_repeat.prepared"
	mpQueueRepeatCancel     stmt.ID = "mp.queue_repeat.cancel_next"
	mpQueueRepeatUpdate     stmt.ID = "mp.queue_repeat.update"
)

func mpQueueRepeatDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	owner := []stmt.Param{t("queue"), t("account"), t("profile"), t("device"), i("revision"), i("custody")}
	bindings := []any{"mp-queue", "a", "p", "d", int64(3), int64(7)}
	predicate := `id=? AND authority='local' AND account_id=? AND profile_id=? AND owner_kind='device' AND owner_id=? AND revision=? AND custody_generation=?`
	defs := []stmt.Definition{
		{ID: mpQueueRepeatCurrent, Kind: stmt.Read, SQL: `SELECT repeat,updated_ms FROM queues_v1 INDEXED BY sqlite_autoindex_queues_v1_1 WHERE ` + predicate, Params: owner, Cohorts: []stmt.Cohort{{Name: "owned_queue", Bindings: bindings}}},
		{ID: mpQueueRepeatPrepared, Kind: stmt.Read, SQL: `SELECT token,media_session_id,mp_media_generation FROM playback_v1_prepared INDEXED BY playback_v1_prepared_queue WHERE queue_id=? AND state='prepared' LIMIT 2`, Params: []stmt.Param{t("queue")}, Cohorts: []stmt.Cohort{{Name: "pending_next", Bindings: []any{"mp-queue"}}}},
		{ID: mpQueueRepeatCancel, Kind: stmt.Write, SQL: `UPDATE playback_v1_prepared SET state='canceled',cancel_reason='queue_changed' WHERE token=? AND queue_id=? AND media_session_id=? AND mp_media_generation=? AND state='prepared' RETURNING token`, Params: []stmt.Param{t("token"), t("queue"), t("media"), i("generation")}, Cohorts: []stmt.Cohort{{Name: "cancel_exact_next", Bindings: []any{"mp-next", "mp-queue", "mp-next-media", int64(2)}}}},
		{ID: mpQueueRepeatUpdate, Kind: stmt.Write, SQL: `UPDATE queues_v1 SET repeat=?,updated_ms=?,revision=revision+1 WHERE ` + predicate + ` AND repeat=? RETURNING revision,updated_ms`, Params: append(append([]stmt.Param{t("repeat"), i("now")}, owner...), t("before")), Cohorts: []stmt.Cohort{{Name: "repeat_changed", Bindings: append(append([]any{"all", int64(3000)}, bindings...), "off")}}},
		{ID: mpQueueRepeatStopFact, Kind: stmt.Read, SQL: `SELECT mp_retired_generation,mp_exited_generation,mp_retire_request_id,mp_exit_request_id,mp_exit_supervisor_incarnation,mp_exited_ms FROM playback_sessions INDEXED BY sqlite_autoindex_playback_sessions_1 WHERE id=?`, Params: []stmt.Param{t("media")}, Cohorts: []stmt.Cohort{{Name: "retired_occurrence", Bindings: []any{"mp-next-media"}}}},
		{ID: mpQueueRepeatStopInsert, Kind: stmt.Write, SQL: `INSERT INTO mp_next_stop_obligations(next_id,media_session_id,media_generation,request_id,reason,requested_ms) SELECT ?,p.id,?,p.mp_retire_request_id,'queue_changed',? FROM playback_sessions p WHERE p.id=? AND p.mp_retired_generation=? AND p.mp_retire_request_id=? RETURNING request_id`, Params: []stmt.Param{t("next"), i("generation"), i("now"), t("media"), i("retired_generation"), t("request")}, Cohorts: []stmt.Cohort{{Name: "retain_exact_stop", Bindings: []any{"mp-next", int64(2), int64(3000), "mp-next-media", int64(2), "mp-stop-request"}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer MP queue repeat"
		defs[n].CallSites = []string{"store/writer/mp_queue_repeat.go"}
		defs[n].Plan = stmt.Plan{Hot: true}
	}
	defs[0].Plan.Indexes = []string{"sqlite_autoindex_queues_v1_1"}
	defs[1].Plan.Indexes = []string{"playback_v1_prepared_queue"}
	defs[2].Plan.Indexes = []string{"sqlite_autoindex_playback_v1_prepared_1"}
	defs[3].Plan.Indexes = []string{"sqlite_autoindex_queues_v1_1"}
	defs[4].Plan.Indexes = []string{"sqlite_autoindex_playback_sessions_1"}
	defs[5].Plan.Indexes = []string{"sqlite_autoindex_playback_sessions_1"}
	return defs
}
