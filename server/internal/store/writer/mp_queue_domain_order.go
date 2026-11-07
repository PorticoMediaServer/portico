package writer

import (
	"sort"
	"strings"
)

func mpCoalesce(spans []mpQueueSpan) []mpQueueSpan {
	out := make([]mpQueueSpan, 0, len(spans))
	for _, s := range spans {
		if s.count == 0 {
			continue
		}
		if len(out) > 0 {
			last := &out[len(out)-1]
			if last.source == s.source && last.start+last.count == s.start {
				last.count += s.count
				continue
			}
		}
		out = append(out, s)
	}
	return out
}
func mpSpanCut(spans []mpQueueSpan, position int64, remove int64, insert []mpQueueSpan) ([]mpQueueSpan, error) {
	var total int64
	for _, s := range spans {
		total += s.count
	}
	if position < 0 || position > total || remove < 0 || remove > total-position {
		return nil, ErrInvalid
	}
	var before, after []mpQueueSpan
	var at int64
	for _, s := range spans {
		end := at + s.count
		if at < position {
			n := min(s.count, position-at)
			before = append(before, mpQueueSpan{s.source, s.start, n})
		}
		if end > position+remove {
			offset := max(int64(0), position+remove-at)
			after = append(after, mpQueueSpan{s.source, s.start + offset, s.count - offset})
		}
		at = end
	}
	out := append(before, insert...)
	out = append(out, after...)
	out = mpCoalesce(out)
	if len(out) > mpMaxSpans {
		return nil, errMPBound
	}
	return out, nil
}

const mpSortDigits = "0123456789abcdefghijklmnopqrstuvwxyz"

func mpBetween(a, b string) (string, error) {
	if len(a) > 128 || len(b) > 128 || b != "" && a >= b {
		return "", ErrInvalid
	}
	digit := func(s string, i int) int {
		if i < len(s) {
			return strings.IndexByte(mpSortDigits, s[i])
		}
		return 0
	}
	var out []byte
	for i := 0; i < 128; i++ {
		lo, hi := digit(a, i), len(mpSortDigits)
		if b != "" {
			hi = digit(b, i)
		}
		if lo < 0 || hi < 0 {
			return "", ErrInvalid
		}
		if b != "" && lo == hi {
			out = append(out, mpSortDigits[lo])
			continue
		}
		if hi-lo > 1 {
			return string(append(out, mpSortDigits[(lo+hi)/2])), nil
		}
		out = append(out, mpSortDigits[lo])
		b = ""
	}
	return "", errMPBound
}

// Insert one immutable snapshot range at a visible source position. Splitting
// ranges preserves every retained occurrence and its tombstones; no key clone.
func (l *mpQueueLayout) insertSegment(position int64, g mpQueueSegment, splitID string) error {
	if position < 0 || position > l.total() || !mpID(g.id) || !mpID(splitID) {
		return ErrInvalid
	}
	if l.head.shuffle {
		return ErrInvalid
	}
	var at int64
	for i, old := range l.segments {
		live := old.count - old.removed
		if position <= at+live {
			left, right := old, old
			var ordinal int64
			if position == at+live {
				ordinal = old.first + old.count
			} else {
				ordinal = old.first + position - at
				for _, r := range l.removals[old.snapshot] {
					if r >= old.first && r <= ordinal && r < old.first+old.count {
						ordinal++
					}
				}
			}
			left.count = ordinal - old.first
			left.removed = l.rangeRemoved(left)
			right.id = splitID
			right.first = ordinal
			right.count = old.first + old.count - ordinal
			right.removed = l.rangeRemoved(right)
			lo := ""
			if i > 0 {
				lo = l.segments[i-1].key
			}
			hi := ""
			if i+1 < len(l.segments) {
				hi = l.segments[i+1].key
			}
			var replacement []mpQueueSegment
			var err error
			switch {
			case left.count == 0:
				g.key, err = mpBetween(lo, old.key)
				right.id = old.id
				right.key = old.key
				replacement = append(replacement, g, right)
			case right.count == 0:
				g.key, err = mpBetween(old.key, hi)
				replacement = append(replacement, left, g)
			default:
				right.key, err = mpBetween(old.key, hi)
				if err == nil {
					g.key, err = mpBetween(old.key, right.key)
				}
				replacement = append(replacement, left, g, right)
			}
			if err != nil {
				return err
			}

			segments := append([]mpQueueSegment(nil), l.segments[:i]...)
			segments = append(segments, replacement...)
			l.segments = append(segments, l.segments[i+1:]...)
			return nil
		}
		at += live
	}
	lo := ""
	if len(l.segments) > 0 {
		lo = l.segments[len(l.segments)-1].key
	}
	key, err := mpBetween(lo, "")
	if err != nil {
		return err
	}
	g.key = key
	l.segments = append(l.segments, g)
	return nil
}

func (l *mpQueueLayout) appendSnapshot(s mpQueueSnapshot, segmentID, splitID string, position int64) error {
	if s.queue != l.head.id || s.state != "ready" || s.built != s.count || s.count < 1 || s.count > mpMaxQueueKeys-l.rawTotal() {
		return ErrConflict
	}
	if _, ok := l.snapshots[s.id]; ok {
		for _, g := range l.segments {
			if g.snapshot == s.id {
				return ErrConflict
			}
		}
	}
	l.snapshots[s.id] = s
	g := mpQueueSegment{id: segmentID, snapshot: s.id, count: s.count}
	if l.head.shuffle {
		start := l.rawTotal()
		lo := ""
		if len(l.segments) > 0 {
			lo = l.segments[len(l.segments)-1].key
		}
		key, err := mpBetween(lo, "")
		if err != nil {
			return err
		}
		g.key = key
		spans, err := mpSpanCut(l.spans, position, 0, []mpQueueSpan{{true, start, s.count}})
		if err != nil {
			return err
		}
		l.segments = append(l.segments, g)
		l.spans = spans
	} else {
		if err := l.insertSegment(position, g, splitID); err != nil {
			return err
		}
	}
	return nil
}
func (l *mpQueueLayout) removeEntry(entry string) error {
	pos, err := l.position(entry)
	if err != nil {
		return err
	}
	snap, o, err := mpParseEntry(entry)
	if err != nil {
		return err
	}
	if l.head.shuffle {
		spans, err := mpSpanCut(l.spans, pos, 1, nil)
		if err != nil {
			return err
		}
		l.spans = spans
	}
	l.removals[snap] = append(l.removals[snap], o)
	sort.Slice(l.removals[snap], func(i, j int) bool { return l.removals[snap][i] < l.removals[snap][j] })
	for i, g := range l.segments {
		if g.snapshot == snap && o >= g.first && o < g.first+g.count {
			l.segments[i].removed++
			break
		}
	}
	if pos < l.head.position {
		l.head.position--
	}
	l.head.position = min(l.head.position, max(int64(0), l.total()-1))
	return nil
}

// A shuffled move keeps its original occurrence ID and selects that exact raw
// source position. The permutation range is split rather than re-snapshotted.
func (l *mpQueueLayout) moveShuffled(entry, anchor string, before bool) (bool, error) {
	from, err := l.position(entry)
	if err != nil {
		return false, err
	}
	to, err := l.position(anchor)
	if err != nil {
		return false, err
	}
	if entry == anchor {
		return false, nil
	}
	if !before {
		to++
	}
	if from < to {
		to--
	}
	if from == to {
		return false, nil
	}
	src, err := l.source(from)
	if err != nil {
		return false, err
	}
	current := l.head.entry
	spans, err := mpSpanCut(l.spans, from, 1, nil)
	if err != nil {
		return false, err
	}
	spans, err = mpSpanCut(spans, to, 0, []mpQueueSpan{{true, src, 1}})
	if err != nil {
		return false, err
	}
	l.spans = spans
	position, err := l.position(current)
	if err != nil {
		return false, err
	}
	l.head.position = position
	return true, nil
}

// Shuffle-on preserves the current occurrence as position zero. Removed raw
// ordinals are cut out of the permutation; explicit later appends stay a tail.
func mpSetShuffle(l mpQueueLayout, on bool, seed int64) (mpQueueLayout, bool, error) {
	l = l.clone()
	current := l.head.entry
	if !on && !l.head.shuffle {
		return l, false, nil
	}
	if on && l.total() == 0 {
		return l, false, nil
	}
	if on {
		for _, s := range l.snapshots {
			if s.state != "ready" || s.kind == "radio" {
				return l, false, errMPPending
			}
		}
		// Locate current in the raw coordinate system, even if source order is
		// compressed by removals while shuffle is off.
		snap, o, err := mpParseEntry(current)
		if err != nil {
			return l, false, err
		}
		var src int64
		found := false
		for _, g := range l.segments {
			if g.snapshot == snap && o >= g.first && o < g.first+g.count {
				src += o - g.first
				found = true
				break
			}
			src += g.count
		}
		if !found {
			return l, false, ErrConflict
		}
		l.head.shuffle = true
		l.head.seed = seed
		l.head.lap = 0
		l.head.domain = l.rawTotal()
		l.head.first = src
		l.head.position = 0
		l.spans = []mpQueueSpan{{false, 0, l.head.domain}}
		var cuts []int64
		var base int64
		for _, g := range l.segments {
			for _, r := range l.removals[g.snapshot] {
				if r >= g.first && r < g.first+g.count {
					cuts = append(cuts, l.basePosition(base+r-g.first))
				}
			}
			base += g.count
		}
		sort.Slice(cuts, func(i, j int) bool { return cuts[i] > cuts[j] })
		for _, p := range cuts {
			l.spans, err = mpSpanCut(l.spans, p, 1, nil)
			if err != nil {
				return l, false, err
			}
		}
	} else {
		l.head.shuffle = false
		l.spans = nil
		l.head.domain = 0
		l.head.first = 0
		l.head.lap = 0
		p := int64(0)
		var err error
		if l.total() > 0 {
			p, err = l.position(current)
		}
		if err != nil {
			return l, false, err
		}
		l.head.position = p
	}
	return l, true, l.validate()
}
