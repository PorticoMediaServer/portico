package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Waiting polls memory, never a retained reader. A foreground capture which
// has not found a playable key within its budget leaves shared demand intact;
// a retry reuses that handle and the normal cold-idle deadline still applies.
func localOrderedNext(ctx context.Context, it catalog.QueueKeyIterator, until time.Time) (catalog.QueueLocalOrderedBatch, error) {
	for {
		batch, err := it.Next(ctx)
		if err != nil || batch.Ready {
			return batch, err
		}
		if !until.IsZero() && time.Now().After(until) {
			return batch, ErrQueueBuilding
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return batch, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Service) localOrderedVisible(ctx context.Context, p identity.Principal, k selectorKeys, ids []string) ([]string, error) {
	var allowed []string
	err := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassMaintenance), s.DB, func(inner context.Context) error {
		tx := dbwork.Snapshot(inner)
		if k.checkBasis == nil {
			return errSelectionChanged
		}
		if err := k.checkBasis(inner, tx); err != nil {
			return err
		}
		visible, err := s.visibleFilter(inner, tx, p)
		if err != nil {
			return err
		}
		allowed, err = visible(ids)
		return err
	})
	return allowed, err
}

func (s *Service) buildLocalOrderedSnapshot(ctx context.Context, p identity.Principal, queue string, in SegmentInput, snap snapshot, k selectorKeys, limit int64, budget scanBudget) (snapshot, error) {
	it := k.iterator
	close := true
	defer func() {
		if close {
			it.Close()
		} else {
			it.Pause()
		}
	}()
	snap.kind, snap.sourceRevision, snap.label, snap.checkBasis = k.kind, k.revision, k.label, k.checkBasis
	until := budget.until
	if until.IsZero() {
		until = time.Now().Add(scanBudgetTime)
	}
	for {
		batch, err := localOrderedNext(ctx, it, until)
		if err != nil {
			if errors.Is(err, ErrQueueBuilding) || ctx.Err() != nil {
				close = false
			}
			return snap, err
		}
		ids, err := s.localOrderedVisible(ctx, p, k, batch.IDs)
		if err != nil {
			return snap, err
		}
		if len(ids) == 0 && !batch.EOF {
			continue
		}
		if int64(len(ids)) > min(limit, MaxSnapshotKeys) {
			return snap, ErrQueueTooLarge
		}
		snap.count = int64(len(ids))
		snap.seen = snap.count
		snap.partial = !batch.EOF
		if len(ids) > 0 {
			snap.pending = []chunkRow{{no: 0, keys: encodeChunk(ids)}}
		}
		if in.Anchor != nil {
			for n, id := range ids {
				if id == in.Anchor.ItemID {
					snap.anchor = int64(n)
					break
				}
			}
			if in.Anchor.Position != nil && *in.Anchor.Position >= 0 && *in.Anchor.Position < snap.count {
				snap.anchor = *in.Anchor.Position
			}
			if snap.partial && snap.anchor < 0 {
				snap.anchorItem = in.Anchor.ItemID
				if in.Anchor.Position != nil {
					at := *in.Anchor.Position
					snap.anchorAt = &at
				}
			}
		}
		return snap, nil
	}
}

// Resume starts from raw EOF's beginning after any memory loss. Before accepting
// a suffix it reconciles every preceding accepted command key, in order, with
// the durable chunks. No raw key, cursor, hit set or partial predicate is stored.
func (s *Service) continueLocalOrderedBuild(ctx context.Context, p identity.Principal, id, queue, kind, selector, label string, built, count int64, start *queueStart, k selectorKeys) error {
	it := k.iterator
	defer it.Close()
	snap := snapshot{id: id, kind: kind, selector: selector, count: built, anchor: -1, stored: true, checkBasis: k.checkBasis}
	accepted := int64(0)
	// One durable command chunk is read at a time. It is not query progress.
	var prior []byte
	priorNo := int64(-1)
	load := func(no int64) error {
		if no == priorNo {
			return nil
		}
		if err := s.DB.QueryRowContext(ctx, `SELECT keys FROM queue_v1_chunks WHERE snapshot_id=? AND chunk_no=?`, id, no).Scan(&prior); err != nil {
			return err
		}
		priorNo = no
		return nil
	}
	chunk := make([]string, 0, chunkKeys)
	chunkNo := built / chunkKeys
	if built%chunkKeys != 0 {
		if err := load(chunkNo); err != nil {
			return err
		}
		for n := int64(0); n < built%chunkKeys; n++ {
			key, ok := chunkKey(prior, int(n))
			if !ok {
				return errSelectionChanged
			}
			chunk = append(chunk, key)
		}
	}
	declared := count > built
	lastEvent := time.Now()
	for {
		batch, err := localOrderedNext(ctx, it, time.Time{})
		if err != nil {
			return err
		}
		ids, err := s.localOrderedVisible(ctx, p, k, batch.IDs)
		if err != nil {
			return err
		}
		// Delivery reservation covers visibility buffers and the two bounded command
		// chunks across the accepting write, including serialization copies.
		if err = it.ReserveDelivery(256 << 10); err != nil {
			return err
		}
		committed := snap.count
		var writes []chunkRow
		changed := false
		for _, key := range ids {
			if accepted < built {
				if err = load(accepted / chunkKeys); err != nil {
					return err
				}
				expected, ok := chunkKey(prior, int(accepted%chunkKeys))
				if !ok || expected != key {
					return errSelectionChanged
				}
				accepted++
				continue
			}
			if snap.count >= min(s.queueKeyLimit(), MaxSnapshotKeys) {
				return ErrQueueTooLarge
			}
			if start != nil && start.Mode == "ordered" && snap.anchor < 0 {
				if start.AnchorItem != "" && key == start.AnchorItem || start.AnchorAt != nil && snap.count >= *start.AnchorAt {
					snap.anchor = snap.count
				}
			}
			chunk = append(chunk, key)
			snap.count++
			accepted++
			changed = true
			if len(chunk) == chunkKeys {
				writes = append(writes, chunkRow{no: chunkNo, keys: encodeChunk(chunk)})
				chunkNo++
				chunk = chunk[:0]
			}
		}
		if batch.EOF && accepted < built {
			return errSelectionChanged
		}
		if changed && len(chunk) > 0 {
			writes = append(writes, chunkRow{no: chunkNo, keys: encodeChunk(chunk)})
		}
		if changed || batch.EOF {
			err = dbwork.WithWriteTxContext(ctx, s.DB, dbwork.ClassBackgroundMedia, func(inner context.Context, w *sql.Tx) error {
				if err := k.checkBasis(inner, w); err != nil {
					return err
				}
				// The writer reauthorizes this exact incoming window too. Its content
				// policy must still produce the same accepted sequence as the fresh read.
				visible, err := s.visibleFilter(inner, w, p)
				if err != nil {
					return err
				}
				live, err := visible(batch.IDs)
				if err != nil {
					return err
				}
				if !sameOrderedKeys(live, ids) {
					return errSelectionChanged
				}
				var liveBuilt int64
				if err = w.QueryRowContext(inner, `SELECT built FROM queue_v1_snapshots WHERE id=? AND state='building'`, id).Scan(&liveBuilt); err != nil {
					return err
				}
				if liveBuilt != committed {
					return errSelectionChanged
				}
				q, err := loadQueue(inner, w, queue)
				if err != nil {
					return err
				}
				layout, err := s.readLayout(inner, w, q)
				if err != nil {
					return err
				}
				if layout.total()+snap.count-committed > s.queueKeyLimit() {
					return ErrQueueTooLarge
				}
				for _, row := range writes {
					if err = writeQueueChunkTx(inner, w, id, row.no, row.keys, true); err != nil {
						return err
					}
				}
				if batch.EOF {
					if _, err = w.ExecContext(inner, `UPDATE queue_v1_snapshots SET built=?,seen=?,count=?,label=?,state='ready',start='' WHERE id=?`, snap.count, snap.count, snap.count, label, id); err != nil {
						return err
					}
					return s.finishBuildTx(inner, w, queue, id, snap.count, start, snap.anchor)
				}
				if _, err = w.ExecContext(inner, `UPDATE queue_v1_snapshots SET built=?,seen=? WHERE id=?`, snap.count, snap.count, id); err != nil {
					return err
				}
				if declared {
					return nil
				}
				return s.growSegmentTx(inner, w, queue, id, snap.count, start, snap.anchor, time.Since(lastEvent) >= time.Second)
			})
			if err != nil {
				return err
			}
			if time.Since(lastEvent) >= time.Second {
				lastEvent = time.Now()
			}
		}
		if batch.EOF {
			return nil
		}
	}
}

func sameOrderedKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for n := range a {
		if a[n] != b[n] {
			return false
		}
	}
	return true
}
