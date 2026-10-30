package dvr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"time"
)

// Explicit commitments follow reliable guide facts and keep the accepted
// interval when the guide is temporarily missing. Every cursor page is reachable.
func (s *Store) ReconcileScheduled(ctx context.Context) error {
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	cursor := ""
	e = tx.QueryRowContext(ctx, `SELECT cursor FROM dvr_maintenance_cursors WHERE kind='scheduled'`).Scan(&cursor)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	type entry struct {
		id string
		o  livechannels.Owner
	}
	list := []entry{}
	rows, e := tx.QueryContext(ctx, `SELECT id,authority,account_id,profile_id FROM dvr_recordings WHERE rule_id='' AND state IN('scheduled','conflicted','waiting-source','waiting-guide') AND id>? ORDER BY id LIMIT 64`, cursor)
	if e != nil {
		return e
	}
	for rows.Next() {
		var v entry
		if e = rows.Scan(&v.id, &v.o.Authority, &v.o.AccountID, &v.o.ProfileID); e != nil {
			rows.Close()
			return e
		}
		list = append(list, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range list {
		cursor = v.id
		r, e := loadTx(ctx, tx, v.o, v.id)
		if e != nil {
			return e
		}
		permission := s.durable(ctx, tx, v.o, r.Occurrence.SourceID, r.Occurrence.ChannelID)
		if permission != nil && !errors.Is(permission, livechannels.ErrDenied) && !errors.Is(permission, ErrDenied) {
			continue
		}
		var generation, sourceState string
		e = tx.QueryRowContext(ctx, `SELECT active_generation,state FROM live_sources WHERE id=?`, r.Occurrence.SourceID).Scan(&generation, &sourceState)
		if e != nil {
			return e
		}
		shared, err := s.hasSharedCaptureTx(ctx, tx, r.ID)
		if err != nil {
			return err
		}
		// This intent already committed to a capture. Finalization and linking
		// may outlast the airing; guide maintenance must not expire it.
		if permission == nil && shared {
			continue
		}
		p, lookup := s.committedProgrammeTx(ctx, tx, r, generation)
		state, reason := "scheduled", ""
		switch {
		case permission != nil:
			state, reason = "cancelled", "permission-denied"
		case sourceState != "active":
			state, reason = "waiting-source", "source-disabled"
		case errors.Is(lookup, livechannels.ErrConflict):
			state, reason = "waiting-guide", "programme-unavailable"
		case lookup != nil:
			return lookup
		}
		start, end := time.Time{}, time.Time{}
		if state == "scheduled" {
			start, end, e = padded(p, r.Options)
			if e != nil {
				return e
			}
			if !end.After(s.now()) {
				state, reason = "failed", "capture-window-missed"
			}
		}
		changed := state != r.State || reason != r.Reason
		if state == "scheduled" {
			if generation != r.Occurrence.Generation || digest(p) != digest(r.Programme) {
				changed = true
			}
			if changed {
				body, _ := json.Marshal(p)
				_, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET programme_id=?,guide_generation=?,programme_json=?,start_ms=?,end_ms=?,state='scheduled',reason='',revision=revision+1,updated_ms=? WHERE id=?`, p.ID, generation, string(body), start.UnixMilli(), end.UnixMilli(), s.now().UnixMilli(), r.ID)
				if e != nil {
					return e
				}
			}
			if e = reserveTx(ctx, tx, r.ID, r.Occurrence.SourceID, start, end, r.Options.Priority); e != nil {
				return e
			}
		} else {
			if changed {
				_, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET state=?,reason=?,revision=revision+1,updated_ms=? WHERE id=?`, state, reason, s.now().UnixMilli(), r.ID)
				if e != nil {
					return e
				}
			}
			if e = unreserveTx(ctx, tx, r.ID); e != nil {
				return e
			}
		}
		if changed {
			if e = touchTx(ctx, tx, v.o); e != nil {
				return e
			}
		}
	}
	if len(list) < 64 {
		cursor = ""
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO dvr_maintenance_cursors VALUES('scheduled',?) ON CONFLICT(kind) DO UPDATE SET cursor=excluded.cursor WHERE dvr_maintenance_cursors.cursor<>excluded.cursor`, cursor)
	if e != nil {
		return e
	}
	return gated.Commit()
}

// A current exact guide occurrence can extend an old accepted window. Let
// reconciliation update it before expiry; do not let a delayed bounded sweep
// turn that valid commitment into terminal history. This is identity-only, never
// a title/time guess, and the anti-join keeps later expired rows reachable.
const expiredCandidates = `SELECT id,authority,account_id,profile_id,reason FROM dvr_recordings
 WHERE state IN('scheduled','conflicted','waiting-source','waiting-guide') AND end_ms<=?
 AND NOT EXISTS(SELECT 1 FROM dvr_recording_shares x JOIN dvr_recordings leader ON leader.id=x.capture_recording_id WHERE x.recording_id=dvr_recordings.id AND (leader.state IN('preparing','recording','finalizing','completed','incomplete-playable','pending-delete') OR (leader.state='cancelled' AND (leader.lease_until_ms>0 OR COALESCE(json_extract(leader.checkpoint_json,'$.sharedReady'),0)=1))))
 AND NOT EXISTS(SELECT 1 FROM live_sources s JOIN live_programmes p ON p.generation_id=s.active_generation
 WHERE s.id=dvr_recordings.source_id AND p.id=dvr_recordings.programme_id AND p.channel_id=dvr_recordings.channel_id
 AND CAST(round((julianday(p.end_utc)-2440587.5)*86400000) AS INTEGER)+1000*COALESCE(
 (SELECT json_extract(config_json,'$.options.afterSeconds') FROM dvr_rules rule WHERE rule.id=dvr_recordings.rule_id AND rule.deleted=0),
 json_extract(dvr_recordings.options_json,'$.afterSeconds'),0)>?)
 ORDER BY end_ms,id LIMIT 64`

// Expiry is independent of installed decoder availability. Disabled sources,
// missing guide data, and lost confinement cannot strand elapsed intents forever.
func (s *Store) ExpireMissed(ctx context.Context) error {
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
	if e != nil {
		return e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	rows, e := tx.QueryContext(ctx, expiredCandidates, s.now().UnixMilli(), s.now().UnixMilli())
	if e != nil {
		return e
	}
	type expired struct {
		id, reason string
		o          livechannels.Owner
	}
	list := []expired{}
	for rows.Next() {
		var v expired
		if e = rows.Scan(&v.id, &v.o.Authority, &v.o.AccountID, &v.o.ProfileID, &v.reason); e != nil {
			rows.Close()
			return e
		}
		list = append(list, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range list {
		reason := "capture-window-missed"
		if v.reason == "tuner-conflict" {
			reason = "tuner-conflict"
		}
		if !v.o.Valid() {
			return ErrDenied
		}
		if _, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET state='failed',reason=?,finished_ms=?,updated_ms=?,revision=revision+1 WHERE id=?`, reason, s.now().UnixMilli(), s.now().UnixMilli(), v.id); e != nil {
			return e
		}
		if e = unreserveTx(ctx, tx, v.id); e != nil {
			return e
		}
		if e = touchTx(ctx, tx, v.o); e != nil {
			return e
		}
	}
	return gated2.Commit()
}
