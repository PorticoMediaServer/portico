package playback

import (
	"context"
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
)

// cleanupDiscInputsTx runs inside ordinary playback maintenance. It retires at
// most 64 exact ended occurrences, then collects at most 64 original snapshot
// references. It never touches capture lifecycle/quota or performs artifact IO.
// The caller serializes the cursor and publishes next only after its COMMIT.
func (s *Service) cleanupDiscInputsTx(ctx context.Context, tx *sql.Tx) (string, error) {
	if s.discInputs == nil {
		return "", nil
	}
	now := time.Now()
	rows, err := tx.QueryContext(ctx, `SELECT d.session_id,d.generation,d.id FROM playback_disc_snapshots d
 JOIN playback_disc_lifecycle l ON l.snapshot_id=d.id JOIN playback_sessions p ON p.id=d.session_id
 WHERE l.state<>'retired' AND (p.generation<>d.generation OR p.state IN('stopped','ended','failed') OR p.expires_at<=?)
 ORDER BY d.id LIMIT 64`, now.UTC().Format(time.RFC3339))
	if err != nil {
		return "", err
	}
	type original struct {
		key playbackdisc.Key
		id  string
	}
	var originals []original
	for rows.Next() {
		var own original
		if err = rows.Scan(&own.key.SessionID, &own.key.Generation, &own.id); err != nil {
			break
		}
		originals = append(originals, own)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return "", err
	}
	for _, own := range originals {
		if err = s.discInputs.store.RetireTx(ctx, tx, own.key, own.id); err != nil {
			return "", err
		}
	}
	next, _, err := s.discInputs.store.CollectTx(ctx, tx, s.discInputs.after, 64)
	return next, err
}
