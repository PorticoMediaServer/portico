package playbackv1

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

const radioBatchSize = 32
const radioLookahead = 8

func isRadioSource(source Selector) bool {
	return source.Container != nil && source.Container.Kind == "radio"
}
func radioSegment(l *layout) int {
	for i := len(l.segments) - 1; i >= 0; i-- {
		if l.segments[i].kind == "radio" {
			return i
		}
	}
	return -1
}

// The callback is a domain producer, not a second unbounded queue input.
func validRadioBatch(ids []string, limit int) bool {
	if len(ids) == 0 || len(ids) > limit {
		return false
	}
	for _, id := range ids {
		if _, ok := entityid.Decode(id); !ok {
			return false
		}
	}
	return true
}

type radioRefill struct {
	snapshot, seed string
	built          int64
	ids            []string
}

// prepareRadioRefill performs only bounded selection reads. The actual append
// joins Advance's existing queue-revision write, so a stale command cannot
// publish extra keys, move the clock or create a parallel playback session.
func (s *Service) prepareRadioRefill(ctx context.Context, c Caller, q queueRow) (*radioRefill, error) {
	if s.RadioKeys == nil || q.shuffle || q.repeat == "one" {
		return nil, nil
	}
	var out *radioRefill
	err := s.withLayout(ctx, q, func(l *layout) error {
		at := radioSegment(l)
		if at < 0 || l.total()-l.q.current > radioLookahead {
			return nil
		}
		g := l.segments[at]
		if g.state != "ready" {
			return ErrQueueBuilding
		}
		var raw string
		var built int64
		if err := l.tx.QueryRowContext(ctx, `SELECT selector,built FROM queue_v1_snapshots WHERE id=? AND queue_id=? AND state='ready'`, g.snapshot, q.id).Scan(&raw, &built); err != nil {
			return err
		}
		var source Selector
		if json.Unmarshal([]byte(raw), &source) != nil || !isRadioSource(source) {
			return ErrUnsupportedSelector
		}
		excluded := []string{}
		last := ""
		for n := max(int64(0), l.total()-200); n < l.total(); n++ {
			_, key, err := l.entryAtErr(n)
			if err != nil {
				return err
			}
			excluded = append(excluded, key)
			last = key
		}
		room := min(s.queueKeyLimit()-l.total(), MaxSnapshotKeys-built)
		if room <= 0 {
			return ErrQueueTooLarge
		}
		batch, err := s.RadioKeys(ctx, l.tx, c.Principal, catalog.RadioBatchRequest{Seed: source.Container.ID, LastItem: last, Exclude: excluded, Cycle: built, Limit: min(radioBatchSize, int(room)), Now: s.now()})
		if err != nil {
			return err
		}
		if !validRadioBatch(batch.IDs, min(radioBatchSize, int(room))) {
			return ErrNotFound
		}
		out = &radioRefill{snapshot: g.snapshot, seed: source.Container.ID, built: built, ids: batch.IDs}
		return nil
	})
	return out, err
}

// appendRadioTx preserves every existing occurrence and private media identity.
// Only new ordinals are appended; manual queue insertions remain in their order.
func (s *Service) appendRadioTx(ctx context.Context, tx *sql.Tx, l *layout, p identity.Principal, refill *radioRefill) error {
	if refill == nil {
		return nil
	}
	if l.q.shuffle || l.total()+int64(len(refill.ids)) > s.queueKeyLimit() || refill.built+int64(len(refill.ids)) > MaxSnapshotKeys {
		return ErrQueueTooLarge
	}
	var built int64
	if err := tx.QueryRowContext(ctx, `SELECT built FROM queue_v1_snapshots WHERE id=? AND queue_id=? AND state='ready'`, refill.snapshot, l.q.id).Scan(&built); err != nil {
		return err
	}
	if built != refill.built {
		return ErrRevision
	}
	var kind int
	var library string
	if err := tx.QueryRowContext(ctx, `SELECT item.kind,library.library_id FROM catalog_entities item JOIN catalog_libraries library ON library.id=item.library_id JOIN libraries configured ON configured.id=library.library_id AND configured.kind='music' WHERE item.public_id=pid_blob(?) AND item.retired=0 AND library.retired=0`, refill.seed).Scan(&kind, &library); err != nil {
		return err
	}
	if s.LibraryCheck != nil {
		if err := s.LibraryCheck(ctx, tx, p, library); err != nil {
			return ErrNotFound
		}
	}
	predicate := "1"
	args := []any{}
	if s.Visibility != nil {
		var err error
		predicate, args, err = s.Visibility(ctx, tx, p, "item.id")
		if err != nil {
			return err
		}
	}
	var seedAllowed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities item JOIN catalog_songs song ON song.entity_id=item.id JOIN catalog_albums album ON album.entity_id=song.album_id WHERE ((?=7 AND item.public_id=pid_blob(?)) OR (?=6 AND song.album_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))) OR (?=5 AND (album.artist_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) OR EXISTS(SELECT 1 FROM catalog_song_artists credit JOIN catalog_entities artist ON artist.id=credit.artist_id WHERE credit.song_id=item.id AND artist.public_id=pid_blob(?))))) AND `+predicate+`)`, append([]any{kind, refill.seed, kind, refill.seed, kind, refill.seed, refill.seed}, args...)...).Scan(&seedAllowed); err != nil {
		return err
	}
	if !seedAllowed {
		return ErrNotFound
	}
	visible, err := s.visibleFilter(ctx, tx, p)
	if err != nil {
		return err
	}
	allowed, err := visible(refill.ids)
	if err != nil {
		return err
	}
	if len(allowed) != len(refill.ids) {
		return ErrNotFound
	}
	return s.writeRadioKeysTx(ctx, tx, l, refill)
}

func (s *Service) writeRadioKeysTx(ctx context.Context, tx *sql.Tx, l *layout, refill *radioRefill) error {
	keys := []string{}
	no := refill.built / chunkKeys
	if offset := refill.built % chunkKeys; offset > 0 {
		var old []byte
		if err := tx.QueryRowContext(ctx, `SELECT keys FROM queue_v1_chunks WHERE snapshot_id=? AND chunk_no=?`, refill.snapshot, no).Scan(&old); err != nil {
			return err
		}
		for n := 0; n < int(offset); n++ {
			key, ok := chunkKey(old, n)
			if !ok {
				return ErrNotFound
			}
			keys = append(keys, key)
		}
	}
	keys = append(keys, refill.ids...)
	for len(keys) > 0 {
		take := min(len(keys), chunkKeys)
		if err := writeQueueChunkTx(ctx, tx, refill.snapshot, no, encodeChunk(keys[:take]), true); err != nil {
			return err
		}
		delete(l.chunks, refill.snapshot+"/"+strconv.FormatInt(no, 10))
		no++
		keys = keys[take:]
	}
	count := refill.built + int64(len(refill.ids))
	if _, err := tx.ExecContext(ctx, `UPDATE queue_v1_snapshots SET count=?,built=?,seen=? WHERE id=?`, count, count, count, refill.snapshot); err != nil {
		return err
	}
	tail := len(l.segments) - 1
	if tail >= 0 && l.segments[tail].snapshot == refill.snapshot && l.segments[tail].first+l.segments[tail].count == refill.built {
		if _, err := tx.ExecContext(ctx, `UPDATE queue_v1_segments SET count=count+? WHERE id=?`, len(refill.ids), l.segments[tail].id); err != nil {
			return err
		}
	} else {
		key := ""
		if tail >= 0 {
			key = l.segments[tail].sortKey
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO queue_v1_segments(id,queue_id,sort_key,snapshot_id,first_ordinal,count,removed) VALUES(?,?,?,?,?,?,0)`, newShortID("g"), l.q.id, sortKeyBetween(key, ""), refill.snapshot, refill.built, len(refill.ids)); err != nil {
			return err
		}
	}
	fresh, err := s.readLayout(ctx, tx, l.q)
	if err != nil {
		return err
	}
	*l = *fresh
	return nil
}
