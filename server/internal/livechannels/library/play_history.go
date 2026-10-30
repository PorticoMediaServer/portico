package librarychannels

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
)

// playHistorySnapshot is a single durable event cutoff, shared by every
// candidate page in a generation. Events after it cannot reorder that plan.
type playHistorySnapshot struct {
	ChannelID string
	Fence     int64
	Ordered   bool // bounded preview must take the actual globally first candidates
}

func hasLeastPlayed(c Config) bool {
	for _, rule := range c.Rules {
		if rule.Query.Order == "least-played" {
			return true
		}
	}
	return false
}

func playHistoryFence(ctx context.Context, tx *sql.Tx) (int64, error) {
	var fence int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(event_id),0) FROM lc_play_events`).Scan(&fence)
	return fence, err
}

// RecordPlayableStart is called only after the engine commits validated,
// nonempty playable media output. The retained real schedule occurrence is a
// witness, never a reason to call this method without output. A continuation's
// original programme start excludes its source offset; changing files, rules,
// generations or listeners does not manufacture another occurrence.
// No viewer identity, completion estimate or wall-clock backfill is recorded.
func (s *Store) RecordPlayableStart(ctx context.Context, channelID, generationID, occurrenceID, itemID string) error {
	if !validID(channelID) || !validID(generationID) || !validID(occurrenceID) || itemID == "" {
		return ErrInvalid
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	item, err := entityid.Resolve(ctx, tx, itemID)
	if err != nil {
		return err
	}
	// A continuation uses its original programme start, not a file or the
	// current producer's start time. Read the exact retained plan witness.
	var origin int64
	err = tx.QueryRowContext(ctx, `SELECT e.start_ms-e.source_offset_ms
      FROM lc_entries e JOIN lc_channels c ON c.id=e.channel_id
      WHERE e.generation_id=? AND e.occurrence_id=? AND e.channel_id=? AND e.item_id=?
        AND e.item_id>0 AND e.slate_reason='' AND c.enabled=1 AND c.removed=0`, generationID, occurrenceID, channelID, item).Scan(&origin)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO lc_play_events(channel_id,occurrence_start_ms,item_id) VALUES(?,?,?) ON CONFLICT(channel_id,occurrence_start_ms,item_id) DO NOTHING`, channelID, origin, item)
	if err != nil {
		return err
	}
	return gated.Commit()
}
