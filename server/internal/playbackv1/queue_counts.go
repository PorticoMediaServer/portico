package playbackv1

import (
	"container/list"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// containerCount is a container's size at one catalog revision (P16; migration
// 0067). A large shuffle needs the size before its first entry, and counting
// through the caller's item fence costs seconds at 1M keys. For a caller the
// fence admits everything for (Service.Unrestricted) the size is the
// container's own, the same for every such caller, so it is kept and reused
// until the catalog changes. The catalog revision is every library's
// revision: a container's keys are catalog rows (songs, episodes, book files,
// collection members, items), and every change to one moves its library's
// revision. Playlists aren't catalog rows, and a restricted caller's view is
// narrower: both keep the counting pass.
type containerCount struct {
	selector, catalog string
	keys              int64
}

// containerCountFor is the cache entry this caller and selector would use:
// with its stored size when one is current (keys > 0), or empty to be filled
// by the counting pass. Nil when the cache doesn't apply.
func (s *Service) containerCountFor(ctx context.Context, tx *sql.Tx, p identity.Principal, in SegmentInput, k selectorKeys) *containerCount {
	c := in.Source.Container
	if s.Unrestricted == nil || k.kind != "selector" || c == nil || c.Kind == "playlist" {
		return nil
	}
	if open, err := s.Unrestricted(ctx, tx, p); err != nil || !open {
		return nil
	}
	args, _ := json.Marshal(k.args)
	out := &containerCount{selector: c.Kind + ":" + string(args)}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(group_concat(library_id||':'||revision,','),'')||':c:'||(SELECT revision FROM catalog_summary_classification_revision WHERE singleton=1) FROM (SELECT library_id,revision FROM library_revisions ORDER BY library_id)`).Scan(&out.catalog); err != nil {
		return nil
	}
	var catalog string
	var keys int64
	err := tx.QueryRowContext(ctx, `SELECT catalog,keys FROM queue_v1_container_counts WHERE selector=?`, out.selector).Scan(&catalog, &keys)
	if err == nil && catalog == out.catalog {
		out.keys = keys
	}
	return out
}

// pick is countAndPick without the pass: the anchor's ordinal (a walk of the
// container's own keys, unfenced), or the seeded pick the pass would have
// made, read with one ordered seek. ok is false if the stored size and the
// keys disagree (then the pass runs).
func (c *containerCount) pick(ctx context.Context, tx *sql.Tx, k selectorKeys, seed int64, anchorItem string) (n, ordinal int64, key string, ok bool) {
	if anchorItem == "" {
		ordinal = reservoirOrdinal(seed, c.keys)
		if tx.QueryRowContext(ctx, k.query+` LIMIT 1 OFFSET ?`, append(append([]any{}, k.args...), ordinal)...).Scan(&key) != nil {
			return 0, 0, "", false
		}
		return c.keys, ordinal, key, true
	}
	rows, err := tx.QueryContext(ctx, k.query, k.args...)
	if err != nil {
		return 0, 0, "", false
	}
	defer rows.Close()
	for i := int64(0); rows.Next() && i < c.keys; i++ {
		var id string
		if rows.Scan(&id) != nil {
			return 0, 0, "", false
		}
		if id == anchorItem {
			return c.keys, i, id, true
		}
	}
	return 0, 0, "", false
}

// reservoirOrdinal is the pick the counting pass makes over n keys: the last
// ordinal reservoirPick accepts. It depends on the seed and n alone, so the
// cached path and the pass agree; each ordinal i is accepted with
// probability 1/(i+1), so walking down from n-1 takes n/2 steps on average
// (a few milliseconds at 1M).
func reservoirOrdinal(seed, n int64) int64 {
	for i := n - 1; i > 0; i-- {
		if reservoirPick(seed, i) {
			return i
		}
	}
	return 0
}

// storeContainerCount keeps a counted size, at background priority, after
// the counting pass's read has ended. A failure only costs the next shuffle
// its pass.
func (s *Service) storeContainerCount(ctx context.Context, c containerCount) {
	write, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := dbwork.ExecWrite(write, s.DB, dbwork.ClassBackgroundMedia, `INSERT INTO queue_v1_container_counts(selector,catalog,keys,counted_ms) VALUES(?,?,?,?) ON CONFLICT(selector) DO UPDATE SET catalog=excluded.catalog,keys=excluded.keys,counted_ms=excluded.counted_ms`, c.selector, c.catalog, c.keys, s.now().UnixMilli()); err != nil {
		log.Printf("A container's size could not be kept: %v", err)
	}
}

// Duration summaries are bounded memoized projections. Their key is what the
// total depends on: the queue's members (layout.membership), the current
// visibility predicate/arguments, and catalogue revisions (asset
// duration/availability and metadata changes move those). It is not the queue
// revision, which every advance moves: keyed on that, each advance in a large
// queue walked every entry again. They never retain item IDs, titles, paths or
// media URLs.
type queueDurationSummary struct {
	ms    int64
	known bool
}

// queueDurationCache keeps the queueDurationLimit most recently used summaries.
type queueDurationCache struct {
	sync.Mutex
	values map[string]*list.Element
	order  list.List // most recent first; values are durationEntry
}

type durationEntry struct {
	key     string
	summary queueDurationSummary
}

const queueDurationLimit = 64

func (c *queueDurationCache) get(key string) (queueDurationSummary, bool) {
	c.Lock()
	defer c.Unlock()
	e, ok := c.values[key]
	if !ok {
		return queueDurationSummary{}, false
	}
	c.order.MoveToFront(e)
	return e.Value.(durationEntry).summary, true
}

func (c *queueDurationCache) put(key string, summary queueDurationSummary) {
	c.Lock()
	defer c.Unlock()
	if c.values == nil {
		c.values = map[string]*list.Element{}
	}
	if e, ok := c.values[key]; ok {
		e.Value = durationEntry{key, summary}
		c.order.MoveToFront(e)
		return
	}
	c.values[key] = c.order.PushFront(durationEntry{key, summary})
	for len(c.values) > queueDurationLimit {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.values, oldest.Value.(durationEntry).key)
	}
}

// membership digests what the members of a queue are: its segments, the
// removals inside them, and the shuffle state and play order. Moving through
// the queue changes none of it.
func (l *layout) membership() string {
	h := sha256.New()
	for _, g := range l.segments {
		fmt.Fprintf(h, "g%q,%q,%d,%d,%d,%q,%d,%q,%t;", g.id, g.snapshot, g.first, g.count, g.removed, g.state, g.built, g.sourceRevision, g.musicOnly)
	}
	snapshots := make([]string, 0, len(l.removals))
	for snapshot := range l.removals {
		snapshots = append(snapshots, snapshot)
	}
	sort.Strings(snapshots)
	for _, snapshot := range snapshots {
		fmt.Fprintf(h, "r%q", snapshot)
		for _, ordinal := range l.removals[snapshot] {
			fmt.Fprintf(h, ",%d", ordinal)
		}
		h.Write([]byte{';'})
	}
	fmt.Fprintf(h, "s%t,%d,%d,%d,%d;", l.q.shuffle, l.q.seed, l.q.lap, l.q.domain, l.q.first)
	for _, span := range l.spans {
		fmt.Fprintf(h, "p%t,%d,%d;", span.source, span.start, span.count)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Service) queueDuration(ctx context.Context, l *layout, p identity.Principal) (queueDurationSummary, error) {
	key := ""
	if s.Visibility != nil {
		where, args, err := s.Visibility(ctx, l.tx, p, "i.id")
		if err != nil {
			return queueDurationSummary{}, err
		}
		var catalog string
		var pendingSources bool
		if err = l.tx.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT group_concat(library_id||':'||revision,',') FROM (SELECT library_id,revision FROM library_revisions ORDER BY library_id)),'')||':c:'||(SELECT revision FROM catalog_summary_classification_revision WHERE singleton=1),
 EXISTS(SELECT 1 FROM catalog_upstream_sources WHERE domain=41)
 OR EXISTS(SELECT 1 FROM catalog_upstream_sources WHERE domain=42)`).Scan(&catalog, &pendingSources); err != nil {
			return queueDurationSummary{}, err
		}
		// Asset/entity source acceptance precedes its bounded per-owner
		// library-revision delivery. While either raw source prefix has work,
		// read current file facts without consulting or filling an old cache
		// key. The delivery advances the library revision before retiring the
		// source row, after which ordinary memoization resumes. All probes and
		// facts share this read snapshot; playback/progress writes add no epoch.
		if !pendingSources {
			raw, _ := json.Marshal([]any{l.q.id, l.membership(), p.Authority, p.AccountID, p.ProfileID, where, args, catalog})
			key = string(raw)
			if cached, ok := s.durations.get(key); ok {
				return cached, nil
			}
		}
	}
	summary := queueDurationSummary{known: true}
	for from := int64(0); from < l.total(); from += WindowMax {
		// A summary walks the queue in pages, discarding each page's key chunks.
		l.chunks = map[string][]byte{}
		entries, err := s.window(ctx, l, p, from, WindowMax)
		if err != nil {
			return queueDurationSummary{}, err
		}
		for _, entry := range entries {
			if !entry.Available || entry.DurationMs <= 0 {
				summary.known = false
				continue
			}
			summary.ms += entry.DurationMs
		}
	}
	l.chunks = map[string][]byte{}
	if key != "" {
		s.durations.put(key, summary)
	}
	return summary, nil
}
