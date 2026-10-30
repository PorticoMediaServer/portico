package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// These are policy-class mutation intents, never query work. A save writes at
// most six rows, regardless of catalogue size. Finish an in-flight finite pass
// before serving one coalesced follow-up so frequent saves cannot starve tails.
func requestLookupFanoutTx(ctx context.Context, tx *sql.Tx, effects ...string) error {
	for _, effect := range effects {
		if _, err := tx.ExecContext(ctx, `INSERT INTO metadata_lookup_fanout(effect) VALUES(?)
 ON CONFLICT(effect) DO UPDATE SET generation=generation+1,pending=1,rerun=1,next_attempt_ms=0`, effect); err != nil {
			return err
		}
	}
	return nil
}

const lookupFanoutRowLimit = 12

var lookupFanoutPlan = dbwork.WritePlan{
	Name: "global-lookup-policy-fanout-quantum", Rows: 160, Pages: 256, VMOps: 250_000,
	Why: "twelve raw work keys and their attention/count, selection predicate, artwork and publication-head effects, plus two marker writes; no provider IO",
}

type lookupFanoutMarker struct {
	effect, afterKind, upperKind                             string
	generation, passGeneration, afterID, upperID, lastServed int64
	attempts                                                 int
}

type lookupFanoutSource struct{ table, kind, id string }

func lookupFanoutSourceFor(effect string) (lookupFanoutSource, error) {
	switch effect {
	case "screen_consent":
		return lookupFanoutSource{"screen_metadata_policies", "library_id", ""}, nil
	case "screen", "screen_retry":
		return lookupFanoutSource{"screen_metadata_work", "target_kind", "target_id"}, nil
	case "mb":
		return lookupFanoutSource{"mb_jobs", "kind", "entity_id"}, nil
	case "tvdb":
		return lookupFanoutSource{"tvdb_jobs", "", "show_id"}, nil
	case "artwork":
		return lookupFanoutSource{"artwork_entity_heads", "kind", "entity_id"}, nil
	case "subtitles":
		return lookupFanoutSource{"subtitle_provider_candidates", "id", ""}, nil
	}
	return lookupFanoutSource{}, fmt.Errorf("unknown lookup fanout effect %q", effect)
}

// All SQL identifiers come exclusively from the fixed internal source map.
// Each bound is a PK seek, including composite row-value bounds. Filtering is
// deliberately deferred until after LIMIT so rejected rows advance the cursor.
func (s lookupFanoutSource) columns() string {
	if s.kind == "" {
		return "''," + s.id
	}
	if s.id == "" {
		return s.kind + ",0"
	}
	return s.kind + "," + s.id
}
func (s lookupFanoutSource) order(desc bool) string {
	suffix := ""
	if desc {
		suffix = " DESC"
	}
	if s.kind == "" {
		return s.id + suffix
	}
	if s.id == "" {
		return s.kind + suffix
	}
	return s.kind + suffix + "," + s.id + suffix
}
func (s lookupFanoutSource) window(m *lookupFanoutMarker) (string, []any) {
	if s.kind == "" {
		return s.id + ">? AND " + s.id + "<=?", []any{m.afterID, m.upperID}
	}
	if s.id == "" {
		return s.kind + ">? AND " + s.kind + "<=?", []any{m.afterKind, m.upperKind}
	}
	return "(" + s.columns() + ")>(?,?) AND (" + s.columns() + ")<=(?,?)", []any{m.afterKind, m.afterID, m.upperKind, m.upperID}
}

// LookupFanoutStep commits one atomic bounded turn. Writer admission serializes
// overlapping callers. No lease, goroutine or provider request outlives a turn;
// cancellation/rollback leaves the previously committed cursor intact.
func (s *Service) LookupFanoutStep(ctx context.Context) error {
	now := s.publicationTime().UnixMilli()
	var due bool
	if err := dbwork.QueryRow(ctx, s.db, `SELECT EXISTS(SELECT 1 FROM metadata_lookup_fanout WHERE pending=1 AND next_attempt_ms<=?)`, now).Scan(&due); err != nil || !due {
		return err
	}
	w, err := dbwork.Begin(dbwork.WithPlan(ctx, lookupFanoutPlan), s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer w.Rollback()
	var m lookupFanoutMarker
	err = w.Tx().QueryRowContext(ctx, `SELECT effect,generation,pass_generation,after_kind,after_id,upper_kind,upper_id,last_served_ms,attempts FROM metadata_lookup_fanout WHERE pending=1 AND next_attempt_ms<=? ORDER BY last_served_ms,effect LIMIT 1`, now).Scan(&m.effect, &m.generation, &m.passGeneration, &m.afterKind, &m.afterID, &m.upperKind, &m.upperID, &m.lastServed, &m.attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = s.lookupFanoutQuantum(ctx, w.Tx(), &m, now); err != nil {
		_ = w.Rollback()
		if ctx.Err() != nil {
			return err
		}
		return errors.Join(err, s.lookupFanoutBackoff(ctx, m, now))
	}
	return w.Commit()
}

func (s *Service) lookupFanoutQuantum(ctx context.Context, tx *sql.Tx, m *lookupFanoutMarker, now int64) error {
	source, err := lookupFanoutSourceFor(m.effect)
	if err != nil {
		return err
	}
	if m.passGeneration == 0 {
		m.passGeneration = m.generation
		m.afterKind = ""
		m.afterID = 0
		m.upperKind = ""
		m.upperID = 0
		err = tx.QueryRowContext(ctx, `SELECT `+source.columns()+` FROM `+source.table+` ORDER BY `+source.order(true)+` LIMIT 1`).Scan(&m.upperKind, &m.upperID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE metadata_lookup_fanout SET pass_generation=?,rerun=0,after_kind='',after_id=0,upper_kind=?,upper_id=? WHERE effect=?`, m.passGeneration, m.upperKind, m.upperID, m.effect); err != nil {
			return err
		}
	}
	where, args := source.window(m)
	args = append(args, lookupFanoutRowLimit)
	rows, err := tx.QueryContext(ctx, `SELECT `+source.columns()+` FROM `+source.table+` WHERE `+where+` ORDER BY `+source.order(false)+` LIMIT ?`, args...)
	if err != nil {
		return err
	}
	type key struct {
		kind string
		id   int64
	}
	page := make([]key, 0, lookupFanoutRowLimit)
	for rows.Next() {
		var k key
		if err = rows.Scan(&k.kind, &k.id); err != nil {
			rows.Close()
			return err
		}
		page = append(page, k)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, k := range page {
		if err = applyLookupFanoutKey(ctx, tx, m.effect, k.kind, k.id); err != nil {
			return err
		}
		m.afterKind, m.afterID = k.kind, k.id
	}
	stamp := `MAX(?,COALESCE((SELECT MAX(last_served_ms) FROM metadata_lookup_fanout),0)+1)` // <= seven policy classes
	done := len(page) < lookupFanoutRowLimit || m.afterKind == m.upperKind && m.afterID == m.upperID
	if done {
		// A completed old pass acknowledges only its captured witness. Even if
		// newer intent arrived mid-pass, that generation remains pending.
		_, err = tx.ExecContext(ctx, `UPDATE metadata_lookup_fanout SET served_generation=pass_generation,pending=CASE WHEN generation=pass_generation AND rerun=0 THEN 0 ELSE 1 END,pass_generation=0,after_kind='',after_id=0,upper_kind='',upper_id=0,attempts=0,next_attempt_ms=0,error_code='',last_served_ms=`+stamp+` WHERE effect=?`, now, m.effect)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE metadata_lookup_fanout SET after_kind=?,after_id=?,attempts=0,next_attempt_ms=0,error_code='',last_served_ms=`+stamp+` WHERE effect=?`, m.afterKind, m.afterID, now, m.effect)
	}
	return err
}

func applyLookupFanoutKey(ctx context.Context, tx *sql.Tx, effect, kind string, id int64) error {
	var err error
	switch effect {
	case "screen_consent":
		_, err = tx.ExecContext(ctx, `INSERT INTO screen_metadata_fanout(library_id,effect) VALUES(?,'screen')
 ON CONFLICT(library_id,effect) DO UPDATE SET generation=generation+1,pending=1,rerun=1`, kind)
	case "screen":
		_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status=CASE WHEN status='provider_disabled' THEN 'pending' ELSE status END,generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error='' WHERE target_kind=? AND target_id=?`, kind, id)
	case "screen_retry":
		_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status='pending',next_attempt='',attempts=0,error='',generation=generation+1,revision=revision+1 WHERE target_kind=? AND target_id=? AND status='unavailable' AND error IN('request_rejected','provider_not_configured')`, kind, id)
	case "mb":
		_, err = tx.ExecContext(ctx, `UPDATE mb_jobs SET generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error='' WHERE kind=? AND entity_id=?`, kind, id)
	case "tvdb":
		_, err = tx.ExecContext(ctx, `UPDATE tvdb_jobs SET generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error='' WHERE show_id=?`, id)
	case "artwork":
		_, err = tx.ExecContext(ctx, `INSERT INTO artwork_dirty(kind,entity_id) VALUES(?,?) ON CONFLICT(kind,entity_id) DO NOTHING`, kind, id)
	case "subtitles":
		_, err = tx.ExecContext(ctx, `DELETE FROM subtitle_provider_candidates WHERE id=? AND lookup_epoch<>(SELECT lookup_epoch FROM metadata_runtime_settings WHERE singleton=1)`, kind)
	default:
		return fmt.Errorf("unknown lookup fanout effect %q", effect)
	}
	return err
}

func (s *Service) lookupFanoutBackoff(ctx context.Context, m lookupFanoutMarker, now int64) error {
	w, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer w.Rollback()
	_, err = w.Tx().ExecContext(ctx, `UPDATE metadata_lookup_fanout SET attempts=MIN(attempts,11)+1,next_attempt_ms=?,error_code='fanout_failed' WHERE effect=? AND last_served_ms=? AND generation=?`, now+screenFanoutRetryDelay(m.attempts).Milliseconds(), m.effect, m.lastServed, m.generation)
	if err != nil {
		return err
	}
	return w.Commit()
}
