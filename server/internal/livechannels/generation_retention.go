package livechannels

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Every guide refresh publishes a complete new generation: a full copy of the
// lineup and every programme in the window. Readers only ever follow
// live_sources.active_generation, and DVR keeps its own copy of what it
// schedules, so a superseded generation is dead weight. Nothing removed them,
// which made the one database file everything depends on grow by a whole guide
// per refresh, forever: a few hundred channels refreshed a few times a day is
// gigabytes a week, and every backup, checkpoint and cold page read pays for it.
const (
	// supersededGenerationGrace keeps a replaced generation long enough for any
	// request that resolved it just before the swap to finish, with a wide margin.
	supersededGenerationGrace = 5 * time.Minute
	// retentionBatchRows bounds one gated delete so the writer is held for
	// milliseconds, not for the length of a guide.
	retentionBatchRows = 2000
	retentionInterval  = 10 * time.Minute
)

// PruneSupersededGeneration removes at most one superseded generation and
// reports whether it found one. Deleting in small maintenance-class batches
// with a yield between them keeps playback and navigation ahead of it.
func (s *Store) PruneSupersededGeneration(ctx context.Context) (bool, error) {
	ctx = dbwork.WithClass(ctx, dbwork.ClassMaintenance)
	cutoff := time.Now().UTC().Add(-supersededGenerationGrace).Format(time.RFC3339)
	var generation string
	err := dbwork.QueryRow(ctx, s.db, `SELECT g.id FROM live_generations g
 WHERE g.created_at<? AND NOT EXISTS(SELECT 1 FROM live_sources s WHERE s.active_generation=g.id)
 AND NOT EXISTS(SELECT 1 FROM live_operations o WHERE o.generation_id=g.id AND o.status='staging')
 ORDER BY g.created_at,g.id LIMIT 1`, cutoff).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Children before parents: the schema's foreign keys are enforced.
	// live_programme_icons is WITHOUT ROWID, so its batches delete by primary
	// key rather than by rowid like the rest.
	for _, table := range []string{"live_channel_choices", "live_channel_attributes", "live_import_diagnostics", "live_programme_icons", "live_programme_metadata", "live_programmes", "live_channel_versions", "live_operations"} {
		for {
			if !dbwork.Yield(ctx) {
				return true, ctx.Err()
			}
			var result sql.Result
			var err error
			if table == "live_channel_attributes" {
				result, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassMaintenance, `DELETE FROM live_channel_attributes WHERE generation_id=? AND channel_id IN(SELECT channel_id FROM live_channel_attributes WHERE generation_id=? LIMIT ?)`, generation, generation, retentionBatchRows)
			} else if table == "live_programme_icons" {
				result, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassMaintenance,
					`DELETE FROM live_programme_icons WHERE generation_id=? AND programme_id IN(SELECT programme_id FROM live_programme_icons WHERE generation_id=? LIMIT ?)`, generation, generation, retentionBatchRows)
			} else {
				result, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassMaintenance,
					`DELETE FROM `+table+` WHERE rowid IN(SELECT rowid FROM `+table+` WHERE generation_id=? LIMIT ?)`, generation, retentionBatchRows)
			}
			if err != nil {
				return true, err
			}
			if n, _ := result.RowsAffected(); n < retentionBatchRows {
				break
			}
		}
	}
	// The generation must still be superseded: a source cannot return to an old
	// generation, but the guard costs nothing and makes the delete self-proving.
	_, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassMaintenance,
		`DELETE FROM live_generations WHERE id=? AND NOT EXISTS(SELECT 1 FROM live_sources s WHERE s.active_generation=live_generations.id)`, generation)
	return true, err
}
