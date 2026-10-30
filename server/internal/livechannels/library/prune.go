package librarychannels

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"
)

// generationPruneBatch caps each child delete inside a maintenance write plan.
const generationPruneBatch = 128

// PruneGenerations releases at most one unreferenced generation per pass. Each
// child delete is capped, so a large candidate set cannot monopolize the writer.
// Active schedules, pending copy bases and live playback references are pinned.
func (s *Store) PruneGenerations(ctx context.Context) (bool, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassMaintenance)
	if err != nil {
		return false, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	now := s.now().UnixMilli()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT g.id FROM lc_generations g WHERE g.status<>'pending' AND g.created_ms<?
 AND NOT EXISTS(SELECT 1 FROM lc_channels c WHERE c.active_generation=g.id)
 AND NOT EXISTS(SELECT 1 FROM lc_generations child WHERE child.status='pending' AND child.base_generation=g.id)
 AND NOT EXISTS(SELECT 1 FROM lc_playback_refs r WHERE r.generation_id=g.id AND r.lease_until_ms>?)
 ORDER BY g.created_ms,g.id LIMIT 1`, now-int64(7*24*time.Hour/time.Millisecond), now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, table := range []string{"lc_candidates", "lc_entries", "lc_rule_state", "lc_used", "lc_playback_refs"} {
		result, e := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE rowid IN(SELECT rowid FROM `+table+` WHERE generation_id=? LIMIT ?)`, id, generationPruneBatch)
		if e != nil {
			return false, e
		}
		count, e := result.RowsAffected()
		if e != nil {
			return false, e
		}
		if count == generationPruneBatch {
			return true, gated.Commit()
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM lc_generations WHERE id=?`, id); err != nil {
		return false, err
	}
	return true, gated.Commit()
}
