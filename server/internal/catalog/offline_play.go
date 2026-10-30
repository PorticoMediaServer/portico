package catalog

import (
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// AcceptPlaybackProgressAt is AcceptPlaybackProgress for evidence recorded at
// an earlier moment: a play of a downloaded copy, made with no server in reach
// and reported later. It is the same writer with the same rules; only the time
// differs, and it is the time the play happened that goes into both histories.
//
// The watched intent the writer records is dated to the play as well. An
// intent is ordered against the marks a viewer makes on a whole show or album
// by its date, so one dated "now" would beat a "mark the show unwatched" that
// the viewer made after watching the episode on a plane.
func (s *Service) AcceptPlaybackProgressAt(tx *sql.Tx, v identity.Viewer, playbackID, item string, sequence int64, position, duration float64, state string, canonical bool, at time.Time) error {
	if at.IsZero() || at.After(time.Now().Add(24*time.Hour)) {
		return jobInvalid("invalid playback time")
	}
	// A downloaded play carries no player rate, so it cannot prove a played
	// interval and records no chapter completion.
	return acceptPlaybackProgress(tx, v, playbackID, item, sequence, position, duration, state, canonical, 0, nil, 0, state == "playing", at.UTC(), nil)
}
