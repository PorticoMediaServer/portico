package dvr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	workerloop "github.com/PorticoMediaServer/portico/server/internal/worker"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

// ReconcileOne makes bounded, restartable progress. A rule edit, guide generation
// change, or current permission change is checked in the publication transaction.
// History and physically running captures are never rebound to another airing.
func (s *Store) ReconcileOne(ctx context.Context) (bool, error) {
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
	if e != nil {
		return false, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var id, generation, phase, cursor, config string
	var revision int64
	var enabled, deleted bool
	var o livechannels.Owner
	e = tx.QueryRowContext(ctx, `SELECT r.id,r.revision,r.authority,r.account_id,r.profile_id,r.config_json,r.enabled,r.deleted,w.generation,w.phase,w.cursor FROM dvr_rule_work w JOIN dvr_rules r ON r.id=w.rule_id WHERE w.phase!='idle' AND w.not_before_ms<=? ORDER BY w.updated_ms,w.rule_id LIMIT 1`, s.now().UnixMilli()).Scan(&id, &revision, &o.Authority, &o.AccountID, &o.ProfileID, &config, &enabled, &deleted, &generation, &phase, &cursor)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	var c RuleConfig
	if json.Unmarshal([]byte(config), &c) != nil {
		return false, ErrUnavailable
	}
	var current, state string
	e = tx.QueryRowContext(ctx, `SELECT active_generation,state FROM live_sources WHERE id=?`, c.SourceID).Scan(&current, &state)
	if e != nil {
		return false, e
	}
	if generation != current {
		_, e = tx.ExecContext(ctx, `UPDATE dvr_rule_work SET rule_revision=?,generation=?,phase='existing',cursor='',updated_ms=?,diagnostic='' WHERE rule_id=?`, revision, current, s.now().UnixMilli(), id)
		if e != nil {
			return false, e
		}
		return true, gated.Commit()
	}
	authorityError := s.durable(ctx, tx, o, c.SourceID, "")
	if authorityError != nil && !errors.Is(authorityError, ErrDenied) && !errors.Is(authorityError, livechannels.ErrDenied) {
		return false, authorityError
	}
	permitted := authorityError == nil
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	c.Enabled = enabled && !deleted && permitted && state == "active"
	diagnostic := ""
	if !permitted {
		diagnostic = "permission-denied"
	} else if state != "active" && enabled {
		diagnostic = "source-disabled"
	}
	nextPhase, nextCursor := phase, cursor
	changed := false
	if phase == "existing" {
		rows, e := tx.QueryContext(ctx, `SELECT `+recordingColumns+` FROM dvr_recordings WHERE rule_id=? AND manual=0 AND id>? AND (state IN('scheduled','conflicted','waiting-source','waiting-guide') OR(state='cancelled' AND reason IN('rule-disabled','rule-no-longer-matches') AND end_ms>?)) ORDER BY id LIMIT 64`, id, cursor, s.now().UnixMilli())
		if e != nil {
			return false, e
		}
		records := []Recording{}
		for rows.Next() {
			r, e := scanRecording(rows)
			if e != nil {
				rows.Close()
				return false, e
			}
			records = append(records, r)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return false, e
		}
		for _, r := range records {
			nextCursor = r.ID
			// Only unclaimed intent (including reversible rule exclusions) changes.
			p, e := s.live.ProgrammeTx(ctx, tx, c.SourceID, r.Occurrence.ChannelID, current, r.Occurrence.ProgrammeID)
			shared, err := s.hasSharedCaptureTx(ctx, tx, r.ID)
			if err != nil {
				return false, err
			}
			if shared && enabled && !deleted && permitted {
				p, e = r.Programme, nil
			}
			reason := ""
			desired := "scheduled"
			if !enabled || deleted {
				desired, reason = "cancelled", "rule-disabled"
			} else if !permitted {
				desired, reason = "cancelled", "permission-denied"
			} else if state != "active" {
				desired, reason = "waiting-source", "source-disabled"
			} else if errors.Is(e, livechannels.ErrConflict) {
				desired, reason = "waiting-guide", "programme-unavailable"
			} else if e != nil {
				return false, e
			} else if !c.Matches(p) {
				desired, reason = "cancelled", "rule-no-longer-matches"
			}
			if desired != "scheduled" {
				if r.State != desired || r.Reason != reason {
					_, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET state=?,reason=?,revision=revision+1,updated_ms=? WHERE id=?`, desired, reason, s.now().UnixMilli(), r.ID)
					if e != nil {
						return false, e
					}
					changed = true
				}
				if e = unreserveTx(ctx, tx, r.ID); e != nil {
					return false, e
				}
				continue
			}
			start, end, e := padded(p, c.Options)
			if e != nil {
				return false, e
			}
			pb, _ := json.Marshal(p)
			ob, _ := json.Marshal(c.Options)
			var acceptedRevision int64
			if e = tx.QueryRowContext(ctx, `SELECT rule_revision FROM dvr_recordings WHERE id=?`, r.ID).Scan(&acceptedRevision); e != nil {
				return false, e
			}
			if acceptedRevision != revision || r.Occurrence.Generation != current || digest(r.Programme) != digest(p) || digest(r.Options) != digest(c.Options) || r.State != "scheduled" {
				_, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET guide_generation=?,programme_json=?,options_json=?,start_ms=?,end_ms=?,priority=?,rule_revision=?,state='scheduled',reason='',revision=revision+1,updated_ms=? WHERE id=?`, current, string(pb), string(ob), start.UnixMilli(), end.UnixMilli(), c.Options.Priority, revision, s.now().UnixMilli(), r.ID)
				if e != nil {
					return false, e
				}
				changed = true
			}
			if e = reserveTx(ctx, tx, r.ID, c.SourceID, start, end, c.Options.Priority); e != nil {
				return false, e
			}
		}
		if len(records) < 64 {
			nextPhase, nextCursor = "discover", ""
		}
	} else if phase == "discover" {
		if !c.Enabled || !s.captureAvailable {
			nextPhase, nextCursor = "idle", ""
		} else {
			programmeAllowed, err := programmePredicateTx(ctx, tx, o)
			if err != nil {
				return false, err
			}
			predicate, args := c.identitySQL()
			args = append([]any{current}, args...)
			args = append(args, cursor, s.now().UTC().Format(time.RFC3339))
			rows, e := tx.QueryContext(ctx, programmeQuery+` WHERE p.generation_id=? AND `+predicate+` AND p.id>? AND p.end_utc>? ORDER BY p.id LIMIT 64`, args...)
			if e != nil {
				return false, e
			}
			programmes := []livechannels.Programme{}
			for rows.Next() {
				p, e := scanProgramme(rows)
				if e != nil {
					rows.Close()
					return false, e
				}
				programmes = append(programmes, p)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return false, e
			}
			for _, p := range programmes {
				nextCursor = p.ID
				if c.Episodes == "new" && p.NewEvidence == "unknown" {
					diagnostic = "unknown-new-evidence-skipped"
				}
				if !c.Matches(p) || !programmeAllowed(programmeRating(p)) {
					continue
				}
				if e = s.durable(ctx, tx, o, c.SourceID, p.ChannelID); e != nil {
					if errors.Is(e, livechannels.ErrDenied) || errors.Is(e, ErrDenied) {
						continue
					}
					return false, e
				}
				duplicate, err := duplicateEpisodeTx(ctx, tx, o, c.SourceID, p)
				if err != nil {
					return false, err
				}
				if duplicate {
					continue
				}
				start, end, e := padded(p, c.Options)
				if e != nil {
					return false, e
				}
				_, e = s.insertTx(ctx, tx, o, Occurrence{c.SourceID, p.ChannelID, current, p.ID}, p, c.Options, id, revision, false, start, end)
				// Cancellation of a canonical occurrence is a durable tombstone, not an
				// invitation for a subsequent guide refresh to create it again.
				if e != nil && !errors.Is(e, ErrConflict) {
					return false, e
				}
				changed = true
			}
			if len(programmes) < 64 {
				nextPhase, nextCursor = "idle", ""
			}
		}
	} else {
		return false, ErrUnavailable
	}
	if changed {
		if e = touchTx(ctx, tx, o); e != nil {
			return false, e
		}
	}
	_, e = tx.ExecContext(ctx, `UPDATE dvr_rule_work SET phase=?,cursor=?,updated_ms=?,diagnostic=CASE WHEN ?='' THEN diagnostic ELSE ? END WHERE rule_id=?`, nextPhase, nextCursor, s.now().UnixMilli(), diagnostic, diagnostic, id)
	if e != nil {
		return false, e
	}
	return true, gated.Commit()
}

// SweepRules is fair across the entire rule collection. It does not repeatedly
// stop after the first page. Active policy is rechecked even without a guide edit.
func (s *Store) SweepRules(ctx context.Context) error {
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
	if e != nil {
		return e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	var cursor string
	e = tx.QueryRowContext(ctx, `SELECT cursor FROM dvr_maintenance_cursors WHERE kind='rules'`).Scan(&cursor)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	type rule struct {
		id, generation, source string
		revision               int64
	}
	rows, e := tx.QueryContext(ctx, `SELECT r.id,r.revision,r.source_id,s.active_generation FROM dvr_rules r JOIN live_sources s ON s.id=r.source_id WHERE r.deleted=0 AND r.id>? ORDER BY r.id LIMIT 64`, cursor)
	if e != nil {
		return e
	}
	rules := []rule{}
	for rows.Next() {
		var r rule
		if e = rows.Scan(&r.id, &r.revision, &r.source, &r.generation); e != nil {
			rows.Close()
			return e
		}
		rules = append(rules, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, r := range rules {
		cursor = r.id
		_, e = tx.ExecContext(ctx, `INSERT INTO dvr_rule_work VALUES(?,?,?,'existing','',0,?,'') ON CONFLICT(rule_id) DO UPDATE SET rule_revision=excluded.rule_revision,generation=excluded.generation,phase='existing',cursor='',not_before_ms=0,updated_ms=excluded.updated_ms,diagnostic='' WHERE dvr_rule_work.phase='idle' AND (dvr_rule_work.generation<>excluded.generation OR dvr_rule_work.rule_revision<>excluded.rule_revision OR dvr_rule_work.updated_ms<?)`, r.id, r.revision, r.generation, s.now().UnixMilli(), s.now().Add(-time.Minute).UnixMilli())
		if e != nil {
			return e
		}
	}
	if len(rules) < 64 {
		cursor = ""
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO dvr_maintenance_cursors VALUES('rules',?) ON CONFLICT(kind) DO UPDATE SET cursor=excluded.cursor WHERE dvr_maintenance_cursors.cursor<>excluded.cursor`, cursor)
	if e != nil {
		return e
	}
	return gated2.Commit()
}

// RunIntentMaintenance has no physical capture side effects. It keeps drafts,
// exact guide bindings, and conflict projections coherent at server startup.
func (s *Store) RunIntentMaintenance(ctx context.Context) {
	wake := workerloop.NewSignal()
	unregister := dbwork.WakeOnTables(wake, "dvr_*", "live_*")
	defer unregister()
	workerloop.Run(ctx, "dvr.intent-maintenance", wake, func(ctx context.Context) time.Duration {
		work, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		defer cancel()
		_ = s.SweepRules(work)
		_ = s.ReconcileScheduled(work)
		for n := 0; n < 4; n++ {
			done, e := s.ReconcileOne(work)
			if e != nil || !done {
				// Nothing left to reconcile: wait to be told rather than asking
				// again every second for the life of the process.
				return 0
			}
		}
		return time.Second
	})
}

// Only reliable episode evidence suppresses another airing. A generic provider
// identifier or a title match never hides a programme the viewer meant to record.
func duplicateEpisodeTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner, source string, p livechannels.Programme) (bool, error) {
	if p.SeriesID == "" {
		return false, nil
	}
	episode := p.EpisodeID
	if episode != "" && !strings.HasSuffix(episode, "0000") {
		var exists bool
		e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dvr_recordings WHERE owner_key=? AND source_id=? AND programme_id<>? AND json_extract(programme_json,'$.seriesId')=? AND json_extract(programme_json,'$.episodeId')=? AND state NOT IN('failed','cancelled','deleted','pending-delete'))`, o.Key(), source, p.ID, p.SeriesID, episode).Scan(&exists)
		return exists, e
	}
	if p.Episode == nil || p.Episode.Season <= 0 || p.Episode.Number <= 0 {
		return false, nil
	}
	var exists bool
	e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dvr_recordings WHERE owner_key=? AND source_id=? AND programme_id<>? AND json_extract(programme_json,'$.seriesId')=? AND json_extract(programme_json,'$.episode.season')=? AND json_extract(programme_json,'$.episode.number')=? AND state NOT IN('failed','cancelled','deleted','pending-delete'))`, o.Key(), source, p.ID, p.SeriesID, p.Episode.Season, p.Episode.Number).Scan(&exists)
	return exists, e
}
