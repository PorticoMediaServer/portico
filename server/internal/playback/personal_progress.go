package playback

import (
	"database/sql"
	"errors"
	"math"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// Explicit plays reserve their position when accepted, not when remote
// preparation completes. Exact replay retains the original ordinal.
func reservePersonalIntent(tx *sql.Tx, key string) (int64, error) {
	if _, err := tx.Exec(`INSERT OR IGNORE INTO playback_personal_intents(request_key) VALUES(?)`, key); err != nil {
		return 0, err
	}
	var ordinal int64
	err := tx.QueryRow(`SELECT ordinal FROM playback_personal_intents WHERE request_key=?`, key).Scan(&ordinal)
	return ordinal, err
}

// Automatic next/repeat carries the explicit queue intent's order. It is not a
// new user play and cannot overtake a newer intentional play on another device.
func inheritedPersonalIntent(tx *sql.Tx, p identity.Principal, playbackID string) (int64, error) {
	var ordinal int64
	err := tx.QueryRow(`SELECT accepted_ordinal FROM playback_personal_claims WHERE playback_id=? AND profile_key=?`, playbackID, identity.PersonalKey(p.Viewer)).Scan(&ordinal)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	} // Pre-migration evidence has no new authority.
	return ordinal, err
}

func claimSessionProgress(tx *sql.Tx, p identity.Principal, item, id string, resume float64) error {
	ordinal, err := reservePersonalIntent(tx, "playback:"+id)
	if err != nil {
		return err
	}
	return activatePersonalWriter(tx, p, item, id, resume, ordinal)
}

// Called only at accepted explicit occurrence creation or queue activation.
// Private candidates and retries do not claim progress. A newer manual watched/
// resume mutation fences even an earlier accepted candidate still preparing.
func activatePersonalWriter(tx *sql.Tx, p identity.Principal, item, id string, resume float64, ordinal int64) error {
	key := identity.PersonalKey(p.Viewer)
	if _, err := tx.Exec(`INSERT OR IGNORE INTO playback_personal_claims(playback_id,profile_key,item_id,accepted_ordinal) SELECT ?,?,e.id,? FROM catalog_entities e WHERE e.public_id=pid_blob(?)`, id, key, ordinal, item); err != nil {
		return err
	}
	var oldID string
	var oldOrdinal int64
	err := tx.QueryRow(`SELECT p.playback_id,COALESCE(c.accepted_ordinal,0) FROM progress p LEFT JOIN playback_personal_claims c ON c.playback_id=p.playback_id AND c.profile_key=p.profile_id AND c.item_id=p.item_id WHERE p.profile_id=? AND p.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, key, item).Scan(&oldID, &oldOrdinal)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && (oldOrdinal > ordinal || ordinal == 0 && oldID != "" && oldID != id) {
		return nil
	}
	var blocked int
	if err = tx.QueryRow(`SELECT (SELECT count(*) FROM playback_personal_fences WHERE profile_key=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND through_ordinal>=?)+(SELECT count(*) FROM playback_personal_profile_fences WHERE profile_key=? AND through_ordinal>=?)`, key, item, ordinal, key, ordinal).Scan(&blocked); err != nil {
		return err
	}
	if blocked > 0 {
		return nil
	}
	// Positions are integer milliseconds with a unit (Rewrite Map §7); the
	// caller still passes seconds.
	preferences, _, _, err := operations.EffectivePreferences(tx, p.Viewer, "")
	if err != nil {
		return err
	}
	paused := preferences.Bool("privacy.pauseWatchHistory")
	resumeMS := int64(math.Round(resume * 1000))
	if paused {
		resumeMS = 0
	}
	if _, err = tx.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) SELECT ?,e.id,?,0,? FROM catalog_entities e WHERE e.public_id=pid_blob(?) ON CONFLICT(profile_id,item_id) DO UPDATE SET playback_id=excluded.playback_id,position=CASE WHEN ? THEN progress.position ELSE excluded.position END,unit=excluded.unit`, key, resumeMS, id, item, paused); err != nil {
		return err
	}
	if paused {
		return nil
	}
	// A book summary is shared by its parts; order it by the same explicit intent.
	_, err = tx.Exec(`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position,unit)
 SELECT ?,bf.book_id,bf.entity_id,?,?,0 FROM catalog_book_files bf JOIN catalog_entities i ON i.id=bf.entity_id WHERE bf.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND i.kind=9
 ON CONFLICT(profile_id,book_id) DO UPDATE SET item_id=excluded.item_id,playback_id=excluded.playback_id,position=excluded.position,unit=excluded.unit
 WHERE COALESCE((SELECT accepted_ordinal FROM playback_personal_claims WHERE playback_id=book_resume.playback_id AND profile_key=book_resume.profile_id),0)<=?`, key, id, resumeMS, item, ordinal)
	return err
}
