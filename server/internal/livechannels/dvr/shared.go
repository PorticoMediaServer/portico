package dvr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"time"
)

func (s *Store) sharedCaptureTx(ctx context.Context, tx *sql.Tx, c *claim, r Recording, generation int64) (bool, bool, error) {
	if _, ok := s.driver.(SharedCaptureDriver); !ok {
		return false, false, nil
	}
	var leader string
	e := tx.QueryRowContext(ctx, `SELECT capture_recording_id FROM dvr_recording_shares WHERE recording_id=?`, r.ID).Scan(&leader)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return false, false, e
	}
	if leader == "" {
		e = tx.QueryRowContext(ctx, `SELECT id FROM dvr_recordings WHERE source_id=? AND channel_id=? AND programme_id=? AND id<>? AND (state IN('preparing','recording','finalizing','completed','incomplete-playable') OR (state IN('cancelled','pending-delete') AND reason IN('owner-cancelled','owner-delete') AND lease_until_ms>0)) AND NOT EXISTS(SELECT 1 FROM dvr_recording_shares x WHERE x.recording_id=dvr_recordings.id) ORDER BY start_ms,id LIMIT 1`, r.Occurrence.SourceID, r.Occurrence.ChannelID, r.Occurrence.ProgrammeID, r.ID).Scan(&leader)
		if errors.Is(e, sql.ErrNoRows) {
			return false, false, nil
		}
		if e != nil {
			return false, false, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO dvr_recording_shares VALUES(?,?)`, r.ID, leader); e != nil {
			return false, false, e
		}
	}
	var state string
	var ready bool
	var lease int64
	if e = tx.QueryRowContext(ctx, `SELECT state,lease_until_ms,(item_id>0 OR COALESCE(json_extract(checkpoint_json,'$.sharedReady'),0)=1) FROM dvr_recordings WHERE id=?`, leader).Scan(&state, &lease, &ready); e != nil {
		return false, false, e
	}
	withdrawn := state == "cancelled" || state == "pending-delete"
	if state != "completed" && state != "incomplete-playable" && !(withdrawn && ready) {
		if capturing(state) || withdrawn && lease > 0 {
			return true, true, nil
		}
		_, e = tx.ExecContext(ctx, `DELETE FROM dvr_recording_shares WHERE recording_id=?`, r.ID)
		return false, false, e
	}
	c.sharedFrom = leader
	c.request.Generation = generation + 1
	c.token, e = randomID()
	if e != nil {
		return false, false, e
	}
	c.request.ArtifactKey, e = mediaartifact.RetainedCaptureKey(r.ID, c.request.Generation)
	if e != nil {
		return false, false, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET state='preparing',reason='',claim_token=?,claim_generation=?,artifact_id=?,lease_until_ms=?,owner_instance=?,recovery_attempt=recovery_attempt+1,revision=revision+1,updated_ms=? WHERE id=?`, c.token, c.request.Generation, c.request.ArtifactKey, s.now().Add(workerTTL).UnixMilli(), s.instance, s.now().UnixMilli(), r.ID)
	if e != nil {
		return false, false, e
	}
	e = touchTx(ctx, tx, c.request.Owner)
	return true, false, e
}
func (s *Store) runSharedClaim(parent context.Context, c *claim) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	done := make(chan struct{})
	supervise.Go("dvr.shared.renew", func() {
		defer close(done)
		tick := time.NewTicker(4 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if s.renew(ctx, c, true) != nil {
					cancel()
					return
				}
			}
		}
	})
	defer func() { cancel(); <-done }()

	var source CaptureRequest
	var cp CaptureCheckpoint
	var raw, state string
	e := s.db.QueryRowContext(ctx, `SELECT authority,account_id,profile_id,artifact_id,checkpoint_json,state FROM dvr_recordings WHERE id=?`, c.sharedFrom).Scan(&source.Owner.Authority, &source.Owner.AccountID, &source.Owner.ProfileID, &source.ArtifactKey, &raw, &state)
	source.Recording.ID = c.sharedFrom
	if e == nil {
		e = json.Unmarshal([]byte(raw), &cp)
	}
	if e == nil && state != "completed" && state != "incomplete-playable" && state != "pending-delete" && !(state == "cancelled" && cp.SharedReady) {
		e = ErrConflict
	}
	if e == nil {
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			e = err
		} else {
			source.Recording, e = loadTx(ctx, tx, source.Owner, c.sharedFrom)
			tx.Rollback()
		}
	}
	if e == nil {
		c.checkpoint = cp
		cp.WriterRetired = true
		e = s.checkpoint(ctx, c, cp)
	}
	if e == nil {
		result, err := s.driver.(SharedCaptureDriver).Share(ctx, c.request, source, cp)
		e = err
		// Shared media keeps the leader's interval and gap evidence. It is never
		// made complete merely because this profile attached after capture ended.
		if e == nil {
			result, e = s.prepareRecordingLayout(ctx, c, result)
		}
		if e == nil {
			e = s.publish(ctx, c, result)
		}
	}
	if e != nil && ctx.Err() == nil {
		retry := !errors.Is(e, ErrDenied) && !errors.Is(e, livechannels.ErrDenied) && !errors.Is(e, ErrLease) && !errors.Is(e, ErrInvalid)
		s.failWorker(c, "shared-capture-unavailable", retry)
	}
}

// preventLastCaptureRemoval retains the source object until every pending
// profile attachment has acquired its independent immutable hard link.
func (s *Store) pendingSharedTx(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var busy bool
	e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dvr_recording_shares x JOIN dvr_recordings r ON r.id=x.recording_id WHERE x.capture_recording_id=? AND r.state NOT IN('completed','incomplete-playable','deleted','failed','cancelled','pending-delete'))`, id).Scan(&busy)
	return busy, e
}

// hasSharedCaptureTx includes finished leaders until the follower acquires its
// own path. Explicit cancellation and durable permission checks stay separate.
func (s *Store) hasSharedCaptureTx(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var shared bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dvr_recording_shares x JOIN dvr_recordings leader ON leader.id=x.capture_recording_id WHERE x.recording_id=? AND (leader.state IN('preparing','recording','finalizing','completed','incomplete-playable','pending-delete') OR (leader.state='cancelled' AND (leader.lease_until_ms>0 OR COALESCE(json_extract(leader.checkpoint_json,'$.sharedReady'),0)=1))))`, id).Scan(&shared)
	return shared, err
}

// A withdrawn owner cannot keep a capture running alone. Existing independent
// followers are reauthorized here; their normal claim/rule fences still apply.
func (s *Store) sharedAuthorityTx(ctx context.Context, tx *sql.Tx, id string) error {
	cursor := ""
	for {
		rows, err := tx.QueryContext(ctx, `SELECT r.id,r.authority,r.account_id,r.profile_id,r.source_id,r.channel_id,r.programme_json FROM dvr_recording_shares x JOIN dvr_recordings r ON r.id=x.recording_id LEFT JOIN dvr_rules rule ON rule.id=r.rule_id WHERE x.capture_recording_id=? AND r.id>? AND r.state IN('scheduled','conflicted','waiting-source','waiting-guide','preparing','recording','finalizing') AND (r.rule_id='' OR (rule.enabled=1 AND rule.deleted=0 AND rule.revision=r.rule_revision)) ORDER BY r.id LIMIT 64`, id, cursor)
		if err != nil {
			return err
		}
		type follower struct {
			id, source, channel, programme string
			owner                          livechannels.Owner
		}
		batch := []follower{}
		for rows.Next() {
			var f follower
			if err = rows.Scan(&f.id, &f.owner.Authority, &f.owner.AccountID, &f.owner.ProfileID, &f.source, &f.channel, &f.programme); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, f)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, f := range batch {
			cursor = f.id
			if err = s.durable(ctx, tx, f.owner, f.source, f.channel); err == nil {
				var programme livechannels.Programme
				if err = json.Unmarshal([]byte(f.programme), &programme); err != nil {
					return err
				}
				if err = programmeVisibleTx(ctx, tx, f.owner, programme); err == nil {
					return nil
				}
			}
			if !errors.Is(err, ErrDenied) && !errors.Is(err, livechannels.ErrDenied) {
				return err
			}
		}
		if len(batch) < 64 {
			return ErrLease
		}
	}
}
