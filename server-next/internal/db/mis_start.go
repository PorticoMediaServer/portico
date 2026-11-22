package db

import "time"

// rewindAllowance is the largest "Rewind on resume" a profile can choose (playback and
// audiobooks both offer up to 30 seconds): a playing opened that far before the resume point
// was opened from it.
const rewindAllowance = 30_000

// misStart reports a playing opened by mistake under the started mark: it began clearly before
// the resume point (not from it, nor from it less a rewind), under the mark, and never reached
// the mark. A playing that began from the resume point, wherever that point is, keeps rule 8
// exactly. One the server did not see begin (nil) is never a mis-start.
//
// Whether it reached the mark is exact while the server has the session. A final position sent
// after the server let the session go has only a bound that cannot see a seek forward, so then
// a point moved since the playing began is taken to be its doing, and rule 8 applies. (It may
// have been another device's; the earlier point is then not kept for this late stop.)
func misStart(o *Opening, started, resumeAt, pointAt, began int64) bool {
	return o != nil && o.FromMS < started && o.FurthestMS < started && o.FromMS < resumeAt-rewindAllowance && (o.Exact || pointAt < began)
}

// began is when the sample's playing began, on the clock a state row's times are kept by: as
// long before the sample as the playing had run when the sample was taken.
func (w *stateWriter) began(sample Progress, ago time.Duration) int64 {
	if sample.Opening == nil {
		return 0
	}
	return w.now - ago.Milliseconds() - max(sample.At.UnixMilli()-sample.Opening.StartedAtMS, 0)
}

// touchPlayed records that the title was played at, and nothing else: no position and no
// count, once for a playing (the first save after it began). A title taken off Continue
// Watching earlier than that comes back to it (rule 13).
func (w *stateWriter) touchPlayed(id, at, began int64) error {
	res, err := w.exec(`UPDATE user_item_state SET last_played_at = ?1, updated_at = ?2
		WHERE profile_id = ?3 AND item_id = ?4 AND ifnull(last_played_at, 0) < min(?1, ?5)`, at, w.now, w.viewer.ProfileID, id, began)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	return appendChange(w.tx, Change{At: w.now, Topic: TopicState, Op: OpUpdated, RefID: id, ProfileID: w.viewer.ProfileID})
}
