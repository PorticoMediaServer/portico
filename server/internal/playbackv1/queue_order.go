package playbackv1

import (
	"context"
	"database/sql"
)

// A span refers either to the fixed shuffle permutation or to appended source
// positions. Editing splits ranges, never expands millions of queue entries.
type orderSpan struct {
	source       bool
	start, count int64
}

func (l *layout) sourceTotal() int64 {
	var n int64
	for _, g := range l.segments {
		n += g.count
	}
	return n
}
func (l *layout) initOrder() {
	if len(l.spans) > 0 {
		return
	}
	if l.q.domain > 0 {
		l.spans = append(l.spans, orderSpan{start: 0, count: l.q.domain})
	}
	if n := l.sourceTotal() - l.q.domain; n > 0 {
		l.spans = append(l.spans, orderSpan{source: true, start: l.q.domain, count: n})
	}
}
func (l *layout) insertOrder(pos int64, v orderSpan) {
	l.initOrder()
	out := []orderSpan{}
	for _, s := range l.spans {
		if pos >= 0 && pos <= s.count {
			if pos > 0 {
				out = append(out, orderSpan{s.source, s.start, pos})
			}
			out = append(out, v)
			if pos < s.count {
				out = append(out, orderSpan{s.source, s.start + pos, s.count - pos})
			}
			pos = -1
		} else {
			out = append(out, s)
			if pos >= 0 {
				pos -= s.count
			}
		}
	}
	if pos == 0 {
		out = append(out, v)
	}
	l.spans = out
}
func (l *layout) removeOrder(pos int64) {
	l.initOrder()
	out := []orderSpan{}
	for _, s := range l.spans {
		if pos >= 0 && pos < s.count {
			if pos > 0 {
				out = append(out, orderSpan{s.source, s.start, pos})
			}
			if pos+1 < s.count {
				out = append(out, orderSpan{s.source, s.start + pos + 1, s.count - pos - 1})
			}
			pos = -1
		} else {
			out = append(out, s)
			if pos >= 0 {
				pos -= s.count
			}
		}
	}
	l.spans = out
}
func (l *layout) saveOrder(ctx context.Context, tx *sql.Tx) error {
	// Coalesce adjacent ranges before applying the structural bound.
	merged := []orderSpan{}
	for _, span := range l.spans {
		if span.count <= 0 {
			continue
		}
		if len(merged) > 0 {
			last := &merged[len(merged)-1]
			if last.source == span.source && last.start+last.count == span.start {
				last.count += span.count
				continue
			}
		}
		merged = append(merged, span)
	}
	if len(merged) == 0 {
		merged = append(merged, orderSpan{source: true})
	}
	l.spans = merged
	if len(l.spans) > MaxQueueSegments {
		return ErrQueueTooLarge
	}
	if _, e := tx.ExecContext(ctx, `DELETE FROM queue_v1_play_spans WHERE queue_id=?`, l.q.id); e != nil {
		return e
	}
	for i, s := range l.spans {
		if s.count < 0 {
			continue
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO queue_v1_play_spans(queue_id,rank,source,start,count)VALUES(?,?,?,?,?)`, l.q.id, i, s.source, s.start, s.count); e != nil {
			return e
		}
	}
	return nil
}
func (l *layout) ordinaryOrderToSource(p int64) int64 {
	sh := l.shuffle()
	if sh == nil || p >= l.q.domain {
		return p
	}
	if p == 0 {
		return l.q.first
	}
	c, _ := sh.IndexOf(uint64(l.q.first))
	k := uint64(p)
	if uint64(p-1) < c {
		k = uint64(p - 1)
	}
	at, _ := sh.At(k)
	return int64(at)
}
func (l *layout) ordinarySourceToOrder(src int64) int64 {
	sh := l.shuffle()
	if sh == nil || src >= l.q.domain {
		return src
	}
	if src == l.q.first {
		return 0
	}
	c, _ := sh.IndexOf(uint64(l.q.first))
	k, _ := sh.IndexOf(uint64(src))
	if k < c {
		return int64(k) + 1
	}
	return int64(k)
}

// extendShuffleOrder retains explicit placement when an appended selector is
// checkpointed by the background builder. Later source positions shift, while
// the original shuffle domain never does.
func (l *layout) extendShuffleOrder(ctx context.Context, tx *sql.Tx, snapshot string, built int64) error {
	if !l.q.shuffle || len(l.spans) == 0 {
		return nil
	}
	var base, oldEnd int64
	found := false
	for _, g := range l.segments {
		if g.snapshot == snapshot {
			oldEnd = base + g.count
			found = true
			break
		}
		base += g.count
	}
	if !found {
		return nil
	}
	delta := built - (oldEnd - base)
	if delta <= 0 {
		return nil
	}
	// Find the effective position following the selector's last retained range.
	var insertion, pos int64
	highest := int64(-1)
	for _, span := range l.spans {
		if span.source {
			end := span.start + span.count
			if span.start < oldEnd && end > base {
				last := min(end, oldEnd)
				if last > highest {
					highest = last
					insertion = pos + last - span.start
				}
			}
		}
		pos += span.count
	}
	if highest < 0 {
		insertion = pos
	}
	shifted := []orderSpan{}
	for _, span := range l.spans {
		if !span.source {
			shifted = append(shifted, span)
			continue
		}
		if span.start < oldEnd && span.start+span.count > oldEnd {
			n := oldEnd - span.start
			shifted = append(shifted, orderSpan{true, span.start, n}, orderSpan{true, oldEnd + delta, span.count - n})
		} else {
			if span.start >= oldEnd {
				span.start += delta
			}
			shifted = append(shifted, span)
		}
	}
	l.spans = shifted
	l.insertOrder(insertion, orderSpan{true, oldEnd, delta})
	return l.saveOrder(ctx, tx)
}
