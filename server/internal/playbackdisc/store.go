package playbackdisc

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

type lifecycle struct {
	state, kind string
	sealed      bool
	expires     int64
}

func writeTx(ctx context.Context, tx *sql.Tx) error {
	if ctx == nil || tx == nil {
		return ErrInput
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if held := dbwork.Snapshot(ctx); held != nil && held != tx {
		return ErrInput
	}
	return dbwork.RequireGatedWriteTx(tx)
}
func (s *Store) identityTx(ctx context.Context, tx *sql.Tx, key Key, id string) (l lifecycle, err error) {
	if s == nil || ctx == nil || tx == nil || !validKey(key) || !text(id, 256, false) {
		return l, ErrInput
	}
	if err = ctx.Err(); err != nil {
		return l, err
	}
	err = tx.QueryRowContext(ctx, `SELECT l.state,l.sealed,l.expires_ms,d.input_kind FROM playback_disc_snapshots d JOIN playback_disc_lifecycle l ON l.snapshot_id=d.id WHERE d.id=? AND d.session_id=? AND d.generation=?`, id, key.SessionID, key.Generation).Scan(&l.state, &l.sealed, &l.expires, &l.kind)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrCurrent
	}
	return
}
func (s *Store) sessionTx(ctx context.Context, tx *sql.Tx, v Snapshot) (int64, error) {
	return s.sessionProofTx(ctx, tx, v, nil)
}

// A continuation is private store code issued from a live same-Tx parent.
// Normal publication/activation supplies none and retains its strict hash rule.
func (s *Store) sessionProofTx(ctx context.Context, tx *sql.Tx, v Snapshot, continuation func(string) error) (int64, error) {
	if v.Actor.ServerID != s.serverID {
		return 0, ErrCurrent
	}
	var expires, currentHash string
	err := tx.QueryRowContext(ctx, `SELECT ps.expires_at,ps.session_hash FROM playback_sessions ps JOIN catalog_entities item ON item.id=ps.item_id JOIN catalog_libraries cl ON cl.id=item.library_id
 WHERE ps.id=? AND ps.generation=? AND ps.account_id=? AND ps.profile_id=? AND ps.asset_id=? AND ps.request_id=? AND ps.state NOT IN('stopped','ended','failed') AND item.retired=0 AND cl.library_id=?`, v.SessionID, v.Generation, v.Actor.AccountID, v.Actor.ProfileID, v.Source.AssetID, v.Operation.LegacyRequestID, v.Source.LibraryID).Scan(&expires, &currentHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrCurrent
		}
		return 0, err
	}
	if currentHash != v.Actor.SessionHash {
		if continuation != nil {
			err = continuation(currentHash)
		} else {
			err = s.activeFamilySessionTx(ctx, tx, v, currentHash)
		}
		if err != nil {
			return 0, err
		}
	}
	end, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		return 0, ErrCurrent
	}
	until := min(end.UnixMilli(), v.Actor.OriginalHorizonMs)
	if v.Input.Captured != nil {
		until = min(until, v.Input.Captured.Receipt.ExpiresAt.UnixMilli())
	}
	if until <= time.Now().UnixMilli() {
		return 0, ErrCurrent
	}
	// No global disc-ready predicate is used here: these are exact physical and
	// configuration facts, not catalogue stream/chapter facts or authority.
	var current bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_objects o JOIN library_sources ls ON ls.id=o.source_id JOIN catalog_assets a ON a.token=o.asset_id JOIN remote_sources r ON r.id=ls.remote_source_id
 LEFT JOIN mount_backend_configs m ON m.mount_id=r.mount_id LEFT JOIN remote_source_read_profiles rp ON rp.source_id=r.id
 WHERE o.id=? AND o.asset_id=? AND o.source_id=? AND o.revision=? AND o.size=? AND o.modified_ns=? AND o.root_incarnation=? AND o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND (lower(o.relative_path) GLOB '*.iso' OR lower(o.relative_path) GLOB '*.img')
 AND a.available=1 AND a.size=o.size AND a.modified_ns=o.modified_ns AND ls.library_id=? AND ls.kind='remote' AND ls.enabled=1 AND ls.identity_confirmed=1 AND ls.health='healthy' AND ls.generation=? AND ls.incarnation=? AND ls.root_identity=?
 AND r.id=? AND r.removed=0 AND r.generation=? AND (CASE WHEN r.kind IN('rclone','sftp','smb') THEN CAST(m.generation AS TEXT) ELSE CAST(r.generation AS TEXT) END)=? AND COALESCE(rp.revision,0)=?)`, v.Source.InventoryObjectID, v.Source.AssetID, v.Source.LibrarySourceID, v.Source.InventoryRevision, v.Source.Size, v.Source.ModifiedNS, v.Source.Incarnation, v.Source.LibraryID, v.Source.LibraryGeneration, v.Source.Incarnation, v.Source.RootIdentity, v.Source.RemoteSourceID, v.Source.RawConfigurationGeneration, v.Source.EffectiveConfiguration, v.Source.ProfileRevision).Scan(&current)
	if err != nil {
		return 0, err
	}
	if !current {
		return 0, ErrCurrent
	}
	return until, nil
}

// activeFamilySessionTx follows the existing playback session-family relation
// only for an already-active immutable presentation. Pending publication and
// activation keep the exact original token hash. This does not select a latest
// actor: the actual legacy row must name the current token in this stored
// original family/generation/device/viewer tuple. The mandatory Check still
// proves current ordinary presentation/content/source authority separately.
func (s *Store) activeFamilySessionTx(ctx context.Context, tx *sql.Tx, v Snapshot, currentHash string) error {
	var horizon, tokenExpiry string
	err := tx.QueryRowContext(ctx, `SELECT family.authorization_horizon,current.expires_at
 FROM authorization_family_tokens original
 JOIN authorization_family_tokens current ON current.family_id=original.family_id
 JOIN authorization_session_families family ON family.id=current.family_id
 JOIN identity_device_families device ON device.family_id=family.id
 WHERE original.token_hash=? AND original.generation=? AND original.family_id=?
 AND current.token_hash=? AND current.retired=0 AND current.generation=family.current_generation AND current.generation>=original.generation
 AND family.revoked=0 AND family.server_id=? AND family.account_id=? AND family.profile_id=? AND family.authority=? AND family.epoch=? AND device.device_id=?
 AND EXISTS(SELECT 1 FROM playback_disc_snapshots d JOIN playback_disc_lifecycle l ON l.snapshot_id=d.id
 WHERE d.id=? AND d.session_id=? AND d.generation=? AND l.state='active' AND l.sealed=1)`,
		v.Actor.SessionHash, v.Actor.TokenGeneration, v.Actor.FamilyID, currentHash, v.Actor.ServerID, v.Actor.AccountID, v.Actor.ProfileID, v.Actor.Authority, v.Actor.Epoch, v.Actor.DeviceID,
		v.ID, v.SessionID, v.Generation).Scan(&horizon, &tokenExpiry)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrCurrent
		}
		return err
	}
	now := time.Now()
	for _, raw := range []string{horizon, tokenExpiry} {
		expires, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || !expires.After(now) {
			return ErrCurrent
		}
	}
	return nil
}
func (s *Store) checkTx(ctx context.Context, tx *sql.Tx, v Snapshot, check Check) (int64, error) {
	if s == nil || s.serverID == "" || check == nil {
		return 0, ErrInput
	}
	// A callback gets its own deep value; mutating it cannot retarget this store's
	// original candidate or the value returned after its check.
	copy, err := freeze(v)
	if err != nil {
		return 0, err
	}
	if err = check(ctx, tx, copy); err != nil {
		return 0, err
	}
	if err = ctx.Err(); err != nil {
		return 0, err
	}
	until, err := s.sessionTx(ctx, tx, v)
	if err != nil {
		return 0, err
	}
	if err = s.checkCaptureTx(ctx, tx, v); err != nil {
		return 0, err
	}
	return until, nil
}

// PublishPendingTx stores one exact measured candidate beside the newly created
// legacy session in its accepting writer. It neither activates a presentation
// nor starts a converter. A duplicate key is a conflict, never replacement.
func (s *Store) PublishPendingTx(ctx context.Context, tx *sql.Tx, in Snapshot, check Check) error {
	if e := writeTx(ctx, tx); e != nil {
		return e
	}
	v, e := freeze(in)
	if e != nil {
		return e
	}
	return s.publishPendingValueTx(ctx, tx, v, func() (int64, error) { return s.checkTx(ctx, tx, v, check) })
}

// The value and proof closure come only from normal frozen publication or the
// private same-transaction generation claim. No row caller can select a mode.
func (s *Store) publishPendingValueTx(ctx context.Context, tx *sql.Tx, v Snapshot, prove func() (int64, error)) error {
	until, e := prove()
	if e != nil {
		return e
	}
	var exists bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_disc_snapshots WHERE id=? OR (session_id=? AND generation=?))`, v.ID, v.SessionID, v.Generation).Scan(&exists); e != nil {
		return e
	}
	if exists {
		return ErrConflict
	}
	kind := "conditional"
	if v.Input.Captured != nil {
		kind = "captured"
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO playback_disc_snapshots(id,session_id,generation,input_kind) VALUES(?,?,?,?)`, v.ID, v.SessionID, v.Generation, kind); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO playback_disc_lifecycle(snapshot_id,state,sealed,created_ms,expires_ms) VALUES(?,'pending',0,?,?)`, v.ID, time.Now().UnixMilli(), until); e != nil {
		return e
	}
	for _, fn := range []func() error{
		func() error { return writeActor(ctx, tx, v.ID, v.Actor) }, func() error { return writeOperation(ctx, tx, v.ID, v.Operation) }, func() error { return writeSource(ctx, tx, v.ID, v.Source) },
		func() error { return writeDisc(ctx, tx, v.ID, v.Disc) }, func() error { return writeMedia(ctx, tx, v.ID, v.Media) }, func() error { return writeTools(ctx, tx, v.ID, v.Tools) },
		func() error { return writeInput(ctx, tx, v) }, func() error { return writeStructure(ctx, tx, v.ID, v.Disc.Structure) }, func() error { return writeStreams(ctx, tx, v.ID, v.Media.Streams) }, func() error { return writeChapters(ctx, tx, v.ID, v.Media.Chapters) },
	} {
		if e = fn(); e != nil {
			return e
		}
	}
	if v.Input.Captured != nil {
		if _, e = tx.ExecContext(ctx, `INSERT INTO owned_capture_presentation_refs(snapshot_id,capture_id,expires_ms) VALUES(?,?,?)`, v.ID, v.Input.Captured.Receipt.ID, until); e != nil {
			return e
		}
	}
	// The seal is internal publication state, not a caller-provided readiness bit.
	if _, e = prove(); e != nil {
		return e
	}
	result, e := tx.ExecContext(ctx, `UPDATE playback_disc_lifecycle SET sealed=1 WHERE snapshot_id=? AND state='pending' AND sealed=0 AND expires_ms>?`, v.ID, time.Now().UnixMilli())
	return one(result, e)
}
func one(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrCurrent
	}
	return nil
}
func (s *Store) readTx(ctx context.Context, tx *sql.Tx, key Key, id string, l lifecycle) (Snapshot, error) {
	v := Snapshot{ID: id, SessionID: key.SessionID, Generation: key.Generation}
	var err error
	if v.Actor, err = readActor(ctx, tx, id); err != nil {
		return Snapshot{}, err
	}
	if v.Operation, err = readOperation(ctx, tx, id); err != nil {
		return Snapshot{}, err
	}
	if v.Source, err = readSource(ctx, tx, id); err != nil {
		return Snapshot{}, err
	}
	if v.Input, err = s.readInput(ctx, tx, id, l.kind); err != nil {
		return Snapshot{}, err
	}
	if v.Disc, err = readDisc(ctx, tx, id); err != nil {
		return Snapshot{}, err
	}
	if v.Disc.Structure, err = readStructure(ctx, tx, id, v.Disc.Structure); err != nil {
		return Snapshot{}, err
	}
	var present bool
	if v.Media, present, err = readMedia(ctx, tx, id); err != nil {
		return Snapshot{}, err
	}
	if v.Media.Streams, err = readStreams(ctx, tx, id); err != nil {
		return Snapshot{}, err
	}
	if v.Media.Chapters, err = readChapters(ctx, tx, id, present); err != nil {
		return Snapshot{}, err
	}
	if v.Tools, err = readTools(ctx, tx, id); err != nil {
		return Snapshot{}, err
	}
	return freeze(v)
}

// ActivateTx is called only by the original v1 presentation accepting writer,
// after its queue/custody/cast/idempotency proofs. The mandatory Check retains
// the original process proof; normalized facts cannot reconstruct one.
// Its returned value grants no bytes: consumers start only after the OUTERMOST
// accepting transaction commits, never from an uncommitted activation result.
func (s *Store) ActivateTx(ctx context.Context, tx *sql.Tx, key Key, id string, check Check) (Snapshot, error) {
	if e := writeTx(ctx, tx); e != nil {
		return Snapshot{}, e
	}
	return s.activateValueTx(ctx, tx, key, id, func(v Snapshot) (int64, error) { return s.checkTx(ctx, tx, v, check) })
}

func (s *Store) activateValueTx(ctx context.Context, tx *sql.Tx, key Key, id string, prove func(Snapshot) (int64, error)) (Snapshot, error) {
	l, e := s.identityTx(ctx, tx, key, id)
	if e != nil {
		return Snapshot{}, e
	}
	if l.state != "pending" || !l.sealed || l.expires <= time.Now().UnixMilli() {
		return Snapshot{}, ErrCurrent
	}
	v, e := s.readTx(ctx, tx, key, id, l)
	if e != nil {
		return Snapshot{}, e
	}
	until, e := prove(v)
	if e != nil {
		return Snapshot{}, e
	}
	if until < l.expires {
		return Snapshot{}, ErrCurrent
	}
	r, e := tx.ExecContext(ctx, `UPDATE playback_disc_lifecycle SET state='active' WHERE snapshot_id=? AND state='pending' AND sealed=1 AND expires_ms>?`, id, time.Now().UnixMilli())
	if e = one(r, e); e != nil {
		return Snapshot{}, e
	}
	return v, nil
}

// ResolveTx requires the exact original key AND snapshot ID. It never selects a
// latest snapshot, another viewer's capture or mutable global disc facts.
func (s *Store) ResolveTx(ctx context.Context, tx *sql.Tx, key Key, id string, check Check) (Snapshot, error) {
	l, e := s.identityTx(ctx, tx, key, id)
	if e != nil {
		return Snapshot{}, e
	}
	if l.state != "active" || !l.sealed || l.expires <= time.Now().UnixMilli() {
		return Snapshot{}, ErrCurrent
	}
	v, e := s.readTx(ctx, tx, key, id, l)
	if e != nil {
		return Snapshot{}, e
	}
	until, e := s.checkTx(ctx, tx, v, check)
	if e != nil {
		return Snapshot{}, e
	}
	if until < l.expires {
		return Snapshot{}, ErrCurrent
	}
	after, e := s.identityTx(ctx, tx, key, id)
	if e != nil || after != l {
		return Snapshot{}, ErrCurrent
	}
	return v, ctx.Err()
}
