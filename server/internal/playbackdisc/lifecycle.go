package playbackdisc

import (
	"context"
	"database/sql"
	"time"
)

// RetireTx revokes only this original presentation binding. It never retires a
// capture shared by another generation or releases source quota/physical bytes.
// Media owns cancellation/join of its actual readers, decoder grants and helper.
func (s *Store) RetireTx(ctx context.Context, tx *sql.Tx, key Key, id string) error {
	if e := writeTx(ctx, tx); e != nil {
		return e
	}
	if _, e := s.identityTx(ctx, tx, key, id); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `UPDATE playback_disc_lifecycle SET state='retired',retired_ms=CASE WHEN retired_ms=0 THEN ? ELSE retired_ms END WHERE snapshot_id=?`, time.Now().UnixMilli(), id)
	return e
}

// PinTx is a normalized reference for an actual reader/grant. It is not a file
// descriptor or permission; consumers retain their physical reader and original
// authority checks too. Pins cannot renew the original presentation deadline.
func (s *Store) PinTx(ctx context.Context, tx *sql.Tx, key Key, id, pinID string, expires time.Time, check Check) (Pin, error) {
	if e := writeTx(ctx, tx); e != nil {
		return Pin{}, e
	}
	if !text(pinID, 256, false) {
		return Pin{}, ErrInput
	}
	if _, e := s.ResolveTx(ctx, tx, key, id, check); e != nil {
		return Pin{}, e
	}
	l, e := s.identityTx(ctx, tx, key, id)
	if e != nil {
		return Pin{}, e
	}
	until := expires.UnixMilli()
	if until <= time.Now().UnixMilli() || until > l.expires {
		return Pin{}, ErrInput
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM playback_disc_reader_pins WHERE id IN(SELECT id FROM playback_disc_reader_pins WHERE snapshot_id=? AND expires_ms<=? LIMIT 256)`, id, time.Now().UnixMilli()); e != nil {
		return Pin{}, e
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM playback_disc_reader_pins WHERE snapshot_id=?`, id).Scan(&count); e != nil {
		return Pin{}, e
	}
	if count >= 256 {
		return Pin{}, ErrCapacity
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO playback_disc_reader_pins(id,snapshot_id,expires_ms) VALUES(?,?,?)`, pinID, id, until); e != nil {
		return Pin{}, e
	}
	return Pin{ID: pinID, SnapshotID: id, SessionID: key.SessionID, Generation: key.Generation, ExpiresAt: time.UnixMilli(until).UTC()}, nil
}

// ResolvePinTx repeats the exact original reader/grant pin and active snapshot
// proof. A released, expired or borrowed pin never authorizes continued input.
func (s *Store) ResolvePinTx(ctx context.Context, tx *sql.Tx, p Pin, check Check) (Snapshot, error) {
	if ctx == nil || tx == nil || !text(p.ID, 256, false) || p.ExpiresAt.UnixMilli() <= time.Now().UnixMilli() {
		return Snapshot{}, ErrCurrent
	}
	var held bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_disc_reader_pins p JOIN playback_disc_lifecycle l ON l.snapshot_id=p.snapshot_id WHERE p.id=? AND p.snapshot_id=? AND p.expires_ms=? AND p.expires_ms>? AND p.expires_ms<=l.expires_ms AND l.state='active' AND l.sealed=1)`, p.ID, p.SnapshotID, p.ExpiresAt.UnixMilli(), time.Now().UnixMilli()).Scan(&held)
	if err != nil {
		return Snapshot{}, err
	}
	if !held {
		return Snapshot{}, ErrCurrent
	}
	return s.ResolveTx(ctx, tx, Key{p.SessionID, p.Generation}, p.SnapshotID, check)
}

// ReleasePinTx accepts only that reader's exact original pin even after actor or
// session retirement. It never needs a replacement principal or latest key.
func (s *Store) ReleasePinTx(ctx context.Context, tx *sql.Tx, p Pin) error {
	if e := writeTx(ctx, tx); e != nil {
		return e
	}
	if _, e := s.identityTx(ctx, tx, Key{p.SessionID, p.Generation}, p.SnapshotID); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `DELETE FROM playback_disc_reader_pins WHERE id=? AND snapshot_id=? AND expires_ms=?`, p.ID, p.SnapshotID, p.ExpiresAt.UnixMilli())
	return e
}

// CollectTx performs at most 64 exact-reference cleanups. It performs no provider
// or artifact IO. Only ownedcapture may physically remove bytes and uncharge
// quota, after checking its actual descriptors and all remaining references.
func (s *Store) CollectTx(ctx context.Context, tx *sql.Tx, after string, limit int) (next string, collected int, err error) {
	if s == nil || s.serverID == "" {
		err = ErrInput
		return
	}
	if err = writeTx(ctx, tx); err != nil {
		return
	}
	if limit < 1 || limit > 64 || !text(after, 256, true) {
		err = ErrInput
		return
	}
	rows, e := tx.QueryContext(ctx, `SELECT d.id,d.session_id,d.generation FROM playback_disc_snapshots d JOIN playback_disc_lifecycle l ON l.snapshot_id=d.id WHERE d.id>? AND (l.state='retired' OR l.expires_ms<=?) ORDER BY d.id LIMIT ?`, after, time.Now().UnixMilli(), limit)
	if e != nil {
		err = e
		return
	}
	type target struct {
		id  string
		key Key
	}
	list := []target{}
	for rows.Next() {
		var t target
		if e = rows.Scan(&t.id, &t.key.SessionID, &t.key.Generation); e != nil {
			break
		}
		list = append(list, t)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		err = e
		return
	}
	if len(list) == limit {
		next = list[len(list)-1].id
	}
	for _, t := range list {
		if err = s.RetireTx(ctx, tx, t.key, t.id); err != nil {
			return
		}
		var live int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM playback_disc_reader_pins WHERE snapshot_id=? AND expires_ms>?`, t.id, time.Now().UnixMilli()).Scan(&live); err != nil {
			return
		}
		if live > 0 {
			continue
		}
		// Subtitle resources are private children of this exact measured input.
		// Existing subtitle cleanup releases ended session pins; until then the
		// resource, snapshot and any owned input remain together. The resource
		// index makes this an existence check per bounded measured stream.
		var subtitlePinned bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_disc_subtitle_resources r JOIN playback_subtitle_pins p ON p.resource_id=r.resource_id WHERE r.snapshot_id=?)`, t.id).Scan(&subtitlePinned); err != nil {
			return
		}
		if subtitlePinned {
			continue
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM playback_disc_reader_pins WHERE snapshot_id=?`, t.id); err != nil {
			return
		}
		// The mapping's delete trigger soft-deletes only its own personal
		// resource before it can lose the private scope discriminator. Revision
		// and artifact cleanup remains owned by the existing subtitle collector.
		if _, err = tx.ExecContext(ctx, `DELETE FROM playback_disc_subtitle_resources WHERE snapshot_id=?`, t.id); err != nil {
			return
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM owned_capture_presentation_refs WHERE snapshot_id=?`, t.id); err != nil {
			return
		}
		for _, table := range []string{"playback_disc_snapshot_clips", "playback_disc_snapshot_disc_streams", "playback_disc_snapshot_disc_chapters", "playback_disc_snapshot_titles", "playback_disc_snapshot_streams", "playback_disc_snapshot_chapters", "playback_disc_snapshot_actor", "playback_disc_snapshot_operation", "playback_disc_snapshot_source", "playback_disc_snapshot_disc", "playback_disc_snapshot_media", "playback_disc_snapshot_tools", "playback_disc_conditional_inputs", "playback_disc_capture_inputs"} {
			if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE snapshot_id=?`, t.id); err != nil {
				return
			}
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM playback_disc_snapshots WHERE id=? AND session_id=? AND generation=?`, t.id, t.key.SessionID, t.key.Generation); err != nil {
			return
		}
		collected++
	}
	return
}
