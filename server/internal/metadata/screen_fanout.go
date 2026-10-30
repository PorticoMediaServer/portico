package metadata

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The screen row's transitive plan is <=8 writes: work, attention, two
// count changes, artwork dirty, the exact identity lock, library browse revision
// and its change hint. Twelve work rows plus two marker writes have <=84 child
// effects and <=98 total writes, within 32 base units +128 children (160 total).
// This includes a low-confidence accepted publication. Artwork has at most
// one dirty insert per raw row, plus two marker writes. Neither window filters
// rejected rows before LIMIT. These are writer-turn limits, not corpus limits.
const (
	screenFanoutRawLimit  = 64
	screenFanoutRowLimit  = 12
	artworkFanoutRowLimit = 30
)

var screenFanoutPlan = dbwork.WritePlan{
	Name: "screen-policy-fanout-quantum", Rows: 160, Pages: 256, VMOps: 250_000,
	Why: "one atomic raw-keyset quantum: twelve screen base rows plus <=84 attention/count, browse revision/hint, artwork and identity-lock child effects and two marker writes; or thirty artwork heads",
}

type screenFanoutMarker struct {
	library, effect, afterKind, upperKind                    string
	afterID, upperID, generation, passGeneration, lastServed int64
	rematch, passRematch                                     bool
	attempts                                                 int
}

// screenRequestRematchTx only ORs intent into the marker created by the policy
// trigger. It deliberately does not advance its generation a second time.
func screenRequestRematchTx(ctx context.Context, tx *sql.Tx, library string, providers []string) error {
	if len(providers) == 0 {
		return nil // lookups off keeps the last match, as before
	}
	_, err := tx.ExecContext(ctx, `UPDATE screen_metadata_fanout SET rematch=1 WHERE library_id=? AND effect='screen'`, library)
	return err
}

// ScreenFanoutStep commits one raw-keyset quantum and its effects atomically.
// SQLite's writer admission serializes callers; no lease survives the turn and
// a failed or cancelled turn cannot move the durable cursor. Markers convey
// mutation intent only: acquisition and publication still prove live authority.
func (s *Service) ScreenFanoutStep(ctx context.Context) error {
	now := s.publicationTime().UnixMilli()
	var due bool
	if err := dbwork.QueryRow(ctx, s.db, `SELECT EXISTS(SELECT 1 FROM screen_metadata_fanout WHERE pending=1 AND next_attempt_ms<=?)`, now).Scan(&due); err != nil || !due {
		return err
	}
	w, err := dbwork.Begin(dbwork.WithPlan(ctx, screenFanoutPlan), s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer w.Rollback()
	tx := w.Tx()
	var m screenFanoutMarker
	err = tx.QueryRowContext(ctx, `SELECT library_id,effect,generation,pass_generation,rematch,pass_rematch,after_kind,after_id,upper_kind,upper_id,last_served_ms,attempts FROM screen_metadata_fanout WHERE pending=1 AND next_attempt_ms<=? ORDER BY next_attempt_ms,last_served_ms,library_id,effect LIMIT 1`, now).Scan(
		&m.library, &m.effect, &m.generation, &m.passGeneration, &m.rematch, &m.passRematch, &m.afterKind, &m.afterID, &m.upperKind, &m.upperID, &m.lastServed, &m.attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// Record retry state only after rollback, guarded by the last committed
	// turn's stamp so another caller's successful progress cannot be penalized.
	if err = s.screenFanoutQuantum(ctx, tx, &m, now); err != nil {
		_ = w.Rollback()
		if ctx.Err() == nil {
			return errors.Join(err, s.screenFanoutBackoff(ctx, m, now))
		}
		return err
	}
	return w.Commit()
}

func (s *Service) screenFanoutQuantum(ctx context.Context, tx *sql.Tx, m *screenFanoutMarker, now int64) error {
	var providers []string
	var catalogLibrary int64
	// Re-read the current policy, consent, scan and configuration in this same
	// transaction. Sources are not copied into a marker: no stale source decision
	// is used by the pass, and every provider publication keeps its full fence.
	if m.effect == "screen" {
		var configuration, scan int64
		err := tx.QueryRowContext(ctx, `SELECT c.metadata_revision,p.revision FROM libraries l JOIN library_configuration c ON c.library_id=l.id JOIN library_scan_policies p ON p.library_id=l.id JOIN screen_metadata_policies s ON s.library_id=l.id WHERE l.id=?`, m.library).Scan(&configuration, &scan)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = tx.ExecContext(ctx, `DELETE FROM screen_metadata_fanout WHERE library_id=? AND effect=?`, m.library, m.effect)
			return err
		}
		if err != nil {
			return err
		}
		p, err := readScreenPolicy(ctx, tx, m.library)
		if err != nil {
			return err
		}
		providers = p.Providers
	} else {
		// Non-screen libraries have no screen policy. Artwork needs only the
		// library/catalogue identity; integer library and kind keys stay integer.
		err := tx.QueryRowContext(ctx, `SELECT c.id FROM catalog_libraries c JOIN libraries l ON l.id=c.library_id WHERE l.id=?`, m.library).Scan(&catalogLibrary)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = tx.ExecContext(ctx, `DELETE FROM screen_metadata_fanout WHERE library_id=? AND effect=?`, m.library, m.effect)
			return err
		}
		if err != nil {
			return err
		}
	}
	if m.passGeneration == 0 {
		m.passGeneration, m.passRematch = m.generation, m.rematch
		m.afterKind, m.afterID = "", 0
		var err error
		if m.effect == "screen" {
			err = tx.QueryRowContext(ctx, `SELECT target_kind,target_id FROM screen_metadata_work INDEXED BY screen_metadata_fanout_walk WHERE library_id=? ORDER BY target_kind DESC,target_id DESC LIMIT 1`, m.library).Scan(&m.upperKind, &m.upperID)
		} else {
			var kind int64
			err = tx.QueryRowContext(ctx, `SELECT kind,id FROM catalog_entities INDEXED BY catalog_entities_library WHERE library_id=? ORDER BY kind DESC,id DESC LIMIT 1`, catalogLibrary).Scan(&kind, &m.upperID)
			m.upperKind = strconv.FormatInt(kind, 10)
			m.afterKind = "0"
		}
		if errors.Is(err, sql.ErrNoRows) {
			m.upperID = 0
		} else if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE screen_metadata_fanout SET pass_generation=?,pass_rematch=?,rerun=0,after_kind=?,after_id=0,upper_kind=?,upper_id=? WHERE library_id=? AND effect=?`, m.passGeneration, m.passRematch, m.afterKind, m.upperKind, m.upperID, m.library, m.effect); err != nil {
			return err
		}
	}
	done := m.upperID == 0
	if !done {
		var err error
		if m.effect == "screen" {
			done, err = screenFanoutScreenPage(ctx, tx, m, providers)
		} else {
			done, err = screenFanoutArtworkPage(ctx, tx, m, catalogLibrary)
		}
		if err != nil {
			return err
		}
	}
	// An indexed reverse seek supplies a logical clock even when the wall clock
	// is fixed or moves backwards. Each library/effect goes behind all pending
	// peers after a turn, including peers with a long-running pass.
	stamp := `MAX(?,COALESCE((SELECT last_served_ms FROM screen_metadata_fanout WHERE pending=1 ORDER BY last_served_ms DESC LIMIT 1),0)+1)`
	if done {
		_, err := tx.ExecContext(ctx, `UPDATE screen_metadata_fanout SET served_generation=pass_generation,
 rematch=CASE WHEN pass_rematch=1 AND rerun=0 THEN 0 ELSE rematch END,
 pending=rerun,pass_generation=0,pass_rematch=0,after_kind='',after_id=0,upper_kind='',upper_id=0,
 attempts=0,next_attempt_ms=?,error_code='',last_served_ms=`+stamp+` WHERE library_id=? AND effect=?`, now, now, m.library, m.effect)
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE screen_metadata_fanout SET after_kind=?,after_id=?,attempts=0,next_attempt_ms=?,error_code='',last_served_ms=`+stamp+` WHERE library_id=? AND effect=?`, m.afterKind, m.afterID, now, now, m.library, m.effect)
	return err
}

func screenFanoutScreenPage(ctx context.Context, tx *sql.Tx, m *screenFanoutMarker, providers []string) (bool, error) {
	type target struct {
		kind, provider string
		id             int64
	}
	rows, err := tx.QueryContext(ctx, `SELECT target_kind,target_id,provider FROM screen_metadata_work INDEXED BY screen_metadata_fanout_walk WHERE library_id=? AND (target_kind,target_id)>(?,?) AND (target_kind,target_id)<=(?,?) ORDER BY target_kind,target_id LIMIT ?`, m.library, m.afterKind, m.afterID, m.upperKind, m.upperID, screenFanoutRowLimit)
	if err != nil {
		return false, err
	}
	page := make([]target, 0, screenFanoutRowLimit)
	for rows.Next() {
		var t target
		if err = rows.Scan(&t.kind, &t.id, &t.provider); err != nil {
			rows.Close()
			return false, err
		}
		page = append(page, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, t := range page {
		rematch := m.passRematch && len(providers) > 0 && t.provider != ""
		for _, p := range providers {
			if p == t.provider {
				rematch = false
			}
		}
		if rematch {
			if _, err = tx.ExecContext(ctx, `UPDATE metadata_relationship_decisions SET locked=0 WHERE kind=? AND entity_id=? AND relationship='identity' AND locked=1`, t.kind, t.id); err != nil {
				return false, err
			}
			_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET provider='',provider_type='',provider_id='',selection_mode='none',episode_order='official',requested_provider='',child_cursor='',selection_revision=selection_revision+1,status='pending',generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error='' WHERE library_id=? AND target_kind=? AND target_id=?`, m.library, t.kind, t.id)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status='pending',generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error='' WHERE library_id=? AND target_kind=? AND target_id=?`, m.library, t.kind, t.id)
		}
		if err != nil {
			return false, err
		}
		m.afterKind, m.afterID = t.kind, t.id
	}
	return len(page) < screenFanoutRowLimit || m.afterKind == m.upperKind && m.afterID == m.upperID, nil
}

func screenFanoutArtworkPage(ctx context.Context, tx *sql.Tx, m *screenFanoutMarker, library int64) (bool, error) {
	after, err := strconv.ParseInt(m.afterKind, 10, 64)
	if err != nil {
		return false, err
	}
	upper, err := strconv.ParseInt(m.upperKind, 10, 64)
	if err != nil {
		return false, err
	}
	type target struct {
		kind, id int64
		name     string
	}
	rows, err := tx.QueryContext(ctx, `SELECT ce.kind,ce.id,k.name FROM catalog_entities ce INDEXED BY catalog_entities_library JOIN catalog_kinds k ON k.id=ce.kind WHERE ce.library_id=? AND (ce.kind,ce.id)>(?,?) AND (ce.kind,ce.id)<=(?,?) ORDER BY ce.kind,ce.id LIMIT ?`, library, after, m.afterID, upper, m.upperID, screenFanoutRawLimit)
	if err != nil {
		return false, err
	}
	page := make([]target, 0, screenFanoutRawLimit)
	for rows.Next() {
		var t target
		if err = rows.Scan(&t.kind, &t.id, &t.name); err != nil {
			rows.Close()
			return false, err
		}
		page = append(page, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	effects, examined := 0, 0
	for _, t := range page {
		// Same mapping as artwork_head_entities_insert, derived from kind names
		// after the bounded integer walk. Unsupported/headless rows still advance.
		kind := "item"
		switch t.name {
		case "show", "season", "artist", "album", "book":
			kind = t.name
		}
		var head bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artwork_entity_heads WHERE kind=? AND entity_id=?)`, kind, t.id).Scan(&head); err != nil {
			return false, err
		}
		if head {
			if _, err = tx.ExecContext(ctx, `INSERT INTO artwork_dirty(kind,entity_id) VALUES(?,?) ON CONFLICT(kind,entity_id) DO NOTHING`, kind, t.id); err != nil {
				return false, err
			}
			effects++
		}
		m.afterKind, m.afterID = strconv.FormatInt(t.kind, 10), t.id
		examined++
		if effects == artworkFanoutRowLimit {
			break
		}
	}
	return examined == len(page) && len(page) < screenFanoutRawLimit || m.afterKind == m.upperKind && m.afterID == m.upperID, nil
}

func screenFanoutRetryDelay(attempts int) time.Duration {
	return min(6*time.Hour, 30*time.Second*time.Duration(1<<min(max(attempts, 0), 11)))
}

func (s *Service) screenFanoutBackoff(ctx context.Context, m screenFanoutMarker, now int64) error {
	w, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer w.Rollback()
	_, err = w.Tx().ExecContext(ctx, `UPDATE screen_metadata_fanout SET attempts=MIN(attempts,11)+1,next_attempt_ms=?,error_code='fanout_failed' WHERE library_id=? AND effect=? AND last_served_ms=? AND generation=?`, now+screenFanoutRetryDelay(m.attempts).Milliseconds(), m.library, m.effect, m.lastServed, m.generation)
	if err != nil {
		return err
	}
	return w.Commit()
}
