package playbackv1

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// Queues are removed by one sweep (review P19), never inside a command's
// write: a queue can hold millions of keys, so its chunks go a batch per
// short background write, then the queue row (its segments, removals and
// snapshots cascade, all small). The sweep runs when there may be something
// to do (a restart, a create, a delete), never on a timer, and does nothing
// but a few indexed reads when there isn't.
const (
	// stagedQueueLifetime is how long a large create's staged queue may exist
	// before its create is taken as dead (a crash between the first background
	// batch and the commit, P27). A live create commits within seconds.
	stagedQueueLifetime = 15 * time.Minute
	// queueIdleLifetime is how long a device keeps a queue nobody touches.
	queueIdleLifetime = 90 * 24 * time.Hour
	// createReceiptLifetime is how long a create's key is answered by replay.
	createReceiptLifetime = 24 * time.Hour
	// SessionRetention is how long an ended session is kept: the longest
	// playback history period an owner can read (operations.PlaybackHistoryPeriods).
	SessionRetention  = 30 * 24 * time.Hour
	sweepChunkBatch   = 256
	sessionPruneBatch = 256
)

type queueSweeper struct {
	mu      sync.Mutex
	running bool
	again   bool
}

// sweepQueues starts the sweep in the background unless it is running
// (then it runs once more when done).
func (s *Service) sweepQueues() {
	s.queueSweep.mu.Lock()
	if s.queueSweep.running {
		s.queueSweep.again = true
		s.queueSweep.mu.Unlock()
		return
	}
	s.queueSweep.running = true
	s.queueSweep.mu.Unlock()
	supervise.Go("playbackv1.queue-sweep", func() {
		for {
			s.SweepQueues(context.Background())
			s.queueSweep.mu.Lock()
			if !s.queueSweep.again {
				s.queueSweep.running = false
				s.queueSweep.mu.Unlock()
				return
			}
			s.queueSweep.again = false
			s.queueSweep.mu.Unlock()
		}
	})
}

// SweepQueues removes, at background priority: queues retired by a newer
// Play or a delete ("!" owner); staged queues of a create that died ("~"
// owner, older than stagedQueueLifetime); device queues whose device is gone;
// device queues idle for queueIdleLifetime; create receipts past
// createReceiptLifetime; and sessions ended more than SessionRetention ago.
func (s *Service) SweepQueues(ctx context.Context) {
	now := s.now()
	staged, idle := now.Add(-stagedQueueLifetime).UnixMilli(), now.Add(-queueIdleLifetime).UnixMilli()
	const live = `owner_kind='device' AND substr(owner_id,1,1) NOT IN('~','!')`
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM queues_v1 WHERE owner_kind='device' AND owner_id>='!' AND owner_id<'!'||char(1114111)
 UNION SELECT id FROM queues_v1 WHERE owner_kind='device' AND owner_id>='~' AND owner_id<'~'||char(1114111) AND created_ms<?
 UNION SELECT id FROM queues_v1 INDEXED BY queues_v1_idle WHERE updated_ms<? AND `+live+`
 UNION SELECT q.id FROM queues_v1 q WHERE q.`+live+` AND NOT EXISTS(SELECT 1 FROM identity_devices d WHERE d.id=q.owner_id)
 LIMIT 64`, staged, idle)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if err := s.removeQueue(ctx, id); err != nil {
			if ctx.Err() == nil {
				log.Printf("A queue could not be removed: %v", err)
			}
			return
		}
	}
	s.sweepSaves(ctx)
	// Preparations past their expiry (spec §18.2): canceled, their private
	// presentations stopped; old rows pruned with the receipts.
	var expired bool
	if s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_v1_prepared WHERE state='prepared' AND expires_ms<=?)`, now.UnixMilli()).Scan(&expired) == nil && expired {
		rows, err := s.DB.QueryContext(ctx, `SELECT token FROM playback_v1_prepared WHERE state='prepared' AND expires_ms<=? LIMIT 64`, now.UnixMilli())
		if err == nil {
			var tokens []string
			for rows.Next() {
				var t string
				if rows.Scan(&t) == nil {
					tokens = append(tokens, t)
				}
			}
			rows.Close()
			for _, t := range tokens {
				s.cancelPrepared(ctx, "token", t, "expired")
			}
		}
	}
	var stale bool
	if s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_v1_prepared WHERE created_ms<?)`, now.Add(-createReceiptLifetime).UnixMilli()).Scan(&stale) == nil && stale {
		_, _ = dbwork.ExecWrite(ctx, s.DB, dbwork.ClassBackgroundMedia, `DELETE FROM playback_v1_prepared WHERE state<>'prepared' AND created_ms<?`, now.Add(-createReceiptLifetime).UnixMilli())
	}
	var old bool
	if s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM queue_v1_create_receipts WHERE created_ms<?)`, now.Add(-createReceiptLifetime).UnixMilli()).Scan(&old) == nil && old {
		_, _ = dbwork.ExecWrite(ctx, s.DB, dbwork.ClassBackgroundMedia, `DELETE FROM queue_v1_create_receipts WHERE created_ms<?`, now.Add(-createReceiptLifetime).UnixMilli())
	}
	// Ended sessions past the retention, a batch per write from the ended index;
	// their channel rows cascade.
	cutoff := now.Add(-SessionRetention).UnixMilli()
	pruned := int64(0)
	var ended bool
	if s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_v1_sessions INDEXED BY playback_v1_sessions_ended WHERE ended_ms>0 AND ended_ms<?)`, cutoff).Scan(&ended) == nil && ended {
		if res, err := dbwork.ExecWrite(ctx, s.DB, dbwork.ClassBackgroundMedia, `DELETE FROM playback_v1_sessions WHERE id IN (SELECT id FROM playback_v1_sessions INDEXED BY playback_v1_sessions_ended WHERE ended_ms>0 AND ended_ms<? LIMIT ?)`, cutoff, sessionPruneBatch); err == nil {
			pruned, _ = res.RowsAffected()
		}
	}
	if len(ids) == 64 || pruned == sessionPruneBatch {
		s.sweepQueues() // more than one pass's worth
	}
}

// removeQueue rechecks the selection and retires the queue before deleting any
// chunks. Queue commands recheck ownership inside their writer, so a touch that
// wins the race keeps the queue, and retirement that wins denies later writes.
// A stopped sweep resumes from the durable "!" owner on its next pass.
func (s *Service) removeQueue(ctx context.Context, id string) error {
	now := s.now()
	res, err := dbwork.ExecWrite(ctx, s.DB, dbwork.ClassBackgroundMedia, `UPDATE queues_v1
 SET owner_id=CASE WHEN substr(owner_id,1,1)='!' THEN owner_id ELSE '!'||owner_id END
 WHERE id=? AND owner_kind='device' AND (
 substr(owner_id,1,1)='!' OR
 (substr(owner_id,1,1)='~' AND created_ms<?) OR
 (substr(owner_id,1,1) NOT IN('~','!') AND (updated_ms<? OR NOT EXISTS(SELECT 1 FROM identity_devices d WHERE d.id=queues_v1.owner_id))))`, id, now.Add(-stagedQueueLifetime).UnixMilli(), now.Add(-queueIdleLifetime).UnixMilli())
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	for {
		res, err := dbwork.ExecWrite(ctx, s.DB, dbwork.ClassBackgroundMedia, `DELETE FROM queue_v1_chunks WHERE (snapshot_id,chunk_no) IN (SELECT c.snapshot_id,c.chunk_no FROM queue_v1_snapshots sn JOIN queue_v1_chunks c ON c.snapshot_id=sn.id WHERE sn.queue_id=? LIMIT ?)`, id, sweepChunkBatch)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n < sweepChunkBatch {
			break
		}
	}
	_, err = dbwork.ExecWrite(ctx, s.DB, dbwork.ClassBackgroundMedia, `DELETE FROM queues_v1 WHERE id=? AND owner_kind='device' AND substr(owner_id,1,1)='!'`, id)
	return err
}
