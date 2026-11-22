package db

import "time"

// misStart reports a playing that began under the started mark and never reached it: one
// opened from the start by mistake. A playing the server did not see begin (nil) is not one.
func misStart(o *Opening, started int64) bool {
	return o != nil && o.FromMS < started && o.FurthestMS < started
}

// touchPlayed records that the title was played at, and nothing else: no position and no
// count. A title taken off Continue Watching earlier than that comes back to it (rule 13).
func (w *stateWriter) touchPlayed(id int64, at time.Time) error {
	res, err := w.exec(`UPDATE user_item_state SET last_played_at = max(ifnull(last_played_at, 0), ?1), updated_at = ?2
		WHERE profile_id = ?3 AND item_id = ?4 AND ifnull(last_played_at, 0) < ?1`, at.UnixMilli(), w.now, w.viewer.ProfileID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	return appendChange(w.tx, Change{At: w.now, Topic: TopicState, Op: OpUpdated, RefID: id, ProfileID: w.viewer.ProfileID})
}
