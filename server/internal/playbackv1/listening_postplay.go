package playbackv1

import (
	"context"
	"database/sql"
)

// Next-episode preferences apply to video. Albums and audiobook parts follow
// the queue's real next entry without an episode countdown or credits policy.
func currentListeningItem(ctx context.Context, tx *sql.Tx, l *layout) bool {
	_, key, err := l.entryAtErr(l.q.current)
	if err != nil {
		return false
	}
	var listening bool
	if tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.public_id=pid_blob(?) AND k.listening=1 AND k.playable=1)`, key).Scan(&listening) != nil {
		return false
	}
	return listening
}
