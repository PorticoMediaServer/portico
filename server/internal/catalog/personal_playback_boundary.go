package catalog

import (
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// RecordPlaybackTerminalActivityTx advances Recently played at an accepted
// terminal Stop, including when the last renderer sample was already flushed.
// It never invents a play for an opened/closed item, crosses the privacy pause,
// writes position/history, or creates a new personal command revision.
func RecordPlaybackTerminalActivityTx(tx *sql.Tx, viewer identity.Viewer, playbackID, item string, clock time.Time) error {
	preferences, _, _, err := operations.EffectivePreferences(tx, viewer, "")
	if err != nil {
		return err
	}
	if preferences.Bool("privacy.pauseWatchHistory") {
		return nil
	}
	id, err := personalID(tx, item)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	profile := identity.PersonalKey(viewer)
	var previous string
	err = tx.QueryRow(`SELECT p.last_played_at FROM personal_items p WHERE p.profile_id=? AND p.item_id=? AND EXISTS(SELECT 1 FROM personal_history h WHERE h.profile_id=p.profile_id AND h.id=? AND h.item_id=p.item_id)`, profile, id, playbackID).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if old, parsed := time.Parse(time.RFC3339Nano, previous); parsed == nil && !clock.After(old) {
		return nil
	}
	_, err = tx.Exec(`UPDATE personal_items SET last_played_at=? WHERE profile_id=? AND item_id=?`, clock.UTC().Format("2006-01-02T15:04:05.000Z"), profile, id)
	return err
}
