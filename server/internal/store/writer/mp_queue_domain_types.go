package writer

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Private draft purposes. These are deliberately not registered with Describe:
// the parent must compose the adopted MP receipts and W dispatcher first.
const (
	mpQueueBegin CommandID = 750
	mpQueueStage CommandID = 751
	mpQueueSeal  CommandID = 752
	mpQueueEdit  CommandID = 753
	// 754 belongs to the parent's repeat command.
	mpQueueSettings      CommandID = 755
	mpQueueRetire        CommandID = 756
	mpQueueSave          CommandID = 757
	mpQueueCleanup       CommandID = 758
	mpQueueSnapshotBegin CommandID = 759
	mpQueueAdvance       CommandID = 760
	mpPrepareNext        CommandID = 761
	mpCommitNext         CommandID = 762
	mpCancelNext         CommandID = 763
	mpNextPhysicalExit   CommandID = 764
	mpReadingLocation    CommandID = 770
	mpReadingClear       CommandID = 771
)

const (
	mpChunkKeys            = 256
	mpMaxQueueKeys   int64 = 10_000_000
	mpMaxSegments          = 1000
	mpMaxRemovals          = 10_000
	mpMaxSpans             = 1000
	mpMaxEditWrites        = 48
	mpMaxLayoutBytes       = 2 << 20
	mpMaxSlice             = 48
)

var (
	errMPPending   = errors.New("MP queue entry is pending")
	errMPEnd       = errors.New("MP queue ended")
	errMPAttention = errors.New("MP passout check required")
	errMPBound     = errors.New("MP bounded turn exhausted")
)

func mpID(s string) bool {
	if len(s) == 0 || len(s) > 128 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 33 || r == 127 {
			return false
		}
	}
	return true
}

// Entry IDs retain the public snapshot-ordinal shape. Snapshots cannot contain
// '-' so parsing never changes the identity of a repeated catalogue item.
func mpSnapshotID(s string) bool { return mpID(s) && !strings.Contains(s, "-") }
func mpEntry(snapshot string, ordinal int64) string {
	return snapshot + "-" + strconv.FormatInt(ordinal, 10)
}
func mpParseEntry(entry string) (string, int64, error) {
	s, n, ok := strings.Cut(entry, "-")
	o, err := strconv.ParseInt(n, 10, 64)
	if !ok || err != nil || o < 0 || !mpSnapshotID(s) || strconv.FormatInt(o, 10) != n {
		return "", 0, ErrInvalid
	}
	return s, o, nil
}

// Codec is compatible with the public fixed-width queue chunks. No JSON row
// graph, caller padding, empty IDs, trailing fragments or hidden NULs survive.
func mpEncodeChunk(keys []string) ([]byte, error) {
	if len(keys) < 1 || len(keys) > mpChunkKeys {
		return nil, ErrInvalid
	}
	w := 1
	for _, k := range keys {
		if !mpID(k) || strings.ContainsRune(k, 0) {
			return nil, ErrInvalid
		}
		w = max(w, len(k))
	}
	b := make([]byte, 1+w*len(keys))
	b[0] = byte(w)
	for i, k := range keys {
		copy(b[1+i*w:], k)
	}
	return b, nil
}
func mpDecodeChunk(b []byte) ([]string, error) {
	if len(b) < 2 || len(b) > 1+128*mpChunkKeys {
		return nil, ErrInvalid
	}
	w := int(b[0])
	if w < 1 || w > 128 || (len(b)-1)%w != 0 || (len(b)-1)/w > mpChunkKeys {
		return nil, ErrInvalid
	}
	out := make([]string, 0, (len(b)-1)/w)
	for p := 1; p < len(b); p += w {
		k := strings.TrimRight(string(b[p:p+w]), "\x00")
		if !mpID(k) || strings.ContainsRune(k, 0) {
			return nil, ErrInvalid
		}
		out = append(out, k)
	}
	canonical, err := mpEncodeChunk(out)
	if err != nil || string(canonical) != string(b) {
		return nil, ErrInvalid
	}
	return out, nil
}

type mpQueueHead struct {
	id, ownerKind, owner, authority, account, profile, session, entry, repeat          string
	revision, custody, position, seed, lap, domain, first, automatic, created, updated int64
	shuffle                                                                            bool
}
type mpQueueSnapshot struct {
	id, queue, kind, selector, label, state, sourceRevision string
	count, built                                            int64
}
type mpQueueSegment struct {
	id, key, snapshot     string
	first, count, removed int64
}
type mpQueueSpan struct {
	source       bool
	start, count int64
}
type mpQueueLayout struct {
	head      mpQueueHead
	segments  []mpQueueSegment
	snapshots map[string]mpQueueSnapshot
	removals  map[string][]int64
	spans     []mpQueueSpan
}

func (l mpQueueLayout) clone() mpQueueLayout {
	l.segments = append([]mpQueueSegment(nil), l.segments...)
	l.spans = append([]mpQueueSpan(nil), l.spans...)
	s := make(map[string]mpQueueSnapshot, len(l.snapshots))
	for k, v := range l.snapshots {
		s[k] = v
	}
	l.snapshots = s
	r := make(map[string][]int64, len(l.removals))
	for k, v := range l.removals {
		r[k] = append([]int64(nil), v...)
	}
	l.removals = r
	return l
}
func (l mpQueueLayout) rawTotal() int64 {
	var n int64
	for _, g := range l.segments {
		n += g.count
	}
	return n
}
func (l mpQueueLayout) total() int64 {
	var n int64
	if l.head.shuffle {
		for _, s := range l.spans {
			n += s.count
		}
		return n
	}
	for _, g := range l.segments {
		n += g.count - g.removed
	}
	return n
}
func (l mpQueueLayout) removed(snapshot string, ordinal int64) bool {
	for _, o := range l.removals[snapshot] {
		if o == ordinal {
			return true
		}
		if o > ordinal {
			break
		}
	}
	return false
}
func (l mpQueueLayout) rangeRemoved(g mpQueueSegment) int64 {
	var n int64
	for _, o := range l.removals[g.snapshot] {
		if o >= g.first && o < g.first+g.count {
			n++
		}
	}
	return n
}
func (l mpQueueLayout) validate() error {
	if len(l.segments) > mpMaxSegments || len(l.spans) > mpMaxSpans || l.rawTotal() > mpMaxQueueKeys {
		return errMPBound
	}
	h := l.head
	if !mpID(h.id) || h.revision < 1 || h.custody < 0 || h.position < 0 || h.lap < 0 || h.lap > math.MaxUint32 || h.seed < 0 || h.seed > math.MaxUint32 || h.automatic < 0 || (h.repeat != "off" && h.repeat != "one" && h.repeat != "all") {
		return ErrInvalid
	}
	seen := map[string]bool{}
	key := ""
	removals := 0
	for _, rs := range l.removals {
		removals += len(rs)
		var prev int64 = -1
		for _, o := range rs {
			if o <= prev {
				return ErrInvalid
			}
			prev = o
		}
	}
	if removals > mpMaxRemovals {
		return errMPBound
	}
	for i, g := range l.segments {
		s, ok := l.snapshots[g.snapshot]
		if !mpID(g.id) || seen[g.id] || !mpSnapshotID(g.snapshot) || !ok || s.queue != h.id || g.first < 0 || g.count <= 0 || g.first > s.count || g.count > s.count-g.first || g.removed != l.rangeRemoved(g) || g.removed > g.count || g.key <= key || len(g.key) > 128 || s.count < 0 || s.count > mpMaxQueueKeys || s.built < 0 || s.built > s.count || (s.state != "ready" && s.state != "building") || (s.state == "ready" && s.built != s.count) {
			return ErrInvalid
		}
		for _, prior := range l.segments[:i] {
			if prior.snapshot == g.snapshot && g.first < prior.first+prior.count && prior.first < g.first+g.count {
				return ErrInvalid
			}
		}
		key = g.key
		seen[g.id] = true
	}
	if h.shuffle {
		if h.domain < 1 || h.domain > l.rawTotal() || h.first < 0 || h.first >= h.domain {
			return ErrInvalid
		}
		for i, s := range l.spans {
			bound := h.domain
			if s.source {
				bound = l.rawTotal()
			}
			if s.start < 0 || s.count <= 0 || s.start > bound || s.count > bound-s.start {
				return ErrInvalid
			}
			// In-domain explicit ranges are moved occurrences. Validate their
			// inverse against base ranges below; appended tails need no expansion.
			for _, p := range l.spans[:i] {
				if p.source == s.source && s.start < p.start+p.count && p.start < s.start+s.count {
					return ErrInvalid
				}
			}
		}
		var explicit int64
		for _, s := range l.spans {
			if !s.source || s.start >= h.domain {
				continue
			}
			n := min(s.count, h.domain-s.start)
			explicit += n
			if explicit > mpMaxSpans {
				return errMPBound
			}
			for src := s.start; src < s.start+n; src++ {
				pos := l.basePosition(src)
				for _, b := range l.spans {
					if !b.source && pos >= b.start && pos < b.start+b.count {
						return ErrInvalid
					}
				}
			}
		}
		var retainedRemoved int64
		for _, g := range l.segments {
			retainedRemoved += g.removed
		}
		if l.total() != l.rawTotal()-retainedRemoved {
			return ErrInvalid
		}
	}
	if l.total() > 0 && h.position >= l.total() {
		return ErrInvalid
	}
	return nil
}

// Wire-compatible keyed permutation; bounded cycle walking is independent of
// queue length. The four Feistel rounds match client-core's selected order.
type mpPermutation struct {
	n, mask uint64
	half    uint
	keys    [4]uint32
}

func mpMix(x, k uint32) uint32 {
	h := x ^ k
	h = (h ^ h>>16) * 0x7feb352d
	h = (h ^ h>>15) * 0x846ca68b
	return h ^ h>>16
}
func mpNewPermutation(n int64, seed, lap int64) mpPermutation {
	bits := uint(2)
	for uint64(1)<<bits < uint64(n) {
		bits += 2
	}
	p := mpPermutation{n: uint64(n), mask: uint64(1)<<(bits/2) - 1, half: bits / 2}
	b := mpMix(uint32(seed), 0x9e3779b9^uint32(lap))
	for i := range p.keys {
		p.keys[i] = mpMix(b, uint32(uint64(0x85ebca6b)*uint64(i+1)))
	}
	return p
}
func (p mpPermutation) walk(x uint64, inverse bool) uint64 {
	for {
		a, b := x>>p.half, x&p.mask
		if inverse {
			for i := 3; i >= 0; i-- {
				a, b = (b^(uint64(mpMix(uint32(a), p.keys[i]))&p.mask))&p.mask, a
			}
		} else {
			for _, k := range p.keys {
				a, b = b, (a^(uint64(mpMix(uint32(b), k))&p.mask))&p.mask
			}
		}
		x = a<<p.half | b
		if x < p.n {
			return x
		}
	}
}
func (l mpQueueLayout) baseSource(pos int64) int64 {
	if !l.head.shuffle || pos >= l.head.domain {
		return pos
	}
	if pos == 0 {
		return l.head.first
	}
	p := mpNewPermutation(l.head.domain, l.head.seed, l.head.lap)
	anchor := int64(p.walk(uint64(l.head.first), true))
	k := pos
	if pos-1 < anchor {
		k--
	}
	return int64(p.walk(uint64(k), false))
}
func (l mpQueueLayout) basePosition(src int64) int64 {
	if !l.head.shuffle || src >= l.head.domain {
		return src
	}
	if src == l.head.first {
		return 0
	}
	p := mpNewPermutation(l.head.domain, l.head.seed, l.head.lap)
	anchor := p.walk(uint64(l.head.first), true)
	k := p.walk(uint64(src), true)
	if k < anchor {
		return int64(k) + 1
	}
	return int64(k)
}
func (l mpQueueLayout) source(pos int64) (int64, error) {
	if pos < 0 || pos >= l.total() {
		return 0, errMPEnd
	}
	if !l.head.shuffle {
		return pos, nil
	}
	for _, s := range l.spans {
		if pos < s.count {
			if s.source {
				return s.start + pos, nil
			}
			return l.baseSource(s.start + pos), nil
		}
		pos -= s.count
	}
	return 0, ErrInvalid
}
func (l mpQueueLayout) entryAt(pos int64) (string, error) {
	src, err := l.source(pos)
	if err != nil {
		return "", err
	}
	for _, g := range l.segments {
		n := g.count
		if !l.head.shuffle {
			n -= g.removed
		}
		if src < n {
			o := g.first + src
			if !l.head.shuffle {
				for _, r := range l.removals[g.snapshot] {
					if r >= g.first && r <= o && r < g.first+g.count {
						o++
					}
				}
			}
			if l.removed(g.snapshot, o) {
				return "", ErrInvalid
			}
			return mpEntry(g.snapshot, o), nil
		}
		src -= n
	}
	return "", ErrInvalid
}
func (l mpQueueLayout) position(entry string) (int64, error) {
	s, o, err := mpParseEntry(entry)
	if err != nil {
		return 0, err
	}
	if l.removed(s, o) {
		return 0, errMPEnd
	}
	var src int64
	for _, g := range l.segments {
		if g.snapshot == s && o >= g.first && o < g.first+g.count {
			src += o - g.first
			if !l.head.shuffle {
				for _, r := range l.removals[s] {
					if r >= g.first && r < o {
						src--
					}
				}
				return src, nil
			}
			base := l.basePosition(src)
			var pos int64
			for _, span := range l.spans {
				at := base
				if span.source {
					at = src
				}
				if at >= span.start && at < span.start+span.count {
					return pos + at - span.start, nil
				}
				pos += span.count
			}
			return 0, errMPEnd
		}
		src += g.count
		if !l.head.shuffle {
			src -= g.removed
		}
	}
	return 0, errMPEnd
}

// Result is effect evidence ONLY. It is neither Applied nor a COMMIT/ACK. W
// must durably save this exact value (including no-op) before terminal release.
type mpQueueEffect struct {
	queue, entry, session, token, reason            string
	revision, custody, position, lap, cursor, count int64
	changed, done                                   bool
}

func mpEffect(l mpQueueLayout, changed bool) mpQueueEffect {
	return mpQueueEffect{queue: l.head.id, entry: l.head.entry, session: l.head.session, revision: l.head.revision, custody: l.head.custody, position: l.head.position, lap: l.head.lap, changed: changed}
}

// Fixed-width and length-prefixed typed receipt commitment, never a serialized
// state graph. Every native scalar result column participates, including no-op.
func mpResultBytes(kind CommandID, e mpQueueEffect) ([]byte, error) {
	if !mpDraftPurpose(kind) || !mpID(e.queue) || e.revision < 1 || e.custody < 0 || e.position < 0 || e.lap < 0 || e.cursor < 0 || e.count < 0 {
		return nil, ErrInvalid
	}
	b := []byte("MP-result-v1\x00")
	b = binary.BigEndian.AppendUint16(b, uint16(kind))
	for _, s := range []string{e.queue, e.entry, e.session, e.token, e.reason} {
		if len(s) > 256 || !utf8.ValidString(s) {
			return nil, ErrInvalid
		}
		b = binary.BigEndian.AppendUint16(b, uint16(len(s)))
		b = append(b, s...)
	}
	for _, n := range []int64{e.revision, e.custody, e.position, e.lap, e.cursor, e.count} {
		b = binary.BigEndian.AppendUint64(b, uint64(n))
	}
	flags := byte(0)
	if e.changed {
		flags |= 1
	}
	if e.done {
		flags |= 2
	}
	return append(b, flags), nil
}
func mpDraftPurpose(k CommandID) bool {
	switch k {
	case mpQueueBegin, mpQueueStage, mpQueueSeal, mpQueueEdit, mpQueueSettings, mpQueueRetire, mpQueueSave, mpQueueCleanup, mpQueueSnapshotBegin, mpQueueAdvance, mpPrepareNext, mpCommitNext, mpCancelNext, mpNextPhysicalExit:
		return true
	}
	return false
}

func mpRequireRevision(h mpQueueHead, revision, custody int64) error {
	if h.revision != revision || h.custody != custody || revision < 1 || revision == math.MaxInt64 || custody == math.MaxInt64 {
		return ErrConflict
	}
	return nil
}
func mpOrdinalKey(n int64) string { return fmt.Sprintf("%016x", n) }

type mpQueueSnapshotBirthResult struct {
	id, sourceRevision string
	count              int64
}
type mpQueueBirthEffect struct {
	workerIncarnation [16]byte
	result            mpQueueEffect
	snapshots         []mpQueueSnapshotBirthResult
}
