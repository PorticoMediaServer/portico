package db

import (
	"context"
	"database/sql"
	"time"
)

// Reset recommendations (area 6 rule 22) forgets the profile's taste: what its recommendations
// learn from. History, ratings and lists stay, and so do its watched marks, resume points,
// favourites and Not interested marks: nothing is removed. The profile's taste is read from
// its plays and favourites (chooseMoodRows), so it is forgotten by reading only those made
// after the reset; one write however long the profile has been watching.

// tasteSince is the SQL for when the viewing profile's taste begins: its last reset, or 0.
const tasteSince = `ifnull((SELECT taste_reset_at FROM profiles WHERE id = @viewer_profile), 0)`

// ResetTaste forgets the viewer's taste from now, and the mood rows chosen from it.
func (d *DB) ResetTaste(ctx context.Context, v Viewer, now time.Time) error {
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE profiles SET taste_reset_at = max(ifnull(taste_reset_at, 0), ?) WHERE id = ?`, now.UnixMilli(), v.ProfileID); err != nil {
			return err
		}
		// The profile's other devices read again what they show, Home's rows among it: a state
		// change that names no title.
		return appendChange(tx, Change{At: now.UnixMilli(), Topic: TopicState, Op: OpUpdated, ProfileID: v.ProfileID})
	})
	if err == nil {
		d.moods.forget(v.ProfileID)
	}
	return err
}
