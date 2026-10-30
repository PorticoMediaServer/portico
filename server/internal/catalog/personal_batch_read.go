package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
)

// Container actions read the same personal state as an individual action, but
// resolve their captured, bounded membership together rather than four reads
// for every member while holding the write gate.
func readPersonalBatch(ctx context.Context, tx *sql.Tx, profile string, ids []int64) (map[int64]PersonalState, error) {
	raw, _ := json.Marshal(ids)
	query := `SELECT e.id,COALESCE(pi.watchlisted,0),COALESCE(pi.favorite,0),COALESCE(pi.not_interested,0),pi.rating,COALESCE(pi.revision,0),COALESCE(` + personalstate.SQL("scope.profile", "e.id") + `,0),COALESCE(pi.last_played_at,''),COALESCE(p.position,0)/1000.0,
 EXISTS(SELECT 1 FROM continue_dismissals d WHERE d.profile_id=scope.profile AND d.item_id=e.id AND d.playback_id=COALESCE(p.playback_id,'')),
 (SELECT COALESCE(json_group_array(json_object('id',c.id,'field',c.field,'baseRevision',c.base_revision,'baseValue',json(c.base_value),'choices',json((SELECT COALESCE(json_group_array(json_object('operationId',v.operation_id,'deviceId',v.device_id,'authoredAt',v.authored_at,'value',json(v.value))),'[]') FROM personal_explicit_events v WHERE v.profile_id=c.profile_id AND v.item_id=c.item_id AND v.field=c.field AND v.base_revision=c.base_revision)))),'[]') FROM personal_conflicts c WHERE c.profile_id=scope.profile AND c.item_id=e.id AND c.resolved=0)
 FROM json_each(?) requested CROSS JOIN catalog_entities e ON e.id=CAST(requested.value AS INTEGER) CROSS JOIN (SELECT ? profile) scope
 LEFT JOIN personal_items pi ON pi.profile_id=scope.profile AND pi.item_id=e.id LEFT JOIN progress p ON p.profile_id=scope.profile AND p.item_id=e.id`
	rows, err := tx.QueryContext(ctx, query, string(raw), profile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]PersonalState{}
	for rows.Next() {
		var id int64
		state := PersonalState{Status: "current", Conflicts: []PersonalConflict{}}
		var conflicts string
		if err := rows.Scan(&id, &state.Watchlisted, &state.Favorite, &state.NotInterested, &state.Rating, &state.Revision, &state.Watched, &state.LastPlayedAt, &state.ProgressSeconds, &state.ContinueDismissed, &conflicts); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(conflicts), &state.Conflicts); err != nil {
			return nil, err
		}
		if len(state.Conflicts) > 0 {
			state.Status = "needs-resolution"
		}
		out[id] = state
	}
	return out, rows.Err()
}

func snapshotPersonalBatch(ctx context.Context, tx *sql.Tx, profile string, ids []int64, states map[int64]PersonalState) error {
	values := make([][3]any, 0, len(ids))
	for _, id := range ids {
		state, ok := states[id]
		if !ok {
			return sql.ErrNoRows
		}
		raw, err := json.Marshal(state)
		if err != nil {
			return err
		}
		values = append(values, [3]any{id, state.Revision, string(raw)})
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO personal_snapshots(profile_id,item_id,revision,state,created_at) SELECT ?,CAST(json_extract(value,'$[0]') AS INTEGER),CAST(json_extract(value,'$[1]') AS INTEGER),json_extract(value,'$[2]'),? FROM json_each(?)`, profile, time.Now().UTC().Format(time.RFC3339), string(raw))
	return err
}

func writeContainerMemberStates(ctx context.Context, tx *sql.Tx, profile string, ids []int64, states map[int64]PersonalState, watched bool, at time.Time) error {
	rawIDs, _ := json.Marshal(ids)
	// All fences see the same accepted-intent ordinal inside this atomic action.
	if _, err := tx.ExecContext(ctx, `INSERT INTO personal_evidence_fences(profile_id,item_id,playback_id) SELECT p.profile_id,p.item_id,p.playback_id FROM json_each(?) requested CROSS JOIN progress p ON p.profile_id=? AND p.item_id=CAST(requested.value AS INTEGER) WHERE 1 ON CONFLICT(profile_id,item_id) DO UPDATE SET playback_id=excluded.playback_id`, string(rawIDs), profile); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO playback_personal_fences(profile_key,item_id,through_ordinal) SELECT ?,CAST(value AS INTEGER),(SELECT COALESCE(max(ordinal),0) FROM playback_personal_intents) FROM json_each(?) WHERE 1 ON CONFLICT(profile_key,item_id) DO UPDATE SET through_ordinal=excluded.through_ordinal`, profile, string(rawIDs)); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) SELECT ?,CAST(value AS INTEGER),?,? FROM json_each(?) WHERE 1 ON CONFLICT(profile_id,item_id) DO UPDATE SET watched=excluded.watched,authored_at=excluded.authored_at`, profile, watched, at.UTC().Format("2006-01-02T15:04:05.000000000Z"), string(rawIDs)); err != nil {
		return err
	}
	values := make([][8]any, 0, len(ids))
	for _, id := range ids {
		state, ok := states[id]
		if !ok {
			return sql.ErrNoRows
		}
		state.Watched = watched
		state.ProgressSeconds = 0
		state.Revision++
		states[id] = state
		values = append(values, [8]any{id, state.Watchlisted, state.Favorite, state.NotInterested, state.Rating, state.Revision, state.Watched, state.LastPlayedAt})
	}
	raw, _ := json.Marshal(values)
	if _, err := tx.ExecContext(ctx, `INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,not_interested,rating,revision,watched,last_played_at) SELECT ?,CAST(json_extract(value,'$[0]') AS INTEGER),json_extract(value,'$[1]'),json_extract(value,'$[2]'),json_extract(value,'$[3]'),json_extract(value,'$[4]'),json_extract(value,'$[5]'),json_extract(value,'$[6]'),json_extract(value,'$[7]') FROM json_each(?) WHERE 1 ON CONFLICT(profile_id,item_id) DO UPDATE SET watchlisted=excluded.watchlisted,favorite=excluded.favorite,not_interested=excluded.not_interested,rating=excluded.rating,revision=excluded.revision,watched=excluded.watched,last_played_at=excluded.last_played_at`, profile, string(raw)); err != nil {
		return err
	}
	for _, query := range []string{`UPDATE progress SET position=0 WHERE profile_id=? AND item_id IN(SELECT CAST(value AS INTEGER) FROM json_each(?))`, `UPDATE progress_activity SET state='ended' WHERE profile_id=? AND item_id IN(SELECT CAST(value AS INTEGER) FROM json_each(?))`, `UPDATE book_resume SET position=0 WHERE profile_id=? AND item_id IN(SELECT CAST(value AS INTEGER) FROM json_each(?))`} {
		if _, err := tx.ExecContext(ctx, query, profile, string(rawIDs)); err != nil {
			return err
		}
	}
	return nil
}

func restoreContainerMemberStates(ctx context.Context, tx *sql.Tx, profile, token string) error {
	commands := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM personal_watched_intents WHERE profile_id=? AND item_id IN(SELECT item_id FROM container_personal_undo_items WHERE token=?)`, []any{profile, token}},
		{`INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) SELECT ?,item_id,previous_intent,previous_authored_at FROM container_personal_undo_items WHERE token=? AND previous_authored_at IS NOT NULL`, []any{profile, token}},
		{`UPDATE personal_items AS p SET watched=(SELECT previous_watched FROM container_personal_undo_items u WHERE u.token=? AND u.item_id=p.item_id),revision=revision+1 WHERE profile_id=? AND item_id IN(SELECT item_id FROM container_personal_undo_items WHERE token=?)`, []any{token, profile, token}},
		{`UPDATE progress AS p SET position=(SELECT position FROM container_personal_undo_items u WHERE u.token=? AND u.item_id=p.item_id) WHERE profile_id=? AND item_id IN(SELECT item_id FROM container_personal_undo_items WHERE token=?)`, []any{token, profile, token}},
		{`UPDATE progress_activity AS p SET state=(SELECT activity_state FROM container_personal_undo_items u WHERE u.token=? AND u.item_id=p.item_id) WHERE profile_id=? AND item_id IN(SELECT item_id FROM container_personal_undo_items WHERE token=?)`, []any{token, profile, token}},
		{`UPDATE book_resume AS p SET position=(SELECT book_position FROM container_personal_undo_items u WHERE u.token=? AND u.item_id=p.item_id) WHERE profile_id=? AND item_id IN(SELECT item_id FROM container_personal_undo_items WHERE token=? AND book_position IS NOT NULL)`, []any{token, profile, token}},
	}
	for _, command := range commands {
		if _, err := tx.ExecContext(ctx, command.query, command.args...); err != nil {
			return err
		}
	}
	return nil
}
