package catalog

import (
	"context"
	"database/sql"
)

// moveChannelPlaysTx preserves genuine shared-channel history when two
// canonical titles become one. It moves only the absorbed item's indexed rows;
// unrelated channel history is never scanned. If both aliases recorded the
// same real channel/occurrence, the earlier durable event survives once.
// absorbTx subsequently touches/deletes the canonical items, so the existing
// catalogue fence supersedes affected pending plans before they publish.
func moveChannelPlaysTx(ctx context.Context, tx *sql.Tx, from, into int64) error {
	for _, query := range []string{
		`DELETE FROM lc_play_events WHERE event_id IN(
        SELECT kept.event_id FROM lc_play_events absorbed INDEXED BY lc_play_events_item_fk
        JOIN lc_play_events kept ON kept.channel_id=absorbed.channel_id AND kept.occurrence_start_ms=absorbed.occurrence_start_ms AND kept.item_id=?2
        WHERE absorbed.item_id=?1 AND absorbed.event_id<kept.event_id)`,
		`DELETE FROM lc_play_events WHERE event_id IN(
        SELECT absorbed.event_id FROM lc_play_events absorbed INDEXED BY lc_play_events_item_fk
        JOIN lc_play_events kept ON kept.channel_id=absorbed.channel_id AND kept.occurrence_start_ms=absorbed.occurrence_start_ms AND kept.item_id=?2
        WHERE absorbed.item_id=?1)`,
		`UPDATE lc_play_events INDEXED BY lc_play_events_item_fk SET item_id=?2 WHERE item_id=?1`,
	} {
		if _, err := tx.ExecContext(ctx, query, from, into); err != nil {
			return err
		}
	}
	return nil
}
