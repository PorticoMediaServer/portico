package playback

import (
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// OfflinePlay is one play of a downloaded copy, made with no server in reach
// and reported afterwards.
type OfflinePlay struct {
	// ID names the play. It is stable for as long as the device reports the
	// same play again (a later position of it, or a resend), and it is unique
	// to the viewer: the caller derives it, the device does not choose it.
	ID   string
	Item string
	// At is when the device last observed the play.
	At              time.Time
	PositionSeconds float64
	DurationSeconds float64
	Ended           bool
	// Newest says the caller has established that nothing this server knows
	// about the viewer and this title is newer than the play: no online
	// playback, no explicit mark, no other offline play.
	Newest bool
	// KeepBookPointer is for a part of a book whose own place this play
	// decides while the book itself has since moved to another part: the part
	// is written, the book's pointer is left exactly as it was.
	KeepBookPointer bool
}

// OfflineWriter is the personal-state writer, taking the time the evidence was
// recorded (catalog.AcceptPlaybackProgressAt).
type OfflineWriter func(tx *sql.Tx, v identity.Viewer, playbackID, item string, sequence int64, position, duration float64, state string, canonical bool, at time.Time) error

// AcceptOfflinePlay passes an offline play through the same two steps an
// online play takes, in the same order and by the same code.
//
// A play that is the newest thing known about the title takes its place in
// the order of intentional plays exactly as a session starting now would
// (reservePersonalIntent, activatePersonalWriter): it becomes the writer of
// the title's resume point unless an explicit mark or a newer play fences it.
// An older play claims nothing. Either way the writer is then called, which
// records the play in the server's history and the viewer's own, counts it,
// and — for the writer only — moves the resume point and the watched state by
// the viewer's own thresholds. So an old offline play still appears in history
// and cannot overwrite what a newer device wrote.
func AcceptOfflinePlay(tx *sql.Tx, p identity.Principal, play OfflinePlay, write OfflineWriter) (failure error) {
	if play.Newest && play.KeepBookPointer {
		// Activation moves a book to the part being played, as it must for a
		// session starting now. This play is older than where the book has
		// got to, so the pointer is put back once the part has been written.
		key := identity.PersonalKey(p.Viewer)
		var book, item, position int64
		var unit int
		var writer string
		err := tx.QueryRow(`SELECT r.book_id,r.item_id,r.playback_id,r.position,r.unit FROM book_resume r JOIN catalog_book_files f ON f.book_id=r.book_id WHERE r.profile_id=? AND f.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, key, play.Item).Scan(&book, &item, &writer, &position, &unit)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			defer func() {
				if failure != nil {
					return
				}
				_, failure = tx.Exec(`UPDATE book_resume SET item_id=?,playback_id=?,position=?,unit=? WHERE profile_id=? AND book_id=?`, item, writer, position, unit, key, book)
			}()
		}
	}
	canonical := false
	if play.Newest {
		ordinal, err := reservePersonalIntent(tx, "offline:"+play.ID+":"+play.At.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		// Freshness is established by the caller. A later observation of the
		// same play can reclaim the writer from a play observed in between.
		if _, err = tx.Exec(`UPDATE playback_personal_claims SET accepted_ordinal=? WHERE playback_id=? AND profile_key=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, ordinal, play.ID, identity.PersonalKey(p.Viewer), play.Item); err != nil {
			return err
		}
		// The resume point is written by the writer below from the play's own
		// position; activation only decides who the writer is for this fresh
		// observation. Repeated observations remain one play in history.
		var before sql.NullInt64
		if err = tx.QueryRow(`SELECT position FROM progress WHERE profile_id=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, identity.PersonalKey(p.Viewer), play.Item).Scan(&before); err != nil && err != sql.ErrNoRows {
			return err
		}
		if err = activatePersonalWriter(tx, p, play.Item, play.ID, float64(before.Int64)/1000, ordinal); err != nil {
			return err
		}
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM progress WHERE profile_id=? AND playback_id=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)))`, identity.PersonalKey(p.Viewer), play.ID, play.Item).Scan(&canonical); err != nil {
			return err
		}
	}
	state := "paused"
	if play.Ended {
		state = "ended"
	}
	// A play's reports are ordered by when they were observed.
	return write(tx, p.Viewer, play.ID, play.Item, play.At.UnixMilli(), play.PositionSeconds, play.DurationSeconds, state, canonical, play.At)
}
