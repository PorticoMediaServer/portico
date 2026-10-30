package playbackv1

import (
	"context"
	"database/sql"
)

// Classification is derived once while each bounded chunk is already being
// written. No queue-length scan is added to windows, clocks, or idle work.
func writeQueueChunkTx(ctx context.Context, tx *sql.Tx, snapshot string, number int64, raw []byte, replace bool) error {
	if len(raw) < 2 || raw[0] == 0 || (len(raw)-1)%int(raw[0]) != 0 {
		return ErrNotFound
	}
	count := (len(raw) - 1) / int(raw[0])
	if count < 1 || count > chunkKeys {
		return ErrQueueTooLarge
	}
	ids := make([]string, count)
	for n := range ids {
		var ok bool
		if ids[n], ok = chunkKey(raw, n); !ok {
			return ErrNotFound
		}
	}
	var music int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM json_each(?) wanted JOIN catalog_entities item ON item.public_id=pid_blob(wanted.value) AND item.kind=7 JOIN catalog_libraries library ON library.id=item.library_id JOIN libraries configured ON configured.id=library.library_id AND configured.kind='music' WHERE item.retired=0`, jsonList(ids)).Scan(&music); err != nil {
		return err
	}
	var err error
	if replace {
		_, err = tx.ExecContext(ctx, `INSERT INTO queue_v1_chunks(snapshot_id,chunk_no,keys) VALUES(?,?,?) ON CONFLICT(snapshot_id,chunk_no) DO UPDATE SET keys=excluded.keys`, snapshot, number, raw)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO queue_v1_chunks(snapshot_id,chunk_no,keys) VALUES(?,?,?)`, snapshot, number, raw)
	}
	if err != nil {
		return err
	}
	if music != count {
		_, err = tx.ExecContext(ctx, `UPDATE queue_v1_snapshots SET music_only=0 WHERE id=? AND music_only<>0`, snapshot)
	}
	return err
}

// Every live segment must have a completed, proved music-only capture. A
// music-looking current window cannot activate smoothing in a mixed queue.
func (l *layout) musicQueue() bool {
	if !l.q.shuffle || !l.musicOnlySources() {
		return false
	}
	for _, g := range l.segments {
		if g.live() > 0 && g.state != "ready" {
			return false
		}
	}
	return true
}

func (l *layout) musicOnlySources() bool {
	hasMusic := false
	for _, g := range l.segments {
		if g.live() > 0 {
			if !g.musicOnly {
				return false
			}
			hasMusic = true
		}
	}
	return hasMusic
}
